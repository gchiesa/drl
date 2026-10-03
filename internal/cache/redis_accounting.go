package cache

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/redis/go-redis/v9"
	"github.com/samber/lo"
)

const (
	redisAccountingPrefix = "drl:ac:"
)

// incrByScript atomically increments a counter in Redis. On the first
// increment (TTL == -1, i.e. key has no expiry), the window TTL is set.
// KEYS[1]  = the Redis key
// ARGV[1]  = TTL in seconds (window size)
// ARGV[2]  = increment delta
var incrByScript = redis.NewScript(`
local current = redis.call('INCRBY', KEYS[1], ARGV[2])
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
return current
`)

// RedisAccountingCache is a Redis-backed implementation of AccountingRepository.
// Ownership determination (consistent hashing) still runs locally so that
// forwarding decisions match the in-memory implementation. The counters
// themselves live in Redis so all nodes share the same view.
type RedisAccountingCache struct {
	client     redis.UniversalClient
	ring       *consistent.Consistent
	localNode  string
	logger     *slog.Logger
	windowSize time.Duration
	metrics    RedisMetricsHooks
	mu         sync.RWMutex
}

// RedisAccountingConfig holds construction parameters for RedisAccountingCache.
type RedisAccountingConfig struct {
	Client     redis.UniversalClient
	LocalNode  string
	WindowSize time.Duration
	Logger     *slog.Logger
	Metrics    RedisMetricsHooks
}

// NewRedisAccountingCache creates a Redis-backed accounting cache.
func NewRedisAccountingCache(cfg RedisAccountingConfig) *RedisAccountingCache {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	windowSize := cfg.WindowSize
	if windowSize == 0 {
		windowSize = time.Minute
	}

	ringCfg := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            hasher{},
	}
	ring := consistent.New(nil, ringCfg)
	if cfg.LocalNode != "" {
		ring.Add(Member(cfg.LocalNode))
	}

	return &RedisAccountingCache{
		client:     cfg.Client,
		ring:       ring,
		localNode:  cfg.LocalNode,
		logger:     logger,
		windowSize: windowSize,
		metrics:    cfg.Metrics,
	}
}

func (r *RedisAccountingCache) key(entityKey string) string {
	return redisAccountingPrefix + entityKey
}

func (r *RedisAccountingCache) observe(cmd, status string, start time.Time) {
	if r.metrics.ObserveCommandDuration != nil {
		r.metrics.ObserveCommandDuration(cmd, status, time.Since(start).Seconds())
	}
}

func (r *RedisAccountingCache) incError(cmd, errType string) {
	if r.metrics.IncCommandError != nil {
		r.metrics.IncCommandError(cmd, errType)
	}
}

// IsOwner always returns true for the Redis backend. Because Redis is a shared
// atomic store, every node is effectively the owner of every key — all nodes
// can increment and evaluate the rate limit directly without forwarding.
func (r *RedisAccountingCache) IsOwner(_ string) bool {
	return true
}

// GetOwner returns the local node address for the Redis backend. The concept
// of remote ownership does not apply when Redis is the shared store, so the
// local node is always reported as the owner.
func (r *RedisAccountingCache) GetOwner(_ string) string {
	return r.localNode
}

// Increment atomically increments the counter for key by delta.
// The counter TTL is set to the window size on first creation.
func (r *RedisAccountingCache) Increment(key string, delta int64) int64 {
	ctx := context.Background()
	start := time.Now()

	ttlSecs := int64(r.windowSize.Seconds())
	if ttlSecs < 1 {
		ttlSecs = 1
	}

	result, err := incrByScript.Run(ctx, r.client,
		[]string{r.key(key)},
		strconv.FormatInt(ttlSecs, 10),
		strconv.FormatInt(delta, 10),
	).Int64()
	if err != nil {
		r.observe("INCRBY", "error", start)
		r.incError("INCRBY", "redis_error")
		r.logger.Error("redis INCRBY script failed", "key", key, "error", err)
		return 0
	}
	r.observe("INCRBY", "ok", start)
	return result
}

// Get returns the current counter value for key (0 if not found).
func (r *RedisAccountingCache) Get(key string) int64 {
	ctx := context.Background()
	start := time.Now()

	val, err := r.client.Get(ctx, r.key(key)).Int64()
	if err != nil {
		if err == redis.Nil {
			r.observe("GET", "miss", start)
			return 0
		}
		r.observe("GET", "error", start)
		r.incError("GET", "redis_error")
		return 0
	}
	r.observe("GET", "hit", start)
	return val
}

