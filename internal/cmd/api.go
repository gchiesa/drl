package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"time"

	"github.com/gchiesa/drl/internal/accounting"
	"github.com/gchiesa/drl/internal/api"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/membership"
	"github.com/gchiesa/drl/internal/metrics"
)

// newApiServer initializes and starts a new internal API server based on the provided configuration and dependencies.
// It validates the API key, sets up the server configuration, and ensures proper startup or handles failures gracefully.
// Parameters:
// - cfg: Configuration for the server including addresses, membership, and cache settings.
// - cacheManager: Manages the blocklist and accounting caches.
// - clusterManager: Handles cluster membership and state synchronization.
// - accountingEngine: Provides accounting stats for the API server if enabled.
// - log: Logger for internal server events and errors.
// Returns an initialized and running *api.Server instance or nil on failure.
func newApiServer(
	cfg *config.Config,
	localIP string,
	cacheManager *cache.Manager,
	clusterManager *membership.Cluster,
	accountingEngine *accounting.Engine,
	metricsManager *metrics.Metrics,
	log *slog.Logger,
) (*api.Server, error) {

	var apiServer *api.Server
	var err error

	if clusterManager == nil {
		return nil, fmt.Errorf("cluster manager must be provided")
	}
	if cacheManager == nil {
		return nil, fmt.Errorf("cache manager must be provided")
	}
	if accountingEngine == nil {
		return nil, fmt.Errorf("accounting engine must be provided")
	}
	if metricsManager == nil {
		return nil, fmt.Errorf("metrics manager must be provided")
	}
	if log == nil {
		return nil, fmt.Errorf("logger must be provided")
	}
	if cfg.InternalAPI.Enabled {
		// Validate API key
		if err := config.ValidatePrivateAPIKey(); err != nil {
			log.Error("internal API configuration error", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}

		apiKey, _ := config.GetPrivateAPIKey()

		// Compile header redactions from all accounting rules into a single map.
		// When the same header is configured in multiple rules the first definition wins.
		headerRedactions := make(map[string]*regexp.Regexp)
		for _, rule := range cfg.Accounting.Rules {
			for header, pattern := range rule.HeaderRedactions {
				if _, exists := headerRedactions[header]; exists {
					continue
				}
				compiled, err := regexp.Compile(pattern)
				if err != nil {
					// patterns are validated at config load time; this is a safety net
					log.Warn("skipping invalid header redaction pattern",
						"header", header, "pattern", pattern, "error", err)
					continue
				}
				headerRedactions[header] = compiled
			}
		}

		apiCfg := api.ServerConfig{
			Address:                   cfg.InternalAPI.Address,
			APIKey:                    apiKey,
			ClusterName:               cfg.Membership.ServiceName,
			NodeID:                    localIP,
			Cluster:                   clusterManager,
			Logger:                    log,
			Blocklist:                 cacheManager.Blocklist,
			BlocklistHeaderRedactions: headerRedactions,
			Broadcaster:               clusterManager.GetStateDelegate(),
			DefaultBlockTTL:           time.Duration(cfg.Cache.BlocklistDefaultTTLSeconds) * time.Second,
			MetricsGatherer:           metricsManager,
			AccountingStats:           accountingEngine,
			BulkLoader:                accountingEngine,
			Metrics:                   metricsManager,
			StaticConfig:              cfg,
		}

		apiServer, err = api.NewServer(apiCfg)
		if err != nil {
			log.Error("failed to create internal API server", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}

		if err := apiServer.Start(); err != nil {
			log.Error("failed to start internal API server", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}
		log.Info("internal API server started", "address", cfg.InternalAPI.Address)
	}
	return apiServer, nil
}
