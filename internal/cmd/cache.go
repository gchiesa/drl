package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// newCache initializes and returns a cache manager instance based on the given config, metrics, and logger.
func newCache(cfg *config.Config, localIP string, metric *metrics.Metrics, log *slog.Logger) (*cache.Manager, error) {
	if metric == nil {
		return nil, fmt.Errorf("metrics must be provided")
	}
	if log == nil {
		return nil, fmt.Errorf("logger must be provided")
	}

	cacheManager, err := cache.NewManager(cache.ManagerConfig{
		BlocklistSizeMB:  cfg.Cache.BlocklistSizeMB,
		AccountingSizeMB: cfg.Cache.AccountingSizeMB,
		LocalNode:        localIP,
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
	return cacheManager, nil
}
