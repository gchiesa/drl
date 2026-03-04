package grpc

import (
	"context"
	"log/slog"
	"net"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/gchiesa/drl/internal/metrics"
)

// AccountingEngine provides async request accounting.
type AccountingEngine interface {
	Process(sourceIP, path string, headers map[string]string)
}

// ServerConfig holds configuration for the gRPC ext_authz server.
type ServerConfig struct {
	Address string
	Metrics *metrics.Metrics
	Logger  *slog.Logger
	Engine  AccountingEngine
}

// Server implements the Envoy ext_authz v3 Authorization gRPC service.
type Server struct {
	authv3.UnimplementedAuthorizationServer

	grpcServer *grpc.Server
	address    string
	metrics    *metrics.Metrics
	logger     *slog.Logger
	listener   net.Listener
	engine     AccountingEngine
}

// NewServer creates a new gRPC ext_authz server.
func NewServer(cfg ServerConfig) *Server {
	gs := grpc.NewServer()

	s := &Server{
		grpcServer: gs,
		address:    cfg.Address,
		metrics:    cfg.Metrics,
		logger:     cfg.Logger,
		engine:     cfg.Engine,
	}

	authv3.RegisterAuthorizationServer(gs, s)

	return s
}

// Check implements the ext_authz v3 Authorization Check RPC.
// In dry-run mode it always returns OK while parsing and logging request metadata.
func (s *Server) Check(_ context.Context, req *authv3.CheckRequest) (*authv3.CheckResponse, error) {
	s.metrics.IncGRPCCheck()

	var sourceIP, path string
	var headers map[string]string

	if attrs := req.GetAttributes(); attrs != nil {
		if src := attrs.GetSource(); src != nil {
			if addr := src.GetAddress(); addr != nil {
				if sa := addr.GetSocketAddress(); sa != nil {
					sourceIP = sa.GetAddress()
				}
			}
		}
		if httpReq := attrs.GetRequest(); httpReq != nil {
			if h := httpReq.GetHttp(); h != nil {
				path = h.GetPath()
				headers = h.GetHeaders()
			}
		}
	}

	s.logger.Debug("received Check request",
		"source_ip", sourceIP,
		"path", path,
		"headers", headers,
	)

	if s.engine != nil {
		go s.engine.Process(sourceIP, path, headers)
	}

	return &authv3.CheckResponse{
		Status: &status.Status{
			Code: int32(codes.OK),
		},
	}, nil
}

// Start begins listening on the configured address and serves gRPC requests.
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return err
	}
	s.listener = lis

	s.logger.Info("gRPC ext_authz server listening", "address", lis.Addr().String())

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			s.logger.Error("gRPC server error", "error", err)
		}
	}()

	return nil
}

// Addr returns the listener address, useful for tests that bind to ":0".
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// Stop gracefully stops the gRPC server. If the context expires before
// GracefulStop completes, it falls back to a hard Stop.
func (s *Server) Stop(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("gRPC server stopped gracefully")
	case <-ctx.Done():
		s.grpcServer.Stop()
		s.logger.Warn("gRPC server stopped forcefully")
	}
}
