//go:build testMembership
// +build testMembership

package membership

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

func TestNewCluster(t *testing.T) {
	cfg := &config.Config{
		NodeName:             "test-node",
		BindAddr:             "127.0.0.1",
		BindPort:             17946,
		DiscoveryServiceName: "drl",
		MetricsPort:          9091,
		StartupDelay:         100 * time.Millisecond,
	}
	m := metrics.NewMetrics()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	cluster := NewCluster(cfg, m, logger)

	if cluster == nil {
		t.Fatal("expected non-nil cluster")
	}

	if cluster.config != cfg {
		t.Error("config not set correctly")
	}

	if cluster.metrics != m {
		t.Error("metrics not set correctly")
	}

	if cluster.logger != logger {
		t.Error("logger not set correctly")
	}
}

func TestClusterStart(t *testing.T) {
	cfg := &config.Config{
		NodeName:             "test-node-start",
		BindAddr:             "127.0.0.1",
		BindPort:             17947,
		DiscoveryServiceName: "invalid-service",
		MetricsPort:          9091,
		StartupDelay:         100 * time.Millisecond,
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
		NodeName:             "test-node-ready",
		BindAddr:             "127.0.0.1",
		BindPort:             17948,
		DiscoveryServiceName: "invalid-service",
		MetricsPort:          9091,
		StartupDelay:         100 * time.Millisecond,
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
		NodeName:             "test-node-members",
		BindAddr:             "127.0.0.1",
		BindPort:             17949,
		DiscoveryServiceName: "invalid-service",
		MetricsPort:          9091,
		StartupDelay:         100 * time.Millisecond,
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
		NodeName:             "test-node-leave",
		BindAddr:             "127.0.0.1",
		BindPort:             17950,
		DiscoveryServiceName: "invalid-service",
		MetricsPort:          9091,
		StartupDelay:         100 * time.Millisecond,
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

func TestSlogWriter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	writer := &slogWriter{logger: logger}

	n, err := writer.Write([]byte("test message"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 12 {
		t.Errorf("expected 12 bytes written, got %d", n)
	}
}
