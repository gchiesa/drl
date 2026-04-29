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
	"github.com/gchiesa/drl/internal/accounting"
	"github.com/gchiesa/drl/internal/api"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/grpc"
	"github.com/gchiesa/drl/internal/membership"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/utils"
)

var args struct {
	ConfigPath string `arg:"env:DRL_CONFIG_PATH" help:"Path to KDL configuration file"`
	Version    bool   `arg:"-v,--version" help:"Show version"`
}

// global log
var log *slog.Logger

// Execute initializes and runs the main application lifecycle for the Distributed Rate Limiter (DRL).
// It handles configuration loading, logger initialization, and startup of core components such as metrics, cache, and gRPC server.
// The function facilitates graceful shutdown on receiving termination signals.
func Execute(version string) {
	var err error
	// Parse command line flags
	arg.MustParse(&args)

	if args.Version {
		_, _ = fmt.Fprintf(os.Stdout, "drl version: %s\n", version)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.Load(args.ConfigPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize a structured log based on config
	log, err = newLoggerSlogCompatible(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Resolve the local instance IP once — used as the node identity everywhere.
	localIP, err := utils.GetInstanceIP()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error resolving instance IP: %v\n", err)
		os.Exit(1)
	}

	log.Info("DRL - Distributed Rate Limiter starting...")
	log.Info("configuration loaded",
		"config_file_path", cfg.GetConfigFilePath(),
		"node_ip", localIP,
		"grpc_addr", cfg.Listen.GRPC,
		"metrics_addr", cfg.Listen.Metrics,
		"membership_service", cfg.Membership.ServiceName,
		"membership_port", cfg.Membership.Port,
		"log_level", cfg.Logging.Level,
		"cache_blocklist_size_mb", cfg.Cache.BlocklistSizeMB,
		"cache_accounting_size_mb", cfg.Cache.AccountingSizeMB,
		"accounting_rules", cfg.Accounting.Rules,
		"accounting_rules_count", len(cfg.Accounting.Rules),
	)

	// Initialize metricsManager
	var metricsManager *metrics.Metrics
	if metricsManager, err = newMetrics(cfg, log); err != nil {
		fmt.Println("metrics-manager: >" + err.Error() + "<")
		os.Exit(1)
	}

	// Initialize cache manager
	var cacheManager *cache.Manager
	if cacheManager, err = newCache(cfg, localIP, metricsManager, log); err != nil {
		fmt.Println("cache-manager: >" + err.Error() + "<")
		os.Exit(1)
	}

	// Initialize cluster manager
	var clusterManager *membership.Cluster
	if clusterManager, err = newCluster(cfg, localIP, cacheManager, metricsManager, log); err != nil {
		fmt.Println("cluster-manager: >" + err.Error() + "<")
		os.Exit(1)
	}

	// Initialize Accounting Engine
	var accountingEngine *accounting.Engine
	if accountingEngine, err = newAccountingEngine(cfg, localIP, cacheManager, metricsManager, cacheManager.Blocklist, clusterManager.GetStateDelegate(), clusterManager, log); err != nil {
		fmt.Println("accounting-engine: >" + err.Error() + "<")
		os.Exit(1)
	}

	// Initialize Handover for graceful state evacuation on shutdown
	var handoverManager *membership.Handover
	if accountingEngine != nil && accountingEngine.GetFlusher() != nil {
		if handoverManager, err = membership.NewHandover(membership.HandoverConfig{
			Cluster:    clusterManager,
			Accounting: cacheManager.Accounting,
			Flusher:    accountingEngine.GetFlusher(),
			Metrics:    metricsManager,
			Logger:     log,
		}); err != nil {
			fmt.Println("handover-manager: >" + err.Error() + "<")
			os.Exit(1)
		}
		clusterManager.GetStateDelegate().SetHandover(handoverManager)
		log.Info("handover manager initialized")
	}

	// Initialize internal API if enabled
	var apiServer *api.Server
	if apiServer, err = newApiServer(cfg, localIP, cacheManager, clusterManager, accountingEngine, metricsManager, log); err != nil {
		fmt.Println("api-server: >" + err.Error() + "<")
		os.Exit(1)
	}

	// Initialize gRPC ext_authz server for Envoy
	var grpcServer *grpc.Server
	if grpcServer, err = newGRPCServer(cfg, cacheManager, accountingEngine, apiServer, metricsManager, log); err != nil {
		fmt.Println("grpc-server: >" + err.Error() + "<")
		os.Exit(1)
	}

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

	// Stop Accounting Engine Flusher (final flush of pending batches)
	if accountingEngine != nil && accountingEngine.GetFlusher() != nil {
		accountingEngine.GetFlusher().Stop()
	}

	// Evacuate accounting state to an adopter node before leaving
	if handoverManager != nil {
		if err := handoverManager.Evacuate(); err != nil {
			log.Error("handover failed", "error", err)
		}
	}

	// Leave the cluster (after handover completes or times out)
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
