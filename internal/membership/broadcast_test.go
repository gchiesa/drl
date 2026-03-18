package membership

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/model"
)

func TestBroadcastEvent_EncodeDecodeRoundTrip(t *testing.T) {
	original := BroadcastEvent{
		Type: BroadcastEventBlock,
		Key:  "abc123",
		TTL:  24 * time.Hour,
	}

	data, err := encodeBroadcastEvent(original)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	decoded, err := decodeBroadcastEvent(data)
	require.NoError(t, err)
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.Key, decoded.Key)
	assert.Equal(t, original.TTL, decoded.TTL)
}

func TestBroadcastEvent_UnblockRoundTrip(t *testing.T) {
	original := BroadcastEvent{
		Type: BroadcastEventUnblock,
		Key:  "deadbeef",
	}

	data, err := encodeBroadcastEvent(original)
	require.NoError(t, err)

	decoded, err := decodeBroadcastEvent(data)
	require.NoError(t, err)
	assert.Equal(t, BroadcastEventUnblock, decoded.Type)
	assert.Equal(t, original.Key, decoded.Key)
	assert.Zero(t, decoded.TTL)
}

func TestBlocklistBroadcast_MessageAndFinished(t *testing.T) {
	data := []byte("payload")
	b := &blocklistBroadcast{data: data}

	assert.Equal(t, data, b.Message())
	assert.False(t, b.Invalidates(&blocklistBroadcast{}))
	// Finished must not panic
	assert.NotPanics(t, func() { b.Finished() })
}

// --- StateDelegate broadcast integration tests ---

func TestStateDelegate_QueueBlockEvent_AppliedViaNotifyMsg(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	const key = "testEntityKey001"
	const ttl = 10 * time.Minute

	// Queue a block event
	entity := &model.Entity{IP: "10.0.0.1", Path: "api/v1"}
	require.NoError(t, delegate.QueueBlockEvent(key, ttl, entity))

	// Retrieve the broadcast from the queue
	broadcasts := delegate.GetBroadcasts(0, 65535)
	require.Len(t, broadcasts, 1, "one broadcast must be pending")

	// Simulate a peer receiving the message via NotifyMsg
	remotePeerDelegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})
	remotePeerDelegate.NotifyMsg(broadcasts[0])

	assert.True(t, bc.IsBlocked(key), "entity must appear in the blocklist after NotifyMsg")
}

func TestStateDelegate_QueueBlockEvent_PropagatesEntityMetadata(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	entity := &model.Entity{
		IP:      "10.0.0.1",
		Path:    "api/v1/payments",
		Headers: map[string]string{"X-Bot": "true"},
	}
	key := entity.Key()

	require.NoError(t, delegate.QueueBlockEvent(key, 10*time.Minute, entity))

	broadcasts := delegate.GetBroadcasts(0, 65535)
	require.Len(t, broadcasts, 1)

	// Simulate receiving the broadcast on a peer node
	peerBC, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer peerBC.Close()

	peerDelegate := NewStateDelegate(DelegateConfig{
		Blocklist:   peerBC,
		SyncTimeout: 30 * time.Second,
	})
	peerDelegate.NotifyMsg(broadcasts[0])

	assert.True(t, peerBC.IsBlocked(key))

	// Verify entity metadata was propagated
	entries := peerBC.ListEntries()
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].Entity, "entity metadata must be propagated via broadcast")
	assert.Equal(t, "10.0.0.1", entries[0].Entity.IP)
	assert.Equal(t, "api/v1/payments", entries[0].Entity.Path)
	assert.Equal(t, map[string]string{"X-Bot": "true"}, entries[0].Entity.Headers)
}

func TestStateDelegate_QueueUnblockEvent_AppliedViaNotifyMsg(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	const key = "testEntityKey002"
	bc.Block(key, nil, time.Hour)
	require.True(t, bc.IsBlocked(key))

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	require.NoError(t, delegate.QueueUnblockEvent(key))

	broadcasts := delegate.GetBroadcasts(0, 65535)
	require.Len(t, broadcasts, 1)

	delegate.NotifyMsg(broadcasts[0])

	assert.False(t, bc.IsBlocked(key), "entity must be absent from blocklist after unblock NotifyMsg")
}

func TestStateDelegate_NotifyMsg_InvalidData(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	// Garbage data must not panic and must not alter the blocklist
	assert.NotPanics(t, func() { delegate.NotifyMsg([]byte("not-msgpack-garbage")) })
	assert.Equal(t, 0, bc.Count())
}

func TestStateDelegate_NotifyMsg_EmptyBuffer(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{SyncTimeout: 30 * time.Second})
	// Must not panic even with a nil blocklist
	assert.NotPanics(t, func() { delegate.NotifyMsg(nil) })
	assert.NotPanics(t, func() { delegate.NotifyMsg([]byte{}) })
}

func TestStateDelegate_GetBroadcasts_NoPendingEvents(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{SyncTimeout: 30 * time.Second})
	broadcasts := delegate.GetBroadcasts(10, 100)
	// Empty queue returns nil
	assert.Nil(t, broadcasts)
}

func TestStateDelegate_QueueBlockEvent_MultipleEvents(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:    bc,
		SyncTimeout:  30 * time.Second,
		NumNodesFunc: func() int { return 3 },
	})

	require.NoError(t, delegate.QueueBlockEvent("key1", time.Hour, nil))
	require.NoError(t, delegate.QueueBlockEvent("key2", time.Hour, nil))

	broadcasts := delegate.GetBroadcasts(0, 65535)
	assert.GreaterOrEqual(t, len(broadcasts), 1, "at least one broadcast must be returned")
}
