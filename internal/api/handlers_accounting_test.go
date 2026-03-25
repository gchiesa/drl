package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAccountingStats implements AccountingStatsProvider for testing.
type mockAccountingStats struct {
	pending   int64
	tracked   int64
	estimated int64
}

func (m *mockAccountingStats) PendingUpdates() int64    { return m.pending }
func (m *mockAccountingStats) TrackedEntities() int64   { return m.tracked }
func (m *mockAccountingStats) EstimatedEntities() int64 { return m.estimated }

func TestAccountingStats_Unauthorized(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := NewServer(ServerConfig{
		Address:     ":8082",
		APIKey:      "thisIsAVerySecureAPIKey123",
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     &mockCluster{ready: true, members: []string{"node-1"}},
		Logger:      logger,
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/accounting/stats", nil)
	resp, err := server.app.Test(req)
	require.NoError(t, err)

	assert.Equal(t, 401, resp.StatusCode)
}

func TestAccountingStats_WithProvider(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKey := "thisIsAVerySecureAPIKey123"

	stats := &mockAccountingStats{pending: 42, tracked: 1000, estimated: 200}

	server, err := NewServer(ServerConfig{
		Address:         ":8082",
		APIKey:          apiKey,
		ClusterName:     "test-cluster",
		NodeID:          "node-1",
		Cluster:         &mockCluster{ready: true, members: []string{"node-1"}},
		Logger:          logger,
		AccountingStats: stats,
	})
	require.NoError(t, err)

	// Step 1: Get challenge
	req1 := httptest.NewRequest("GET", "/accounting/stats", nil)
	resp1, err := server.app.Test(req1)
	require.NoError(t, err)
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	// Step 2: Authenticated request
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/accounting/stats", "GET")
	req2 := httptest.NewRequest("GET", "/accounting/stats", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := server.app.Test(req2)
	require.NoError(t, err)

	assert.Equal(t, 200, resp2.StatusCode)

	body, _ := io.ReadAll(resp2.Body)
	var result accountingStatsResponse
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.Equal(t, "node-1", result.LocalNodeID)
	assert.Equal(t, int64(1000), result.MonitoredEntitiesCount)
	assert.Equal(t, int64(42), result.BatchedUpdatesPending)
	assert.Equal(t, int64(200), result.EstimatedEntitiesCount)
}

func TestAccountingStats_NilProvider(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiKey := "thisIsAVerySecureAPIKey123"

	server, err := NewServer(ServerConfig{
		Address:     ":8082",
		APIKey:      apiKey,
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     &mockCluster{ready: true, members: []string{"node-1"}},
		Logger:      logger,
		// No AccountingStats provider
	})
	require.NoError(t, err)

	// Step 1: Get challenge
	req1 := httptest.NewRequest("GET", "/accounting/stats", nil)
	resp1, err := server.app.Test(req1)
	require.NoError(t, err)
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	// Step 2: Authenticated request
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/accounting/stats", "GET")
	req2 := httptest.NewRequest("GET", "/accounting/stats", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := server.app.Test(req2)
	require.NoError(t, err)

	assert.Equal(t, 200, resp2.StatusCode)

	body, _ := io.ReadAll(resp2.Body)
	var result accountingStatsResponse
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	assert.Equal(t, "node-1", result.LocalNodeID)
	assert.Equal(t, int64(0), result.MonitoredEntitiesCount)
	assert.Equal(t, int64(0), result.BatchedUpdatesPending)
	assert.Equal(t, int64(0), result.EstimatedEntitiesCount)
}
