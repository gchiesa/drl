package cache

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/model"
)

func newMiniredisBlocklistCache(t *testing.T) (*RedisBlocklistCache, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{mr.Addr()},
	})

	bc := NewRedisBlocklistCache(RedisBlocklistConfig{
		Client: client,
	})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})
	return bc, mr
}

func TestRedisBlocklistCache_BlockAndIsBlocked(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)

	const key = "test-entity-key"

	assert.False(t, bc.IsBlocked(key), "entity should not be blocked initially")

	bc.Block(key, time.Minute, nil)
	assert.True(t, bc.IsBlocked(key), "entity should be blocked after Block()")
}

func TestRedisBlocklistCache_BlockWithExpiresAt(t *testing.T) {
	bc, mr := newMiniredisBlocklistCache(t)

	const key = "entity-001"
	entity := &model.Entity{IP: "10.0.0.1", Path: "/api"}

	expiresAt := time.Now().Add(5 * time.Minute)
	bc.BlockWithExpiresAt(key, expiresAt, entity)

	gotAt, found := bc.IsBlockedWithExpiration(key)
	require.True(t, found)
	assert.WithinDuration(t, expiresAt, gotAt, time.Second)

	// Older expiry must not replace a fresher one.
	olderExpiry := expiresAt.Add(-time.Minute)
	bc.BlockWithExpiresAt(key, olderExpiry, entity)
	gotAt2, found2 := bc.IsBlockedWithExpiration(key)
	require.True(t, found2)
	// Expiry should remain roughly the same (fresher wins).
	assert.WithinDuration(t, expiresAt, gotAt2, 2*time.Second)

	// Advance miniredis clock past expiry.
	mr.FastForward(6 * time.Minute)
	assert.False(t, bc.IsBlocked(key), "entity should be unblocked after TTL expires")
}

func TestRedisBlocklistCache_Unblock(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)

	bc.Block("k1", time.Minute, nil)
	require.True(t, bc.IsBlocked("k1"))

	bc.Unblock("k1")
	assert.False(t, bc.IsBlocked("k1"))
}

func TestRedisBlocklistCache_ListEntries(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)

	entity := &model.Entity{IP: "1.2.3.4", Path: "/health"}
	bc.Block("entry-a", time.Minute, entity)
	bc.Block("entry-b", time.Minute, nil)

	entries := bc.ListEntries()
	assert.Len(t, entries, 2)
}

func TestRedisBlocklistCache_Count(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)

	assert.Equal(t, 0, bc.Count())
	bc.Block("k1", time.Minute, nil)
	bc.Block("k2", time.Minute, nil)
	assert.Equal(t, 2, bc.Count())
}

func TestRedisBlocklistCache_Clear(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)

	bc.Block("k1", time.Minute, nil)
	bc.Block("k2", time.Minute, nil)
	require.Equal(t, 2, bc.Count())

	bc.Clear()
	assert.Equal(t, 0, bc.Count())
}

func TestRedisBlocklistCache_GetStateMergeState(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)

	// For Redis, GetState returns nil and MergeState is a no-op.
	state, err := bc.GetState()
	require.NoError(t, err)
	assert.Nil(t, state)

	require.NoError(t, bc.MergeState(nil))
	require.NoError(t, bc.MergeState([]byte{}))
}

func TestRedisBlocklistCache_ImplementsBlocklistRepository(t *testing.T) {
	bc, _ := newMiniredisBlocklistCache(t)
	var _ BlocklistRepository = bc // compile-time interface check
}
