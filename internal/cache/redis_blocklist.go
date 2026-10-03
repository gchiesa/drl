package cache

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/gchiesa/drl/internal/model"
)

const (
	redisBlocklistPrefix = "drl:bl:"
)

// redisBlocklistValue is the value stored in Redis for each blocklist entry.
type redisBlocklistValue struct {
	ExpiresAtNano int64             `msgpack:"expires_at_nano"`
	EntityIP      string            `msgpack:"entity_ip,omitempty"`
	EntityPath    string            `msgpack:"entity_path,omitempty"`
	EntityHdrs    map[string]string `msgpack:"entity_hdrs,omitempty"`
}

// RedisBlocklistCache is a Redis-backed implementation of BlocklistRepository.
// It stores each blocked entity as a Redis key with a TTL matching the block
// expiration. Because Redis is the shared source of truth, GetState and
// MergeState are no-ops (state does not need to be transferred peer-to-peer).
type RedisBlocklistCache struct {
	client  redis.UniversalClient
	logger  *slog.Logger
	metrics RedisMetricsHooks
}

// RedisMetricsHooks collects optional hooks for recording Redis operation
// durations and errors. A nil hook is silently ignored.
type RedisMetricsHooks struct {
	ObserveCommandDuration func(command, status string, seconds float64)
	IncCommandError        func(command, errorType string)
	SetPoolActive          func(active float64)
	SetPoolIdle            func(idle float64)
}

// RedisBlocklistConfig holds construction parameters for RedisBlocklistCache.
type RedisBlocklistConfig struct {
	Client  redis.UniversalClient
	Logger  *slog.Logger
	Metrics RedisMetricsHooks
}

// NewRedisBlocklistCache creates a Redis-backed blocklist cache.
func NewRedisBlocklistCache(cfg RedisBlocklistConfig) *RedisBlocklistCache {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &RedisBlocklistCache{
		client:  cfg.Client,
		logger:  logger,
		metrics: cfg.Metrics,
	}
}

func (r *RedisBlocklistCache) key(entityKey string) string {
	return redisBlocklistPrefix + entityKey
}

func (r *RedisBlocklistCache) observe(cmd, status string, start time.Time) {
	if r.metrics.ObserveCommandDuration != nil {
		r.metrics.ObserveCommandDuration(cmd, status, time.Since(start).Seconds())
	}
}

func (r *RedisBlocklistCache) incError(cmd, errType string) {
	if r.metrics.IncCommandError != nil {
		r.metrics.IncCommandError(cmd, errType)
	}
}

func (r *RedisBlocklistCache) encode(expiresAt time.Time, entity *model.Entity) ([]byte, error) {
	v := redisBlocklistValue{ExpiresAtNano: expiresAt.UnixNano()}
	if entity != nil {
		v.EntityIP = entity.IP
		v.EntityPath = entity.Path
		v.EntityHdrs = entity.Headers
	}
	return msgpack.Marshal(v)
}

func (r *RedisBlocklistCache) decode(data []byte) (*redisBlocklistValue, error) {
	var v redisBlocklistValue
	if err := msgpack.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// IsBlocked reports whether the key is currently in the blocklist.
func (r *RedisBlocklistCache) IsBlocked(key string) bool {
	_, found := r.IsBlockedWithExpiration(key)
	return found
}

// IsBlockedWithExpiration returns the expiration time and existence status.
func (r *RedisBlocklistCache) IsBlockedWithExpiration(key string) (time.Time, bool) {
	ctx := context.Background()
	start := time.Now()

	data, err := r.client.Get(ctx, r.key(key)).Bytes()
	if err != nil {
		if err == redis.Nil {
			r.observe("GET", "miss", start)
			return time.Time{}, false
		}
		r.observe("GET", "error", start)
		r.incError("GET", "redis_error")
		r.logger.Error("redis GET failed", "key", key, "error", err)
		return time.Time{}, false
	}
	r.observe("GET", "hit", start)

	v, err := r.decode(data)
	if err != nil {
		r.incError("GET", "decode_error")
		return time.Time{}, false
	}

	expiresAt := time.Unix(0, v.ExpiresAtNano)
	if time.Now().After(expiresAt) {
		return time.Time{}, false
	}
	return expiresAt, true
}

// Block adds the key to the blocklist with the given TTL.
func (r *RedisBlocklistCache) Block(key string, ttl time.Duration, entity *model.Entity) {
	expiresAt := time.Now().Add(ttl)
	r.BlockWithExpiresAt(key, expiresAt, entity)
}

// BlockWithExpiresAt adds the key with an absolute expiration time.
// Skips the update if the existing entry is fresher.
func (r *RedisBlocklistCache) BlockWithExpiresAt(key string, expiresAt time.Time, entity *model.Entity) {
	ctx := context.Background()
	start := time.Now()

	data, err := r.encode(expiresAt, entity)
	if err != nil {
		r.incError("SET", "encode_error")
		r.logger.Error("failed to encode blocklist entry", "key", key, "error", err)
		return
	}

	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return
	}

	// Use SET NX first, then check-and-update if key exists.
	// GETSET would be cleaner but SET ... GET is Redis 6.2+.
	// We accept occasional double-writes under concurrent updates.
	existing, getErr := r.client.Get(ctx, r.key(key)).Bytes()
	if getErr == nil {
		// Key exists — only update if incoming expiration is fresher.
		existing, decErr := r.decode(existing)
		if decErr == nil {
			if !time.Unix(0, existing.ExpiresAtNano).Before(expiresAt) {
				r.observe("SET", "skipped", start)
				return
			}
		}
	}

	if err := r.client.Set(ctx, r.key(key), data, ttl).Err(); err != nil {
		r.observe("SET", "error", start)
		r.incError("SET", "redis_error")
		r.logger.Error("redis SET failed", "key", key, "error", err)
		return
	}
	r.observe("SET", "ok", start)
	r.logger.Debug("entity blocked in redis", "key", key, "expires_at", expiresAt)
}

