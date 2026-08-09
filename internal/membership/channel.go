package membership

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

// nodeAddrMetadataKey is the gRPC metadata key used by the dialer to
// identify itself to the acceptor when opening a PersistentChannel stream.
// It is required because the acceptor must key its peer map by the node's
// cluster (memberlist) address, not by the ephemeral TCP source address of
// the dialing connection.
const nodeAddrMetadataKey = "x-drl-node-addr"

// channelSendBuffer bounds the number of outbound messages queued per peer
// before Send() starts reporting back-pressure errors. Hi-priority events
// are infrequent relative to accounting traffic, so a small buffer suffices.
const channelSendBuffer = 256

// duplexStream is the minimal interface shared by the client-side
// (grpc.BidiStreamingClient) and server-side (grpc.BidiStreamingServer)
// handles of a PersistentChannel stream, letting the rest of this file treat
// both ends identically.
type duplexStream interface {
	Send(*drlproto.ChannelMessage) error
	Recv() (*drlproto.ChannelMessage, error)
}

// channelMessageHandler processes hi-priority events received over the
// persistent gRPC channel. *StateDelegate implements this interface.
// The methods are unexported deliberately: only types within package
// membership are meant to implement this handler.
type channelMessageHandler interface {
	handleChannelBlockWithExpiresAt(evt *drlproto.BlockEventWithExpiresAt)
	handleChannelUnblock(evt *drlproto.UnblockEvent)
}

// peerChannel tracks the live duplex stream and outbound queue for a single
// peer connection.
type peerChannel struct {
	addr   string
	stream duplexStream
	sendCh chan *drlproto.ChannelMessage

	// conn is non-nil only when the local node is the dialer for this peer.
	// Closing it force-terminates the underlying stream immediately.
	conn *grpc.ClientConn

	// closeSignal, when set, is used to unblock the acceptor-side Stream()
	// handler goroutine when the peer is torn down proactively (e.g. on
	// NotifyLeave) rather than by the remote end closing the connection.
	closeSignal func()

	closeOnce sync.Once
}

// ChannelManagerConfig holds configuration for creating a ChannelManager.
type ChannelManagerConfig struct {
	// LocalAddr is this node's cluster (memberlist) address. It is used both
	// to identify this node to peers and to deterministically decide dial
	// direction (see ChannelManager doc comment).
	LocalAddr string
	// Port is the TCP port the persistent channel gRPC server listens on,
	// and the port used to dial peers.
	Port int
	// Handler receives decoded hi-priority events. Normally the cluster's
	// *StateDelegate.
	Handler channelMessageHandler
	Metrics *metrics.Metrics
	Logger  *slog.Logger
}

// ChannelManager manages the persistent, bidirectional gRPC channel used to
// propagate hi-priority (block/unblock) events between cluster members,
// replacing the on-demand memberlist SendReliable path when enabled via
// DRL_MEMBERSHIP_USE_HIPRIO_PERSISTENT_CHANNEL.
//
// Design decision: rather than each ordered pair of nodes independently
// dialing the other (2 TCP/HTTP2 connections per pair, "spaghetti web"),
// ChannelManager establishes exactly ONE bidirectional gRPC stream per
// unordered pair of nodes. gRPC streams are natively full-duplex, so a
// single stream is sufficient for both peers to Send and Recv events
// concurrently — a second connection would add file descriptors, TCP/TLS
// handshake overhead, and keepalive traffic without adding capability.
//
// The dial direction is decided deterministically, without any coordination
// round-trip: the node whose address sorts lexicographically smaller dials
// the other (see EstablishForPeer). Both sides independently reach the same
// conclusion when they observe a memberlist join event, so exactly one side
// dials and the other passively accepts — halving connection count
// cluster-wide compared to a two-connections-per-pair model.
type ChannelManager struct {
	localAddr string
	port      int
	handler   channelMessageHandler
	metrics   *metrics.Metrics
	logger    *slog.Logger

	grpcServer *grpc.Server
	listener   net.Listener

	mu    sync.RWMutex
	peers map[string]*peerChannel

	drlproto.UnimplementedPersistentChannelServer
}

// NewChannelManager creates a new ChannelManager. Start must be called to
// begin listening for inbound peer connections.
func NewChannelManager(cfg ChannelManagerConfig) *ChannelManager {
	return &ChannelManager{
		localAddr: cfg.LocalAddr,
		port:      cfg.Port,
		handler:   cfg.Handler,
		metrics:   cfg.Metrics,
		logger:    cfg.Logger,
		peers:     make(map[string]*peerChannel),
	}
}

