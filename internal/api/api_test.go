package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
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

	req := httptest.NewRequest("GET", "/status", nil)
	resp, err := server.app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", fiber.StatusUnauthorized, resp.StatusCode)
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

	// Perform full SCRAM authentication
	clientNonce := "testClientNonce12345"
	clientFirst := fmt.Sprintf("n,,n=%s,r=%s", scramUsername, clientNonce)
	clientFirstBare := fmt.Sprintf("n=%s,r=%s", scramUsername, clientNonce)

	// Step 1: Client first
	req1 := httptest.NewRequest("GET", "/status", nil)
	req1.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFirst)
	resp1, err := server.app.Test(req1)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("Step 1: Expected 401, got %d", resp1.StatusCode)
	}

	serverFirstHeader := resp1.Header.Get("WWW-Authenticate")
	serverFirst := strings.TrimPrefix(serverFirstHeader, "SCRAM-SHA-256 ")

	var serverNonce, saltB64 string
	var iterations int
	for _, part := range strings.Split(serverFirst, ",") {
		if strings.HasPrefix(part, "r=") {
			serverNonce = strings.TrimPrefix(part, "r=")
		}
		if strings.HasPrefix(part, "s=") {
			saltB64 = strings.TrimPrefix(part, "s=")
		}
		if strings.HasPrefix(part, "i=") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(part, "i="), "%d", &iterations)
		}
	}

	salt, _ := base64.StdEncoding.DecodeString(saltB64)

	// Calculate proof
	channelBinding := base64.StdEncoding.EncodeToString([]byte("n,,"))
	clientFinalWithoutProof := fmt.Sprintf("c=%s,r=%s", channelBinding, serverNonce)
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalWithoutProof

	saltedPassword := pbkdf2Sha256([]byte(apiKey), salt, iterations, 32)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	clientSignature := hmacSHA256(storedKey[:], []byte(authMessage))

	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ clientSignature[i]
	}
	proofB64 := base64.StdEncoding.EncodeToString(proof)

	clientFinal := fmt.Sprintf("c=%s,r=%s,p=%s", channelBinding, serverNonce, proofB64)

	// Step 2: Client final
	req2 := httptest.NewRequest("GET", "/status", nil)
	req2.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFinal)
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

func TestStatusEndpoint_NilCluster(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKey := "thisIsAVerySecureAPIKey123"

	// Create server with nil cluster
	server, err := NewServer(ServerConfig{
		Address:     ":8082",
		APIKey:      apiKey,
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     nil,
		Logger:      logger,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Perform authentication and check that nil cluster is handled
	clientNonce := "testNonce"
	clientFirst := fmt.Sprintf("n,,n=%s,r=%s", scramUsername, clientNonce)
	clientFirstBare := fmt.Sprintf("n=%s,r=%s", scramUsername, clientNonce)

	// Step 1
	req1 := httptest.NewRequest("GET", "/status", nil)
	req1.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFirst)
	resp1, _ := server.app.Test(req1)

	serverFirst := strings.TrimPrefix(resp1.Header.Get("WWW-Authenticate"), "SCRAM-SHA-256 ")

	var serverNonce, saltB64 string
	var iterations int
	for _, part := range strings.Split(serverFirst, ",") {
		if strings.HasPrefix(part, "r=") {
			serverNonce = strings.TrimPrefix(part, "r=")
		}
		if strings.HasPrefix(part, "s=") {
			saltB64 = strings.TrimPrefix(part, "s=")
		}
		if strings.HasPrefix(part, "i=") {
			_, _ = fmt.Sscanf(strings.TrimPrefix(part, "i="), "%d", &iterations)
		}
	}

	salt, _ := base64.StdEncoding.DecodeString(saltB64)
	channelBinding := base64.StdEncoding.EncodeToString([]byte("n,,"))
	clientFinalWithoutProof := fmt.Sprintf("c=%s,r=%s", channelBinding, serverNonce)
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalWithoutProof

	saltedPassword := pbkdf2Sha256([]byte(apiKey), salt, iterations, 32)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	clientSignature := hmacSHA256(storedKey[:], []byte(authMessage))

	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ clientSignature[i]
	}
	proofB64 := base64.StdEncoding.EncodeToString(proof)
	clientFinal := fmt.Sprintf("c=%s,r=%s,p=%s", channelBinding, serverNonce, proofB64)

	// Step 2
	req2 := httptest.NewRequest("GET", "/status", nil)
	req2.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFinal)
	resp2, _ := server.app.Test(req2)

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK even with nil cluster, got %d", resp2.StatusCode)
	}

	body, _ := io.ReadAll(resp2.Body)
	var status map[string]any
	_ = json.Unmarshal(body, &status)

	// active_peers should be nil or empty when cluster is nil
	if status["active_peers"] != nil {
		t.Errorf("Expected active_peers to be nil when cluster is nil")
	}
}