// Unblock removes the key from the blocklist.
func (r *RedisBlocklistCache) Unblock(key string) {
	ctx := context.Background()
	start := time.Now()

	if err := r.client.Del(ctx, r.key(key)).Err(); err != nil {
		r.observe("DEL", "error", start)
		r.incError("DEL", "redis_error")
		r.logger.Error("redis DEL failed", "key", key, "error", err)
		return
	}
	r.observe("DEL", "ok", start)
}

// ListEntries returns all non-expired entries with their metadata.
func (r *RedisBlocklistCache) ListEntries() []model.BlockedEntityInfo {
	entries := r.Entries()
	now := time.Now()
	result := make([]model.BlockedEntityInfo, 0, len(entries))
	for _, e := range entries {
		if e.ExpiresAt.After(now) {
			var entity *model.Entity
			if e.EntityIP != "" || e.EntityPath != "" {
				entity = &model.Entity{
					IP:      e.EntityIP,
					Path:    e.EntityPath,
					Headers: e.EntityHdrs,
				}
			}
			result = append(result, model.BlockedEntityInfo{
				Key:       e.Key,
				ExpiresAt: e.ExpiresAt,
				Entity:    entity,
			})
		}
	}
	return result
}

// GetState returns an empty byte slice for Redis — no peer-to-peer state sync
// is needed because Redis acts as the shared source of truth.
func (r *RedisBlocklistCache) GetState() ([]byte, error) {
	return nil, nil
}

// MergeState is a no-op for Redis — the store is already shared.
func (r *RedisBlocklistCache) MergeState(_ []byte) error {
	return nil
}

// Count returns the approximate number of blocklist entries.
func (r *RedisBlocklistCache) Count() int {
	return len(r.Entries())
}

// Entries returns all non-expired blocklist entries.
func (r *RedisBlocklistCache) Entries() []BlocklistEntry {
	ctx := context.Background()
	now := time.Now()
	var result []BlocklistEntry

	iter := r.client.Scan(ctx, 0, redisBlocklistPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		redisKey := iter.Val()
		entityKey := strings.TrimPrefix(redisKey, redisBlocklistPrefix)

		data, err := r.client.Get(ctx, redisKey).Bytes()
		if err != nil {
			continue
		}
		v, err := r.decode(data)
		if err != nil {
			continue
		}

		expiresAt := time.Unix(0, v.ExpiresAtNano)
		if !expiresAt.After(now) {
			continue
		}

		entry := BlocklistEntry{
			Key:       entityKey,
			ExpiresAt: expiresAt,
		}
		if v.EntityIP != "" || v.EntityPath != "" {
			entry.EntityIP = v.EntityIP
			entry.EntityPath = v.EntityPath
			entry.EntityHdrs = v.EntityHdrs
		}
		result = append(result, entry)
	}
	return result
}

// Clear removes all blocklist entries from Redis.
func (r *RedisBlocklistCache) Clear() {
	ctx := context.Background()
	iter := r.client.Scan(ctx, 0, redisBlocklistPrefix+"*", 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		r.client.Del(ctx, keys...)
	}
}

// Close is a no-op — the Redis client lifecycle is managed externally.
func (r *RedisBlocklistCache) Close() {}

// CostEstimate returns an approximate memory cost in bytes.
// For Redis this is an estimate based on entry count.
func (r *RedisBlocklistCache) CostEstimate() int64 {
	return int64(r.Count()) * 150
}
