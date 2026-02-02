package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestNewDigestAuthenticator(t *testing.T) {
	tests := []struct {
		name    string
		apiKey  string
		wantErr bool
	}{
		{
			name:    "valid API key",
			apiKey:  "thisIsAVerySecureAPIKey123",
			wantErr: false,
		},
		{
			name:    "minimum length API key",
			apiKey:  "1234567890123456",
			wantErr: false,
		},
		{
			name:    "too short API key",
			apiKey:  "short",
			wantErr: true,
		},
		{
			name:    "empty API key",
			apiKey:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, err := NewDigestAuthenticator(tt.apiKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDigestAuthenticator() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && auth == nil {
				t.Error("NewDigestAuthenticator() returned nil authenticator without error")
			}
			if !tt.wantErr {
				if auth.hashedCredentials == "" {
					t.Error("NewDigestAuthenticator() hashedCredentials is empty")
				}
				// Verify A1 hash is correctly computed
				expectedA1 := fmt.Sprintf("%s:%s:%s", digestUsername, digestRealm, tt.apiKey)
				expectedHash := sha256Sum(expectedA1)
				if auth.hashedCredentials != expectedHash {
					t.Errorf("NewDigestAuthenticator() hashedCredentials = %s, want %s", auth.hashedCredentials, expectedHash)
				}
			}
		})
	}
}

func TestDigestMiddleware_NoAuth(t *testing.T) {
	auth, err := NewDigestAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(wwwAuth, "Digest") {
		t.Errorf("Expected WWW-Authenticate header to start with 'Digest', got %s", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "algorithm=SHA-256") {
		t.Errorf("Expected WWW-Authenticate to contain 'algorithm=SHA-256', got %s", wwwAuth)
	}
	if !strings.Contains(wwwAuth, "nonce=") {
		t.Errorf("Expected WWW-Authenticate to contain 'nonce=', got %s", wwwAuth)
	}
	if !strings.Contains(wwwAuth, `realm="DRL Internal API"`) {
		t.Errorf("Expected WWW-Authenticate to contain realm, got %s", wwwAuth)
	}
}

func TestDigestMiddleware_InvalidScheme(t *testing.T) {
	auth, err := NewDigestAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Basic YWRtaW46c2VjcmV0")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}
}

func TestDigestMiddleware_WrongUsername(t *testing.T) {
	auth, err := NewDigestAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// First get a valid nonce
	req1 := httptest.NewRequest("GET", "/test", nil)
	resp1, _ := app.Test(req1)
	nonce := extractNonceFromWWWAuth(resp1.Header.Get("WWW-Authenticate"))

	// Try with wrong username
	digestAuth := buildDigestAuth("wronguser", "thisIsAVerySecureAPIKey123", nonce, "/test", "GET")
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", digestAuth)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d for wrong username, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}
}

func TestDigestMiddleware_WrongPassword(t *testing.T) {
	auth, err := NewDigestAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// First get a valid nonce
	req1 := httptest.NewRequest("GET", "/test", nil)
	resp1, _ := app.Test(req1)
	nonce := extractNonceFromWWWAuth(resp1.Header.Get("WWW-Authenticate"))

	// Try with wrong password
	digestAuth := buildDigestAuth(digestUsername, "wrongPasswordHere1234", nonce, "/test", "GET")
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", digestAuth)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d for wrong password, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}
}

func TestDigestMiddleware_FullHandshake(t *testing.T) {
	apiKey := "thisIsAVerySecureAPIKey123"
	auth, err := NewDigestAuthenticator(apiKey)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// Step 1: Get challenge
	req1 := httptest.NewRequest("GET", "/test", nil)
	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("Step 1: Expected status %d, got %d", fiber.StatusUnauthorized, resp1.StatusCode)
	}

	wwwAuth := resp1.Header.Get("WWW-Authenticate")
	nonce := extractNonceFromWWWAuth(wwwAuth)
	if nonce == "" {
		t.Fatalf("Failed to extract nonce from WWW-Authenticate header: %s", wwwAuth)
	}

	// Step 2: Send authenticated request
	digestAuth := buildDigestAuth(digestUsername, apiKey, nonce, "/test", "GET")
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}

	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Step 2: Expected status %d, got %d. Body: %s", fiber.StatusOK, resp2.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp2.Body)
	if string(body) != "OK" {
		t.Errorf("Expected body 'OK', got %s", string(body))
	}
}

