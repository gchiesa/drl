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

// Server represents the internal API server
type Server struct {
	app         *fiber.App
	auth        *SCRAMAuthenticator
	logger      *slog.Logger
	address     string
	clusterName string
	nodeID      string
	cluster     ClusterInfo
	startTime   time.Time
}

// ServerConfig holds configuration for the API server
type ServerConfig struct {
	Address     string
	APIKey      string
	ClusterName string
	NodeID      string
	Cluster     ClusterInfo
	Logger      *slog.Logger
}

// NewServer creates a new internal API server
func NewServer(cfg ServerConfig) (*Server, error) {
	// Validate API key
	if len(cfg.APIKey) < MinAPIKeyLength {
		return nil, fmt.Errorf("API key must be at least %d characters", MinAPIKeyLength)
	}

	// Create SCRAM authenticator
	auth, err := NewSCRAMAuthenticator(cfg.APIKey)
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
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// setupRoutes configures the API routes
func (s *Server) setupRoutes() {
	// Apply SCRAM authentication middleware to /status
	s.app.Get("/status", s.auth.Middleware(), s.handleStatus)
}

// handleStatus returns the cluster status
func (s *Server) handleStatus(c *fiber.Ctx) error {
	uptime := time.Since(s.startTime)

	var activePeers []string
	if s.cluster != nil {
		activePeers = s.cluster.MemberNames()
	}

	return c.JSON(fiber.Map{
		"cluster_name":   s.clusterName,
		"node_id":        s.nodeID,
		"active_peers":   activePeers,
		"uptime":         uptime.String(),
		"uptime_seconds": uptime.Seconds(),
	})
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
