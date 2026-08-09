package membership

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"
	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

// StateDelegate implements memberlist.Delegate interface for state synchronization.
// It handles TCP Push/Pull state sync for the blocklist cache and processes
// incoming DrlMessage envelopes (accounting batches, block/unblock events).
type StateDelegate struct {
	blocklist  *cache.BlocklistCache
	accounting *cache.AccountingCache
	metrics    *metrics.Metrics
	logger     *slog.Logger
	cluster    *Cluster

	// Readiness tracking
	ready        atomic.Bool
	syncComplete chan struct{}
	syncOnce     sync.Once
	syncTimeout  time.Duration

	// Sync start time for metrics
	syncStartTime time.Time

	// Broadcast queue for gossip-based block/unblock propagation
	broadcastQueue *memberlist.TransmitLimitedQueue

	// bufPool reuses bytes.Buffer allocations for broadcast serialisation
	bufPool sync.Pool

	// handover handles graceful state evacuation on shutdown
	handover *Handover
}

// DelegateConfig holds configuration for the state delegate
type DelegateConfig struct {
	Blocklist   *cache.BlocklistCache
	Accounting  *cache.AccountingCache
	Metrics     *metrics.Metrics
	Logger      *slog.Logger
	SyncTimeout time.Duration
	// NumNodesFunc returns the current number of live cluster nodes.
	// Used by the TransmitLimitedQueue to calculate retransmit counts.
	// Defaults to a function that returns 1 when not provided.
	NumNodesFunc func() int
}

