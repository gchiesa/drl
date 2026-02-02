package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBlocklistCache(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, bc)
	defer bc.Close()
}

func TestBlocklistCache_BlockAndCheck(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	ip := "192.168.1.1"
	ttl := 5 * time.Second

	// Initially not blocked
	assert.False(t, bc.IsBlocked(ip))

	// Block the IP
	bc.Block(ip, ttl)

	// Now should be blocked
	assert.True(t, bc.IsBlocked(ip))
}

func TestBlocklistCache_Unblock(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	ip := "192.168.1.1"
	ttl := 5 * time.Second

	// Block and verify
	bc.Block(ip, ttl)
	assert.True(t, bc.IsBlocked(ip))

	// Unblock and verify
	bc.Unblock(ip)
	assert.False(t, bc.IsBlocked(ip))
}

func TestBlocklistCache_TTLExpiration(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	ip := "192.168.1.1"
	ttl := 100 * time.Millisecond

	// Block with short TTL
	bc.Block(ip, ttl)
	assert.True(t, bc.IsBlocked(ip))

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Should no longer be blocked
	assert.False(t, bc.IsBlocked(ip))
}

func TestBlocklistCache_MultipleIPs(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	ips := []string{
		"192.168.1.1",
		"192.168.1.2",
		"10.0.0.1",
		"172.16.0.1",
	}

	// Block all IPs
	for _, ip := range ips {
		bc.Block(ip, 5*time.Second)
	}

	// All should be blocked
	for _, ip := range ips {
		assert.True(t, bc.IsBlocked(ip), "IP %s should be blocked", ip)
	}

	// Count should match
	assert.Equal(t, len(ips), bc.Count())
}

func TestBlocklistCache_GetState(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	// Block some IPs
	bc.Block("192.168.1.1", 5*time.Second)
	bc.Block("192.168.1.2", 5*time.Second)

	// Get state
	state, err := bc.GetState()
	require.NoError(t, err)
	assert.NotEmpty(t, state)
}

func TestBlocklistCache_MergeState(t *testing.T) {
	// Create source cache
	source, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer source.Close()

	// Create destination cache
	dest, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer dest.Close()

	// Block IPs in source
	source.Block("192.168.1.1", 5*time.Second)
	source.Block("192.168.1.2", 5*time.Second)

	// Get state from source
	state, err := source.GetState()
	require.NoError(t, err)

	// Merge into destination
	err = dest.MergeState(state)
	require.NoError(t, err)

	// Verify IPs are blocked in destination
	assert.True(t, dest.IsBlocked("192.168.1.1"))
	assert.True(t, dest.IsBlocked("192.168.1.2"))
}

func TestBlocklistCache_MergeState_Empty(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	// Merging empty data should not error
	err = bc.MergeState(nil)
	assert.NoError(t, err)

	err = bc.MergeState([]byte{})
	assert.NoError(t, err)
}

func TestBlocklistCache_MergeState_ExpiredEntries(t *testing.T) {
	// Create source cache
	source, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer source.Close()

	// Create destination cache
	dest, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer dest.Close()

	// Block IP with very short TTL in source
	source.Block("192.168.1.1", 50*time.Millisecond)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Get state (should filter expired entries)
	state, err := source.GetState()
	require.NoError(t, err)

	// Merge into destination
	err = dest.MergeState(state)
	require.NoError(t, err)

	// IP should not be blocked in destination (was expired in source)
	// Note: The source's GetState filters expired entries
	assert.False(t, dest.IsBlocked("192.168.1.1"))
}

func TestBlocklistCache_Clear(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	// Block some IPs
	bc.Block("192.168.1.1", 5*time.Second)
	bc.Block("192.168.1.2", 5*time.Second)

	assert.Equal(t, 2, bc.Count())

	// Clear all
	bc.Clear()

	// Should be empty
	assert.Equal(t, 0, bc.Count())
	assert.False(t, bc.IsBlocked("192.168.1.1"))
	assert.False(t, bc.IsBlocked("192.168.1.2"))
}

func TestBlocklistCache_Entries(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	// Block some IPs
	bc.Block("192.168.1.1", 5*time.Second)
	bc.Block("192.168.1.2", 5*time.Second)

	entries := bc.Entries()
	assert.Len(t, entries, 2)

	// Check that IPs are in entries
	ips := make(map[string]bool)
	for _, e := range entries {
		ips[e.IP] = true
	}
	assert.True(t, ips["192.168.1.1"])
	assert.True(t, ips["192.168.1.2"])
}

func TestBlocklistCache_MetricsCallbacks(t *testing.T) {
	var hits, misses, evictions int
	var mu sync.Mutex

	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
		OnHit: func() {
			mu.Lock()
			hits++
			mu.Unlock()
		},
		OnMiss: func() {
			mu.Lock()
			misses++
			mu.Unlock()
		},
		OnEvict: func() {
			mu.Lock()
			evictions++
			mu.Unlock()
		},
	})
	require.NoError(t, err)
	defer bc.Close()

	// Miss
	bc.IsBlocked("192.168.1.1")
	mu.Lock()
	assert.Equal(t, 1, misses)
	mu.Unlock()

	// Block and hit
	bc.Block("192.168.1.1", 5*time.Second)
	bc.IsBlocked("192.168.1.1")
	mu.Lock()
	assert.Equal(t, 1, hits)
	mu.Unlock()
}

func TestBlocklistCache_ConcurrentAccess(t *testing.T) {
	bc, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: 1,
	})
	require.NoError(t, err)
	defer bc.Close()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent blocks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := "192.168.1." + string(rune('0'+i%10))
			bc.Block(ip, 5*time.Second)
		}(i)
	}

	// Concurrent checks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ip := "192.168.1." + string(rune('0'+i%10))
			bc.IsBlocked(ip)
		}(i)
	}

	wg.Wait()
}
