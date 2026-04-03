package membership

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

func TestDrlMessage_BlockEventRoundTrip(t *testing.T) {
	original := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Block{
			Block: &drlproto.BlockEvent{
				Key:        "abc123",
				TtlNanos:   int64(24 * time.Hour),
				EntityIp:   "10.0.0.1",
				EntityPath: "/api/v1",
				EntityHdrs: map[string]string{"X-Bot": "true"},
			},
		},
	}

	data, err := proto.Marshal(original)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	decoded := &drlproto.DrlMessage{}
	require.NoError(t, proto.Unmarshal(data, decoded))

	block := decoded.GetBlock()
	require.NotNil(t, block)
	assert.Equal(t, "abc123", block.Key)
	assert.Equal(t, int64(24*time.Hour), block.TtlNanos)
	assert.Equal(t, "10.0.0.1", block.EntityIp)
	assert.Equal(t, "/api/v1", block.EntityPath)
	assert.Equal(t, map[string]string{"X-Bot": "true"}, block.EntityHdrs)
}

func TestDrlMessage_UnblockEventRoundTrip(t *testing.T) {
	original := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Unblock{
			Unblock: &drlproto.UnblockEvent{Key: "deadbeef"},
		},
	}

	data, err := proto.Marshal(original)
	require.NoError(t, err)

	decoded := &drlproto.DrlMessage{}
	require.NoError(t, proto.Unmarshal(data, decoded))

	unblock := decoded.GetUnblock()
	require.NotNil(t, unblock)
	assert.Equal(t, "deadbeef", unblock.Key)
}

func TestDrlMessage_CounterBatchRoundTrip(t *testing.T) {
	original := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Counters{
			Counters: &drlproto.CounterBatch{
				SenderId:  42,
				Timestamp: 1234567890,
				Entries: []*drlproto.CounterEntry{
					{EntityHash: 0xdeadbeef, Hits: 10},
				},
			},
		},
	}

	data, err := proto.Marshal(original)
	require.NoError(t, err)

	decoded := &drlproto.DrlMessage{}
	require.NoError(t, proto.Unmarshal(data, decoded))

	counters := decoded.GetCounters()
	require.NotNil(t, counters)
	assert.Equal(t, uint64(42), counters.SenderId)
	require.Len(t, counters.Entries, 1)
	assert.Equal(t, uint64(0xdeadbeef), counters.Entries[0].EntityHash)
}

func TestStateDelegate_BlockEvent_AppliedViaNotifyMsg(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	const key = "testEntityKey001"
	const ttl = 10 * time.Minute

	// Build a DrlMessage with BlockEvent
	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Block{
			Block: &drlproto.BlockEvent{
				Key:        key,
				TtlNanos:   int64(ttl),
				EntityIp:   "10.0.0.1",
				EntityPath: "api/v1",
			},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	delegate.NotifyMsg(data)

	assert.True(t, bc.IsBlocked(key), "entity must appear in the blocklist after NotifyMsg")
}

func TestBlocklistBroadcast_MessageAndFinished(t *testing.T) {
	data := []byte("payload")
	b := &blocklistBroadcast{data: data}

	assert.Equal(t, data, b.Message())
	assert.False(t, b.Invalidates(&blocklistBroadcast{}))
	assert.NotPanics(t, func() { b.Finished() })
}

func TestStateDelegate_QueueBlockEvent_NoCluster(t *testing.T) {
	bc, err := cache.NewBlocklistCache(cache.BlocklistConfig{MaxSizeMB: 1})
	require.NoError(t, err)
	defer bc.Close()

	// Delegate without a cluster — QueueBlockEvent should not panic
	delegate := NewStateDelegate(DelegateConfig{
		Blocklist:   bc,
		SyncTimeout: 30 * time.Second,
	})

	// Should not panic even without cluster (sends are no-ops)
	assert.NotPanics(t, func() {
		_ = delegate.QueueBlockEvent("key1", time.Hour, nil)
	})
}

func TestStateDelegate_UnblockEvent_AppliedViaNotifyMsg(t *testing.T) {
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

	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Unblock{
			Unblock: &drlproto.UnblockEvent{Key: key},
		},
	}
	data, err := proto.Marshal(msg)
	require.NoError(t, err)

	delegate.NotifyMsg(data)

	assert.False(t, bc.IsBlocked(key), "entity must be absent from blocklist after unblock NotifyMsg")
}
