package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBulkLoader implements AccountingBulkLoader for tests. The function
// field lets each test rewrite the per-call outcome.
type mockBulkLoader struct {
	mu    sync.Mutex
	calls []bulkLoadCall
	fn    func(sourceIP, path string, headers map[string]string, distributionEnabled bool) string
}

type bulkLoadCall struct {
	sourceIP            string
	path                string
	headers             map[string]string
	distributionEnabled bool
}

func (m *mockBulkLoader) BulkLoad(sourceIP, path string, headers map[string]string, distributionEnabled bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, bulkLoadCall{
		sourceIP: sourceIP, path: path, headers: headers,
		distributionEnabled: distributionEnabled,
	})
	if m.fn != nil {
		return m.fn(sourceIP, path, headers, distributionEnabled)
	}
	return bulkLoadResultAcceptedLocal
}

func (m *mockBulkLoader) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// mockBulkLoadMetrics records IncAccountingBulkLoad calls for assertions.
type mockBulkLoadMetrics struct {
	mu      sync.Mutex
	results map[string]int
}

func newMockBulkLoadMetrics() *mockBulkLoadMetrics {
	return &mockBulkLoadMetrics{results: make(map[string]int)}
}

func (m *mockBulkLoadMetrics) IncAccountingBulkLoad(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[result]++
}

func (m *mockBulkLoadMetrics) get(result string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.results[result]
}

func newTestServerWithBulkLoader(t *testing.T, loader AccountingBulkLoader, mtr BulkLoadMetricsRecorder) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Address:         ":0",
		APIKey:          "thisIsAVerySecureAPIKey123",
		ClusterName:     "test-cluster",
		NodeID:          "node-1",
		Cluster:         &mockCluster{ready: true, members: []string{"node-1"}},
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		DefaultBlockTTL: 1 * time.Hour,
		BulkLoader:      loader,
		Metrics:         mtr,
	})
	require.NoError(t, err)
	return server
}

// doAuthenticatedPOST runs the two-step digest exchange for a POST with body.
func doAuthenticatedPOST(t *testing.T, server *Server, uri, body string) (int, []byte) {
	t.Helper()
	apiKey := "thisIsAVerySecureAPIKey123"

	// Step 1: challenge
	req1 := httptest.NewRequest("POST", uri, strings.NewReader(body))
	resp1, err := server.app.Test(req1)
	require.NoError(t, err)
	require.Equal(t, 401, resp1.StatusCode)

	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))
	require.NotEmpty(t, nonce)

	// Step 2: authenticated request
	authHeader := buildDigestAuthForTest(digestUsername, apiKey, nonce, uri, "POST")
	req2 := httptest.NewRequest("POST", uri, strings.NewReader(body))
	req2.Header.Set("Authorization", authHeader)
	req2.Header.Set("Content-Type", "application/x-ndjson")

	resp2, err := server.app.Test(req2)
	require.NoError(t, err)
	respBody, _ := io.ReadAll(resp2.Body)
	return resp2.StatusCode, respBody
}

func TestAccountingLoad_Unauthenticated(t *testing.T) {
	loader := &mockBulkLoader{}
	server := newTestServerWithBulkLoader(t, loader, nil)

	req := httptest.NewRequest("POST", "/accounting/load",
		strings.NewReader(`{"sourceIP":"1.1.1.1","path":"/api"}`))
	resp, err := server.app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestAccountingLoad_NotConfigured(t *testing.T) {
	// No bulkLoader → endpoint must not even be registered.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := NewServer(ServerConfig{
		Address:     ":0",
		APIKey:      "thisIsAVerySecureAPIKey123",
		ClusterName: "test-cluster",
		NodeID:      "node-1",
		Cluster:     &mockCluster{ready: true, members: []string{"node-1"}},
		Logger:      logger,
	})
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/accounting/load",
		strings.NewReader(`{"sourceIP":"1.1.1.1","path":"/api"}`))
	resp, err := server.app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 404, resp.StatusCode, "endpoint must not be registered without a bulk loader")
}