// Start begins listening for inbound persistent channel connections from
// peers. It does not block.
func (cm *ChannelManager) Start() error {
	lis, err := net.Listen("tcp", net.JoinHostPort(cm.localAddr, strconv.Itoa(cm.port)))
	if err != nil {
		return fmt.Errorf("failed to listen on persistent channel port %d: %w", cm.port, err)
	}
	cm.listener = lis

	gs := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second,
			Timeout: 5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	cm.grpcServer = gs
	drlproto.RegisterPersistentChannelServer(gs, cm)

	cm.logger.Info("persistent gRPC channel server listening",
		"local_addr", cm.localAddr,
		"port", cm.port,
	)

	go func() {
		if err := gs.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			cm.logger.Error("persistent channel gRPC server error", "error", err)
		}
	}()

	return nil
}

// Stop tears down all peer connections and stops the gRPC server. Intended
// to be called once, when this node leaves the cluster.
func (cm *ChannelManager) Stop() {
	cm.mu.Lock()
	peers := make([]*peerChannel, 0, len(cm.peers))
	for _, p := range cm.peers {
		peers = append(peers, p)
	}
	cm.mu.Unlock()

	for _, p := range peers {
		cm.teardownPeer(p.addr, p)
	}

	if cm.grpcServer != nil {
		cm.grpcServer.GracefulStop()
	}
	cm.logger.Info("persistent gRPC channel server stopped", "local_addr", cm.localAddr)
}

// EstablishForPeer decides, deterministically, whether the local node should
// dial the given peer's persistent channel. It is intended to be called from
// the membership event delegate whenever a node joins the cluster (for every
// other known member, from both the joiner's and the existing members'
// perspective — memberlist delivers NotifyJoin symmetrically). Only the side
// whose address sorts smaller dials; the other side waits to accept the
// inbound connection.
func (cm *ChannelManager) EstablishForPeer(remoteAddr string) {
	if remoteAddr == "" || remoteAddr == cm.localAddr {
		return
	}
	if cm.localAddr >= remoteAddr {
		// Passive side: the peer with the smaller address will dial us.
		return
	}

	cm.mu.RLock()
	_, exists := cm.peers[remoteAddr]
	cm.mu.RUnlock()
	if exists {
		return
	}

	go cm.connect(remoteAddr)
}

// connect dials the given peer and opens the PersistentChannel stream.
func (cm *ChannelManager) connect(remoteAddr string) {
	target := net.JoinHostPort(remoteAddr, strconv.Itoa(cm.port))

	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		cm.logger.Warn("failed to dial persistent channel peer", "peer_addr", remoteAddr, "error", err)
		if cm.metrics != nil {
			cm.metrics.IncMembershipChannelErrors()
		}
		return
	}

	client := drlproto.NewPersistentChannelClient(conn)
	md := metadata.Pairs(nodeAddrMetadataKey, cm.localAddr)
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	stream, err := client.Stream(ctx)
	if err != nil {
		cm.logger.Warn("failed to open persistent channel stream", "peer_addr", remoteAddr, "error", err)
		if cm.metrics != nil {
			cm.metrics.IncMembershipChannelErrors()
		}
		_ = conn.Close()
		return
	}

	p, registered := cm.registerPeer(remoteAddr, stream, conn, nil)
	if !registered {
		// Lost a race with a concurrent connect/accept for the same peer.
		_ = conn.Close()
		return
	}

	cm.logger.Info("persistent channel established",
		"peer_addr", remoteAddr,
		"direction", "outbound",
	)

	cm.dialerRecvLoop(p)
}

// Stream implements drlproto.PersistentChannelServer. It is invoked by
// grpc-go once per inbound RPC, i.e. once per peer that dials us.
func (cm *ChannelManager) Stream(stream drlproto.PersistentChannel_StreamServer) error {
	remoteAddr := extractPeerAddr(stream.Context())
	if remoteAddr == "" {
		return status.Error(codes.InvalidArgument, "missing "+nodeAddrMetadataKey+" metadata")
	}

	done := make(chan struct{})
	p, registered := cm.registerPeer(remoteAddr, stream, nil, func() {
		select {
		case <-done:
		default:
			close(done)
		}
	})
	if !registered {
		return status.Errorf(codes.AlreadyExists, "persistent channel to %s already established", remoteAddr)
	}

	cm.logger.Info("persistent channel established",
		"peer_addr", remoteAddr,
		"direction", "inbound",
	)

	msgCh := make(chan *drlproto.ChannelMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- msg
		}
	}()

	for {
		select {
		case <-done:
			cm.teardownPeer(remoteAddr, p)
			return nil
		case err := <-errCh:
			cm.logger.Info("persistent channel closed", "peer_addr", remoteAddr, "error", err)
			cm.teardownPeer(remoteAddr, p)
			return nil
		case msg := <-msgCh:
			cm.dispatch(msg)
		}
	}
}

// dialerRecvLoop continuously receives messages on a client-dialed stream
// until it errors out (peer closed / connection dropped), then tears down
// the peer entry.
func (cm *ChannelManager) dialerRecvLoop(p *peerChannel) {
	for {
		msg, err := p.stream.Recv()
		if err != nil {
			cm.logger.Info("persistent channel closed", "peer_addr", p.addr, "error", err)
			cm.teardownPeer(p.addr, p)
			return
		}
		cm.dispatch(msg)
	}
}

