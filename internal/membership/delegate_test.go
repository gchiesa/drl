package membership

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gchiesa/drl/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

func TestNewStateDelegate(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})
	require.NotNil(t, delegate)
}

func TestStateDelegate_NodeMeta(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// NodeMeta should return nil (we don't use metadata)
	meta := delegate.NodeMeta(100)
	assert.Nil(t, meta)
}

func TestStateDelegate_NotifyMsg_EmptyBuffer(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{SyncTimeout: 30 * time.Second})
	// Must not panic even with a nil blocklist
	assert.NotPanics(t, func() { delegate.NotifyMsg(nil) })
	assert.NotPanics(t, func() { delegate.NotifyMsg([]byte{}) })
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
	assert.NotPanics(t, func() { delegate.NotifyMsg([]byte("not-protobuf-garbage")) })
	assert.Equal(t, 0, bc.Count())
}

func TestStateDelegate_NotifyMsg_AccountingBatch(t *testing.T) {
	ac, err := cache.NewAccountingCache(cache.AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "test-node",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	m := metrics.NewMetrics()

	delegate := NewStateDelegate(DelegateConfig{
		Accounting:  ac,
		Metrics:     m,
		SyncTimeout: 30 * time.Second,
	})

	// Build a DrlMessage with CounterBatch
	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Counters{
			Counters: &drlproto.CounterBatch{
				SenderId:  99999,
				Timestamp: uint64(time.Now().UnixMilli()),
				Entries: []*drlproto.CounterEntry{
					{EntityHash: 0xaabbccdd, Hits: 5},
					{EntityHash: 0x11223344, Hits: 3},
				},
			},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	delegate.NotifyMsg(data)

	// Verify increments were applied
	assert.Equal(t, int64(5), ac.Get("00000000aabbccdd"))
	assert.Equal(t, int64(3), ac.Get("0000000011223344"))
}

func TestStateDelegate_NotifyMsg_BlockEvent(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	// Build a DrlMessage with BlockEvent
	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Block{
			Block: &drlproto.BlockEvent{
				Key:        "testkey001",
				TtlNanos:   int64(10 * time.Minute),
				EntityIp:   "10.0.0.1",
				EntityPath: "/api/v1",
				EntityHdrs: map[string]string{"X-Bot": "true"},
			},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	delegate.NotifyMsg(data)

	assert.True(t, bc.IsBlocked("testkey001"))

	// Verify entity metadata
	entries := bc.ListEntries()
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].Entity)
	assert.Equal(t, "10.0.0.1", entries[0].Entity.IP)
	assert.Equal(t, "/api/v1", entries[0].Entity.Path)
	assert.Equal(t, map[string]string{"X-Bot": "true"}, entries[0].Entity.Headers)
}

func TestStateDelegate_NotifyMsg_BlockEventWithExpiresAt(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	expiresAt := time.Now().Add(10 * time.Minute)

	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_BlockWithExpiresAt{
			BlockWithExpiresAt: &drlproto.BlockEventWithExpiresAt{
				Key:            "testkey003",
				ExpiresAtNanos: expiresAt.UnixNano(),
				EntityIp:       "10.0.0.2",
				EntityPath:     "/api/v2",
				EntityHdrs:     map[string]string{"X-Bot": "true"},
			},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	delegate.NotifyMsg(data)

	assert.True(t, bc.IsBlocked("testkey003"))

	entries := bc.ListEntries()
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].Entity)
	assert.Equal(t, "10.0.0.2", entries[0].Entity.IP)
	assert.Equal(t, "/api/v2", entries[0].Entity.Path)
	assert.Equal(t, map[string]string{"X-Bot": "true"}, entries[0].Entity.Headers)
	// ExpiresAt should be within 1 ms of the sent timestamp
	assert.WithinDuration(t, expiresAt, entries[0].ExpiresAt, time.Millisecond)
}

