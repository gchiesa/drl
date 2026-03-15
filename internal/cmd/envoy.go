package cmd

import (
	"log/slog"
	"os"

	"github.com/gchiesa/drl/internal/accounting"
	"github.com/gchiesa/drl/internal/api"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/grpc"
	"github.com/gchiesa/drl/internal/metrics"
)

// newGRPCServer initializes and starts a new gRPC server with the provided configuration, cache manager, and dependencies.
// It sets up signal handling for graceful shutdown and incorporates optional components like accounting and API server.
// Returns the initialized *grpc.Server instance.
func newGRPCServer(
	cfg *config.Config,
	cacheManager *cache.Manager,
	accountingEngine *accounting.Engine,
	apiServer *api.Server,
	metricsManager *metrics.Metrics,
	log *slog.Logger) *grpc.Server {
	// Initialize gRPC ext_authz server for envoy
	grpcCfg := grpc.ServerConfig{
		Address:   cfg.Listen.GRPC,
		Metrics:   metricsManager,
		Logger:    log,
		Blocklist: cacheManager.Blocklist,
	}

	if accountingEngine != nil {
		grpcCfg.Engine = accountingEngine
	}

	grpcServer := grpc.NewServer(grpcCfg)
	if err := grpcServer.Start(); err != nil {
		log.Error("failed to start gRPC server", "error", err)
		cacheManager.Close()
		os.Exit(1)
	}
	log.Info("gRPC ext_authz server started", "address", cfg.Listen.GRPC)
	return grpcServer
}
