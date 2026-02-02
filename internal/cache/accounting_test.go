package cache

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAccountingCache(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	require.NotNil(t, ac)
	defer ac.Close()
}

func TestAccountingCache_Increment(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	ip := "192.168.1.1"

	// First increment
	count := ac.Increment(ip)
	assert.Equal(t, int64(1), count)

	// Second increment
	count = ac.Increment(ip)
	assert.Equal(t, int64(2), count)

	// Third increment
	count = ac.Increment(ip)
	assert.Equal(t, int64(3), count)
}

func TestAccountingCache_Get(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	ip := "192.168.1.1"

	// Initially zero
	assert.Equal(t, int64(0), ac.Get(ip))

	// After increments
	ac.Increment(ip)
	ac.Increment(ip)
	assert.Equal(t, int64(2), ac.Get(ip))
}

func TestAccountingCache_Reset(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	ip := "192.168.1.1"

	// Increment some
	ac.Increment(ip)
	ac.Increment(ip)
	assert.Equal(t, int64(2), ac.Get(ip))

	// Reset
	ac.Reset(ip)
	assert.Equal(t, int64(0), ac.Get(ip))
}

func TestAccountingCache_WindowExpiration(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	defer ac.Close()

	ip := "192.168.1.1"

	// Increment
	ac.Increment(ip)
	assert.Equal(t, int64(1), ac.Get(ip))

	// Wait for window to expire
	time.Sleep(200 * time.Millisecond)

	// Counter should be reset (entry expired)
	assert.Equal(t, int64(0), ac.Get(ip))
}

func TestAccountingCache_HashRing(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	// Add some nodes
	ac.AddNode("node2")
	ac.AddNode("node3")

	nodes := ac.GetNodes()
	assert.Len(t, nodes, 3)
	assert.Contains(t, nodes, "node1")
	assert.Contains(t, nodes, "node2")
	assert.Contains(t, nodes, "node3")
}

func TestAccountingCache_RemoveNode(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	// Add and remove nodes
	ac.AddNode("node2")
	ac.AddNode("node3")
	assert.Len(t, ac.GetNodes(), 3)

	ac.RemoveNode("node2")
	nodes := ac.GetNodes()
	assert.Len(t, nodes, 2)
	assert.NotContains(t, nodes, "node2")
}

func TestAccountingCache_UpdateNodes(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	// Add initial nodes
	ac.AddNode("node2")
	ac.AddNode("node3")

	// Update with new set
	ac.UpdateNodes([]string{"node1", "node4", "node5"})

	nodes := ac.GetNodes()
	assert.Len(t, nodes, 3)
	assert.Contains(t, nodes, "node1")
	assert.Contains(t, nodes, "node4")
	assert.Contains(t, nodes, "node5")
	assert.NotContains(t, nodes, "node2")
	assert.NotContains(t, nodes, "node3")
}

func TestAccountingCache_IsOwner(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	// With only local node, should own everything
	assert.True(t, ac.IsOwner("192.168.1.1"))
	assert.True(t, ac.IsOwner("10.0.0.1"))

	// Add more nodes
	ac.AddNode("node2")
	ac.AddNode("node3")

	// Check ownership - some IPs should be owned by other nodes
	// We can't predict which IPs will be owned by which node,
	// but GetOwner should return consistent results
	ip := "192.168.1.1"
	owner := ac.GetOwner(ip)
	assert.Contains(t, []string{"node1", "node2", "node3"}, owner)
}

func TestAccountingCache_GetOwner(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	ac.AddNode("node2")
	ac.AddNode("node3")

	// Same IP should always return same owner
	ip := "192.168.1.1"
	owner1 := ac.GetOwner(ip)
	owner2 := ac.GetOwner(ip)
	assert.Equal(t, owner1, owner2)
}

func TestAccountingCache_SetLocalNode(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	assert.Equal(t, "node1", ac.LocalNode())

	// Change local node
	ac.SetLocalNode("new-node")
	assert.Equal(t, "new-node", ac.LocalNode())

	// Old node should be removed from ring
	nodes := ac.GetNodes()
	assert.NotContains(t, nodes, "node1")
	assert.Contains(t, nodes, "new-node")
}

func TestAccountingCache_MetricsCallbacks(t *testing.T) {
	var hits, misses, evictions int
	var mu sync.Mutex

	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
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
	defer ac.Close()

	ip := "192.168.1.1"

	// First increment should be a miss (create new counter)
	ac.Increment(ip)
	mu.Lock()
	assert.Equal(t, 1, misses)
	mu.Unlock()

	// Second increment should be a hit
	ac.Increment(ip)
	mu.Lock()
	assert.Equal(t, 1, hits)
	mu.Unlock()
}

func TestAccountingCache_ConcurrentIncrement(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	// Initialize the counter first to avoid race conditions during creation
	ip := "192.168.1.1"
	ac.Increment(ip)
	initialCount := ac.Get(ip)

	numGoroutines := 100

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ac.Increment(ip)
		}()
	}
	wg.Wait()

	// Verify all increments were counted (starting from initial count)
	finalCount := ac.Get(ip)
	assert.Equal(t, initialCount+int64(numGoroutines), finalCount)
}

func TestAccountingCache_ConsistentHashingDistribution(t *testing.T) {
	ac, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	defer ac.Close()

	// Add multiple nodes
	ac.AddNode("node2")
	ac.AddNode("node3")
	ac.AddNode("node4")
	ac.AddNode("node5")

	// Count ownership distribution
	ownership := make(map[string]int)
	numIPs := 1000
	for i := 0; i < numIPs; i++ {
		ip := "192.168." + string(rune(i/256)) + "." + string(rune(i%256))
		owner := ac.GetOwner(ip)
		ownership[owner]++
	}

	// Each node should have some IPs (roughly equal distribution)
	// With 5 nodes and 1000 IPs, each should have roughly 200
	for node, count := range ownership {
		// Allow for some variance (50-350 per node)
		assert.Greater(t, count, 50, "Node %s has too few IPs: %d", node, count)
		assert.Less(t, count, 350, "Node %s has too many IPs: %d", node, count)
	}
}
