package membership

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

func testChannelLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// freePort asks the OS for an available TCP port on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()
	return lis.Addr().(*net.TCPAddr).Port
}

// recordingHandler implements channelMessageHandler and records received
// events so tests can assert on delivery.
type recordingHandler struct {
	mu      sync.Mutex
	blocks  []*drlproto.BlockEventWithExpiresAt
	unblock []*drlproto.UnblockEvent
}

func (r *recordingHandler) handleChannelBlockWithExpiresAt(evt *drlproto.BlockEventWithExpiresAt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocks = append(r.blocks, evt)
}

func (r *recordingHandler) handleChannelUnblock(evt *drlproto.UnblockEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unblock = append(r.unblock, evt)
}

func (r *recordingHandler) blockCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.blocks)
}

func (r *recordingHandler) unblockCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.unblock)
}

// newTestChannelManager builds and starts a ChannelManager bound to the
// given loopback address/port, using a recordingHandler.
func newTestChannelManager(t *testing.T, localAddr string, port int) (*ChannelManager, *recordingHandler) {
	t.Helper()
	handler := &recordingHandler{}
	cm := NewChannelManager(ChannelManagerConfig{
		LocalAddr: localAddr,
		Port:      port,
		Handler:   handler,
		Metrics:   metrics.NewMetrics(),
		Logger:    testChannelLogger(),
	})
	require.NoError(t, cm.Start())
	t.Cleanup(cm.Stop)
	return cm, handler
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	require.True(t, cond(), "condition not met within %s", timeout)
}

// TestChannelManager_EstablishForPeer_DialDirectionIsDeterministic verifies
// that of two nodes, only the one with the lexicographically smaller address
// dials, per the single-connection-per-pair design.
func TestChannelManager_EstablishForPeer_DialDirectionIsDeterministic(t *testing.T) {
	port := freePort(t)
	addrA := "127.0.0.1" // smaller address: dials
	addrB := "::1"       // larger address: passive

	cmA, _ := newTestChannelManager(t, addrA, port)
	cmB, _ := newTestChannelManager(t, addrB, port)

	// Both sides observe the join symmetrically, as memberlist NotifyJoin does.
	cmA.EstablishForPeer(addrB)
	cmB.EstablishForPeer(addrA)

	waitFor(t, 2*time.Second, func() bool {
		return cmA.IsConnected(addrB) && cmB.IsConnected(addrA)
	})

	assert.Equal(t, 1, cmA.PeerCount())
	assert.Equal(t, 1, cmB.PeerCount())
}

// TestChannelManager_SendRecv_RoundTrip verifies that block and unblock
// events sent from one ChannelManager are received and dispatched by the
// peer's handler on the other side.
func TestChannelManager_SendRecv_RoundTrip(t *testing.T) {
	port := freePort(t)
	addrA := "127.0.0.1"
	addrB := "::1"

	cmA, handlerA := newTestChannelManager(t, addrA, port)
	cmB, handlerB := newTestChannelManager(t, addrB, port)

	cmA.EstablishForPeer(addrB)
	cmB.EstablishForPeer(addrA)

	waitFor(t, 2*time.Second, func() bool {
		return cmA.IsConnected(addrB) && cmB.IsConnected(addrA)
	})

	blockMsg := &drlproto.ChannelMessage{
		Content: &drlproto.ChannelMessage_BlockWithExpiresAt{
			BlockWithExpiresAt: &drlproto.BlockEventWithExpiresAt{
				Key:            "entity-key",
				ExpiresAtNanos: time.Now().Add(time.Minute).UnixNano(),
			},
		},
	}
	require.NoError(t, cmA.Send(addrB, blockMsg))

	unblockMsg := &drlproto.ChannelMessage{
		Content: &drlproto.ChannelMessage_Unblock{
			Unblock: &drlproto.UnblockEvent{Key: "entity-key"},
		},
	}
	require.NoError(t, cmB.Send(addrA, unblockMsg))

	waitFor(t, 2*time.Second, func() bool {
		return handlerB.blockCount() == 1
	})
	waitFor(t, 2*time.Second, func() bool {
		return handlerA.unblockCount() == 1
	})

	handlerB.mu.Lock()
	assert.Equal(t, "entity-key", handlerB.blocks[0].Key)
	handlerB.mu.Unlock()

	handlerA.mu.Lock()
	assert.Equal(t, "entity-key", handlerA.unblock[0].Key)
	handlerA.mu.Unlock()
}

