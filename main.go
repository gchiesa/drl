package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gchiesa/drl/internal/api"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	drlgrpc "github.com/gchiesa/drl/internal/grpc"
	"github.com/gchiesa/drl/internal/membership"
	"github.com/gchiesa/drl/internal/metrics"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to KDL configuration file")
	flag.StringVar(configPath, "c", "", "Path to KDL configuration file (shorthand)")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize structured logger based on config
	logLevel := parseLogLevel(cfg.Logging.Level)
	var handler slog.Handler
	if strings.ToLower(cfg.Logging.Format) == "text" {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	logger.Info("DRL - Distributed Rate Limiter starting...")
	logger.Info("configuration loaded",
		"node_name", cfg.NodeName,
		"grpc_addr", cfg.Listen.GRPC,
		"metrics_addr", cfg.Listen.Metrics,
		"membership_service", cfg.Membership.ServiceName,
		"membership_port", cfg.Membership.Port,
		"log_level", cfg.Logging.Level,
		"cache_blocklist_size_mb", cfg.Cache.BlocklistSizeMB,
		"cache_accounting_size_mb", cfg.Cache.AccountingSizeMB,
	)

	// Initialize metrics
	m := metrics.NewMetrics()
	if err := m.StartServer(cfg.MetricsPort()); err != nil {
		logger.Error("failed to start metrics server", "error", err)
		os.Exit(1)
	}
	logger.Info("metrics server started", "port", cfg.MetricsPort())

	// Initialize cache manager
	cacheManager, err := cache.NewManager(cache.ManagerConfig{
		BlocklistSizeMB:  cfg.Cache.BlocklistSizeMB,
		AccountingSizeMB: cfg.Cache.AccountingSizeMB,
		LocalNode:        cfg.NodeName,
		WindowSize:       time.Minute, // Rate limiting window
		Logger:           logger,
		// Connect metrics callbacks
		OnBlocklistHit: func() {
			m.IncCacheHit(metrics.CacheTypeBlocklist)
		},
		OnBlocklistMiss: func() {
			m.IncCacheMiss(metrics.CacheTypeBlocklist)
		},
		OnBlocklistEvict: func() {
			m.IncCacheEviction(metrics.CacheTypeBlocklist)
		},
		OnAccountingHit: func() {
			m.IncCacheHit(metrics.CacheTypeAccounting)
		},
		OnAccountingMiss: func() {
			m.IncCacheMiss(metrics.CacheTypeAccounting)
		},
		OnAccountingEvict: func() {
			m.IncCacheEviction(metrics.CacheTypeAccounting)
		},
	})
	if err != nil {
		logger.Error("failed to create cache manager", "error", err)
		os.Exit(1)
	}
	logger.Info("cache manager initialized",
		"blocklist_size_mb", cfg.Cache.BlocklistSizeMB,
		"accounting_size_mb", cfg.Cache.AccountingSizeMB,
	)

	// clusterRef is captured by the NumNodesFunc closure below.
	// It is assigned immediately after NewCluster returns, before any broadcast
	// can be triggered, so no additional synchronisation is required.
	var clusterRef *membership.Cluster

	// Create state delegate for blocklist sync
	stateDelegate := membership.NewStateDelegate(membership.DelegateConfig{
		Blocklist:   cacheManager.Blocklist,
		Metrics:     m,
		Logger:      logger,
		SyncTimeout: time.Duration(cfg.Cache.SyncTimeoutSeconds) * time.Second,
		NumNodesFunc: func() int {
			if clusterRef == nil {
				return 1
			}
			return clusterRef.NumMembers()
		},
	})

	// Initialize cluster membership
	cluster := membership.NewCluster(cfg, m, logger)
	clusterRef = cluster

	// Set state delegate before starting the cluster
	cluster.SetStateDelegate(stateDelegate)

	// Start the cluster
	if err := cluster.Start(); err != nil {
		logger.Error("failed to start cluster", "error", err)
		cacheManager.Close()
		os.Exit(1)
	}

	// Join the cluster in background
	go func() {
		if err := cluster.JoinCluster(); err != nil {
			logger.Error("failed to join cluster", "error", err)
		}

		// Update accounting cache with cluster member addresses
		cacheManager.UpdateNodes(cluster.MemberAddrs())
	}()

	// Initialize internal API if enabled
	var apiServer *api.Server
	if cfg.InternalAPI.Enabled {
		// Validate API key
		if err := config.ValidatePrivateAPIKey(); err != nil {
			logger.Error("internal API configuration error", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}

		apiKey, _ := config.GetPrivateAPIKey()
		apiServer, err = api.NewServer(api.ServerConfig{
			Address:         cfg.InternalAPI.Address,
			APIKey:          apiKey,
			ClusterName:     cfg.Membership.ServiceName,
			NodeID:          cfg.NodeName,
			Cluster:         cluster,
			Logger:          logger,
			Blocklist:       cacheManager.Blocklist,
			Broadcaster:     stateDelegate,
			DefaultBlockTTL: time.Duration(cfg.Cache.BlocklistDefaultTTLSeconds) * time.Second,
		})
		if err != nil {
			logger.Error("failed to create internal API server", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}

		if err := apiServer.Start(); err != nil {
			logger.Error("failed to start internal API server", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}
		logger.Info("internal API server started", "address", cfg.InternalAPI.Address)
	}

	// Initialize gRPC ext_authz server
	grpcServer := drlgrpc.NewServer(drlgrpc.ServerConfig{
		Address: cfg.Listen.GRPC,
		Metrics: m,
		Logger:  logger,
	})
	if err := grpcServer.Start(); err != nil {
		logger.Error("failed to start gRPC server", "error", err)
		cacheManager.Close()
		os.Exit(1)
	}
	logger.Info("gRPC ext_authz server started", "address", cfg.Listen.GRPC)

	logger.Info("DRL is running")

	// Wait for shutdown signal
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if apiServer != nil {
		if err := apiServer.Stop(shutdownCtx); err != nil {
			logger.Error("failed to stop internal API server", "error", err)
		}
	}

	grpcServer.Stop(shutdownCtx)

	if err := cluster.Leave(5 * time.Second); err != nil {
		logger.Error("failed to leave cluster gracefully", "error", err)
	}

	// Close cache manager
	cacheManager.Close()

	if err := m.Stop(); err != nil {
		logger.Error("failed to stop metrics server", "error", err)
	}

	logger.Info("DRL shutdown complete")
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
