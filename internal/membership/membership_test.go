package membership

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/utils"
)

func testLocalIP(t *testing.T) string {
	t.Helper()
	ip, err := utils.GetInstanceIP()
	require.NoError(t, err)
	return ip
}

func testCacheManager(t *testing.T, localIP string) *cache.Manager {
	t.Helper()
	cm, err := cache.NewManager(cache.ManagerConfig{
		BlocklistSizeMB:  1,
		AccountingSizeMB: 1,
		LocalNode:        localIP,
		WindowSize:       time.Minute,
	})
	require.NoError(t, err)
	return cm
}

func TestNewCluster(t *testing.T) {
	localIP := testLocalIP(t)
	cfg := &config.Config{
		Listen: config.ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: config.MembershipConfig{
			ServiceName:  "drl",
			Port:         17946,
			BindAddr:     "127.0.0.1",
			StartupDelay: 100 * time.Millisecond,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
	m := metrics.NewMetrics()
	cm := testCacheManager(t, localIP)
	defer cm.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, localIP, cm, m, logger)
	require.NotNil(t, cluster, "expected non-nil cluster")

	assert.Equal(t, cfg, cluster.config, "config not set correctly")
	assert.Equal(t, localIP, cluster.localIP, "localIP not set correctly")
	assert.Equal(t, m, cluster.metrics, "metrics not set correctly")
	assert.Equal(t, logger, cluster.logger, "logger not set correctly")
}

func TestClusterStart(t *testing.T) {
	localIP := testLocalIP(t)
	cfg := &config.Config{
		Listen: config.ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: config.MembershipConfig{
			ServiceName:  "invalid-service",
			Port:         17947,
			BindAddr:     "127.0.0.1",
			StartupDelay: 100 * time.Millisecond,
		},
		Logging: config.LoggingConfig{
			Level:  "debug",
			Format: "json",
		},
	}
	m := metrics.NewMetrics()
	cm := testCacheManager(t, localIP)
	defer cm.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, localIP, cm, m, logger)

	err := cluster.Start()
	if err != nil {
		t.Fatalf("failed to start cluster: %v", err)
	}
	defer func() {
		_ = cluster.Leave(time.Second)
	}()

	// Verify memberlist is running
	if cluster.memberlist == nil {
		t.Error("memberlist should not be nil after start")
	}

	// Verify we have at least ourselves as a member
	if cluster.NumMembers() < 1 {
		t.Error("expected at least 1 member (self)")
	}
}

func TestClusterIsReady(t *testing.T) {
	localIP := testLocalIP(t)
	cfg := &config.Config{
		Listen: config.ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: config.MembershipConfig{
			ServiceName:  "invalid-service",
			Port:         17948,
			BindAddr:     "127.0.0.1",
			StartupDelay: 100 * time.Millisecond,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
	m := metrics.NewMetrics()
	cm := testCacheManager(t, localIP)
	defer cm.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, localIP, cm, m, logger)

	// Initially not ready
	if cluster.IsReady() {
		t.Error("cluster should not be ready before start")
	}

	err := cluster.Start()
	if err != nil {
		t.Fatalf("failed to start cluster: %v", err)
	}
	defer func() {
		_ = cluster.Leave(time.Second)
	}()

	// Join cluster (will fail to find peers but should still become ready)
	err = cluster.JoinCluster()
	if err != nil {
		t.Fatalf("failed to join cluster: %v", err)
	}

	// Should be ready after join attempt
	if !cluster.IsReady() {
		t.Error("cluster should be ready after join attempt")
	}
}

func TestClusterMembers(t *testing.T) {
	localIP := testLocalIP(t)
	cfg := &config.Config{
		Listen: config.ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: config.MembershipConfig{
			ServiceName:  "invalid-service",
			Port:         17949,
			BindAddr:     "127.0.0.1",
			StartupDelay: 100 * time.Millisecond,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
	m := metrics.NewMetrics()
	cm := testCacheManager(t, localIP)
	defer cm.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, localIP, cm, m, logger)

	err := cluster.Start()
	if err != nil {
		t.Fatalf("failed to start cluster: %v", err)
	}
	defer func() {
		_ = cluster.Leave(time.Second)
	}()

	members := cluster.Members()
	if len(members) != 1 {
		t.Errorf("expected 1 member (self), got %d", len(members))
	}

	// Node name is now the IP address
	if members[0].Name != localIP {
		t.Errorf("expected node name %s, got %s", localIP, members[0].Name)
	}
}

func TestClusterLeave(t *testing.T) {
	localIP := testLocalIP(t)
	cfg := &config.Config{
		Listen: config.ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: config.MembershipConfig{
			ServiceName:  "invalid-service",
			Port:         17950,
			BindAddr:     "127.0.0.1",
			StartupDelay: 100 * time.Millisecond,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
	m := metrics.NewMetrics()
	cm := testCacheManager(t, localIP)
	defer cm.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, localIP, cm, m, logger)

	err := cluster.Start()
	if err != nil {
		t.Fatalf("failed to start cluster: %v", err)
	}

	err = cluster.Leave(time.Second)
	if err != nil {
		t.Errorf("failed to leave cluster: %v", err)
	}
}

func TestClusterLeaveWithoutStart(t *testing.T) {
	localIP := testLocalIP(t)
	cfg := &config.Config{
		Listen: config.ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: config.MembershipConfig{
			ServiceName:  "drl",
			Port:         7946,
			BindAddr:     "0.0.0.0",
			StartupDelay: 100 * time.Millisecond,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
	m := metrics.NewMetrics()
	cm := testCacheManager(t, localIP)
	defer cm.Close()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, localIP, cm, m, logger)

	// Should not panic or error
	err := cluster.Leave(time.Second)
	if err != nil {
		t.Errorf("unexpected error leaving unstarted cluster: %v", err)
	}
}
