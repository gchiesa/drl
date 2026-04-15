package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/model"
)

// ClusterInfo provides cluster information for the status endpoint
type ClusterInfo interface {
	IsReady() bool
	NumMembers() int
	MemberNames() []string
	// MemberAddrs returns the raw IP addresses of all cluster members
	// (including this node). Used to derive peer API addresses for
	// cross-node aggregation.
	MemberAddrs() []string
	// LocalAddr returns the IP address of this node. Used to exclude the
	// current node from the peer API address list so the SPA does not
	// double-fetch its own metrics via the proxy.
	LocalAddr() string
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

// StaticConfigProvider returns the JSON-serialisable representation of a named
// top-level configuration section (e.g. "accounting", "membership", "cache").
type StaticConfigProvider interface {
	GetConfigSection(section string) (any, bool)
}

// MetricsGatherer gathers current Prometheus metric values for the UI dashboard.
// Implementations return a flat map of metric name (plus label suffix) to value.
type MetricsGatherer interface {
	GatherForUI() map[string]float64
}

// Server represents the internal API server
type Server struct {
	app             *fiber.App
	auth            *DigestAuthenticator
	uiAuth          *uiAuthManager
	logger          *slog.Logger
	address         string
	apiPort         string // port portion of address (e.g. "8082")
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
	staticConfig    StaticConfigProvider
	metricsGatherer MetricsGatherer
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
	// StaticConfig is optional; when set, the GET /configuration/static/:section endpoint is active.
	StaticConfig StaticConfigProvider
	// MetricsGatherer is optional; when set, the GET /drl/ui/api/metrics endpoint returns
	// a JSON snapshot of current Prometheus metric values for the dashboard.
	MetricsGatherer MetricsGatherer
}

// NewServer creates a new internal API server
func NewServer(cfg ServerConfig) (*Server, error) {
	// Create Digest authenticator
	auth, err := NewDigestAuthenticator(cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	// Create UI auth manager (ECDH + session management)
	uiAuth, err := newUIAuthManager(cfg.APIKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create UI auth manager: %w", err)
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

	// Extract port from address (e.g. ":8082" → "8082")
	apiPort := cfg.Address
	if idx := strings.LastIndex(apiPort, ":"); idx >= 0 {
		apiPort = apiPort[idx+1:]
	}

	server := &Server{
		app:             app,
		auth:            auth,
		uiAuth:          uiAuth,
		logger:          cfg.Logger,
		address:         cfg.Address,
		apiPort:         apiPort,
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
		staticConfig:    cfg.StaticConfig,
		metricsGatherer: cfg.MetricsGatherer,
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// dualAuthMiddleware returns a Fiber handler that accepts:
//   - "Authorization: DRL-Session <token>" — browser SPA sessions (ECDH-derived)
//   - "Authorization: Bearer <token>"      — same as DRL-Session, alternative prefix
//   - HTTP Digest authentication            — CLI tools (curl, scripts)
//
// When neither is present the client receives a standard Digest challenge,
// matching the existing CLI behaviour.
func (s *Server) dualAuthMiddleware() fiber.Handler {
	digestMW := s.auth.Middleware()
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		var sessionToken string
		switch {
		case strings.HasPrefix(auth, "DRL-Session "):
			sessionToken = strings.TrimPrefix(auth, "DRL-Session ")
		case strings.HasPrefix(auth, "Bearer "):
			sessionToken = strings.TrimPrefix(auth, "Bearer ")
		}
		if sessionToken != "" {
			if s.uiAuth != nil && s.uiAuth.ValidateSession(sessionToken) {
				return c.Next()
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid or expired DRL session",
			})
		}
		// Fall through to Digest auth (CLI path).
		return digestMW(c)
	}
}

// setupRoutes configures the API routes
func (s *Server) setupRoutes() {
	authMW := s.dualAuthMiddleware()

	// ── UI routes ─────────────────────────────────────────────────────────────
	// Serve the Svelte SPA (no auth — bootstrap token is injected into HTML).
	s.app.Get("/drl/ui", s.handleUIIndex)
	s.app.Get("/drl/ui/", s.handleUIIndex)

	// ECDH key exchange (protected only by the bootstrap token in the request body).
	s.app.Post("/drl/ui/exchange", s.handleUIExchange)

	// Metrics snapshot for the dashboard (dual-auth).
	s.app.Get("/drl/ui/api/metrics", authMW, s.handleUIMetrics)

	// Cross-node proxy: GET /drl/ui/proxy/:nodeAddr/*endpoint
	// :nodeAddr — URL-encoded "host:port" of the peer's private API
	// *endpoint — the API path to forward
	s.app.Get("/drl/ui/proxy/:nodeAddr/*", authMW, s.handleUIProxy)

	// ── Existing admin API routes (now dual-auth aware) ────────────────────────
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

	// Static configuration introspection
	s.app.Get("/configuration/static/:section", authMW, s.handleGetStaticConfig)
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
