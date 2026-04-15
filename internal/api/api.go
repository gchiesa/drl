package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/model"
)

// ClusterInfo provides cluster information for the status endpoint
type ClusterInfo interface {
	IsReady() bool
	NumMembers() int
	MemberNames() []string
}

// BlocklistOperator allows the API to add and remove entities from the local
// Ristretto blocklist cache.
type BlocklistOperator interface {
	Block(key string, ttl time.Duration, entity *model.Entity)
	Unblock(key string)
	IsBlocked(key string) bool
	ListEntries() []model.BlockedEntityInfo
}

// Broadcaster queues block/unblock events for cluster-wide eventual propagation
// via the memberlist user-level broadcast mechanism.
type Broadcaster interface {
	QueueBlockEvent(key string, ttl time.Duration, entity *model.Entity) error
	QueueUnblockEvent(key string) error
}

// AccountingStatsProvider exposes accounting statistics for the stats endpoint.
type AccountingStatsProvider interface {
	PendingUpdates() int64
	TrackedEntities() int64
	EstimatedEntities() int64
}

// AccountingBulkLoader ingests a single bulk-load record into the accounting
// engine. Implementations return one of the bulk-load outcome strings:
// "no_match", "accepted_local", "accepted_remote", or "dropped".
type AccountingBulkLoader interface {
	BulkLoad(sourceIP, path string, headers map[string]string, distributionEnabled bool) string
}

// BulkLoadMetricsRecorder lets the bulk-load handler bump the
// drl_accounting_bulk_load_total counter for parser-side outcomes
// (e.g. "invalid") that the engine doesn't know about.
type BulkLoadMetricsRecorder interface {
	IncAccountingBulkLoad(result string)
}

// Server represents the internal API server
type Server struct {
	app             *fiber.App
	auth            *DigestAuthenticator
	logger          *slog.Logger
	address         string
	clusterName     string
	nodeID          string
	cluster         ClusterInfo
	startTime       time.Time
	blocklist       BlocklistOperator
	broadcaster     Broadcaster
	defaultBlockTTL time.Duration
	accountingStats AccountingStatsProvider
	bulkLoader      AccountingBulkLoader
	metrics         BulkLoadMetricsRecorder
}

// ServerConfig holds configuration for the API server
type ServerConfig struct {
	Address     string
	APIKey      string
	ClusterName string
	NodeID      string
	Cluster     ClusterInfo
	Logger      *slog.Logger
	// Blocklist is optional; when set, the block-entity endpoints are active.
	Blocklist BlocklistOperator
	// Broadcaster is optional; when set, admin block/unblock events are gossiped
	// to the rest of the cluster.
	Broadcaster Broadcaster
	// DefaultBlockTTL is the default time-to-live for admin-API blocks.
	// Overridden per-request via ?ttl= query parameter.
	DefaultBlockTTL time.Duration
	// AccountingStats is optional; when set, the /accounting/stats endpoint is active.
	AccountingStats AccountingStatsProvider
	// BulkLoader is optional; when set, the POST /accounting/load endpoint is active.
	BulkLoader AccountingBulkLoader
	// Metrics is optional; when set, the bulk-load handler records parser-side outcomes.
	Metrics BulkLoadMetricsRecorder
}

// NewServer creates a new internal API server
func NewServer(cfg ServerConfig) (*Server, error) {
	// Create Digest authenticator
	auth, err := NewDigestAuthenticator(cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	// Create Fiber app
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
		AppName:               "DRL Internal API",
	})

	defaultTTL := cfg.DefaultBlockTTL
	if defaultTTL == 0 {
		defaultTTL = 24 * time.Hour
	}

	server := &Server{
		app:             app,
		auth:            auth,
		logger:          cfg.Logger,
		address:         cfg.Address,
		clusterName:     cfg.ClusterName,
		nodeID:          cfg.NodeID,
		cluster:         cfg.Cluster,
		startTime:       time.Now(),
		blocklist:       cfg.Blocklist,
		broadcaster:     cfg.Broadcaster,
		defaultBlockTTL: defaultTTL,
		accountingStats: cfg.AccountingStats,
		bulkLoader:      cfg.BulkLoader,
		metrics:         cfg.Metrics,
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// setupRoutes configures the API routes
func (s *Server) setupRoutes() {
	authMW := s.auth.Middleware()

	// Cluster status
	s.app.Get("/status", authMW, s.handleStatus)

	// Admin blocklist management.
	s.app.Get("/blocked-entity", authMW, s.handleBlockEntityList)

	// :ip captures the client IP, then the greedy wildcard captures the full
	// URI path and optional /_headers/<key:val,...> suffix.
	s.app.Post("/blocked-entity/:ip/_path/*", authMW, s.handleBlockEntityAdd)
	s.app.Delete("/blocked-entity/:ip/_path/*", authMW, s.handleBlockEntityDelete)

	// Accounting stats
	s.app.Get("/accounting/stats", authMW, s.handleAccountingStats)

	// Bulk-load accounting (testing endpoint, milestone 014)
	if s.bulkLoader != nil {
		s.app.Post("/accounting/load", authMW, s.handleAccountingLoad)
	}
}

// Start starts the internal API server
func (s *Server) Start() error {
	s.logger.Info("starting internal API server", "address", s.address)

	go func() {
		if err := s.app.Listen(s.address); err != nil {
			s.logger.Error("internal API server error", "error", err)
		}
	}()

	return nil
}

// Stop gracefully stops the internal API server
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("stopping internal API server")
	return s.app.ShutdownWithContext(ctx)
}
