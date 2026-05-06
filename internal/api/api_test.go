package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// mockCluster implements ClusterInfo for testing
type mockCluster struct {
	ready   bool
	members []string
	addrs   []string
}

func (m *mockCluster) IsReady() bool {
	return m.ready
}

func (m *mockCluster) NumMembers() int {
	return len(m.members)
}

func (m *mockCluster) MemberNames() []string {
	return m.members
}

func (m *mockCluster) MemberAddrs() []string {
	return m.addrs
}

func (m *mockCluster) LocalAddr() string {
	return ""
}

func TestNewServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{
			name: "valid configuration",
			cfg: ServerConfig{
				Address:     ":8082",
				APIKey:      "thisIsAVerySecureAPIKey123",
				ClusterName: "test-cluster",
				NodeID:      "node-1",
				Cluster:     &mockCluster{ready: true, members: []string{"node-1"}},
				Logger:      logger,
			},
			wantErr: false,
		},
		{
			name: "short API key",
			cfg: ServerConfig{
				Address:     ":8082",
				APIKey:      "short",
				ClusterName: "test-cluster",
				NodeID:      "node-1",
				Cluster:     &mockCluster{},
				Logger:      logger,
			},
			wantErr: true,
		},
		{
			name: "empty API key",
			cfg: ServerConfig{
				Address:     ":8082",
				APIKey:      "",
				ClusterName: "test-cluster",
				NodeID:      "node-1",
				Cluster:     &mockCluster{},
				Logger:      logger,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewServer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewServer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && server == nil {
				t.Error("NewServer() returned nil server without error")
			}
		})
	}
}

func TestStatusEndpoint_Unauthorized(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := NewServer(ServerConfig{
		Address:     ":8082",
		APIKey:      "thisIsAVerySecureAPIKey123",
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     &mockCluster{ready: true, members: []string{"node-1", "node-2"}},
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/status", nil)
	resp, err := server.app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}

	// Verify it returns Digest challenge
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(wwwAuth, "Digest") {
		t.Errorf("Expected WWW-Authenticate to start with 'Digest', got %s", wwwAuth)
	}
}

func TestStatusEndpoint_Authenticated(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKey := "thisIsAVerySecureAPIKey123"

	cluster := &mockCluster{
		ready:   true,
		members: []string{"node-1", "node-2", "node-3"},
	}

	server, err := NewServer(ServerConfig{
		Address:     ":8082",
		APIKey:      apiKey,
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     cluster,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Step 1: Get challenge
	req1 := httptest.NewRequest("GET", "/v1/status", nil)
	resp1, err := server.app.Test(req1)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("Step 1: Expected 401, got %d", resp1.StatusCode)
	}

	wwwAuth := resp1.Header.Get("WWW-Authenticate")
	nonce := extractNonceFromHeader(wwwAuth)
	if nonce == "" {
		t.Fatalf("Failed to extract nonce from WWW-Authenticate: %s", wwwAuth)
	}

	// Step 2: Send authenticated request
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/v1/status", "GET")
	req2 := httptest.NewRequest("GET", "/v1/status", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := server.app.Test(req2)
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}

	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Step 2: Expected 200, got %d. Body: %s", resp2.StatusCode, string(body))
	}

	// Parse response body
	body, _ := io.ReadAll(resp2.Body)
	var status map[string]any
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("Failed to parse status response: %v", err)
	}

	// Verify response fields
	if status["cluster_name"] != "test-cluster" {
		t.Errorf("Expected cluster_name 'test-cluster', got %v", status["cluster_name"])
	}
	if status["node_id"] != "node-1" {
		t.Errorf("Expected node_id 'node-1', got %v", status["node_id"])
	}

	peers, ok := status["active_peers"].([]any)
	if !ok {
		t.Fatalf("active_peers is not an array")
	}
	if len(peers) != 3 {
		t.Errorf("Expected 3 active_peers, got %d", len(peers))
	}

	if status["uptime"] == nil {
		t.Error("uptime should be present")
	}
	if status["uptime_seconds"] == nil {
		t.Error("uptime_seconds should be present")
	}
}

func TestServerStartStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := NewServer(ServerConfig{
		Address:     ":0", // Use any available port
		APIKey:      "thisIsAVerySecureAPIKey123",
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     &mockCluster{ready: true},
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Start should not return error
	if err := server.Start(); err != nil {
		t.Errorf("Start() error = %v", err)
	}

	// Give it time to start
	time.Sleep(100 * time.Millisecond)

	// Stop should work
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Stop(ctx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}

func TestStatusEndpoint_EmptyCluster(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKey := "thisIsAVerySecureAPIKey123"

	// Create server with an empty cluster (no members)
	server, err := NewServer(ServerConfig{
		Address:     ":8082",
		APIKey:      apiKey,
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     &mockCluster{},
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Step 1: Get challenge
	req1 := httptest.NewRequest("GET", "/v1/status", nil)
	resp1, _ := server.app.Test(req1)
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	// Step 2: Authenticated request
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/v1/status", "GET")
	req2 := httptest.NewRequest("GET", "/v1/status", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, _ := server.app.Test(req2)

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK with empty cluster, got %d", resp2.StatusCode)
	}

	body, _ := io.ReadAll(resp2.Body)
	var status map[string]any
	_ = json.Unmarshal(body, &status)

	// active_peers should be nil or empty when cluster has no members
	if status["active_peers"] != nil {
		t.Errorf("Expected active_peers to be nil when cluster has no members")
	}
}

// Helper functions for api_test.go

func extractNonceFromHeader(wwwAuth string) string {
	params := parseDigestAuth(wwwAuth)
	return params["nonce"]
}

func buildDigestAuthForTest(username, password, nonce, uri, method string) string {
	realm := digestRealm
	cnonce := "testcnonce123456"
	nc := "00000001"
	qop := "auth"

	// A1 = username:realm:password
	a1 := fmt.Sprintf("%s:%s:%s", username, realm, password)
	a1Hash := testSha256(a1)

	// A2 = method:uri
	a2 := fmt.Sprintf("%s:%s", method, uri)
	a2Hash := testSha256(a2)

	// response = H(A1:nonce:nc:cnonce:qop:A2)
	response := testSha256(fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		a1Hash, nonce, nc, cnonce, qop, a2Hash))

	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=SHA-256, qop=%s, nc=%s, cnonce="%s", response="%s"`,
		username, realm, nonce, uri, qop, nc, cnonce, response)
}

func testSha256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