// registerPeer records a newly established stream in the peers map and
// starts its outbound writer goroutine. Returns (peer, false) if a peer
// entry for addr already exists (caller should discard the new stream).
func (cm *ChannelManager) registerPeer(addr string, stream duplexStream, conn *grpc.ClientConn, closeSignal func()) (*peerChannel, bool) {
	cm.mu.Lock()
	if _, exists := cm.peers[addr]; exists {
		cm.mu.Unlock()
		return nil, false
	}
	p := &peerChannel{
		addr:        addr,
		stream:      stream,
		sendCh:      make(chan *drlproto.ChannelMessage, channelSendBuffer),
		conn:        conn,
		closeSignal: closeSignal,
	}
	cm.peers[addr] = p
	cm.mu.Unlock()

	if cm.metrics != nil {
		cm.metrics.IncMembershipChannelConnections()
	}

	go cm.writeLoop(p)

	return p, true
}

// writeLoop drains a peer's outbound queue, serialising Send() calls (gRPC
// streams are not safe for concurrent Send from multiple goroutines).
func (cm *ChannelManager) writeLoop(p *peerChannel) {
	for msg := range p.sendCh {
		if err := p.stream.Send(msg); err != nil {
			cm.logger.Warn("failed to send persistent channel message", "peer_addr", p.addr, "error", err)
			if cm.metrics != nil {
				cm.metrics.IncMembershipChannelErrors()
			}
			cm.teardownPeer(p.addr, p)
			return
		}
		if cm.metrics != nil {
			cm.metrics.IncMembershipChannelMsgsSent()
		}
	}
}

// dispatch decodes the oneof content of a ChannelMessage and routes it to
// the configured handler.
func (cm *ChannelManager) dispatch(msg *drlproto.ChannelMessage) {
	if msg == nil {
		return
	}
	if cm.metrics != nil {
		cm.metrics.IncMembershipChannelMsgsRecv()
	}
	if cm.handler == nil {
		return
	}
	switch content := msg.Content.(type) {
	case *drlproto.ChannelMessage_BlockWithExpiresAt:
		cm.handler.handleChannelBlockWithExpiresAt(content.BlockWithExpiresAt)
	case *drlproto.ChannelMessage_Unblock:
		cm.handler.handleChannelUnblock(content.Unblock)
	default:
		cm.logger.Warn("received ChannelMessage with unknown content type")
	}
}

// Send queues msg for delivery to the peer at addr over its persistent
// channel. Returns an error (never blocking the caller for long) if no
// channel is established to that peer or if the outbound queue is full;
// callers should log and continue rather than fail the request path.
func (cm *ChannelManager) Send(addr string, msg *drlproto.ChannelMessage) error {
	cm.mu.RLock()
	p, ok := cm.peers[addr]
	cm.mu.RUnlock()
	if !ok {
		return fmt.Errorf("no persistent channel established to peer %s", addr)
	}

	select {
	case p.sendCh <- msg:
		return nil
	default:
		return fmt.Errorf("persistent channel send buffer full for peer %s", addr)
	}
}

// IsConnected reports whether a persistent channel is currently established
// to the given peer address.
func (cm *ChannelManager) IsConnected(addr string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	_, ok := cm.peers[addr]
	return ok
}

// PeerCount returns the number of currently established peer channels.
func (cm *ChannelManager) PeerCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.peers)
}

// Close tears down the persistent channel to the given peer, if one exists.
// Intended to be called from the membership event delegate when a peer
// leaves the cluster.
func (cm *ChannelManager) Close(addr string) {
	cm.mu.RLock()
	p, ok := cm.peers[addr]
	cm.mu.RUnlock()
	if !ok {
		return
	}
	cm.teardownPeer(addr, p)
}

// teardownPeer removes the peer from the map and releases its resources.
// Safe to call multiple times (e.g. once from a Recv-loop failure and once
// from an explicit Close) — only the first call has an effect.
func (cm *ChannelManager) teardownPeer(addr string, p *peerChannel) {
	p.closeOnce.Do(func() {
		cm.mu.Lock()
		if cm.peers[addr] == p {
			delete(cm.peers, addr)
		}
		cm.mu.Unlock()

		close(p.sendCh)
		if p.conn != nil {
			_ = p.conn.Close()
		}
		if p.closeSignal != nil {
			p.closeSignal()
		}
		if cm.metrics != nil {
			cm.metrics.DecMembershipChannelConnections()
		}
		cm.logger.Info("persistent channel torn down", "peer_addr", addr)
	})
}

// extractPeerAddr reads the dialer's identity from the inbound gRPC
// metadata attached by connect().
func extractPeerAddr(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(nodeAddrMetadataKey)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}
