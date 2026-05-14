package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/gchiesa/drl/internal/accounting"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/ratelimit"
)

// newRateLimiter constructs the RateLimiter implementation selected by the
// accounting settings. Defaults to SlidingWindow when the algorithm is unknown.
func newRateLimiter(cfg *config.Config, log *slog.Logger) ratelimit.RateLimiter {
	switch strings.ToLower(cfg.Accounting.Settings.Algorithm) {
	case "token-bucket":
		log.Info("rate limiter: token-bucket",
			"capacity", cfg.Accounting.Settings.Capacity,
			"refill_rate", cfg.Accounting.Settings.RefillRate,
		)
		return ratelimit.NewTokenBucket(
			float64(cfg.Accounting.Settings.Capacity),
			cfg.Accounting.Settings.RefillRate,
		)
	default:
		log.Info("rate limiter: sliding-window")
		return ratelimit.NewSlidingWindow()
	}
}

// newAccountingEngine initializes and returns a new accounting.Engine based on the provided configuration and dependencies.
// It sets up the accounting flusher and applies the accounting rules. If initialization fails, the application exits.
func newAccountingEngine(
	cfg *config.Config,
	localIP string,
	cacheManager *cache.Manager,
	metricsManager *metrics.Metrics,
	blocklist accounting.BlocklistEnforcer,
	broadcaster accounting.BlockBroadcaster,
	sender accounting.NodeSender,
	log *slog.Logger,
) (*accounting.Engine, error) {
	var flusher *accounting.Flusher
	var engine *accounting.Engine

	if cacheManager == nil {
		return nil, fmt.Errorf("cache manager must be provided")
	}
	if metricsManager == nil {
		return nil, fmt.Errorf("metrics manager must be provided")
	}
	if log == nil {
		return nil, fmt.Errorf("logger must be provided")
	}
	if len(cfg.Accounting.Rules) > 0 {
		senderID := xxhash.Sum64String(localIP)

		flusher = accounting.NewFlusher(accounting.FlusherConfig{
			SenderID:      senderID,
			Accounting:    cacheManager.Accounting,
			Logger:        log,
			Metrics:       metricsManager,
			Sender:        sender,
			FlushInterval: cfg.Accounting.Settings.FlushInterval,
			MaxBatchSize:  cfg.Accounting.Settings.MaxBatchSize,
		})
		if err := flusher.Start(); err != nil {
			log.Error("failed to start accounting flusher", "error", err)
			cacheManager.Close()
			os.Exit(1)
		}
		log.Info("accounting flusher started")

		limiter := newRateLimiter(cfg, log)

		engine = accounting.NewEngine(accounting.EngineConfig{
			Rules:       cfg.Accounting.Rules,
			Settings:    cfg.Accounting.Settings,
			Accounting:  cacheManager.Accounting,
			Flusher:     flusher,
			Logger:      log,
			Metrics:     metricsManager,
			Limiter:     limiter,
			Blocklist:   blocklist,
			Broadcaster: broadcaster,
		})
		log.Info("accounting engine initialized",
			"rules", len(cfg.Accounting.Rules),
			"algorithm", cfg.Accounting.Settings.Algorithm,
		)
	} else {
		return nil, fmt.Errorf("accounting rules must be provided")
	}
	return engine, nil
}
