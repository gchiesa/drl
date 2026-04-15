package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// ─── uiAuthManager unit tests ────────────────────────────────────────────────

func TestNewUIAuthManager(t *testing.T) {
	m, err := newUIAuthManager("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("newUIAuthManager returned error: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.serverPrivKey == nil {
		t.Error("expected server private key to be set")
	}
	if len(m.bootstrapSigningKey) == 0 {
		t.Error("expected bootstrap signing key to be set")
	}
}

func TestUIAuthManager_ServerPublicKeyBase64(t *testing.T) {
	m, _ := newUIAuthManager("thisIsAVerySecureAPIKey123")
	pubKey := m.ServerPublicKeyBase64()
	if pubKey == "" {
		t.Fatal("expected non-empty public key")
	}
	// P-256 uncompressed point = 65 bytes → base64 length = ceil(65/3)*4 = 88
	decoded, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil {
		t.Fatalf("public key is not valid base64: %v", err)
	}
	if len(decoded) != 65 {
		t.Errorf("expected 65-byte P-256 uncompressed point, got %d bytes", len(decoded))
	}
	if decoded[0] != 0x04 {
		t.Errorf("expected uncompressed point prefix 0x04, got 0x%02x", decoded[0])
	}
}

func TestUIAuthManager_BootstrapToken_RoundTrip(t *testing.T) {
	m, _ := newUIAuthManager("thisIsAVerySecureAPIKey123")

	token := m.GenerateBootstrapToken()
	if token == "" {
		t.Fatal("expected non-empty bootstrap token")
	}

	// Token must contain exactly one "." separator.
	if strings.Count(token, ".") != 1 {
		t.Errorf("expected exactly one '.' in token, got: %q", token)
	}

	if !m.ValidateBootstrapToken(token) {
		t.Error("freshly generated bootstrap token should be valid")
	}
}

func TestUIAuthManager_BootstrapToken_InvalidTokens(t *testing.T) {
	m, _ := newUIAuthManager("thisIsAVerySecureAPIKey123")

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no dot", "invalidtoken"},
		{"bad base64 payload", "!!!.abc"},
		{"tampered signature", m.GenerateBootstrapToken() + "x"},
		{"wrong key", func() string {
			other, _ := newUIAuthManager("differentKey12345678")
			return other.GenerateBootstrapToken()
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if m.ValidateBootstrapToken(tc.token) {
				t.Errorf("expected invalid token to fail validation: %q", tc.token)
			}
		})
	}
}

func TestUIAuthManager_Session_RoundTrip(t *testing.T) {
	m, _ := newUIAuthManager("thisIsAVerySecureAPIKey123")

	sharedKey := make([]byte, 32)
	token, err := m.CreateSession(sharedKey)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty session token")
	}

	if !m.ValidateSession(token) {
		t.Error("freshly created session token should be valid")
	}
}

func TestUIAuthManager_Session_InvalidTokens(t *testing.T) {
	m, _ := newUIAuthManager("thisIsAVerySecureAPIKey123")

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"garbage", "not.a.token"},
		{"no dot", "nodot"},
		{"bad base64", "!!!.sig"},
		{"tampered", func() string {
			tok, _ := m.CreateSession(make([]byte, 32))
			return tok + "X"
		}()},
		{"wrong key", func() string {
			other, _ := newUIAuthManager("anotherKeyXXXXXXXXXX")
			tok, _ := other.CreateSession(make([]byte, 32))
			return tok
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if m.ValidateSession(tc.token) {
				t.Errorf("expected invalid token to fail: %q", tc.token)
			}
		})
	}
}

func TestUIAuthManager_ActiveSessions(t *testing.T) {
	m, _ := newUIAuthManager("thisIsAVerySecureAPIKey123")
	if m.ActiveSessions() != 0 {
		t.Error("expected 0 active sessions initially")
	}
	_, _ = m.CreateSession(make([]byte, 32))
	if m.ActiveSessions() != 1 {
		t.Errorf("expected 1 active session, got %d", m.ActiveSessions())
	}
}

