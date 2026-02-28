package membership

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

func TestNewCluster(t *testing.T) {
	cfg := &config.Config{
		NodeName: "test-node",
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)
	require.NotNil(t, cluster, "expected non-nil cluster")

	assert.Equal(t, cfg, cluster.config, "config not set correctly")
	assert.Equal(t, m, cluster.metrics, "metrics not set correctly")
	assert.Equal(t, logger, cluster.logger, "logger not set correctly")
}

func TestClusterStart(t *testing.T) {
	cfg := &config.Config{
		NodeName: "test-node-start",
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)

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
	cfg := &config.Config{
		NodeName: "test-node-ready",
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)

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
	cfg := &config.Config{
		NodeName: "test-node-members",
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)

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

	if members[0].Name != "test-node-members" {
		t.Errorf("expected node name test-node-members, got %s", members[0].Name)
	}
}

func TestClusterLeave(t *testing.T) {
	cfg := &config.Config{
		NodeName: "test-node-leave",
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)

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
	cfg := &config.Config{
		NodeName: "test-node-leave-no-start",
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)

	// Should not panic or error
	err := cluster.Leave(time.Second)
	if err != nil {
		t.Errorf("unexpected error leaving unstarted cluster: %v", err)
	}
}
