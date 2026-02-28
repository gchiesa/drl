package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/model"
)

// --- test doubles ---

// mockBlocklist implements BlocklistOperator for tests.
type mockBlocklist struct {
	blocked map[string]mockBlockEntry
	deleted []string
}

type mockBlockEntry struct {
	ttl    time.Duration
	entity *model.Entity
}

func newMockBlocklist() *mockBlocklist {
	return &mockBlocklist{blocked: make(map[string]mockBlockEntry)}
}

func (m *mockBlocklist) Block(key string, ttl time.Duration) {
	m.blocked[key] = mockBlockEntry{ttl: ttl}
}

func (m *mockBlocklist) BlockWithMeta(key string, ttl time.Duration, entity *model.Entity) {
	m.blocked[key] = mockBlockEntry{ttl: ttl, entity: entity}
}

func (m *mockBlocklist) Unblock(key string) {
	delete(m.blocked, key)
	m.deleted = append(m.deleted, key)
}

func (m *mockBlocklist) IsBlocked(key string) bool {
	_, ok := m.blocked[key]
	return ok
}

func (m *mockBlocklist) ListEntries() []model.BlockedEntityInfo {
	var result []model.BlockedEntityInfo
	for k, v := range m.blocked {
		result = append(result, model.BlockedEntityInfo{
			Key:       k,
			ExpiresAt: time.Now().Add(v.ttl),
			Entity:    v.entity,
		})
	}
	return result
}

// mockBroadcaster implements Broadcaster for tests.
type mockBroadcaster struct {
	blockedKeys   []string
	unblockedKeys []string
}

func (m *mockBroadcaster) QueueBlockEvent(key string, _ time.Duration) error {
	m.blockedKeys = append(m.blockedKeys, key)
	return nil
}

func (m *mockBroadcaster) QueueUnblockEvent(key string) error {
	m.unblockedKeys = append(m.unblockedKeys, key)
	return nil
}

// --- server factory ---

func newTestServerWithBlocklist(t *testing.T, bl BlocklistOperator, bc Broadcaster) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Address:         ":0",
		APIKey:          "thisIsAVerySecureAPIKey123",
		ClusterName:     "test-cluster",
		NodeID:          "node-1",
		Cluster:         &mockCluster{ready: true, members: []string{"node-1"}},
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		Blocklist:       bl,
		Broadcaster:     bc,
		DefaultBlockTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	return server
}

// doAuthenticatedRequest performs the two-step digest-auth exchange against the
// Fiber test adapter and returns (statusCode, responseBody).
func doAuthenticatedRequest(t *testing.T, server *Server, method, uri string) (int, []byte) {
	t.Helper()
	apiKey := "thisIsAVerySecureAPIKey123"

	// Step 1: get challenge
	req1 := httptest.NewRequest(method, uri, nil)
	resp1, err := server.app.Test(req1)
	require.NoError(t, err)
	require.Equal(t, 401, resp1.StatusCode)

	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))
	require.NotEmpty(t, nonce, "nonce must be present in WWW-Authenticate")

	// Step 2: authenticated request
	authHeader := buildDigestAuthForTest(digestUsername, apiKey, nonce, uri, method)
	req2 := httptest.NewRequest(method, uri, nil)
	req2.Header.Set("Authorization", authHeader)

	resp2, err := server.app.Test(req2)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp2.Body)
	return resp2.StatusCode, body
}

// --- parseEntityFromWildcard unit tests ---

func TestParseEntityFromWildcard_PathOnly(t *testing.T) {
	path, headers, err := parseEntityFromWildcard("api/v1/payments")
	require.NoError(t, err)
	assert.Equal(t, "api/v1/payments", path)
	assert.Empty(t, headers)
}

func TestParseEntityFromWildcard_WithSingleHeader(t *testing.T) {
	path, headers, err := parseEntityFromWildcard("api/v1/payments/_headers/User-Agent:ScraperBot")
	require.NoError(t, err)
	assert.Equal(t, "api/v1/payments", path)
	assert.Equal(t, map[string]string{"User-Agent": "ScraperBot"}, headers)
}

func TestParseEntityFromWildcard_WithMultipleHeaders(t *testing.T) {
	path, headers, err := parseEntityFromWildcard("api/v1/_headers/User-Agent:Bot,X-Custom:val")
	require.NoError(t, err)
	assert.Equal(t, "api/v1", path)
	assert.Equal(t, map[string]string{"User-Agent": "Bot", "X-Custom": "val"}, headers)
}