func TestDigestMiddleware_NonceReplay(t *testing.T) {
	apiKey := "thisIsAVerySecureAPIKey123"
	auth, err := NewDigestAuthenticator(apiKey)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// Get a nonce
	req1 := httptest.NewRequest("GET", "/test", nil)
	resp1, _ := app.Test(req1)
	nonce := extractNonceFromWWWAuth(resp1.Header.Get("WWW-Authenticate"))

	// First request with nonce should succeed
	digestAuth := buildDigestAuth(digestUsername, apiKey, nonce, "/test", "GET")
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", digestAuth)
	resp2, _ := app.Test(req2)

	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("First request should succeed, got %d", resp2.StatusCode)
	}

	// Second request with same nonce should fail (replay protection)
	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.Header.Set("Authorization", digestAuth)
	resp3, _ := app.Test(req3)

	if resp3.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Replay should be rejected, expected %d, got %d", fiber.StatusUnauthorized, resp3.StatusCode)
	}
}

func TestDigestMiddleware_InvalidNonce(t *testing.T) {
	apiKey := "thisIsAVerySecureAPIKey123"
	auth, err := NewDigestAuthenticator(apiKey)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// Try with invalid nonce
	digestAuth := buildDigestAuth(digestUsername, apiKey, "invalidnonce123456", "/test", "GET")
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", digestAuth)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d for invalid nonce, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}

	// Check for stale indicator
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(wwwAuth, "stale=true") {
		t.Errorf("Expected 'stale=true' in WWW-Authenticate for invalid nonce, got %s", wwwAuth)
	}
}

func TestGenerateNonce_Uniqueness(t *testing.T) {
	auth, err := NewDigestAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	nonces := make(map[string]bool)
	for i := 0; i < 100; i++ {
		nonce := auth.generateNonce()
		if nonces[nonce] {
			t.Errorf("Duplicate nonce generated: %s", nonce)
		}
		nonces[nonce] = true
	}
}

func TestParseDigestAuth(t *testing.T) {
	tests := []struct {
		name     string
		auth     string
		expected map[string]string
	}{
		{
			name: "standard digest auth",
			auth: `Digest username="admin", realm="test", nonce="abc123", uri="/test", response="xyz"`,
			expected: map[string]string{
				"username": "admin",
				"realm":    "test",
				"nonce":    "abc123",
				"uri":      "/test",
				"response": "xyz",
			},
		},
		{
			name: "with qop and nc",
			auth: `Digest username="admin", realm="test", nonce="abc", uri="/", response="xyz", qop=auth, nc=00000001, cnonce="client123"`,
			expected: map[string]string{
				"username": "admin",
				"realm":    "test",
				"nonce":    "abc",
				"uri":      "/",
				"response": "xyz",
				"qop":      "auth",
				"nc":       "00000001",
				"cnonce":   "client123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseDigestAuth(tt.auth)
			for key, expected := range tt.expected {
				if result[key] != expected {
					t.Errorf("parseDigestAuth()[%s] = %q, want %q", key, result[key], expected)
				}
			}
		})
	}
}

func TestSha256Sum(t *testing.T) {
	input := "test string"
	expected := "d5579c46dfcc7f18207013e65b44e4cb4e2c2298f4ac457ba8f82743f31e930b"

	result := sha256Sum(input)
	if result != expected {
		t.Errorf("sha256Sum(%q) = %q, want %q", input, result, expected)
	}
}

// Helper functions

func extractNonceFromWWWAuth(wwwAuth string) string {
	// Parse: Digest realm="...", nonce="...", algorithm=SHA-256, qop="auth"
	params := parseDigestAuth(wwwAuth)
	return params["nonce"]
}

func buildDigestAuth(username, password, nonce, uri, method string) string {
	realm := digestRealm
	cnonce := "abcdef123456"
	nc := "00000001"
	qop := "auth"

	// A1 = username:realm:password
	a1 := fmt.Sprintf("%s:%s:%s", username, realm, password)
	a1Hash := computeSha256(a1)

	// A2 = method:uri
	a2 := fmt.Sprintf("%s:%s", method, uri)
	a2Hash := computeSha256(a2)

	// response = H(A1:nonce:nc:cnonce:qop:A2)
	response := computeSha256(fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		a1Hash, nonce, nc, cnonce, qop, a2Hash))

	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=SHA-256, qop=%s, nc=%s, cnonce="%s", response="%s"`,
		username, realm, nonce, uri, qop, nc, cnonce, response)
}

func computeSha256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
