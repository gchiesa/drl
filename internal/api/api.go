package api

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"
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
	Block(key string, ttl time.Duration)
	Unblock(key string)
	IsBlocked(key string) bool
}

// Broadcaster queues block/unblock events for cluster-wide eventual propagation
// via the memberlist user-level broadcast mechanism.
type Broadcaster interface {
	QueueBlockEvent(key string, ttl time.Duration) error
	QueueUnblockEvent(key string) error
}

// Server represents the internal API server
type Server struct {
	app         *fiber.App
	auth        *DigestAuthenticator
	logger      *slog.Logger
	address     string
	clusterName string
	nodeID      string
	cluster     ClusterInfo
	startTime   time.Time
	blocklist   BlocklistOperator
	broadcaster Broadcaster
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

	server := &Server{
		app:         app,
		auth:        auth,
		logger:      cfg.Logger,
		address:     cfg.Address,
		clusterName: cfg.ClusterName,
		nodeID:      cfg.NodeID,
		cluster:     cfg.Cluster,
		startTime:   time.Now(),
		blocklist:   cfg.Blocklist,
		broadcaster: cfg.Broadcaster,
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
	// :ip captures the client IP, then the greedy wildcard captures the full
	// URI path and optional /_headers/<key:val,...> suffix.
	s.app.Post("/blocked-entity/:ip/_path/*", authMW, s.handleBlockEntityAdd)
	s.app.Delete("/blocked-entity/:ip/_path/*", authMW, s.handleBlockEntityDelete)
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