func TestParseEntityFromWildcard_EmptyWildcard(t *testing.T) {
	_, _, err := parseEntityFromWildcard("")
	assert.Error(t, err, "empty wildcard must return an error")
}

func TestParseEntityFromWildcard_HeaderMarkerWithEmptyPath(t *testing.T) {
	_, _, err := parseEntityFromWildcard("/_headers/User-Agent:Bot")
	assert.Error(t, err, "empty path segment must return an error")
}

func TestParseHeadersStr_MalformedPair(t *testing.T) {
	_, err := parseHeadersStr("NoColon")
	assert.Error(t, err)
}

func TestParseHeadersStr_EmptyKey(t *testing.T) {
	_, err := parseHeadersStr(":value")
	assert.Error(t, err)
}

func TestParseHeadersStr_EmptyString(t *testing.T) {
	m, err := parseHeadersStr("")
	require.NoError(t, err)
	assert.Empty(t, m)
}

// --- HTTP endpoint tests ---

func TestBlockEntityAdd_Unauthenticated(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	req := httptest.NewRequest("POST",
		"/blocked-entity/192.168.1.1/_path/api/v1/payments/_headers/User-Agent:ScraperBot", nil)
	resp, err := server.app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestBlockEntityDelete_Unauthenticated(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	req := httptest.NewRequest("DELETE",
		"/blocked-entity/192.168.1.1/_path/api/v1/payments", nil)
	resp, err := server.app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestBlockEntityAdd_Success(t *testing.T) {
	bl := newMockBlocklist()
	bc := &mockBroadcaster{}
	server := newTestServerWithBlocklist(t, bl, bc)

	uri := "/blocked-entity/192.168.1.1/_path/api/v1/payments/_headers/User-Agent:ScraperBot"
	code, raw := doAuthenticatedRequest(t, server, "POST", uri)

	require.Equal(t, 200, code, "body: %s", string(raw))
	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))
	assert.NotEmpty(t, er.ID)
	assert.Equal(t, "192.168.1.1", er.IP)
	assert.Equal(t, "api/v1/payments", er.URIPath)
	assert.Equal(t, map[string]string{"User-Agent": "ScraperBot"}, er.Headers)
	assert.Equal(t, "Entity added to blocklist", er.Message)
	assert.Empty(t, er.Errors)

	// The local blocklist must be updated synchronously
	expectedKey := model.Entity{
		IP:      "192.168.1.1",
		Path:    "api/v1/payments",
		Headers: map[string]string{"User-Agent": "ScraperBot"},
	}.Key()
	assert.True(t, bl.IsBlocked(expectedKey), "entity must appear in local blocklist")
}

func TestBlockEntityDelete_Success(t *testing.T) {
	bl := newMockBlocklist()
	bc := &mockBroadcaster{}
	server := newTestServerWithBlocklist(t, bl, bc)

	// Pre-populate the blocklist
	key := model.Entity{IP: "10.0.0.1", Path: "api/v1/payments"}.Key()
	bl.Block(key, 24*time.Hour)
	require.True(t, bl.IsBlocked(key))

	uri := "/blocked-entity/10.0.0.1/_path/api/v1/payments"
	code, raw := doAuthenticatedRequest(t, server, "DELETE", uri)

	require.Equal(t, 200, code, "body: %s", string(raw))
	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))
	assert.Equal(t, "10.0.0.1", er.IP)
	assert.Equal(t, "Entity removed from blocklist", er.Message)
	assert.Empty(t, er.Errors)
	assert.False(t, bl.IsBlocked(key), "entity must be removed from local blocklist")
}

func TestBlockEntityAdd_PathOnly(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	uri := "/blocked-entity/10.0.0.1/_path/api/v1/no-headers"
	code, raw := doAuthenticatedRequest(t, server, "POST", uri)

	require.Equal(t, 200, code, "body: %s", string(raw))
	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))
	assert.Equal(t, "Entity added to blocklist", er.Message)

	expectedKey := model.Entity{IP: "10.0.0.1", Path: "api/v1/no-headers"}.Key()
	assert.True(t, bl.IsBlocked(expectedKey))
}

func TestBlockEntityAdd_MalformedHeader_Returns400(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	// "NoColon" has no colon separator — must yield 400
	uri := "/blocked-entity/10.0.0.1/_path/api/v1/_headers/NoColon"
	code, raw := doAuthenticatedRequest(t, server, "POST", uri)

	assert.Equal(t, 400, code, "malformed header must return 400; body: %s", string(raw))

	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))
	assert.NotEmpty(t, er.Errors)
}

