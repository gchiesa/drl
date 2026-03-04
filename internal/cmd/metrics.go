package cmd

import (
	"log/slog"
	"os"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// newMetrics initializes and starts a metrics server using the provided configuration and logger.
// It returns a pointer to the initialized Metrics instance.
func newMetrics(cfg *config.Config, log *slog.Logger) *metrics.Metrics {
	ms := metrics.NewMetrics()
	if err := ms.StartServer(cfg.MetricsPort()); err != nil {
		log.Error("failed to start metrics server", "error", err)
		os.Exit(1)
	}
	log.Info("metrics server started", "port", cfg.MetricsPort())
	return ms
}
