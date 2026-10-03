package cache

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/gchiesa/drl/internal/config"
)

// NewRedisClient creates a Redis universal client from the given configuration.
// It supports standalone, cluster, and sentinel endpoints based on the number
// of addresses supplied. The connection is verified with a Ping.
//
// If the initial Ping fails, the connection is retried up to MaxRetries times
// using exponential backoff with jitter between RetryBackoffMin and
// RetryBackoffMax. If all attempts fail, an error is returned.
func NewRedisClient(cfg config.RedisConfig, logger *slog.Logger) (redis.UniversalClient, error) {
	client := buildRedisClient(cfg)
	if err := pingWithRetry(client, cfg, logger); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// buildRedisClient constructs a redis.UniversalClient from configuration.
func buildRedisClient(cfg config.RedisConfig) redis.UniversalClient {
	opts := &redis.UniversalOptions{
		Addrs:           cfg.Addrs,
		Password:        cfg.Password,
		DB:              cfg.DB,
		MaxRetries:      cfg.MaxRetries,
		MinRetryBackoff: cfg.RetryBackoffMin,
		MaxRetryBackoff: cfg.RetryBackoffMax,
		DialTimeout:     cfg.DialTimeout,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		PoolSize:        cfg.PoolSize,
	}

	if cfg.ConnectTimeout > 0 {
		opts.DialTimeout = cfg.ConnectTimeout
	}

	return redis.NewUniversalClient(opts)
}

// pingWithRetry attempts to Ping Redis, retrying on failure with exponential
// backoff and jitter. Returns nil on success or an error after all retries
// are exhausted.
func pingWithRetry(client redis.UniversalClient, cfg config.RedisConfig, logger *slog.Logger) error {
	maxAttempts := cfg.MaxRetries + 1 // at least one attempt
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	backoffMin := cfg.RetryBackoffMin
	if backoffMin <= 0 {
		backoffMin = 100 * time.Millisecond
	}
	backoffMax := cfg.RetryBackoffMax
	if backoffMax <= 0 {
		backoffMax = 2 * time.Second
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := client.Ping(ctx).Err()
		cancel()

		if err == nil {
			logger.Info("redis connection established",
				"addrs", cfg.Addrs,
				"attempt", attempt,
			)
			return nil
		}

		lastErr = err
		logger.Warn("redis ping failed",
			"addrs", cfg.Addrs,
			"attempt", attempt,
			"max_attempts", maxAttempts,
			"error", err,
		)

		if attempt == maxAttempts {
			break
		}

		// Exponential backoff: base * 2^(attempt-1), clamped to backoffMax.
		base := backoffMin * (1 << uint(attempt-1))
		if base > backoffMax {
			base = backoffMax
		}
		// Add random jitter: [0, base/2)
		jitter := time.Duration(rand.Int63n(int64(base/2) + 1))
		sleep := base + jitter

		logger.Info("retrying redis connection",
			"next_attempt", attempt+1,
			"backoff", sleep,
		)
		time.Sleep(sleep)
	}

	return fmt.Errorf("redis connection failed after %d attempt(s) to %v: %w",
		maxAttempts, cfg.Addrs, lastErr)
}
