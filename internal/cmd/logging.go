package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gchiesa/drl/internal/config"
	"github.com/phsym/zeroslog"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/diode"
)

// newLoggerSlogCompatible initializes a slog-compatible logger using the provided configuration and returns it.
// It supports "text" and "json" formats and returns an error if the format is invalid or configuration fails.
// The logger includes message buffering, custom log levels, and timestamp inclusion.
func newLoggerSlogCompatible(cfg *config.Config) (logger *slog.Logger, err error) {
	isCI := os.Getenv("CI") == "true"

	var ow io.Writer
	switch strings.ToLower(cfg.Logging.Format) {
	case "text":
		ow = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
			NoColor:    !isCI}
	case "json":
		ow = os.Stdout
	default:
		return logger, fmt.Errorf("invalid logging format: %s", cfg.Logging.Format)
	}

	// log writer
	lw := diode.NewWriter(ow, 100000, 10*time.Millisecond, func(dropped int) {
		fmt.Printf("Logger dropped %d messages\n", dropped)
	})
	ll, err := zerolog.ParseLevel(cfg.Logging.Level)
	if err != nil {
		return logger, err
	}
	zerolog.SetGlobalLevel(ll)
	zl := zerolog.New(lw).With().Timestamp().Logger()

	// slog compatibility layer. Wrap the zerolog into slog handler
	handler := zeroslog.NewHandler(zl, &zeroslog.HandlerOptions{
		Level: parseLogLevel(cfg.Logging.Level),
	})
	return slog.New(handler), nil
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
