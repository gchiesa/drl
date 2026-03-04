package cmd

import (
	"log/slog"
	"os"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// newCache initializes and returns a cache manager instance based on the given config, metrics, and logger.
func newCache(cfg *config.Config, metric *metrics.Metrics, log *slog.Logger) *cache.Manager {
	// Initialize cache manager
	cacheManager, err := cache.NewManager(cache.ManagerConfig{
		BlocklistSizeMB:  cfg.Cache.BlocklistSizeMB,
		AccountingSizeMB: cfg.Cache.AccountingSizeMB,
		LocalNode:        cfg.NodeName,
		WindowSize:       time.Minute, // Rate limiting window
		Logger:           log,
		// Connect metrics callbacks
		OnBlocklistHit: func() {
			metric.IncCacheHit(metrics.CacheTypeBlocklist)
		},
		OnBlocklistMiss: func() {
			metric.IncCacheMiss(metrics.CacheTypeBlocklist)
		},
		OnBlocklistEvict: func() {
			metric.IncCacheEviction(metrics.CacheTypeBlocklist)
		},
		OnAccountingHit: func() {
			metric.IncCacheHit(metrics.CacheTypeAccounting)
		},
		OnAccountingMiss: func() {
			metric.IncCacheMiss(metrics.CacheTypeAccounting)
		},
		OnAccountingEvict: func() {
			metric.IncCacheEviction(metrics.CacheTypeAccounting)
		},
	})
	if err != nil {
		log.Error("failed to create cache manager", "error", err)
		os.Exit(1)
	}
	log.Info("cache manager initialized",
		"blocklist_size_mb", cfg.Cache.BlocklistSizeMB,
		"accounting_size_mb", cfg.Cache.AccountingSizeMB,
	)
	return cacheManager
}