func TestStateDelegate_NotifyMsg_UnblockEvent(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	const key = "testkey002"
	bc.Block(key, time.Hour, nil)
	require.True(t, bc.IsBlocked(key))

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	// Build a DrlMessage with UnblockEvent
	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Unblock{
			Unblock: &drlproto.UnblockEvent{Key: key},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	delegate.NotifyMsg(data)

	assert.False(t, bc.IsBlocked(key))
}

func TestStateDelegate_GetBroadcasts_EmptyQueue(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// Broadcast queue is no longer used; should return nil
	broadcasts := delegate.GetBroadcasts(10, 100)
	assert.Nil(t, broadcasts)
}

func TestStateDelegate_LocalState(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	// Add some blocked IPs
	bc.Block("192.168.1.1", 5*time.Second, nil)
	bc.Block("192.168.1.2", 5*time.Second, nil)

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	// Get local state
	state := delegate.LocalState(false)
	assert.NotEmpty(t, state)

	// LocalState with join=true should also work
	stateOnJoin := delegate.LocalState(true)
	assert.NotEmpty(t, stateOnJoin)
}

func TestStateDelegate_LocalState_NilBlocklist(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// LocalState should return nil when blocklist is nil
	state := delegate.LocalState(false)
	assert.Nil(t, state)
}

func TestStateDelegate_MergeRemoteState(t *testing.T) {
	// Create source blocklist and populate it
	sourceBC, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer sourceBC.Close()

	e1 := model.Entity{
		IP:      "192.168.1.1",
		Path:    "/",
		Headers: nil,
	}
	e2 := model.Entity{
		IP:      "192.168.1.2",
		Path:    "/",
		Headers: nil,
	}
	sourceBC.Block(e1.Key(), 5*time.Second, &e1)
	sourceBC.Block(e2.Key(), 5*time.Second, &e2)

	// Get state from source
	state, err := sourceBC.GetState()
	require.NoError(t, err)

	// Create destination blocklist
	destBC, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer destBC.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   destBC,
		SyncTimeout: 30 * time.Second,
	})

	// Merge state
	delegate.MergeRemoteState(state, true)

	// Verify IPs are blocked in destination
	assert.True(t, destBC.IsBlocked(e1.Key()))
	assert.True(t, destBC.IsBlocked(e2.Key()))

	// Delegate should be ready
	assert.True(t, delegate.IsReady())
}

func TestStateDelegate_MergeRemoteState_NilBlocklist(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// MergeRemoteState should not panic when blocklist is nil
	delegate.MergeRemoteState([]byte("test"), false)
}

func TestStateDelegate_WaitForSync_AlreadyReady(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	// Mark as ready
	delegate.MarkReady()

	// WaitForSync should return immediately
	start := time.Now()
	result := delegate.WaitForSync()
	elapsed := time.Since(start)

	assert.True(t, result)
	assert.Less(t, elapsed, 100*time.Millisecond)
}

func TestStateDelegate_WaitForSync_Timeout(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 100 * time.Millisecond, // Short timeout for test
	})

	// WaitForSync should timeout
	start := time.Now()
	result := delegate.WaitForSync()
	elapsed := time.Since(start)

	assert.False(t, result) // Timeout returns false
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond)
	assert.True(t, delegate.IsReady()) // Should be marked ready after timeout
}

func TestStateDelegate_WaitForSync_SignalledCompletion(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 5 * time.Second,
	})

	// Signal completion in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		delegate.MarkReady()
	}()

	// WaitForSync should complete when signalled
	start := time.Now()
	result := delegate.WaitForSync()
	elapsed := time.Since(start)

	assert.True(t, result)
	assert.Less(t, elapsed, 1*time.Second)
}

func TestStateDelegate_IsReady(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// Initially not ready
	assert.False(t, delegate.IsReady())

	// After marking ready
	delegate.MarkReady()
	assert.True(t, delegate.IsReady())
}

func TestStateDelegate_SetBlocklist(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	assert.Nil(t, delegate.GetBlocklist())

	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	delegate.SetBlocklist(bc)
	assert.NotNil(t, delegate.GetBlocklist())
}

func TestStateDelegate_SyncDurationMetric(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	m := metrics.NewMetrics()

	// We can't easily check the histogram value, but we can verify no panic
	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		Metrics:     m,
		SyncTimeout: 30 * time.Second,
	})

	// Get state and merge to trigger metrics
	sourceBC, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer sourceBC.Close()

	sourceBC.Block("192.168.1.1", 5*time.Second, nil)
	state, err := sourceBC.GetState()
	require.NoError(t, err)

	delegate.MergeRemoteState(state, true)
}

func TestStateDelegate_ConcurrentAccess(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	var done atomic.Bool
	go func() {
		for !done.Load() {
			delegate.LocalState(false)
			time.Sleep(time.Millisecond)
		}
	}()

	go func() {
		for !done.Load() {
			delegate.MergeRemoteState([]byte{}, false)
			time.Sleep(time.Millisecond)
		}
	}()

	go func() {
		for !done.Load() {
			delegate.IsReady()
			time.Sleep(time.Millisecond)
		}
	}()

	// Run for a short time
	time.Sleep(100 * time.Millisecond)
	done.Store(true)
}
