package grpc

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
)

// mockEngine records Process calls for testing.
type mockEngine struct {
	mu    sync.Mutex
	calls []processCall
}

type processCall struct {
	SourceIP string
	Path     string
	Headers  map[string]string
}

func (m *mockEngine) Process(sourceIP, path string, headers map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, processCall{SourceIP: sourceIP, Path: path, Headers: headers})
}

func (m *mockEngine) BuildEntityKey(sourceIP, path string, headers map[string]string) string {
	// In tests, build the key the same way the real engine does — using
	// the model.Entity directly (no header filtering, since the mock has
	// no rules). Tests that need header filtering should use the real engine.
	entity := model.Entity{IP: sourceIP, Path: path, Headers: headers}
	return entity.Key()
}

func (m *mockEngine) getCalls() []processCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]processCall, len(m.calls))
	copy(result, m.calls)
	return result
}

func testConfig() ServerConfig {
	return ServerConfig{
		Address: ":0",
		Metrics: metrics.NewMetrics(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestNewServer(t *testing.T) {
	s := NewServer(testConfig())
	assert.NotNil(t, s)
	assert.NotNil(t, s.grpcServer)
}

func TestCheck_ReturnsOK(t *testing.T) {
	s := NewServer(testConfig())
	resp, err := s.Check(context.Background(), &authv3.CheckRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())
}

func TestCheck_ParsesSourceIP(t *testing.T) {
	s := NewServer(testConfig())
	req := &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Source: &authv3.AttributeContext_Peer{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: "192.168.1.100",
						},
					},
				},
			},
		},
	}
	resp, err := s.Check(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())
}

func TestCheck_ParsesHTTPAttributes(t *testing.T) {
	s := NewServer(testConfig())
	req := &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Source: &authv3.AttributeContext_Peer{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: "10.0.0.1",
						},
					},
				},
			},
			Request: &authv3.AttributeContext_Request{
				Http: &authv3.AttributeContext_HttpRequest{
					Path:    "/api/v1/resource",
					Headers: map[string]string{"x-api-key": "abc123", "content-type": "application/json"},
				},
			},
		},
	}
	resp, err := s.Check(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())
}

func TestCheck_NilAttributes(t *testing.T) {
	s := NewServer(testConfig())
	req := &authv3.CheckRequest{Attributes: nil}
	resp, err := s.Check(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())
}

func TestCheck_IncrementsMetric(t *testing.T) {
	cfg := testConfig()
	s := NewServer(cfg)

	n := 5
	for range n {
		_, err := s.Check(context.Background(), &authv3.CheckRequest{})
		require.NoError(t, err)
	}

	assert.Equal(t, float64(n), testutil.ToFloat64(cfg.Metrics.GRPCCheckTotal))
}

func TestServerStartStop(t *testing.T) {
	s := NewServer(testConfig())
	err := s.Start()
	require.NoError(t, err)
	assert.NotNil(t, s.Addr())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Stop(ctx) // should not panic or block
}

func TestCheck_WithEngine(t *testing.T) {
	engine := &mockEngine{}
	cfg := testConfig()
	cfg.Engine = engine

	s := NewServer(cfg)
	req := &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Source: &authv3.AttributeContext_Peer{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: "192.168.1.50",
						},
					},
				},
			},
			Request: &authv3.AttributeContext_Request{
				Http: &authv3.AttributeContext_HttpRequest{
					Path:    "/api/v1/test",
					Headers: map[string]string{"x-api-key": "key123"},
				},
			},
		},
	}

	resp, err := s.Check(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())

	// Give the goroutine time to execute
	time.Sleep(50 * time.Millisecond)

	calls := engine.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "192.168.1.50", calls[0].SourceIP)
	assert.Equal(t, "/api/v1/test", calls[0].Path)
	assert.Equal(t, "key123", calls[0].Headers["x-api-key"])
}

// mockBlocklist implements BlocklistChecker for testing.
type mockBlocklist struct {
	blocked map[string]bool
}

func (m *mockBlocklist) IsBlockedWithExpiration(key string) (time.Time, bool) {
	return time.Now(), m.blocked[key]
}

func TestCheck_BlockedEntity(t *testing.T) {
	engine := &mockEngine{}
	bl := &mockBlocklist{blocked: make(map[string]bool)}
	cfg := testConfig()
	cfg.Engine = engine
	cfg.Blocklist = bl

	s := NewServer(cfg)

	// Build the key via the engine (same path the Check handler uses)
	key := engine.BuildEntityKey("10.0.0.1", "/api/v1/resource", map[string]string{"x-api-key": "abc123"})
	bl.blocked[key] = true

	req := &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Source: &authv3.AttributeContext_Peer{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: "10.0.0.1",
						},
					},
				},
			},
			Request: &authv3.AttributeContext_Request{
				Http: &authv3.AttributeContext_HttpRequest{
					Path:    "/api/v1/resource",
					Headers: map[string]string{"x-api-key": "abc123"},
				},
			},
		},
	}

	resp, err := s.Check(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(codes.PermissionDenied), resp.GetStatus().GetCode())

	denied := resp.GetDeniedResponse()
	require.NotNil(t, denied)
	assert.Equal(t, int32(429), int32(denied.GetStatus().GetCode()))
}

func TestCheck_NotBlockedEntity(t *testing.T) {
	engine := &mockEngine{}
	bl := &mockBlocklist{blocked: make(map[string]bool)}
	cfg := testConfig()
	cfg.Engine = engine
	cfg.Blocklist = bl

	s := NewServer(cfg)

	req := &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Source: &authv3.AttributeContext_Peer{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: "10.0.0.2",
						},
					},
				},
			},
			Request: &authv3.AttributeContext_Request{
				Http: &authv3.AttributeContext_HttpRequest{
					Path: "/api/v1/resource",
				},
			},
		},
	}

	resp, err := s.Check(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())
}

func TestServerStartStop_WithGRPCClient(t *testing.T) {
	s := NewServer(testConfig())
	require.NoError(t, s.Start())

	addr := s.Addr().String()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	client := authv3.NewAuthorizationClient(conn)
	resp, err := client.Check(context.Background(), &authv3.CheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, int32(codes.OK), resp.GetStatus().GetCode())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Stop(ctx)
}
