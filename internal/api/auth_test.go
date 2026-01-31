package api

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestNewSCRAMAuthenticator(t *testing.T) {
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
			auth, err := NewSCRAMAuthenticator(tt.apiKey)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSCRAMAuthenticator() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && auth == nil {
				t.Error("NewSCRAMAuthenticator() returned nil authenticator without error")
			}
			if !tt.wantErr {
				if len(auth.storedKey) == 0 {
					t.Error("NewSCRAMAuthenticator() storedKey is empty")
				}
				if len(auth.serverKey) == 0 {
					t.Error("NewSCRAMAuthenticator() serverKey is empty")
				}
				if len(auth.salt) != scramSaltLength {
					t.Errorf("NewSCRAMAuthenticator() salt length = %d, want %d", len(auth.salt), scramSaltLength)
				}
			}
		})
	}
}

func TestSCRAMMiddleware_NoAuth(t *testing.T) {
	auth, err := NewSCRAMAuthenticator("thisIsAVerySecureAPIKey123")
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
	if !strings.HasPrefix(wwwAuth, "SCRAM-SHA-256") {
		t.Errorf("Expected WWW-Authenticate header to start with 'SCRAM-SHA-256', got %s", wwwAuth)
	}
}

func TestSCRAMMiddleware_InvalidScheme(t *testing.T) {
	auth, err := NewSCRAMAuthenticator("thisIsAVerySecureAPIKey123")
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

func TestSCRAMMiddleware_ClientFirst(t *testing.T) {
	auth, err := NewSCRAMAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	clientNonce := "fyko+d2lbbFgONRv9qkxdawL"
	clientFirst := fmt.Sprintf("n,,n=%s,r=%s", scramUsername, clientNonce)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFirst)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d for client-first, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(wwwAuth, "SCRAM-SHA-256 r=") {
		t.Errorf("Expected WWW-Authenticate to contain server-first message, got %s", wwwAuth)
	}

	// Check that the nonce starts with client nonce
	if !strings.Contains(wwwAuth, "r="+clientNonce) {
		t.Errorf("Server nonce should start with client nonce")
	}
}

func TestSCRAMMiddleware_WrongUsername(t *testing.T) {
	auth, err := NewSCRAMAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	clientFirst := "n,,n=wronguser,r=someNonce123"

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFirst)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("Failed to test request: %v", err)
	}

	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d for wrong username, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Authentication failed") {
		t.Errorf("Expected 'Authentication failed' message, got %s", string(body))
	}
}

func TestSCRAMMiddleware_FullHandshake(t *testing.T) {
	apiKey := "thisIsAVerySecureAPIKey123"
	auth, err := NewSCRAMAuthenticator(apiKey)
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// Step 1: Send client-first message
	clientNonce := "rOprNGfwEbeRWgbNEkqO"
	clientFirst := fmt.Sprintf("n,,n=%s,r=%s", scramUsername, clientNonce)
	clientFirstBare := fmt.Sprintf("n=%s,r=%s", scramUsername, clientNonce)

	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFirst)
	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	if resp1.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("Step 1: Expected status %d, got %d", fiber.StatusUnauthorized, resp1.StatusCode)
	}

	// Parse server-first message
	serverFirstHeader := resp1.Header.Get("WWW-Authenticate")
	serverFirst := strings.TrimPrefix(serverFirstHeader, "SCRAM-SHA-256 ")

	// Extract server nonce, salt, and iterations
	parts := strings.Split(serverFirst, ",")
	var serverNonce, saltB64 string
	var iterations int
	for _, part := range parts {
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

	if !strings.HasPrefix(serverNonce, clientNonce) {
		t.Fatalf("Server nonce should start with client nonce")
	}

	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		t.Fatalf("Failed to decode salt: %v", err)
	}

	// Step 2: Calculate and send client-final message
	channelBinding := base64.StdEncoding.EncodeToString([]byte("n,,"))
	clientFinalWithoutProof := fmt.Sprintf("c=%s,r=%s", channelBinding, serverNonce)
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalWithoutProof

	// Derive keys using PBKDF2 (matching the server's SCRAM implementation)
	saltedPassword := pbkdf2Sha256([]byte(apiKey), salt, iterations, 32)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	clientSignature := hmacSHA256(storedKey[:], []byte(authMessage))

	// Calculate proof
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ clientSignature[i]
	}
	proofB64 := base64.StdEncoding.EncodeToString(proof)

	clientFinal := fmt.Sprintf("c=%s,r=%s,p=%s", channelBinding, serverNonce, proofB64)

	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFinal)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}

	if resp2.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("Step 2: Expected status %d, got %d. Body: %s", fiber.StatusOK, resp2.StatusCode, string(body))
	}

	// Verify server signature is present
	authInfo := resp2.Header.Get("Authentication-Info")
	if !strings.HasPrefix(authInfo, "v=") {
		t.Errorf("Expected Authentication-Info header with server signature")
	}

	body, _ := io.ReadAll(resp2.Body)
	if string(body) != "OK" {
		t.Errorf("Expected body 'OK', got %s", string(body))
	}
}

