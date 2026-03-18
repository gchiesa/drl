package cmd

import (
	"log/slog"
	"os"

	"github.com/cespare/xxhash/v2"
	"github.com/gchiesa/drl/internal/accounting"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// newAccountingEngine initializes and returns a new accounting.Engine based on the provided configuration and dependencies.
// It sets up the accounting flusher and applies the accounting rules. If initialization fails, the application exits.
func newAccountingEngine(
	cfg *config.Config,
	localIP string,
	cacheManager *cache.Manager,
	metricsManager *metrics.Metrics,
	blocklist accounting.BlocklistEnforcer,
	broadcaster accounting.BlockBroadcaster,
	log *slog.Logger,
) *accounting.Engine {
	var flusher *accounting.Flusher
	var engine *accounting.Engine

	if len(cfg.Accounting.Rules) > 0 {
		senderID := xxhash.Sum64String(localIP)

		flusher = accounting.NewFlusher(accounting.FlusherConfig{
			SenderID:   senderID,
			Accounting: cacheManager.Accounting,
			Logger:     log,
			Metrics:    metricsManager,
			SyncPort:   accounting.DefaultSyncPort,
		})
		if err := flusher.Start(); err != nil {
			log.Error("failed to start accounting flusher", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}
		log.Info("accounting flusher started", "sync_port", accounting.DefaultSyncPort)

		engine = accounting.NewEngine(accounting.EngineConfig{
			Rules:       cfg.Accounting.Rules,
			Accounting:  cacheManager.Accounting,
			Flusher:     flusher,
			Logger:      log,
			Metrics:     metricsManager,
			Blocklist:   blocklist,
			Broadcaster: broadcaster,
		})
		log.Info("accounting engine initialized", "rules", len(cfg.Accounting.Rules))
	}
	return engine
}
