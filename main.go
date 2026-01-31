package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/membership"
	"github.com/gchiesa/drl/internal/metrics"
)

func main() {
	// Initialize structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	logger.Info("DRL - Distributed Rate Limiter starting...")

	// Load configuration
	cfg := config.DefaultConfig()
	logger.Info("configuration loaded",
		"node_name", cfg.NodeName,
		"bind_addr", cfg.BindAddr,
		"bind_port", cfg.BindPort,
		"discovery_service", cfg.DiscoveryServiceName,
		"metrics_port", cfg.MetricsPort,
	)

	// Initialize metrics
	m := metrics.NewMetrics()
	if err := m.StartServer(cfg.MetricsPort); err != nil {
		logger.Error("failed to start metrics server", "error", err)
		os.Exit(1)
	}
	logger.Info("metrics server started", "port", cfg.MetricsPort)

	// Initialize and start cluster membership
	cluster := membership.NewCluster(cfg, m, logger)
	if err := cluster.Start(); err != nil {
		logger.Error("failed to start cluster", "error", err)
		os.Exit(1)
	}

	// Join the cluster in background
	go func() {
		if err := cluster.JoinCluster(); err != nil {
			logger.Error("failed to join cluster", "error", err)
		}
	}()

	logger.Info("DRL is running")

	// Wait for shutdown signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	// Graceful shutdown
	if err := cluster.Leave(5 * time.Second); err != nil {
		logger.Error("failed to leave cluster gracefully", "error", err)
	}

	if err := m.Stop(); err != nil {
		logger.Error("failed to stop metrics server", "error", err)
	}

	logger.Info("DRL shutdown complete")
}
