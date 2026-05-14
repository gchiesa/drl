package api

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/api/models"
)

// ─── uiAuthManager unit tests ────────────────────────────────────────────────

func TestNewUIAuthManager(t *testing.T) {
	m, err := newUIAuthManager("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("newUIAuthManager returned error: %v", err)
	}
	if m == nil { // nolint:staticcheck
		t.Fatal("expected non-nil manager")
	}
	if m.serverPrivKey == nil { // nolint:staticcheck
		t.Error("expected server private key to be set")
	}
	if len(m.bootstrapSigningKey) == 0 { // nolint:staticcheck
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

	req := httptest.NewRequest("GET", "/v1/ui/", nil)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// Must contain the bootstrap meta tag with non-sensitive fields.
	for _, want := range []string{
		`name="drl-bootstrap"`,
		`"serverPublicKey"`,
		`"clusterName"`,
		`"nodeId"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing expected string %q", want)
		}
	}

	// Must NOT contain the bootstrap token in the HTML (security requirement).
	if strings.Contains(html, `"bootstrapToken"`) {
		t.Error("HTML must not contain bootstrapToken — token must be retrieved out-of-band")
	}
}

func TestHandleUIIndex_NoCacheHeader(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/v1/ui/", nil)
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
			req := httptest.NewRequest("POST", "/v1/ui/exchange",
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
	req := httptest.NewRequest("POST", "/v1/ui/exchange", bytes.NewReader(body))

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

	// Retrieve the bootstrap token from the dedicated endpoint (requires Digest auth).
	bootstrapToken := fetchBootstrapTokenViaDigest(t, s)
	if bootstrapToken == "" {
		t.Fatal("could not retrieve bootstrap token from /v1/ui/get-token")
	}

	// Generate a fake client public key using a second uiAuthManager (same P-256 curve).
	clientManager, _ := newUIAuthManager("clientkey1234567890")
	clientPubKeyB64 := clientManager.ServerPublicKeyBase64()

	// Perform the key exchange.
	body, _ := json.Marshal(keyExchangeRequest{
		ClientPublicKey: clientPubKeyB64,
		BootstrapToken:  bootstrapToken,
	})
	req := httptest.NewRequest("POST", "/v1/ui/exchange", bytes.NewReader(body))

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

	req := httptest.NewRequest("GET", "/v1/status", nil)
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

	req := httptest.NewRequest("GET", "/v1/status", nil)
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

	req := httptest.NewRequest("GET", "/v1/status", nil)
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
	req1 := httptest.NewRequest("GET", "/v1/status", nil)
	resp1, _ := s.app.Test(req1)
	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected 401 challenge, got %d", resp1.StatusCode)
	}
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	// Step 2: authenticated request via Digest
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/v1/status", "GET")
	req2 := httptest.NewRequest("GET", "/v1/status", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, _ := s.app.Test(req2)
	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Errorf("expected 200 via Digest, got %d: %s", resp2.StatusCode, body)
	}
}

func TestHandleUIMetrics_WithSession(t *testing.T) {
	s := newTestServer(t)
	sharedKey := make([]byte, 32) // all-zeros key for deterministic test decryption
	sessionToken, _ := s.uiAuth.CreateSession(sharedKey)

	req := httptest.NewRequest("GET", "/v1/ui/api/metrics", nil)
	req.Header.Set("Authorization", "DRL-Session "+sessionToken)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Response is E2EE encrypted — decrypt before decoding.
	plain := decryptTestResponse(t, resp.Body, sharedKey)

	var result models.UIMetricsResponse
	if err := json.Unmarshal(plain, &result); err != nil {
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

func TestEncryptedResponseMiddleware_DigestPassthrough(t *testing.T) {
	// Digest-authenticated requests must receive plaintext (CLI compatibility).
	s := newTestServer(t)
	apiKey := "thisIsAVerySecureAPIKey123"

	req1 := httptest.NewRequest("GET", "/v1/status", nil)
	resp1, _ := s.app.Test(req1)
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/v1/status", "GET")
	req2 := httptest.NewRequest("GET", "/v1/status", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := s.app.Test(req2)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	// Response must be plain JSON (not encrypted) for Digest auth.
	body, _ := io.ReadAll(resp2.Body)
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("expected plain JSON for Digest auth, got parse error: %v", err)
	}
	if _, ok := v["iv"]; ok {
		t.Error("Digest auth response must NOT be encrypted — found 'iv' field")
	}
}

func TestEncryptedResponseMiddleware_SessionEncrypts(t *testing.T) {
	// DRL-Session requests must receive encrypted bodies.
	s := newTestServer(t)
	sharedKey := make([]byte, 32)
	sessionToken, _ := s.uiAuth.CreateSession(sharedKey)

	req := httptest.NewRequest("GET", "/v1/status", nil)
	req.Header.Set("Authorization", "DRL-Session "+sessionToken)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var wrapper map[string]string
	if err := json.Unmarshal(body, &wrapper); err != nil {
		t.Fatalf("expected encrypted JSON wrapper, got parse error: %v", err)
	}
	if wrapper["iv"] == "" || wrapper["data"] == "" {
		t.Fatalf("expected non-empty 'iv' and 'data' fields, got: %v", wrapper)
	}

	// Decrypt and verify it's valid JSON.
	plain := decryptTestResponse(t, bytes.NewReader(body), sharedKey)
	var status map[string]any
	if err := json.Unmarshal(plain, &status); err != nil {
		t.Fatalf("decrypted body is not valid JSON: %v", err)
	}
}

// ─── GET /v1/ui/get-token tests ──────────────────────────────────────────────

func TestHandleUIGetToken_Unauthenticated(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest("GET", "/v1/ui/get-token", nil)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("expected 401 without credentials, got %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate challenge header")
	}
}

func TestHandleUIGetToken_WithDigestAuth(t *testing.T) {
	s := newTestServer(t)
	apiKey := "thisIsAVerySecureAPIKey123"

	// Step 1: obtain Digest challenge.
	req1 := httptest.NewRequest("GET", "/v1/ui/get-token", nil)
	resp1, err := s.app.Test(req1)
	if err != nil {
		t.Fatalf("app.Test (challenge): %v", err)
	}
	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected 401 challenge, got %d", resp1.StatusCode)
	}
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	// Step 2: authenticated request.
	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/v1/ui/get-token", "GET")
	req2 := httptest.NewRequest("GET", "/v1/ui/get-token", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := s.app.Test(req2)
	if err != nil {
		t.Fatalf("app.Test (authed): %v", err)
	}
	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 200, got %d: %s", resp2.StatusCode, body)
	}

	var result models.GetTokenResponse
	if err := json.NewDecoder(resp2.Body).Decode(&result); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if result.BootstrapToken == "" {
		t.Error("expected non-empty bootstrap_token in response")
	}
	// The returned token must be immediately valid.
	if !s.uiAuth.ValidateBootstrapToken(result.BootstrapToken) {
		t.Error("returned bootstrap_token failed validation")
	}
}

func TestHandleUIGetToken_SessionNotAccepted(t *testing.T) {
	s := newTestServer(t)

	// A DRL-Session token must NOT be accepted on the get-token endpoint —
	// it is protected by Digest auth only.
	sessionToken, _ := s.uiAuth.CreateSession(make([]byte, 32))
	req := httptest.NewRequest("GET", "/v1/ui/get-token", nil)
	req.Header.Set("Authorization", "DRL-Session "+sessionToken)
	resp, err := s.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("expected 401 for DRL-Session on get-token endpoint, got %d", resp.StatusCode)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// fetchBootstrapTokenViaDigest retrieves the bootstrap token from
// GET /v1/ui/get-token using the test server's Digest credentials.
func fetchBootstrapTokenViaDigest(t *testing.T, s *Server) string {
	t.Helper()
	apiKey := "thisIsAVerySecureAPIKey123"

	req1 := httptest.NewRequest("GET", "/v1/ui/get-token", nil)
	resp1, err := s.app.Test(req1)
	if err != nil {
		t.Fatalf("get-token challenge: %v", err)
	}
	nonce := extractNonceFromHeader(resp1.Header.Get("WWW-Authenticate"))

	digestAuth := buildDigestAuthForTest(digestUsername, apiKey, nonce, "/v1/ui/get-token", "GET")
	req2 := httptest.NewRequest("GET", "/v1/ui/get-token", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := s.app.Test(req2)
	if err != nil {
		t.Fatalf("get-token authed: %v", err)
	}
	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("get-token returned %d: %s", resp2.StatusCode, body)
	}

	var result models.GetTokenResponse
	if err := json.NewDecoder(resp2.Body).Decode(&result); err != nil {
		t.Fatalf("decoding get-token response: %v", err)
	}
	return result.BootstrapToken
}

// decryptTestResponse reads an encrypted API response body ({"iv":"...","data":"..."})
// and decrypts it with the given AES-256-GCM key, returning the plaintext bytes.
func decryptTestResponse(t *testing.T, r io.Reader, key []byte) []byte {
	t.Helper()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	var wrapper struct {
		IV   string `json:"iv"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		t.Fatalf("response is not an encrypted wrapper: %v\nbody: %s", err, body)
	}
	if wrapper.IV == "" || wrapper.Data == "" {
		t.Fatalf("encrypted wrapper missing iv or data fields: %s", body)
	}

	ivBytes, err := base64.StdEncoding.DecodeString(wrapper.IV)
	if err != nil {
		t.Fatalf("decoding iv: %v", err)
	}
	ctBytes, err := base64.StdEncoding.DecodeString(wrapper.Data)
	if err != nil {
		t.Fatalf("decoding ciphertext: %v", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("creating AES cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("creating GCM: %v", err)
	}
	plain, err := gcm.Open(nil, ivBytes, ctBytes, nil)
	if err != nil {
		t.Fatalf("AES-GCM decryption failed: %v", err)
	}
	return plain
}
