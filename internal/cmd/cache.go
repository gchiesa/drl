package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// newCache initializes and returns a cache manager instance based on the given
// config, metrics, and logger. The provider is selected via cfg.Cache.Provider:
//   - "in-memory" (default): uses the existing Otter-backed in-memory store.
//   - "redis": connects to Redis, retries with backoff on failure, and exits
//     with a non-zero code if the connection cannot be established.
func newCache(cfg *config.Config, localIP string, metric *metrics.Metrics, log *slog.Logger) (*cache.Manager, error) {
	if metric == nil {
		return nil, fmt.Errorf("metrics must be provided")
	}
	if log == nil {
		return nil, fmt.Errorf("logger must be provided")
	}

	switch strings.ToLower(cfg.Cache.Provider) {
	case "redis":
		return newRedisCache(cfg, localIP, metric, log)
	default:
		return newInMemoryCache(cfg, localIP, metric, log)
	}
}

// newInMemoryCache creates a cache manager backed by the in-memory Otter store.
func newInMemoryCache(cfg *config.Config, localIP string, metric *metrics.Metrics, log *slog.Logger) (*cache.Manager, error) {
	cacheManager, err := cache.NewManager(cache.ManagerConfig{
		BlocklistSizeMB:  cfg.Cache.BlocklistSizeMB,
		AccountingSizeMB: cfg.Cache.AccountingSizeMB,
		LocalNode:        localIP,
		WindowSize:       time.Minute,
		Logger:           log,
		OnBlocklistHit: func() {
			metric.IncCacheHit(metrics.CacheTypeBlocklist)
		},
		OnBlocklistMiss: func() {
			metric.IncCacheMiss(metrics.CacheTypeBlocklist)
		},
		OnBlocklistEvict: func() {
			metric.IncCacheEviction(metrics.CacheTypeBlocklist)
		},
		OnAccountingHit: func() {
			metric.IncCacheHit(metrics.CacheTypeAccounting)
		},
		OnAccountingMiss: func() {
			metric.IncCacheMiss(metrics.CacheTypeAccounting)
		},
		OnAccountingEvict: func() {
			metric.IncCacheEviction(metrics.CacheTypeAccounting)
		},
	})
	if err != nil {
		log.Error("failed to create in-memory cache manager", "error", err)
		os.Exit(1)
	}
	log.Info("cache manager initialized (in-memory)",
		"blocklist_size_mb", cfg.Cache.BlocklistSizeMB,
		"accounting_size_mb", cfg.Cache.AccountingSizeMB,
	)
	return cacheManager, nil
}

// newRedisCache creates a cache manager backed by Redis. The Redis client is
// built, a Ping is attempted with exponential-backoff retries, and DRL exits
// immediately if the connection cannot be established.
func newRedisCache(cfg *config.Config, localIP string, metric *metrics.Metrics, log *slog.Logger) (*cache.Manager, error) {
	log.Info("cache provider: redis",
		"addrs", cfg.Cache.Redis.Addrs,
		"pool_size", cfg.Cache.Redis.PoolSize,
		"max_retries", cfg.Cache.Redis.MaxRetries,
	)

	redisHooks := cache.RedisMetricsHooks{
		ObserveCommandDuration: metric.ObserveRedisCommandDuration,
		IncCommandError:        metric.IncRedisError,
		SetPoolActive:          metric.SetRedisPoolActive,
		SetPoolIdle:            metric.SetRedisPoolIdle,
	}

	redisClient, err := cache.NewRedisClient(cfg.Cache.Redis, log)
	if err != nil {
		log.Error("redis connection failed — DRL cannot start",
			"addrs", cfg.Cache.Redis.Addrs,
			"error", err,
		)
		os.Exit(1)
	}

	blocklist := cache.NewRedisBlocklistCache(cache.RedisBlocklistConfig{
		Client:  redisClient,
		Logger:  log,
		Metrics: redisHooks,
	})

	accounting := cache.NewRedisAccountingCache(cache.RedisAccountingConfig{
		Client:     redisClient,
		LocalNode:  localIP,
		WindowSize: time.Minute,
		Logger:     log,
		Metrics:    redisHooks,
	})

	cacheManager := cache.NewManagerWithRepositories(blocklist, accounting, log)

	log.Info("cache manager initialized (redis)",
		"addrs", cfg.Cache.Redis.Addrs,
	)
	return cacheManager, nil
}
