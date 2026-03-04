package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexflint/go-arg"
	"github.com/gchiesa/drl/internal/config"
)

var args struct {
	configPath string `arg:"required,env:DRL_CONFIG_PATH" help:"Path to KDL configuration file"`
}

// global log
var log *slog.Logger

// Execute initializes and runs the main application lifecycle for the Distributed Rate Limiter (DRL).
// It handles configuration loading, logger initialization, and startup of core components such as metrics, cache, and gRPC server.
// The function facilitates graceful shutdown on receiving termination signals.
func Execute() {
	// Parse command line flags
	arg.MustParse(&args)

	// Load configuration
	cfg, err := config.Load(args.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize a structured log based on config
	log, err = newLoggerSlogCompatible(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	log.Info("DRL - Distributed Rate Limiter starting...")
	log.Info("configuration loaded",
		"node_name", cfg.NodeName,
		"grpc_addr", cfg.Listen.GRPC,
		"metrics_addr", cfg.Listen.Metrics,
		"membership_service", cfg.Membership.ServiceName,
		"membership_port", cfg.Membership.Port,
		"log_level", cfg.Logging.Level,
		"cache_blocklist_size_mb", cfg.Cache.BlocklistSizeMB,
		"cache_accounting_size_mb", cfg.Cache.AccountingSizeMB,
	)

	// Initialize metricsManager
	metricsManager := newMetrics(cfg, log)

	// Initialize cache manager
	cacheManager := newCache(cfg, metricsManager, log)

	// Initialize cluster manager
	clusterManager := newCluster(cfg, cacheManager, metricsManager, log)

	// Initialize Accounting Engine
	accountingEngine := newAccountingEngine(cfg, cacheManager, metricsManager, log)

	// Initialize internal API if enabled
	apiServer := newApiServer(cfg, cacheManager, clusterManager, accountingEngine, log)

	// Initialize gRPC ext_authz server for Envoy
	grpcServer := newGRPCServer(cfg, cacheManager, accountingEngine, apiServer, metricsManager, log)

	log.Info("DRL is running")

	// Wait for a shutdown signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	log.Info("shutdown signal received")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// Stop Api Server
	if apiServer != nil {
		if err := apiServer.Stop(shutdownCtx); err != nil {
			log.Error("failed to stop internal API server", "error", err)
		}
	}

	// Stop GRPS Server
	grpcServer.Stop(shutdownCtx)

	// Stop Accounting Engine Flusher
	if accountingEngine.GetFlusher() != nil {
		accountingEngine.GetFlusher().Stop()
	}

	// Leave the cluster
	if err := clusterManager.Leave(5 * time.Second); err != nil {
		log.Error("failed to leave cluster gracefully", "error", err)
	}

	// Close cache manager
	cacheManager.Close()

	// Stop the Metrica Manager
	if err := metricsManager.Stop(); err != nil {
		log.Error("failed to stop metricsManager server", "error", err)
	}

	log.Info("DRL shutdown complete")
}
