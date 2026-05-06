// Package api implements the DRL Private Management HTTP API (port 8082).
//
// # Authentication
//
// All management endpoints (except OpenAPI docs) require authentication via one of:
//
//   - HTTP Digest Authentication (SHA-256): for CLI / curl access
//   - Bearer Token (ECDH session): for browser SPA encrypted communication
//
// # Digest Auth (CLI/Management access)
//
// The SHA-256 Digest challenge-response flow never transmits the password on the wire:
//
//	Client → GET /v1/status
//	Server → 401 WWW-Authenticate: Digest realm="DRL Internal API", nonce="...", algorithm=SHA-256
//	Client computes:
//	  A1 = SHA256(username:realm:password)
//	  A2 = SHA256(method:uri)
//	  response = SHA256(A1:nonce:nc:cnonce:qop:A2)
//	Client → GET /v1/status Authorization: Digest username="...", response="..."
//	Server → 200 OK
//
// Example (curl):
//
//	curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/status
//
// # Bearer Token (ECDH Session — browser SPA)
//
// The browser SPA performs an ECDH P-256 key exchange to establish an encrypted session:
//
//  1. GET /v1/ui/get-token  (Digest auth) → bootstrap_token
//  2. POST /v1/ui/exchange  { clientPublicKey, bootstrapToken } → encrypted session token
//  3. All subsequent requests: Authorization: DRL-Session <session_token>
//     Responses are AES-256-GCM encrypted using the ECDH-derived shared key.
//
// @title DRL Private API
// @version 1.0
// @description.markdown api.md
//
// @contact.name DRL Project
// @contact.url https://github.com/gchiesa/drl
//
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
//
// @host localhost:8082
// @BasePath /v1
//
// @securityDefinitions.apikey DigestAuth
// @in header
// @name Authorization
// @description HTTP Digest Authentication (RFC 7616, SHA-256). Use curl: `curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/status`
//
// @securityDefinitions.apikey BearerToken
// @in header
// @name Authorization
// @description ECDH session token. Format: `DRL-Session <token>` or `Bearer <token>`. Obtain via POST /v1/ui/exchange after completing key exchange.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/swaggo/swag"
	scalar "github.com/yokeTH/gofiber-scalar"

	_ "github.com/gchiesa/drl/internal/api/docs" // swagger generated docs
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

// noopAccountingStats returns zeros for all metrics; used when no real
// AccountingStats provider is configured.
type noopAccountingStats struct{}

func (noopAccountingStats) PendingUpdates() int64    { return 0 }
func (noopAccountingStats) TrackedEntities() int64   { return 0 }
func (noopAccountingStats) EstimatedEntities() int64 { return 0 }

// noopMetricsGatherer returns an empty map; used when no real MetricsGatherer is configured.
type noopMetricsGatherer struct{}

func (noopMetricsGatherer) GatherForUI() map[string]float64 { return map[string]float64{} }

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
	// MetricsGatherer is optional; when set, the GET /v1/ui/api/metrics endpoint returns
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
		accountingStats: accountingStatsOrNoop(cfg.AccountingStats),
		bulkLoader:      cfg.BulkLoader,
		metrics:         cfg.Metrics,
		staticConfig:    cfg.StaticConfig,
		metricsGatherer: metricsGathererOrNoop(cfg.MetricsGatherer),
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

func accountingStatsOrNoop(s AccountingStatsProvider) AccountingStatsProvider {
	if s == nil {
		return noopAccountingStats{}
	}
	return s
}

func metricsGathererOrNoop(g MetricsGatherer) MetricsGatherer {
	if g == nil {
		return noopMetricsGatherer{}
	}
	return g
}