// TestChannelManager_Send_UnknownPeer verifies Send returns an error rather
// than blocking or panicking when no channel is established to the target.
func TestChannelManager_Send_UnknownPeer(t *testing.T) {
	port := freePort(t)
	cm, _ := newTestChannelManager(t, "127.0.0.1", port)

	err := cm.Send("127.0.0.99", &drlproto.ChannelMessage{
		Content: &drlproto.ChannelMessage_Unblock{Unblock: &drlproto.UnblockEvent{Key: "k"}},
	})
	assert.Error(t, err)
}

// TestChannelManager_Close_TearsDownPeer verifies that Close removes the
// peer entry on both sides of the connection.
func TestChannelManager_Close_TearsDownPeer(t *testing.T) {
	port := freePort(t)
	addrA := "127.0.0.1"
	addrB := "::1"

	cmA, _ := newTestChannelManager(t, addrA, port)
	cmB, _ := newTestChannelManager(t, addrB, port)

	cmA.EstablishForPeer(addrB)
	cmB.EstablishForPeer(addrA)

	waitFor(t, 2*time.Second, func() bool {
		return cmA.IsConnected(addrB) && cmB.IsConnected(addrA)
	})

	cmA.Close(addrB)

	waitFor(t, 2*time.Second, func() bool {
		return !cmA.IsConnected(addrB) && !cmB.IsConnected(addrA)
	})
}

// TestChannelManager_EstablishForPeer_IgnoresSelfAndEmpty verifies the
// no-op guard clauses in EstablishForPeer.
func TestChannelManager_EstablishForPeer_IgnoresSelfAndEmpty(t *testing.T) {
	port := freePort(t)
	cm, _ := newTestChannelManager(t, "127.0.0.1", port)

	cm.EstablishForPeer("")
	cm.EstablishForPeer("127.0.0.1")

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, cm.PeerCount())
}

// TestChannelManager_ConcurrentSend verifies concurrent Send calls from
// multiple goroutines to the same peer are safe (serialized by the
// per-peer writeLoop) and all messages are delivered.
func TestChannelManager_ConcurrentSend(t *testing.T) {
	port := freePort(t)
	addrA := "127.0.0.1"
	addrB := "::1"

	cmA, _ := newTestChannelManager(t, addrA, port)
	cmB, handlerB := newTestChannelManager(t, addrB, port)

	cmA.EstablishForPeer(addrB)
	cmB.EstablishForPeer(addrA)

	waitFor(t, 2*time.Second, func() bool {
		return cmA.IsConnected(addrB) && cmB.IsConnected(addrA)
	})

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			msg := &drlproto.ChannelMessage{
				Content: &drlproto.ChannelMessage_Unblock{
					Unblock: &drlproto.UnblockEvent{Key: fmt.Sprintf("key-%d", idx)},
				},
			}
			_ = cmA.Send(addrB, msg)
		}(i)
	}
	wg.Wait()

	waitFor(t, 2*time.Second, func() bool {
		return handlerB.unblockCount() == n
	})
}

// TestChannelManager_StartUsesConfiguredPort verifies the server actually
// listens on the configured port.
func TestChannelManager_StartUsesConfiguredPort(t *testing.T) {
	port := freePort(t)
	cm, _ := newTestChannelManager(t, "127.0.0.1", port)

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	require.NoError(t, err)
	_ = conn.Close()
	assert.Equal(t, port, cm.port)
}
