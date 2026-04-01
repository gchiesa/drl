package cmd

import (
	"log/slog"
	"os"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/membership"
	"github.com/gchiesa/drl/internal/metrics"
)

// newCluster initializes and starts a cluster, sets configuration, state delegate, and handles node membership updates.
func newCluster(cfg *config.Config, localIP string, cacheManager *cache.Manager, metricsManager *metrics.Metrics, log *slog.Logger) *membership.Cluster {
	// Create state delegate for blocklist sync and accounting message handling
	stateDelegate := membership.NewStateDelegate(membership.DelegateConfig{
		Blocklist:   cacheManager.Blocklist,
		Accounting:  cacheManager.Accounting,
		Metrics:     metricsManager,
		Logger:      log,
		SyncTimeout: time.Duration(cfg.Cache.SyncTimeoutSeconds) * time.Second,
	})

	// Initialize cluster membership
	cluster := membership.NewCluster(cfg, localIP, cacheManager, metricsManager, log)

	// Set state delegate before starting the cluster
	cluster.SetStateDelegate(stateDelegate)

	// Set cluster reference on delegate for SendReliable operations
	stateDelegate.SetCluster(cluster)

	// Start the cluster
	if err := cluster.Start(); err != nil {
		log.Error("failed to start cluster", "error", err)
		cacheManager.Close()
		os.Exit(1)
	}

	// Join the cluster in the background
	go func() {
		if err := cluster.JoinCluster(); err != nil {
			log.Error("failed to join cluster", "error", err)
		}
	}()
	return cluster
}