func TestUIAuthManager_DeriveSharedKey(t *testing.T) {
	// Two managers with different keys should derive different shared secrets
	// for the same client public key, but we mainly test that the function doesn't error
	// and returns exactly 32 bytes.
	m, _ := newUIAuthManager("testkey1234567890")

	// Generate a client key pair and export the public key.
	// We simulate this by using a second manager's public key as the "client".
	client, _ := newUIAuthManager("clientkey123456789")
	clientPubKey := client.ServerPublicKeyBase64()

	sharedKey, err := m.DeriveSharedKey(clientPubKey)
	if err != nil {
		t.Fatalf("DeriveSharedKey error: %v", err)
	}
	if len(sharedKey) != 32 {
		t.Errorf("expected 32-byte shared key, got %d bytes", len(sharedKey))
	}
}

func TestUIAuthManager_DeriveSharedKey_Invalid(t *testing.T) {
	m, _ := newUIAuthManager("testkey1234567890")

	// Bad base64
	_, err := m.DeriveSharedKey("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}

	// Valid base64 but not a valid P-256 key
	_, err = m.DeriveSharedKey(base64.StdEncoding.EncodeToString([]byte("tooshort")))
	if err == nil {
		t.Error("expected error for invalid public key bytes")
	}
}

func TestEncryptWithSharedKey(t *testing.T) {
	key := make([]byte, 32)
	plaintext := []byte("hello DRL session token")

	iv, ciphertext, err := encryptWithSharedKey(key, plaintext)
	if err != nil {
		t.Fatalf("encryptWithSharedKey error: %v", err)
	}
	if iv == "" {
		t.Error("expected non-empty IV")
	}
	if ciphertext == "" {
		t.Error("expected non-empty ciphertext")
	}

	// IV must be valid base64 of 12 bytes (96 bits standard AES-GCM nonce).
	ivBytes, err := base64.StdEncoding.DecodeString(iv)
	if err != nil {
		t.Fatalf("IV is not valid base64: %v", err)
	}
	if len(ivBytes) != 12 {
		t.Errorf("expected 12-byte IV, got %d", len(ivBytes))
	}
}

// ─── HTTP handler tests ──────────────────────────────────────────────────────

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(ServerConfig{
		Address:     ":0",
		APIKey:      "thisIsAVerySecureAPIKey123",
		ClusterName: "test-cluster",
		NodeID:      "test-node-1",
		Cluster:     &mockCluster{ready: true, members: []string{"test-node-1", "test-node-2"}},
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func TestHandleUIIndex_ServesHTML(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest("GET", "/drl/ui/", nil)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// Must contain the bootstrap meta tag with required fields.
	for _, want := range []string{
		`name="drl-bootstrap"`,
		`"bootstrapToken"`,
		`"serverPublicKey"`,
		`"clusterName"`,
		`"nodeId"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing expected string %q", want)
		}
	}
}

func TestHandleUIIndex_NoCacheHeader(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/drl/ui/", nil)
	resp, _ := s.app.Test(req)
	cc := resp.Header.Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("expected Cache-Control: no-store, got %q", cc)
	}
}

func TestHandleUIExchange_MissingFields(t *testing.T) {
	s := newTestServer(t)

	cases := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"missing bootstrap token", `{"clientPublicKey":"abc"}`},
		{"missing client key", `{"bootstrapToken":"abc"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/drl/ui/exchange",
				bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := s.app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			if resp.StatusCode != fiber.StatusBadRequest {
				t.Errorf("expected 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestHandleUIExchange_InvalidBootstrapToken(t *testing.T) {
	s := newTestServer(t)

	body, _ := json.Marshal(keyExchangeRequest{
		ClientPublicKey: "dummykey",
		BootstrapToken:  "invalid.token",
	})
	req := httptest.NewRequest("POST", "/drl/ui/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHandleUIExchange_FullFlow(t *testing.T) {
	s := newTestServer(t)

	// Get the SPA HTML to extract the bootstrap token.
	htmlReq := httptest.NewRequest("GET", "/drl/ui/", nil)
	htmlResp, _ := s.app.Test(htmlReq)
	htmlBody, _ := io.ReadAll(htmlResp.Body)
	html := string(htmlBody)

	bootstrapToken := extractBootstrapToken(t, html)
	if bootstrapToken == "" {
		t.Fatal("could not extract bootstrap token from HTML")
	}

	// Generate a fake client public key using a second uiAuthManager (same P-256 curve).
	clientManager, _ := newUIAuthManager("clientkey1234567890")
	clientPubKeyB64 := clientManager.ServerPublicKeyBase64()

	// Perform the key exchange.
	body, _ := json.Marshal(keyExchangeRequest{
		ClientPublicKey: clientPubKeyB64,
		BootstrapToken:  bootstrapToken,
	})
	req := httptest.NewRequest("POST", "/drl/ui/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, respBody)
	}

	var exch keyExchangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&exch); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	// Validate response fields.
	if exch.ServerPublicKey == "" {
		t.Error("expected serverPublicKey in response")
	}
	if exch.IV == "" {
		t.Error("expected iv in response")
	}
	if exch.EncryptedSession == "" {
		t.Error("expected encryptedSession in response")
	}
	if exch.ExpiresIn <= 0 {
		t.Errorf("expected positive expiresIn, got %d", exch.ExpiresIn)
	}
}

func TestDualAuthMiddleware_DRLSession(t *testing.T) {
	s := newTestServer(t)

	// Create a valid session directly.
	sessionToken, err := s.uiAuth.CreateSession(make([]byte, 32))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "DRL-Session "+sessionToken)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200 with DRL-Session, got %d: %s", resp.StatusCode, body)
	}
}

func TestDualAuthMiddleware_BearerToken(t *testing.T) {
	s := newTestServer(t)

	// Create a valid session and use it as a Bearer token.
	sessionToken, err := s.uiAuth.CreateSession(make([]byte, 32))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200 with Bearer token, got %d: %s", resp.StatusCode, body)
	}
}

func TestDualAuthMiddleware_InvalidSession(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest("GET", "/status", nil)
	req.Header.Set("Authorization", "DRL-Session invalid.session.token")
	resp, _ := s.app.Test(req)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("expected 401 for invalid DRL session, got %d", resp.StatusCode)
	}
}

func TestDualAuthMiddleware_DigestStillWorks(t *testing.T) {
	s := newTestServer(t)
	apiKey := "thisIsAVerySecureAPIKey123"

	// Step 1: get challenge
	req1 := httptest.NewRequest("GET", "/status", nil)
	resp1, _ := s.app.Test(req1)
	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected 401 challenge, got %d", resp1.StatusCode)
	}
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	// Step 2: authenticated request via Digest
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/status", "GET")
	req2 := httptest.NewRequest("GET", "/status", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, _ := s.app.Test(req2)
	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("expected 200 via Digest, got %d: %s", resp2.StatusCode, body)
	}
}

func TestHandleUIMetrics_WithSession(t *testing.T) {
	s := newTestServer(t)
	sessionToken, _ := s.uiAuth.CreateSession(make([]byte, 32))

	req := httptest.NewRequest("GET", "/drl/ui/api/metrics", nil)
	req.Header.Set("Authorization", "DRL-Session "+sessionToken)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result uiMetricsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding metrics response: %v", err)
	}
	if result.NodeID != "test-node-1" {
		t.Errorf("expected node_id test-node-1, got %q", result.NodeID)
	}
	if result.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if result.Metrics == nil {
		t.Error("expected metrics map (may be empty)")
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// extractBootstrapToken pulls the bootstrapToken value out of the rendered HTML.
// The new format uses a <meta name="drl-bootstrap" content='{"bootstrapToken":"..."}'>
// tag instead of a JavaScript global.
func extractBootstrapToken(t *testing.T, html string) string {
	t.Helper()
	const openTag = `name="drl-bootstrap" content='`
	idx := strings.Index(html, openTag)
	if idx < 0 {
		t.Logf("HTML snippet: %s", html[:min(len(html), 500)])
		return ""
	}
	rest := html[idx+len(openTag):]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	content := rest[:end]

	var meta bootstrapMeta
	if err := json.Unmarshal([]byte(content), &meta); err != nil {
		t.Logf("JSON parse error: %v, content: %q", err, content)
		return ""
	}
	return meta.BootstrapToken
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
