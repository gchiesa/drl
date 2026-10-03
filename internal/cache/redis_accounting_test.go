package cache

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMiniredisAccountingCache(t *testing.T) (*RedisAccountingCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{mr.Addr()},
	})

	ac := NewRedisAccountingCache(RedisAccountingConfig{
		Client:     client,
		LocalNode:  "node1",
		WindowSize: time.Minute,
	})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})
	return ac, mr
}

func TestRedisAccountingCache_Increment(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)

	got := ac.Increment("entity-key", 1)
	assert.Equal(t, int64(1), got)

	got = ac.Increment("entity-key", 5)
	assert.Equal(t, int64(6), got)
}

func TestRedisAccountingCache_Get(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)

	assert.Equal(t, int64(0), ac.Get("missing-key"))

	ac.Increment("k", 42)
	assert.Equal(t, int64(42), ac.Get("k"))
}

func TestRedisAccountingCache_Reset(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)

	ac.Increment("k", 10)
	require.Equal(t, int64(10), ac.Get("k"))

	ac.Reset("k")
	assert.Equal(t, int64(0), ac.Get("k"))
}

func TestRedisAccountingCache_TTLExpiry(t *testing.T) {
	ac, mr := newMiniredisAccountingCache(t)

	ac.Increment("entity", 3)
	require.Equal(t, int64(3), ac.Get("entity"))

	// Advance clock past the window.
	mr.FastForward(2 * time.Minute)

	assert.Equal(t, int64(0), ac.Get("entity"), "counter should be gone after window expires")
}

func TestRedisAccountingCache_ConsistentHashing(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)
	ac.AddNode("node2")
	ac.AddNode("node3")

	// IsOwner and GetOwner should not panic with multiple nodes.
	_ = ac.IsOwner("any-key")
	owner := ac.GetOwner("any-key")
	assert.NotEmpty(t, owner)
}

func TestRedisAccountingCache_SetLocalNode(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)
	assert.Equal(t, "node1", ac.LocalNode())

	ac.SetLocalNode("node-new")
	assert.Equal(t, "node-new", ac.LocalNode())
}

func TestRedisAccountingCache_ConsumeTransferable(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)

	// Redis backend always returns nil, false — no peer-to-peer transfer needed.
	transferable, ok := ac.ConsumeTransferable("any-addr")
	assert.Nil(t, transferable)
	assert.False(t, ok)
}

func TestRedisAccountingCache_SnapshotAll(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)

	ac.Increment("key-a", 3)
	ac.Increment("key-b", 7)

	snap := ac.SnapshotAll()
	assert.Equal(t, int64(3), snap["key-a"])
	assert.Equal(t, int64(7), snap["key-b"])
}

func TestRedisAccountingCache_GetEstimatedEntities(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)

	ac.Increment("k1", 1)
	ac.Increment("k2", 1)
	ac.Increment("k3", 1)

	count := ac.GetEstimatedEntities()
	assert.Equal(t, int64(3), count)
}

func TestRedisAccountingCache_ImplementsAccountingRepository(t *testing.T) {
	ac, _ := newMiniredisAccountingCache(t)
	var _ AccountingRepository = ac //nolint:staticcheck // compile-time interface check
	_ = ac
}
