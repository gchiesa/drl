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
func newCluster(cfg *config.Config, cacheManager *cache.Manager, metricsManager *metrics.Metrics, log *slog.Logger) *membership.Cluster {
	// clusterRef is captured by the NumNodesFunc closure below.
	// It is assigned immediately after NewCluster returns, before any broadcast
	// can be triggered, so no additional synchronization is required.
	var clusterRef *membership.Cluster

	// Create state delegate for blocklist sync
	stateDelegate := membership.NewStateDelegate(membership.DelegateConfig{
		Blocklist:   cacheManager.Blocklist,
		Metrics:     metricsManager,
		Logger:      log,
		SyncTimeout: time.Duration(cfg.Cache.SyncTimeoutSeconds) * time.Second,
		NumNodesFunc: func() int {
			if clusterRef == nil {
				return 1
			}
			return clusterRef.NumMembers()
		},
	})

	// Initialize cluster membership
	cluster := membership.NewCluster(cfg, metricsManager, log)
	clusterRef = cluster

	// Set state delegate before starting the cluster
	cluster.SetStateDelegate(stateDelegate)

	// Start the cluster
	if err := cluster.Start(); err != nil {
		log.Error("failed to start cluster", "error", err)
		cacheManager.Close()
		os.Exit(1)
	}

	// Join the cluster in background
	go func() {
		if err := cluster.JoinCluster(); err != nil {
			log.Error("failed to join cluster", "error", err)
		}

		// Update accounting cache with cluster member addresses
		cacheManager.UpdateNodes(cluster.MemberAddrs())
	}()
	return cluster
}