// NewStateDelegate creates a new state delegate
func NewStateDelegate(cfg DelegateConfig) *StateDelegate {
	timeout := cfg.SyncTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	numNodes := cfg.NumNodesFunc
	if numNodes == nil {
		numNodes = func() int { return 1 }
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	m := cfg.Metrics
	if m == nil {
		m = metrics.NewMetrics()
	}

	d := &StateDelegate{
		blocklist:    cfg.Blocklist,
		accounting:   cfg.Accounting,
		metrics:      m,
		logger:       logger,
		syncComplete: make(chan struct{}),
		syncTimeout:  timeout,
		broadcastQueue: &memberlist.TransmitLimitedQueue{
			NumNodes:       numNodes,
			RetransmitMult: 3,
		},
	}

	d.bufPool = sync.Pool{
		New: func() any { return new(bytes.Buffer) },
	}

	return d
}

// SetCluster sets the cluster reference for SendReliable operations.
// Must be called after the cluster is created but before any block events.
func (d *StateDelegate) SetCluster(cluster *Cluster) {
	d.cluster = cluster
}

// NodeMeta returns metadata to be sent to other nodes.
// This is optional and we don't use it currently.
func (d *StateDelegate) NodeMeta(limit int) []byte {
	return nil
}

// NotifyMsg is called when a user-level message is received from a peer
// (via SendBestEffort or SendReliable). It unmarshals the DrlMessage
// envelope and dispatches to the appropriate handler.
func (d *StateDelegate) NotifyMsg(buf []byte) {
	if len(buf) == 0 {
		return
	}

	msg := &drlproto.DrlMessage{}
	if err := proto.Unmarshal(buf, msg); err != nil {
		if d.logger != nil {
			d.logger.Warn("failed to unmarshal DrlMessage", "error", err)
		}
		return
	}

	switch content := msg.Content.(type) {
	case *drlproto.DrlMessage_Counters:
		d.handleAccountingMsg(content.Counters)
	case *drlproto.DrlMessage_Block:
		d.handleBlockEvent(content.Block)
	case *drlproto.DrlMessage_BlockWithExpiresAt:
		d.handleBlockEventWithExpiresAt(content.BlockWithExpiresAt)
	case *drlproto.DrlMessage_Unblock:
		d.handleUnblockEvent(content.Unblock)
	case *drlproto.DrlMessage_Handover:
		d.handleHandoverPayload(content.Handover)
	default:
		d.logger.Warn("received DrlMessage with unknown content type")
	}
}

// handleAccountingMsg processes an incoming CounterBatch from a peer,
// applying increments to the local accounting cache.
func (d *StateDelegate) handleAccountingMsg(batch *drlproto.CounterBatch) {
	if batch == nil {
		return
	}

	d.metrics.IncAccountingMsgRecv()
	d.metrics.IncMembershipBestEffort()

	for _, entry := range batch.Entries {
		key := fmt.Sprintf("%016x", entry.EntityHash)
		d.accounting.Increment(key, int64(entry.Hits))
	}

	d.logger.Debug("received counter batch",
		"sender_id", batch.SenderId,
		"entries", len(batch.Entries),
	)
}

// handleBlockEvent processes an incoming legacy block event (TTL-based) from a peer.
func (d *StateDelegate) handleBlockEvent(evt *drlproto.BlockEvent) {
	if evt == nil {
		return
	}

	d.metrics.IncMembershipReliable()

	ttl := time.Duration(evt.TtlNanos)
	var entity *model.Entity
	if evt.EntityIp != "" || evt.EntityPath != "" {
		entity = &model.Entity{
			IP:      evt.EntityIp,
			Path:    evt.EntityPath,
			Headers: evt.EntityHdrs,
		}
	}

	if entity != nil {
		d.blocklist.Block(evt.Key, ttl, entity)
	} else {
		d.blocklist.Block(evt.Key, ttl, nil)
	}

	d.logger.Debug("applied remote block event",
		"key", evt.Key,
		"ttl", ttl,
		"has_entity", entity != nil,
	)
}

// handleBlockEventWithExpiresAt processes an incoming block event that carries an
// absolute expiration timestamp. It delegates to BlockWithExpiresAt so the local
// cache always keeps the freshest deadline, regardless of when the event arrives.
func (d *StateDelegate) handleBlockEventWithExpiresAt(evt *drlproto.BlockEventWithExpiresAt) {
	if evt == nil {
		return
	}

	d.metrics.IncMembershipReliable()

	expiresAt := time.Unix(0, evt.ExpiresAtNanos)
	var entity *model.Entity
	if evt.EntityIp != "" || evt.EntityPath != "" {
		entity = &model.Entity{
			IP:      evt.EntityIp,
			Path:    evt.EntityPath,
			Headers: evt.EntityHdrs,
		}
	}

	d.blocklist.BlockWithExpiresAt(evt.Key, expiresAt, entity)

	d.logger.Debug("applied remote block event (expires_at)",
		"key", evt.Key,
		"expires_at", expiresAt,
		"has_entity", entity != nil,
	)
}

// handleUnblockEvent processes an incoming unblock event from a peer.
func (d *StateDelegate) handleUnblockEvent(evt *drlproto.UnblockEvent) {
	if evt == nil {
		return
	}

	d.metrics.IncMembershipReliable()

	d.blocklist.Unblock(evt.Key)
	d.logger.Debug("applied remote unblock event", "key", evt.Key)
}

// GetBroadcasts is called by memberlist when it wants messages to broadcast.
// Returns pending block/unblock events from the gossip transmit queue.
func (d *StateDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return d.broadcastQueue.GetBroadcasts(overhead, limit)
}

// LocalState returns the local state to be sent to peers during Push/Pull sync.
// This is the main state sync method - it serializes the blocklist for transfer.
func (d *StateDelegate) LocalState(join bool) []byte {
	if d.blocklist == nil {
		return nil
	}
	state, err := d.blocklist.GetState()
	if err != nil {
		d.logger.Error("failed to get local state for sync",
			"error", err,
		)
		return nil
	}

	d.logger.Debug("providing local state for sync",
		"is_join", join,
		"state_size_bytes", len(state),
	)

	return state
}

// MergeRemoteState is called when state is received from a peer during Push/Pull sync.
// This is called on the joining node to receive the current cluster state.
func (d *StateDelegate) MergeRemoteState(buf []byte, join bool) {
	// Record sync start time if this is the first sync
	d.syncOnce.Do(func() {
		d.syncStartTime = time.Now()
	})

	d.logger.Info("state sync started",
		"is_join", join,
		"state_size_bytes", len(buf),
	)

	if err := d.blocklist.MergeState(buf); err != nil {
		d.logger.Error("failed to merge remote state",
			"error", err,
		)
		return
	}

	// Mark sync as complete
	d.markSyncComplete()

	// Record sync duration
	duration := time.Since(d.syncStartTime).Seconds()
	d.metrics.ObserveSyncDuration(duration)

	entries := d.blocklist.Count()
	d.logger.Info("state sync complete",
		"received_entries", entries,
	)
}

// QueueBlockEvent converts the given TTL to an absolute expiration timestamp
// (time.Now().Add(ttl)) and propagates a BlockEventWithExpiresAt to all peers.
// Using a static timestamp instead of a relative TTL ensures every node applies
// the exact same deadline regardless of when the message is delivered.
//
// When the persistent gRPC channel is enabled (config.Membership.
// UseHiPrioPersistentChannel) and established, the event is sent over that
// channel; otherwise it falls back to the legacy memberlist SendReliable
// (TCP) path. Sends are dispatched concurrently so the caller is not blocked.
func (d *StateDelegate) QueueBlockEvent(key string, ttl time.Duration, entity *model.Entity) error {
	expiresAt := time.Now().Add(ttl)
	evt := &drlproto.BlockEventWithExpiresAt{
		Key:            key,
		ExpiresAtNanos: expiresAt.UnixNano(),
	}
	if entity != nil {
		evt.EntityIp = entity.IP
		evt.EntityPath = entity.Path
		evt.EntityHdrs = entity.Headers
	}

	if d.useChannel() {
		d.sendToAllPeersViaChannel(&drlproto.ChannelMessage{
			Content: &drlproto.ChannelMessage_BlockWithExpiresAt{BlockWithExpiresAt: evt},
		})
		return nil
	}

	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_BlockWithExpiresAt{BlockWithExpiresAt: evt},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal block DrlMessage: %w", err)
	}

	d.warnLegacyPath("block")
	d.sendToAllPeersAsync(data)
	return nil
}

