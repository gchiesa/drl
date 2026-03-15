package grpc

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
)

// AccountingEngine provides async request accounting.
type AccountingEngine interface {
	Process(sourceIP, path string, headers map[string]string)
}

// BlocklistChecker checks whether an entity key is blocked.
type BlocklistChecker interface {
	IsBlocked(key string) bool
}

// ServerConfig holds configuration for the gRPC ext_authz server.
type ServerConfig struct {
	Address   string
	Metrics   *metrics.Metrics
	Logger    *slog.Logger
	Engine    AccountingEngine
	Blocklist BlocklistChecker
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
	blocklist  BlocklistChecker
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
		blocklist:  cfg.Blocklist,
	}

	authv3.RegisterAuthorizationServer(gs, s)

	return s
}

// Check implements the ext_authz v3 Authorization Check RPC.
// It checks the blocklist first; if the entity is blocked, it returns
// PermissionDenied (429) with a Retry-After header. Otherwise it returns OK
// and fires async accounting.
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

	// Check blocklist before accounting
	if s.blocklist != nil {
		entity := model.Entity{
			IP:      sourceIP,
			Path:    path,
			Headers: headers,
		}
		key := entity.Key()

		if s.blocklist.IsBlocked(key) {
			s.logger.Debug("entity blocked",
				"key", key,
				"source_ip", sourceIP,
				"path", path,
			)
			if s.metrics != nil {
				s.metrics.IncGRPCResponseCode("DENIED")
			}
			return &authv3.CheckResponse{
				Status: &status.Status{
					Code:    int32(codes.PermissionDenied),
					Message: "rate limit exceeded",
				},
				HttpResponse: &authv3.CheckResponse_DeniedResponse{
					DeniedResponse: &authv3.DeniedHttpResponse{
						Status: &typev3.HttpStatus{
							Code: typev3.StatusCode_TooManyRequests,
						},
						Headers: []*corev3.HeaderValueOption{
							{
								Header: &corev3.HeaderValue{
									Key:   "Retry-After",
									Value: fmt.Sprintf("%d", 60),
								},
							},
						},
					},
				},
			}, nil
		}
	}

	if s.engine != nil {
		go s.engine.Process(sourceIP, path, headers)
	}

	if s.metrics != nil {
		s.metrics.IncGRPCResponseCode("OK")
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
