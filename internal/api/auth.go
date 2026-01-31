package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/xdg-go/scram"
)

const (
	// MinAPIKeyLength is the minimum length for the API key
	MinAPIKeyLength = 16
	// scramIterations is the number of iterations for SCRAM key derivation
	scramIterations = 4096
	// scramSaltLength is the length of the random salt
	scramSaltLength = 16
	// scramUsername is the fixed username for internal API authentication
	scramUsername = "admin"
)

// SCRAMAuthenticator handles SCRAM-SHA-256 authentication
type SCRAMAuthenticator struct {
	mu sync.RWMutex
	// storedKey and serverKey are derived from the password
	storedKey []byte
	serverKey []byte
	salt      []byte
	// conversations tracks ongoing SCRAM handshakes
	conversations map[string]*scramConversation
}

// scramConversation tracks the state of a SCRAM handshake
type scramConversation struct {
	clientNonce string
	serverNonce string
}

// NewSCRAMAuthenticator creates a new SCRAM authenticator from the API key
func NewSCRAMAuthenticator(apiKey string) (*SCRAMAuthenticator, error) {
	if len(apiKey) < MinAPIKeyLength {
		return nil, fmt.Errorf("API key must be at least %d characters", MinAPIKeyLength)
	}

	// Generate random salt
	salt := make([]byte, scramSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	// Derive stored key and server key using SCRAM
	saltedPassword := scram.KeyFactors{
		Salt:  string(salt),
		Iters: scramIterations,
	}

	// Use SHA-256 hash generator
	client, err := scram.SHA256.NewClient(scramUsername, apiKey, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create SCRAM client: %w", err)
	}

	// Derive keys using SCRAM mechanism
	clientKey, err := client.GetStoredCredentialsWithError(saltedPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to derive stored credentials: %w", err)
	}

	return &SCRAMAuthenticator{
		storedKey:     clientKey.StoredKey,
		serverKey:     clientKey.ServerKey,
		salt:          salt,
		conversations: make(map[string]*scramConversation),
	}, nil
}

// Middleware returns a Fiber middleware for SCRAM authentication
func (a *SCRAMAuthenticator) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if auth == "" {
			return a.sendChallenge(c, "")
		}

		// Check for SCRAM-SHA-256 scheme
		if !strings.HasPrefix(auth, "SCRAM-SHA-256 ") {
			return a.sendChallenge(c, "")
		}

		payload := strings.TrimPrefix(auth, "SCRAM-SHA-256 ")

		// Parse the SCRAM message
		if strings.HasPrefix(payload, "n,,") || strings.HasPrefix(payload, "n=") {
			// Client first message
			return a.handleClientFirst(c, payload)
		}

		if strings.HasPrefix(payload, "c=") {
			// Client final message
			return a.handleClientFinal(c, payload)
		}

		return a.sendChallenge(c, "Invalid SCRAM message format")
	}
}

// sendChallenge sends a 401 response with SCRAM challenge
func (a *SCRAMAuthenticator) sendChallenge(c *fiber.Ctx, msg string) error {
	// Generate a new nonce for this challenge
	nonce := generateNonce()

	challenge := fmt.Sprintf("SCRAM-SHA-256 r=%s,s=%s,i=%d",
		nonce,
		base64.StdEncoding.EncodeToString(a.salt),
		scramIterations,
	)
	c.Set("WWW-Authenticate", challenge)

	errMsg := "Authentication required"
	if msg != "" {
		errMsg = msg
	}
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": errMsg,
	})
}

// handleClientFirst processes the client-first message and returns server-first
func (a *SCRAMAuthenticator) handleClientFirst(c *fiber.Ctx, payload string) error {
	// Parse client-first-message
	// Format: n,,n=<user>,r=<client-nonce>
	// or: n=<user>,r=<client-nonce> (without GS2 header)

	msg := strings.TrimPrefix(payload, "n,,")

	parts := strings.Split(msg, ",")
	var username, clientNonce string

	for _, part := range parts {
		if strings.HasPrefix(part, "n=") {
			username = strings.TrimPrefix(part, "n=")
		}
		if strings.HasPrefix(part, "r=") {
			clientNonce = strings.TrimPrefix(part, "r=")
		}
	}

	if username == "" || clientNonce == "" {
		return a.sendChallenge(c, "Invalid client-first message")
	}

	// Verify username matches expected
	if username != scramUsername {
		// Don't reveal whether username exists
		return a.sendChallenge(c, "Authentication failed")
	}

	// Generate server nonce (client nonce + server random)
	serverRandom := generateNonce()
	serverNonce := clientNonce + serverRandom

	// Store conversation state
	a.mu.Lock()
	a.conversations[clientNonce] = &scramConversation{
		clientNonce: clientNonce,
		serverNonce: serverNonce,
	}
	a.mu.Unlock()

	// Send server-first-message
	serverFirst := fmt.Sprintf("r=%s,s=%s,i=%d",
		serverNonce,
		base64.StdEncoding.EncodeToString(a.salt),
		scramIterations,
	)

	c.Set("WWW-Authenticate", "SCRAM-SHA-256 "+serverFirst)
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"scram_step": "server-first",
		"message":    serverFirst,
	})
}

