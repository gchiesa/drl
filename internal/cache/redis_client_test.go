package cache

import (
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/config"
)

func TestNewRedisClient_Success(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	cfg := config.RedisConfig{
		Addrs:      []string{mr.Addr()},
		MaxRetries: 1,
	}
	client, err := NewRedisClient(cfg, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, client)
	_ = client.Close()
}

func TestNewRedisClient_Failure(t *testing.T) {
	cfg := config.RedisConfig{
		Addrs:           []string{"127.0.0.1:19999"}, // nothing listening here
		MaxRetries:      2,
		RetryBackoffMin: 10 * time.Millisecond,
		RetryBackoffMax: 50 * time.Millisecond,
		ConnectTimeout:  100 * time.Millisecond,
		DialTimeout:     100 * time.Millisecond,
	}
	_, err := NewRedisClient(cfg, slog.Default())
	assert.Error(t, err)
}

func TestPingWithRetry_SucceedsOnFirstAttempt(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	cfg := config.RedisConfig{
		Addrs:      []string{mr.Addr()},
		MaxRetries: 3,
	}
	client := buildRedisClient(cfg)
	defer func() { _ = client.Close() }()

	err = pingWithRetry(client, cfg, slog.Default())
	assert.NoError(t, err)
}