// QueueUnblockEvent sends an UnblockEvent to all cluster peers, preferring
// the persistent gRPC channel when enabled and falling back to memberlist
// SendReliable (TCP) otherwise.
func (d *StateDelegate) QueueUnblockEvent(key string) error {
	evt := &drlproto.UnblockEvent{Key: key}

	if d.useChannel() {
		d.sendToAllPeersViaChannel(&drlproto.ChannelMessage{
			Content: &drlproto.ChannelMessage_Unblock{Unblock: evt},
		})
		return nil
	}

	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Unblock{Unblock: evt},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal unblock DrlMessage: %w", err)
	}

	d.warnLegacyPath("unblock")
	d.sendToAllPeersAsync(data)
	return nil
}

// useChannel reports whether hi-priority events should be routed over the
// persistent gRPC channel instead of memberlist SendReliable.
func (d *StateDelegate) useChannel() bool {
	return d.cluster != nil &&
		d.cluster.config != nil &&
		d.cluster.config.Membership.UseHiPrioPersistentChannel &&
		d.cluster.GetChannelManager() != nil
}

// warnLegacyPath logs a WARN-level event whenever a hi-priority (block/unblock)
// message is propagated over the legacy on-demand memberlist SendReliable
// (TCP) path instead of the persistent gRPC channel. This surfaces cases
// where the persistent channel is disabled or not yet established to peers,
// per the milestone requirement to flag use of the legacy transport.
func (d *StateDelegate) warnLegacyPath(eventType string) {
	d.logger.Warn("using legacy on-demand TCP path for hi-priority event; persistent gRPC channel disabled or unavailable",
		"event_type", eventType,
	)
}

