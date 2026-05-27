package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gchiesa/drl/internal/accounting"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/proxy"
)

// newEmbeddedProxy initialises and starts the embedded reverse proxy when it is
// enabled in the configuration.  It returns nil without error when the feature
// is disabled so callers can treat a nil *proxy.Server as "not running".
//
// The proxy registers its Prometheus metrics against the shared registry owned
// by metricsManager so they are served on the existing /metrics endpoint.
// It reuses cacheManager.Blocklist for blocklist checks and accountingEngine
// for entity-key building and async accounting, mirroring the gRPC server path.
func newEmbeddedProxy(
	ctx context.Context,
	cfg *config.Config,
	cacheManager *cache.Manager,
	accountingEngine *accounting.Engine,
	metricsManager *metrics.Metrics,
	log *slog.Logger,
) (*proxy.Server, error) {
	if !cfg.EmbeddedProxy.Enabled {
		log.Info("embedded proxy is disabled, skipping")
		return nil, nil
	}

	if cacheManager == nil {
		return nil, fmt.Errorf("embedded-proxy: cache manager must be provided")
	}
	if metricsManager == nil {
		return nil, fmt.Errorf("embedded-proxy: metrics manager must be provided")
	}
	if log == nil {
		return nil, fmt.Errorf("embedded-proxy: logger must be provided")
	}

	// accountingEngine may be nil when no accounting rules are configured;
	// the proxy will still forward traffic, just without rate-limit enforcement.
	srv, err := proxy.NewServer(
		cfg.EmbeddedProxy,
		cacheManager.Blocklist,
		accountingEngine,
		metricsManager,
	)
	if err != nil {
		return nil, fmt.Errorf("embedded-proxy: create server: %w", err)
	}

	if err := srv.Start(ctx); err != nil {
		return nil, fmt.Errorf("embedded-proxy: start: %w", err)
	}

	log.Info("embedded proxy started",
		"listen", cfg.EmbeddedProxy.Listen,
		"tls", cfg.EmbeddedProxy.TLS.Enabled,
		"hosts", len(cfg.EmbeddedProxy.Hosts),
	)

	return srv, nil
}