func TestSCRAMMiddleware_InvalidProof(t *testing.T) {
	auth, err := NewSCRAMAuthenticator("thisIsAVerySecureAPIKey123")
	if err != nil {
		t.Fatalf("Failed to create authenticator: %v", err)
	}

	app := fiber.New()
	app.Get("/test", auth.Middleware(), func(c *fiber.Ctx) error {
		return c.SendString("OK")
	})

	// Step 1: Send client-first message
	clientNonce := "testNonce123"
	clientFirst := fmt.Sprintf("n,,n=%s,r=%s", scramUsername, clientNonce)

	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFirst)
	resp1, err := app.Test(req1)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}

	// Parse server-first message
	serverFirstHeader := resp1.Header.Get("WWW-Authenticate")
	serverFirst := strings.TrimPrefix(serverFirstHeader, "SCRAM-SHA-256 ")

	var serverNonce string
	for _, part := range strings.Split(serverFirst, ",") {
		if strings.HasPrefix(part, "r=") {
			serverNonce = strings.TrimPrefix(part, "r=")
			break
		}
	}

	// Step 2: Send client-final with wrong proof
	channelBinding := base64.StdEncoding.EncodeToString([]byte("n,,"))
	wrongProof := base64.StdEncoding.EncodeToString([]byte("wrongproof00000000000000000000000"))
	clientFinal := fmt.Sprintf("c=%s,r=%s,p=%s", channelBinding, serverNonce, wrongProof)

	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "SCRAM-SHA-256 "+clientFinal)
	resp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("Step 2 failed: %v", err)
	}

	if resp2.StatusCode != fiber.StatusUnauthorized {
		t.Errorf("Expected status %d for invalid proof, got %d", fiber.StatusUnauthorized, resp2.StatusCode)
	}
}

func TestGenerateNonce(t *testing.T) {
	nonce1 := generateNonce()
	nonce2 := generateNonce()

	if nonce1 == "" {
		t.Error("generateNonce() returned empty string")
	}
	if nonce1 == nonce2 {
		t.Error("generateNonce() should return unique values")
	}

	// Check that it's valid base64
	_, err := base64.StdEncoding.DecodeString(nonce1)
	if err != nil {
		t.Errorf("generateNonce() should return valid base64: %v", err)
	}
}

// pbkdf2Sha256 derives a key using PBKDF2 with SHA-256
func pbkdf2Sha256(password, salt []byte, iterations, keyLen int) []byte {
	numBlocks := (keyLen + sha256.Size - 1) / sha256.Size
	dk := make([]byte, 0, numBlocks*sha256.Size)

	for block := 1; block <= numBlocks; block++ {
		// U1 = PRF(Password, Salt || INT(i))
		blockBytes := []byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)}
		u := hmacSHA256(password, append(salt, blockBytes...))
		result := make([]byte, len(u))
		copy(result, u)

		// U2 = PRF(Password, U1), U3 = PRF(Password, U2), ...
		for i := 1; i < iterations; i++ {
			u = hmacSHA256(password, u)
			for j := range result {
				result[j] ^= u[j]
			}
		}
		dk = append(dk, result...)
	}

	return dk[:keyLen]
}