// encryptedResponseMiddleware wraps all DRL-Session responses with AES-256-GCM
// using the session's ECDH-derived shared key, providing E2EE between the
// browser and the node without requiring TLS.
//
// The encrypted body is returned as:
//
//	{"iv":"<base64-nonce>","data":"<base64-ciphertext>"}
//
// The browser's apiFetch() transparently decrypts this wrapper before passing
// the plain JSON to Svelte components.
//
// Plaintext pass-through occurs when:
//   - The request uses Digest auth (CLI / curl — no shared key is available)
//   - The session's shared key is not in the local map (e.g. a request
//     forwarded by the proxy to a peer node that did not originate the session)
//   - The response body is empty or status is not 2xx
func (s *Server) encryptedResponseMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if err := c.Next(); err != nil {
			return err
		}

		// Only encrypt DRL-Session responses (not Digest auth / CLI path).
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, "DRL-Session ") && !strings.HasPrefix(auth, "Bearer ") {
			return nil
		}

		// Only encrypt 2xx responses — error bodies stay readable.
		if c.Response().StatusCode() < 200 || c.Response().StatusCode() >= 300 {
			return nil
		}

		if s.uiAuth == nil {
			return nil
		}

		var sessionToken string
		switch {
		case strings.HasPrefix(auth, "DRL-Session "):
			sessionToken = strings.TrimPrefix(auth, "DRL-Session ")
		case strings.HasPrefix(auth, "Bearer "):
			sessionToken = strings.TrimPrefix(auth, "Bearer ")
		}

		sharedKey, ok := s.uiAuth.GetSessionSharedKey(sessionToken)
		if !ok {
			// Session key not local (peer node path): return plaintext.
			return nil
		}

		// Copy body before modifying the response.
		rawBody := c.Response().Body()
		if len(rawBody) == 0 {
			return nil
		}
		body := make([]byte, len(rawBody))
		copy(body, rawBody)

		iv, ciphertext, err := encryptWithSharedKey(sharedKey, body)
		if err != nil {
			s.logger.Error("e2ee response encryption failed", "error", err)
			return nil // Fail open: return plaintext rather than breaking the response
		}

		encPayload, err := json.Marshal(map[string]string{"iv": iv, "data": ciphertext})
		if err != nil {
			return nil
		}
		c.Response().SetBody(encPayload)
		c.Set("Content-Type", "application/json")
		return nil
	}
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
	// encMW encrypts 2xx response bodies for DRL-Session requests (E2EE layer).
	// It must be listed BEFORE authMW so it runs as a post-processing wrapper:
	// encMW calls c.Next() → authMW calls c.Next() → handler runs → encMW encrypts.
	encMW := s.encryptedResponseMiddleware()

	// ── OpenAPI docs (unauthenticated — exempt from Digest/ECDH middleware) ───
	// Scalar UI at /v1/apidocs (and sub-paths for embedded JS fallback).
	// Raw spec at /v1/swagger.json served directly from the generated docs.
	s.app.Use("/v1/apidocs", scalar.New(scalar.Config{
		Title:      "DRL Internal API v1",
		BasePath:   "/",
		Path:       "v1/apidocs",
		RawSpecUrl: "swagger.json",
	}))
	s.app.Get("/v1/swagger.json", func(c *fiber.Ctx) error {
		doc, err := swag.ReadDoc()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}
		c.Set("Content-Type", "application/json")
		return c.SendString(doc)
	})

	// ── v1 route group (all management endpoints) ─────────────────────────────
	v1 := s.app.Group("/v1")

	// ── UI routes ─────────────────────────────────────────────────────────────
	// Serve the Svelte SPA (no auth — non-sensitive metadata only in HTML).
	v1.Get("/ui", s.handleUIIndex)
	v1.Get("/ui/", s.handleUIIndex)

	// Token issuance: Digest auth only, returns a short-lived bootstrap token.
	v1.Get("/ui/get-token", s.auth.Middleware(), s.handleUIGetToken)

	// ECDH key exchange (protected only by the bootstrap token in the request body).
	v1.Post("/ui/exchange", s.handleUIExchange)

	// Metrics snapshot for the dashboard — E2EE encrypted for browser sessions.
	v1.Get("/ui/api/metrics", encMW, authMW, s.handleUIMetrics)

	// Cross-node proxy — response is re-encrypted by encMW for the browser.
	// Peer nodes return plaintext (no local shared key) which encMW then wraps.
	v1.Get("/ui/proxy/:nodeAddr/*", encMW, authMW, s.handleUIProxy)

	// ── Admin API routes (dual-auth + E2EE for browser sessions) ──────────────
	v1.Get("/status", encMW, authMW, s.handleStatus)

	v1.Get("/blocked-entity", encMW, authMW, s.handleBlockEntityList)
	v1.Post("/blocked-entity/:ip/_path/*", encMW, authMW, s.handleBlockEntityAdd)
	v1.Delete("/blocked-entity/:ip/_path/*", encMW, authMW, s.handleBlockEntityDelete)

	v1.Get("/accounting/stats", encMW, authMW, s.handleAccountingStats)
	if s.bulkLoader != nil {
		v1.Post("/accounting/load", encMW, authMW, s.handleAccountingLoad)
	}

	v1.Get("/configuration/static/:section", encMW, authMW, s.handleGetStaticConfig)
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
