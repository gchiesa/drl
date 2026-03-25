package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	m, err := NewManager(ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        "node1",
		WindowSize:       time.Minute,
	})
	require.NoError(t, err)
	require.NotNil(t, m)
	require.NotNil(t, m.Blocklist)
	require.NotNil(t, m.Accounting)
	defer m.Close()
}

func TestManager_UpdateNodes(t *testing.T) {
	m, err := NewManager(ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        "node1",
		WindowSize:       time.Minute,
	})
	require.NoError(t, err)
	defer m.Close()

	// Update nodes
	m.UpdateNodes([]string{"node1", "node2", "node3"})

	// Verify nodes are set in accounting cache
	nodes := m.Accounting.GetNodes()
	assert.Len(t, nodes, 3)
}

func TestManager_SetLocalNode(t *testing.T) {
	m, err := NewManager(ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        "node1",
		WindowSize:       time.Minute,
	})
	require.NoError(t, err)
	defer m.Close()

	assert.Equal(t, "node1", m.Accounting.LocalNode())

	m.SetLocalNode("new-node")
	assert.Equal(t, "new-node", m.Accounting.LocalNode())
}

func TestManager_Close(t *testing.T) {
	m, err := NewManager(ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        "node1",
		WindowSize:       time.Minute,
	})
	require.NoError(t, err)

	// Close should not panic
	m.Close()

	// Double close should also not panic
	m.Close()
}

func TestManager_MetricsCallbacks(t *testing.T) {
	var blocklistHits, blocklistMisses int
	var accountingHits, accountingMisses int

	m, err := NewManager(ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        "node1",
		WindowSize:       time.Minute,
		OnBlocklistHit:   func() { blocklistHits++ },
		OnBlocklistMiss:  func() { blocklistMisses++ },
		OnAccountingHit:  func() { accountingHits++ },
		OnAccountingMiss: func() { accountingMisses++ },
	})
	require.NoError(t, err)
	defer m.Close()

	// Test blocklist callbacks
	m.Blocklist.IsBlocked("192.168.1.1") // miss
	m.Blocklist.Block("192.168.1.1", nil, time.Second)
	m.Blocklist.IsBlocked("192.168.1.1") // hit

	assert.Equal(t, 1, blocklistHits)
	assert.Equal(t, 1, blocklistMisses)

	// Test accounting callbacks
	m.Accounting.Increment("10.0.0.1", 1) // miss (create)
	m.Accounting.Increment("10.0.0.1", 1) // hit

	assert.Equal(t, 1, accountingHits)
	assert.Equal(t, 1, accountingMisses)
}

func TestManager_Integration(t *testing.T) {
	m, err := NewManager(ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        "node1",
		WindowSize:       time.Minute,
	})
	require.NoError(t, err)
	defer m.Close()

	// Simulate rate limiting workflow
	ip := "192.168.1.100"
	rateLimit := int64(5)

	// IP should not be blocked initially
	assert.False(t, m.Blocklist.IsBlocked(ip))

	// Increment request count
	for i := int64(0); i < rateLimit; i++ {
		count := m.Accounting.Increment(ip, 1)
		assert.Equal(t, i+1, count)
	}

	// At this point, count equals rate limit
	count := m.Accounting.Get(ip)
	assert.Equal(t, rateLimit, count)

	// One more request exceeds limit - block the IP
	count = m.Accounting.Increment(ip, 1)
	if count > rateLimit {
		m.Blocklist.Block(ip, nil, time.Minute)
	}

	// IP should now be blocked
	assert.True(t, m.Blocklist.IsBlocked(ip))
}
