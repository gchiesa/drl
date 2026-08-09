package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/membership"
	"github.com/gchiesa/drl/internal/metrics"
)

// newCluster initializes and starts a cluster, sets configuration, state delegate, and handles node membership updates.
func newCluster(cfg *config.Config, localIP string, cacheManager *cache.Manager, metricsManager *metrics.Metrics, log *slog.Logger) (*membership.Cluster, error) {
	// clusterRef is captured by the NumNodesFunc closure below.
	// It is assigned immediately after NewCluster returns, before any broadcast
	// can be triggered, so no additional synchronization is required.
	var clusterRef *membership.Cluster

	if cacheManager == nil {
		return nil, fmt.Errorf("cache must be provided")
	}
	if log == nil {
		return nil, fmt.Errorf("logger must be provided")
	}
	if metricsManager == nil {
		return nil, fmt.Errorf("metrics must be provided")
	}

	// Create state delegate for blocklist sync and accounting message handling
	stateDelegate := membership.NewStateDelegate(membership.DelegateConfig{
		Blocklist:   cacheManager.Blocklist,
		Accounting:  cacheManager.Accounting,
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
	cluster := membership.NewCluster(cfg, localIP, cacheManager, metricsManager, log)
	clusterRef = cluster

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

	// Start the persistent gRPC channel for hi-priority (block/unblock)
	// event propagation, when enabled via config/DRL_MEMBERSHIP_USE_HIPRIO_PERSISTENT_CHANNEL.
	if cfg.Membership.UseHiPrioPersistentChannel {
		channelManager := membership.NewChannelManager(membership.ChannelManagerConfig{
			LocalAddr: localIP,
			Port:      cfg.Membership.HiPrioChannelPort,
			Handler:   stateDelegate,
			Metrics:   metricsManager,
			Logger:    log,
		})
		if err := channelManager.Start(); err != nil {
			log.Error("failed to start persistent gRPC channel", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}
		cluster.SetChannelManager(channelManager)
		log.Info("persistent gRPC channel enabled", "port", cfg.Membership.HiPrioChannelPort)
	}

	// Join the cluster in the background
	go func() {
		if err := cluster.JoinCluster(); err != nil {
			log.Error("failed to join cluster", "error", err)
		}
	}()
	return cluster, nil
}