// handleClientFinal processes the client-final message and verifies proof
func (a *SCRAMAuthenticator) handleClientFinal(c *fiber.Ctx, payload string) error {
	// Parse client-final-message
	// Format: c=<channel-binding>,r=<nonce>,p=<proof>

	parts := strings.Split(payload, ",")
	var channelBinding, nonce, proof string

	for _, part := range parts {
		if strings.HasPrefix(part, "c=") {
			channelBinding = strings.TrimPrefix(part, "c=")
		}
		if strings.HasPrefix(part, "r=") {
			nonce = strings.TrimPrefix(part, "r=")
		}
		if strings.HasPrefix(part, "p=") {
			proof = strings.TrimPrefix(part, "p=")
		}
	}

	if channelBinding == "" || nonce == "" || proof == "" {
		return a.sendChallenge(c, "Invalid client-final message")
	}

	// Find the conversation by extracting client nonce from full nonce
	a.mu.RLock()
	var conv *scramConversation
	for clientNonce, c := range a.conversations {
		if strings.HasPrefix(nonce, clientNonce) {
			conv = c
			break
		}
	}
	a.mu.RUnlock()

	if conv == nil {
		return a.sendChallenge(c, "Unknown conversation")
	}

	// Verify the nonce matches
	if nonce != conv.serverNonce {
		return a.sendChallenge(c, "Nonce mismatch")
	}

	// Decode channel binding and verify it's correct (should be "biws" for no binding)
	cbData, err := base64.StdEncoding.DecodeString(channelBinding)
	if err != nil || string(cbData) != "n,," {
		return a.sendChallenge(c, "Invalid channel binding")
	}

	// Decode proof
	proofBytes, err := base64.StdEncoding.DecodeString(proof)
	if err != nil {
		return a.sendChallenge(c, "Invalid proof encoding")
	}

	// Verify proof length matches expected (SHA-256 = 32 bytes)
	if len(proofBytes) != sha256.Size {
		return a.sendChallenge(c, "Invalid proof length")
	}

	// Verify proof
	// AuthMessage = client-first-message-bare + "," + server-first-message + "," + client-final-message-without-proof
	clientFirstBare := fmt.Sprintf("n=%s,r=%s", scramUsername, conv.clientNonce)
	serverFirst := fmt.Sprintf("r=%s,s=%s,i=%d",
		conv.serverNonce,
		base64.StdEncoding.EncodeToString(a.salt),
		scramIterations,
	)
	clientFinalWithoutProof := fmt.Sprintf("c=%s,r=%s", channelBinding, nonce)
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalWithoutProof

	// Calculate ClientSignature = HMAC(StoredKey, AuthMessage)
	clientSignature := hmacSHA256(a.storedKey, []byte(authMessage))

	// Calculate ClientKey = ClientProof XOR ClientSignature
	clientKey := make([]byte, len(proofBytes))
	for i := range proofBytes {
		clientKey[i] = proofBytes[i] ^ clientSignature[i]
	}

	// Calculate StoredKey = H(ClientKey)
	computedStoredKey := sha256.Sum256(clientKey)

	// Verify StoredKey matches
	if !hmac.Equal(computedStoredKey[:], a.storedKey) {
		return a.sendChallenge(c, "Authentication failed")
	}

	// Authentication successful - calculate server signature
	serverSignature := hmacSHA256(a.serverKey, []byte(authMessage))

	// Clean up conversation
	a.mu.Lock()
	delete(a.conversations, conv.clientNonce)
	a.mu.Unlock()

	// Set server verification header
	c.Set("Authentication-Info", "v="+base64.StdEncoding.EncodeToString(serverSignature))

	return c.Next()
}

// generateNonce generates a random nonce for SCRAM
func generateNonce() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// hmacSHA256 calculates HMAC-SHA256
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