func TestBlockEntityAdd_NilBlocklistAndBroadcaster(t *testing.T) {
	// A server without blocklist/broadcaster must still respond 200 without panicking
	server := newTestServerWithBlocklist(t, nil, nil)

	uri := "/blocked-entity/10.0.0.1/_path/api/v1"
	code, raw := doAuthenticatedRequest(t, server, "POST", uri)

	require.Equal(t, 200, code, "body: %s", string(raw))
	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))
	assert.Equal(t, "Entity added to blocklist", er.Message)
}

func TestBlockEntityAdd_ImmediateLocalEnforcement(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	uri := "/blocked-entity/192.168.1.1/_path/api/v1/payments/_headers/User-Agent:ScraperBot"
	code, _ := doAuthenticatedRequest(t, server, "POST", uri)
	require.Equal(t, 200, code)

	key := model.Entity{
		IP:      "192.168.1.1",
		Path:    "api/v1/payments",
		Headers: map[string]string{"User-Agent": "ScraperBot"},
	}.Key()
	assert.True(t, bl.IsBlocked(key),
		"entity must be blocked locally before the request returns")
}

func TestBlockEntityDelete_Removes(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	// Block first
	addCode, _ := doAuthenticatedRequest(t, server, "POST",
		"/blocked-entity/10.0.0.1/_path/api/v1/payments")
	require.Equal(t, 200, addCode)

	key := model.Entity{IP: "10.0.0.1", Path: "api/v1/payments"}.Key()
	require.True(t, bl.IsBlocked(key))

	// Then delete
	delCode, _ := doAuthenticatedRequest(t, server, "DELETE",
		"/blocked-entity/10.0.0.1/_path/api/v1/payments")
	require.Equal(t, 200, delCode)
	assert.False(t, bl.IsBlocked(key))
}

func TestBlockEntityAdd_DifferentIPsSamePathProduceDifferentKeys(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	code1, _ := doAuthenticatedRequest(t, server, "POST",
		"/blocked-entity/10.0.0.1/_path/api/v1")
	require.Equal(t, 200, code1)

	code2, _ := doAuthenticatedRequest(t, server, "POST",
		"/blocked-entity/10.0.0.2/_path/api/v1")
	require.Equal(t, 200, code2)

	key1 := model.Entity{IP: "10.0.0.1", Path: "api/v1"}.Key()
	key2 := model.Entity{IP: "10.0.0.2", Path: "api/v1"}.Key()
	assert.True(t, bl.IsBlocked(key1))
	assert.True(t, bl.IsBlocked(key2))
	assert.NotEqual(t, key1, key2, "different IPs must not share the same blocklist key")
}

func TestBlockEntityAdd_ResponseIncludesEntityFields(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	// Use a single header to avoid commas in the URI, which would conflict
	// with the comma-separated Digest auth header parser.
	uri := "/blocked-entity/172.16.0.5/_path/api/v2/users/_headers/X-Bot:true"
	code, raw := doAuthenticatedRequest(t, server, "POST", uri)

	require.Equal(t, 200, code, "body: %s", string(raw))
	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))

	assert.Equal(t, "172.16.0.5", er.IP)
	assert.Equal(t, "api/v2/users", er.URIPath)
	assert.Equal(t, map[string]string{"X-Bot": "true"}, er.Headers)
}

func TestBlockEntityAdd_StoresEntityMetadata(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	uri := "/blocked-entity/10.0.0.1/_path/api/v1/_headers/X-Bot:true"
	code, _ := doAuthenticatedRequest(t, server, "POST", uri)
	require.Equal(t, 200, code)

	key := model.Entity{
		IP:      "10.0.0.1",
		Path:    "api/v1",
		Headers: map[string]string{"X-Bot": "true"},
	}.Key()

	entry, ok := bl.blocked[key]
	require.True(t, ok)
	require.NotNil(t, entry.entity, "BlockWithMeta must store entity metadata")
	assert.Equal(t, "10.0.0.1", entry.entity.IP)
	assert.Equal(t, "api/v1", entry.entity.Path)
	assert.Equal(t, map[string]string{"X-Bot": "true"}, entry.entity.Headers)
}

func TestBlockEntityAdd_WithTTLQueryParam(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	uri := "/blocked-entity/10.0.0.1/_path/api/v1?ttl=300"
	code, _ := doAuthenticatedRequest(t, server, "POST", uri)
	require.Equal(t, 200, code)

	key := model.Entity{IP: "10.0.0.1", Path: "api/v1"}.Key()
	entry, ok := bl.blocked[key]
	require.True(t, ok)
	assert.Equal(t, 300*time.Second, entry.ttl, "TTL from query param must be used")
}