func TestAccountingLoad_HappyPath(t *testing.T) {
	mtr := newMockBulkLoadMetrics()
	loader := &mockBulkLoader{
		fn: func(_, path string, _ map[string]string, _ bool) string {
			if strings.HasPrefix(path, "/api") {
				return bulkLoadResultAcceptedLocal
			}
			return bulkLoadResultNoMatch
		},
	}
	server := newTestServerWithBulkLoader(t, loader, mtr)

	body := strings.Join([]string{
		`{"sourceIP":"10.0.0.1","path":"/api/v1"}`,
		`{"sourceIP":"10.0.0.2","path":"/api/v1","headers":{"X-API-Key":"abc"}}`,
		`{"sourceIP":"10.0.0.3","path":"/other"}`,
	}, "\n")

	status, respBody := doAuthenticatedPOST(t, server, "/accounting/load", body)
	require.Equal(t, 200, status)

	var resp bulkLoadResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))

	assert.Equal(t, 3, resp.Total)
	assert.Equal(t, 2, resp.AcceptedLocal)
	assert.Equal(t, 1, resp.NoMatch)
	assert.Equal(t, 0, resp.AcceptedRemote)
	assert.Equal(t, 0, resp.Dropped)
	assert.Equal(t, 0, resp.Invalid)
	assert.Empty(t, resp.Errors)

	assert.Equal(t, 3, loader.callCount())

	// Default: distributionEnabled=false
	loader.mu.Lock()
	for _, c := range loader.calls {
		assert.False(t, c.distributionEnabled)
	}
	loader.mu.Unlock()
}

func TestAccountingLoad_DistributionEnabled(t *testing.T) {
	loader := &mockBulkLoader{
		fn: func(_, _ string, _ map[string]string, _ bool) string {
			return bulkLoadResultAcceptedRemote
		},
	}
	server := newTestServerWithBulkLoader(t, loader, nil)

	body := `{"sourceIP":"10.0.0.1","path":"/api"}`
	status, respBody := doAuthenticatedPOST(t, server,
		"/accounting/load?distributionEnabled=true", body)
	require.Equal(t, 200, status)

	var resp bulkLoadResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))
	assert.Equal(t, 1, resp.AcceptedRemote)

	loader.mu.Lock()
	require.Len(t, loader.calls, 1)
	assert.True(t, loader.calls[0].distributionEnabled)
	loader.mu.Unlock()
}

func TestAccountingLoad_InvalidDistributionEnabled(t *testing.T) {
	loader := &mockBulkLoader{}
	server := newTestServerWithBulkLoader(t, loader, nil)

	status, respBody := doAuthenticatedPOST(t, server,
		"/accounting/load?distributionEnabled=maybe",
		`{"sourceIP":"10.0.0.1","path":"/api"}`)
	require.Equal(t, 400, status)

	var resp bulkLoadResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))
	assert.NotEmpty(t, resp.Errors)
	assert.Equal(t, 0, loader.callCount(), "loader must not be invoked on bad query")
}

func TestAccountingLoad_MalformedLinesContinue(t *testing.T) {
	mtr := newMockBulkLoadMetrics()
	loader := &mockBulkLoader{
		fn: func(_, _ string, _ map[string]string, _ bool) string {
			return bulkLoadResultAcceptedLocal
		},
	}
	server := newTestServerWithBulkLoader(t, loader, mtr)

	body := strings.Join([]string{
		`{"sourceIP":"10.0.0.1","path":"/api"}`,
		`{not valid json`,
		``,                              // blank line is skipped, not counted
		`{"sourceIP":"","path":"/api"}`, // missing required field
		`{"sourceIP":"10.0.0.2","path":"/api"}`,
	}, "\n")

	status, respBody := doAuthenticatedPOST(t, server, "/accounting/load", body)
	require.Equal(t, 200, status)

	var resp bulkLoadResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))

	assert.Equal(t, 4, resp.Total, "blank line skipped, two valid + two invalid counted")
	assert.Equal(t, 2, resp.AcceptedLocal)
	assert.Equal(t, 2, resp.Invalid)
	assert.Len(t, resp.Errors, 2)
	assert.Equal(t, 2, mtr.get(bulkLoadResultInvalid))
}

func TestAccountingLoad_Dropped(t *testing.T) {
	loader := &mockBulkLoader{
		fn: func(_, _ string, _ map[string]string, _ bool) string {
			return bulkLoadResultDropped
		},
	}
	server := newTestServerWithBulkLoader(t, loader, nil)

	body := `{"sourceIP":"10.0.0.1","path":"/api"}`
	status, respBody := doAuthenticatedPOST(t, server, "/accounting/load", body)
	require.Equal(t, 200, status)

	var resp bulkLoadResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))
	assert.Equal(t, 1, resp.Dropped)
	assert.Equal(t, 1, resp.Total)
}

func TestAccountingLoad_EmptyBody(t *testing.T) {
	loader := &mockBulkLoader{}
	server := newTestServerWithBulkLoader(t, loader, nil)

	status, respBody := doAuthenticatedPOST(t, server, "/accounting/load", "")
	require.Equal(t, 200, status)

	var resp bulkLoadResponse
	require.NoError(t, json.Unmarshal(respBody, &resp))
	assert.Equal(t, 0, resp.Total)
	assert.Equal(t, 0, loader.callCount())
}
