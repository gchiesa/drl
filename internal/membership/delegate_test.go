package membership

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
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

func TestStateDelegate_NotifyMsg(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// NotifyMsg should not panic
	delegate.NotifyMsg([]byte("test"))
}

func TestStateDelegate_GetBroadcasts(t *testing.T) {
	delegate := NewStateDelegate(DelegateConfig{
		SyncTimeout: 30 * time.Second,
	})

	// GetBroadcasts should return nil (we don't use broadcasts)
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
	bc.Block("192.168.1.1", 5*time.Second)
	bc.Block("192.168.1.2", 5*time.Second)

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

	sourceBC.Block("192.168.1.1", 5*time.Second)
	sourceBC.Block("192.168.1.2", 5*time.Second)

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
	assert.True(t, destBC.IsBlocked("192.168.1.1"))
	assert.True(t, destBC.IsBlocked("192.168.1.2"))

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

	sourceBC.Block("192.168.1.1", 5*time.Second)
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