func TestBlockEntityAdd_DefaultTTLUsedWhenNoQueryParam(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	uri := "/blocked-entity/10.0.0.1/_path/api/v1"
	code, _ := doAuthenticatedRequest(t, server, "POST", uri)
	require.Equal(t, 200, code)

	key := model.Entity{IP: "10.0.0.1", Path: "api/v1"}.Key()
	entry, ok := bl.blocked[key]
	require.True(t, ok)
	assert.Equal(t, 1*time.Hour, entry.ttl, "default TTL from server config must be used")
}

func TestBlockEntityAdd_InvalidTTL_Returns400(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	uri := "/blocked-entity/10.0.0.1/_path/api/v1?ttl=abc"
	code, raw := doAuthenticatedRequest(t, server, "POST", uri)

	assert.Equal(t, 400, code, "body: %s", string(raw))
	var er entityResponse
	require.NoError(t, json.Unmarshal(raw, &er))
	assert.NotEmpty(t, er.Errors)
}

func TestBlockEntityList_Empty(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	code, raw := doAuthenticatedRequest(t, server, "GET", "/blocked-entity")
	require.Equal(t, 200, code, "body: %s", string(raw))

	var entries []blockedEntityEntry
	require.NoError(t, json.Unmarshal(raw, &entries))
	assert.Empty(t, entries)
}

func TestBlockEntityList_NilBlocklist(t *testing.T) {
	server := newTestServerWithBlocklist(t, nil, nil)

	code, raw := doAuthenticatedRequest(t, server, "GET", "/blocked-entity")
	require.Equal(t, 200, code, "body: %s", string(raw))

	var entries []blockedEntityEntry
	require.NoError(t, json.Unmarshal(raw, &entries))
	assert.Empty(t, entries)
}

func TestBlockEntityList_ReturnsBlockedEntities(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	// Add some entities via POST
	code1, _ := doAuthenticatedRequest(t, server, "POST",
		"/blocked-entity/10.0.0.1/_path/api/v1/_headers/X-Bot:true")
	require.Equal(t, 200, code1)

	code2, _ := doAuthenticatedRequest(t, server, "POST",
		"/blocked-entity/10.0.0.2/_path/api/v2")
	require.Equal(t, 200, code2)

	// List
	code, raw := doAuthenticatedRequest(t, server, "GET", "/blocked-entity")
	require.Equal(t, 200, code, "body: %s", string(raw))

	var entries []blockedEntityEntry
	require.NoError(t, json.Unmarshal(raw, &entries))
	assert.Len(t, entries, 2)

	// Build a map by IP for easier assertion (map iteration order is non-deterministic)
	byIP := make(map[string]blockedEntityEntry)
	for _, e := range entries {
		byIP[e.IP] = e
	}

	e1, ok := byIP["10.0.0.1"]
	require.True(t, ok, "entry for 10.0.0.1 must exist")
	assert.Equal(t, "api/v1", e1.URIPath)
	assert.NotEmpty(t, e1.ExpiresAt)
	assert.NotEmpty(t, e1.ID)

	e2, ok := byIP["10.0.0.2"]
	require.True(t, ok, "entry for 10.0.0.2 must exist")
	assert.Equal(t, "api/v2", e2.URIPath)
	assert.NotEmpty(t, e2.ID)
}

func TestBlockEntityList_ExpiresAtIsRFC3339(t *testing.T) {
	bl := newMockBlocklist()
	server := newTestServerWithBlocklist(t, bl, &mockBroadcaster{})

	code, _ := doAuthenticatedRequest(t, server, "POST",
		"/blocked-entity/10.0.0.1/_path/api/v1")
	require.Equal(t, 200, code)

	listCode, raw := doAuthenticatedRequest(t, server, "GET", "/blocked-entity")
	require.Equal(t, 200, listCode)

	var entries []blockedEntityEntry
	require.NoError(t, json.Unmarshal(raw, &entries))
	require.Len(t, entries, 1)

	_, err := time.Parse(time.RFC3339, entries[0].ExpiresAt)
	assert.NoError(t, err, "expires_at must be valid RFC3339; got %q", entries[0].ExpiresAt)
}

func TestBlockEntityList_Unauthenticated(t *testing.T) {
	server := newTestServerWithBlocklist(t, newMockBlocklist(), &mockBroadcaster{})

	req := httptest.NewRequest("GET", "/blocked-entity", nil)
	resp, err := server.app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}