// sendToAllPeersAsync sends data to all cluster peers via SendReliable concurrently.
// Each peer gets its own goroutine so the caller is not blocked.
func (d *StateDelegate) sendToAllPeersAsync(data []byte) {
	if d.cluster == nil {
		return
	}
	localAddr := d.cluster.LocalAddr()
	for _, addr := range d.cluster.MemberAddrs() {
		if addr == localAddr {
			continue
		}
		go func(target string) {
			if err := d.cluster.SendReliableMsg(target, data); err != nil {
				d.logger.Warn("failed to send reliable msg to peer",
					"addr", target,
					"error", err,
				)
			}
		}(addr)
	}
}

// sendToAllPeersViaChannel sends a ChannelMessage to all cluster peers over
// the persistent gRPC channel. Failures (e.g. no channel yet established to
// a given peer) are logged but never fail the caller, consistent with the
// "availability over consistency" principle applied to the legacy
// SendReliable path.
func (d *StateDelegate) sendToAllPeersViaChannel(msg *drlproto.ChannelMessage) {
	if d.cluster == nil {
		return
	}
	cm := d.cluster.GetChannelManager()
	if cm == nil {
		return
	}
	localAddr := d.cluster.LocalAddr()
	for _, addr := range d.cluster.MemberAddrs() {
		if addr == localAddr {
			continue
		}
		if err := cm.Send(addr, msg); err != nil {
			d.logger.Warn("failed to send persistent channel msg to peer",
				"addr", addr,
				"error", err,
			)
		}
	}
}

// handleChannelBlockWithExpiresAt applies a block event received over the
// persistent gRPC channel, reusing the same apply logic as the memberlist
// SendReliable path.
func (d *StateDelegate) handleChannelBlockWithExpiresAt(evt *drlproto.BlockEventWithExpiresAt) {
	d.handleBlockEventWithExpiresAt(evt)
}

// handleChannelUnblock applies an unblock event received over the
// persistent gRPC channel, reusing the same apply logic as the memberlist
// SendReliable path.
func (d *StateDelegate) handleChannelUnblock(evt *drlproto.UnblockEvent) {
	d.handleUnblockEvent(evt)
}

// SetHandover sets the handover handler for graceful state evacuation.
func (d *StateDelegate) SetHandover(h *Handover) {
	d.handover = h
}

// handleHandoverPayload delegates incoming handover payloads to the Handover handler.
func (d *StateDelegate) handleHandoverPayload(payload *drlproto.HandoverPayload) {
	if d.handover == nil {
		d.logger.Warn("received handover payload but no handover handler configured")
		return
	}
	d.handover.HandleIncoming(payload)
}

// markSyncComplete marks the state sync as complete and signals readiness
func (d *StateDelegate) markSyncComplete() {
	d.ready.Store(true)
	select {
	case d.syncComplete <- struct{}{}:
	default:
		// Channel already has a value or is closed
	}
}

// WaitForSync blocks until the initial state sync is complete or timeout is reached.
// Returns true if sync completed, false if timeout occurred.
func (d *StateDelegate) WaitForSync() bool {
	// If we're already ready, return immediately
	if d.ready.Load() {
		return true
	}

	d.logger.Info("waiting for state sync",
		"timeout", d.syncTimeout,
	)

	select {
	case <-d.syncComplete:
		return true
	case <-time.After(d.syncTimeout):
		d.logger.Warn("state sync timeout reached, proceeding without full sync",
			"timeout", d.syncTimeout,
		)
		// Mark as ready anyway after timeout
		d.ready.Store(true)
		return false
	}
}

// IsReady returns true if the initial state sync is complete
func (d *StateDelegate) IsReady() bool {
	return d.ready.Load()
}

// MarkReady marks the delegate as ready (for cases where we don't need sync)
func (d *StateDelegate) MarkReady() {
	d.markSyncComplete()
}

// SetBlocklist sets the blocklist cache reference
func (d *StateDelegate) SetBlocklist(blocklist *cache.BlocklistCache) {
	d.blocklist = blocklist
}

// GetBlocklist returns the blocklist cache reference
func (d *StateDelegate) GetBlocklist() *cache.BlocklistCache {
	return d.blocklist
}