// Reset removes the counter for the given key.
func (r *RedisAccountingCache) Reset(key string) {
	ctx := context.Background()
	r.client.Del(ctx, r.key(key))
}

// AddNode registers a new node in the consistent hash ring.
func (r *RedisAccountingCache) AddNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ring.Add(Member(node))
	r.logger.Debug("redis accounting: node added to hash ring",
		"node", node,
		"total_members", len(r.ring.GetMembers()),
	)
}

// RemoveNode removes a node from the consistent hash ring.
func (r *RedisAccountingCache) RemoveNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ring.Remove(node)
	r.logger.Debug("redis accounting: node removed from hash ring",
		"node", node,
		"total_members", len(r.ring.GetMembers()),
	)
}

// UpdateNodes reconciles the hash ring with the provided node list.
func (r *RedisAccountingCache) UpdateNodes(nodes []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	currentMembers := make(map[string]bool)
	for _, m := range r.ring.GetMembers() {
		currentMembers[m.String()] = true
	}
	newMembers := make(map[string]bool)
	for _, n := range nodes {
		newMembers[n] = true
	}
	for m := range currentMembers {
		if !newMembers[m] {
			r.ring.Remove(m)
		}
	}
	for n := range newMembers {
		if !currentMembers[n] {
			r.ring.Add(Member(n))
		}
	}
	r.logger.Debug("redis accounting: hash ring updated",
		"total_members", len(r.ring.GetMembers()),
	)
}

// GetNodes returns the current set of nodes in the hash ring.
func (r *RedisAccountingCache) GetNodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	members := r.ring.GetMembers()
	return lo.Map(members, func(m consistent.Member, _ int) string { return m.String() })
}

// SnapshotAll returns a point-in-time snapshot of all key→count entries.
func (r *RedisAccountingCache) SnapshotAll() map[string]int64 {
	ctx := context.Background()
	result := make(map[string]int64)

	iter := r.client.Scan(ctx, 0, redisAccountingPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		redisKey := iter.Val()
		entityKey := strings.TrimPrefix(redisKey, redisAccountingPrefix)
		val, err := r.client.Get(ctx, redisKey).Int64()
		if err != nil {
			continue
		}
		result[entityKey] = val
	}
	return result
}

// ConsumeTransferable always returns (nil, false) for Redis — state is shared
// via the central Redis store and does not need peer-to-peer transfer.
func (r *RedisAccountingCache) ConsumeTransferable(_ string) (Transferable, bool) {
	return nil, false
}

// LocalNode returns the local node identifier.
func (r *RedisAccountingCache) LocalNode() string {
	return r.localNode
}

// SetLocalNode updates the local node identifier.
func (r *RedisAccountingCache) SetLocalNode(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.localNode != "" && r.localNode != node {
		r.ring.Remove(r.localNode)
	}
	r.localNode = node
	if node != "" {
		r.ring.Add(Member(node))
	}
}

// GetEstimatedEntities returns the approximate number of tracked entries.
func (r *RedisAccountingCache) GetEstimatedEntities() int64 {
	ctx := context.Background()
	// Use a SCAN-based count to avoid counting non-accounting keys.
	var count int64
	iter := r.client.Scan(ctx, 0, redisAccountingPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		count++
	}
	return count
}

// Close is a no-op — the Redis client lifecycle is managed externally.
func (r *RedisAccountingCache) Close() {}

// UpdatePoolMetrics publishes connection pool stats to the registered metrics
// hooks. Callers should invoke this periodically (e.g. every 10 seconds) to
// keep the pool gauges current.
func (r *RedisAccountingCache) UpdatePoolMetrics() {
	if r.metrics.SetPoolActive == nil && r.metrics.SetPoolIdle == nil {
		return
	}
	stats := r.client.PoolStats()
	if stats == nil {
		return
	}
	if r.metrics.SetPoolActive != nil {
		r.metrics.SetPoolActive(float64(stats.TotalConns - stats.IdleConns))
	}
	if r.metrics.SetPoolIdle != nil {
		r.metrics.SetPoolIdle(float64(stats.IdleConns))
	}
}

// PoolStats returns the current Redis connection pool statistics.
// Returns nil when the client does not support pool stats.
func (r *RedisAccountingCache) PoolStats() *redis.PoolStats {
	return r.client.PoolStats()
}
