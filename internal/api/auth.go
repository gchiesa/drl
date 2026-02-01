package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	// MinAPIKeyLength is the minimum length for the API key
	MinAPIKeyLength = 16
	// digestRealm is the realm for Digest authentication
	digestRealm = "DRL Internal API"
	// digestUsername is the fixed username for internal API authentication
	digestUsername = "admin"
	// nonceValidityDuration is how long a nonce is valid
	nonceValidityDuration = 5 * time.Minute
	// nonceLength is the length of the random nonce
	nonceLength = 32
)

// DigestAuthenticator handles HTTP Digest authentication with SHA-256
type DigestAuthenticator struct {
	mu sync.RWMutex
	// a1Hash stores H(username:realm:password) - never the raw password
	a1Hash string
	// nonceStore tracks issued nonces and their creation time
	nonceStore map[string]time.Time
}

// NewDigestAuthenticator creates a new Digest authenticator from the API key
func NewDigestAuthenticator(apiKey string) (*DigestAuthenticator, error) {
	if len(apiKey) < MinAPIKeyLength {
		return nil, fmt.Errorf("API key must be at least %d characters", MinAPIKeyLength)
	}

	// Compute A1 hash: H(username:realm:password)
	// This ensures the raw API key is never stored in memory after initialization
	a1 := fmt.Sprintf("%s:%s:%s", digestUsername, digestRealm, apiKey)
	a1Hash := sha256Sum(a1)

	return &DigestAuthenticator{
		a1Hash:     a1Hash,
		nonceStore: make(map[string]time.Time),
	}, nil
}

// Middleware returns a Fiber middleware for Digest authentication
func (a *DigestAuthenticator) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if auth == "" {
			return a.sendChallenge(c)
		}

		// Check for Digest scheme
		if !strings.HasPrefix(auth, "Digest ") {
			return a.sendChallenge(c)
		}

		// Parse the Digest response
		params := parseDigestAuth(strings.TrimPrefix(auth, "Digest "))

		// Validate required parameters
		if params["username"] == "" || params["realm"] == "" ||
			params["nonce"] == "" || params["uri"] == "" || params["response"] == "" {
			return a.sendChallenge(c)
		}

		// Verify username matches expected
		if params["username"] != digestUsername {
			return a.sendChallenge(c)
		}

		// Verify realm matches
		if params["realm"] != digestRealm {
			return a.sendChallenge(c)
		}

		// Verify algorithm is SHA-256 if specified
		if alg := params["algorithm"]; alg != "" && alg != "SHA-256" {
			return a.sendChallenge(c)
		}

		// Verify nonce is valid
		if !a.validateNonce(params["nonce"]) {
			return a.sendStaleChallenge(c)
		}

		// Compute expected response
		// A1 = username:realm:password (we have its hash stored)
		// A2 = method:uri
		method := string(c.Method())
		uri := params["uri"]
		a2 := fmt.Sprintf("%s:%s", method, uri)
		a2Hash := sha256Sum(a2)

		var expectedResponse string
		if params["qop"] == "auth" {
			// response = H(A1:nonce:nc:cnonce:qop:A2)
			nc := params["nc"]
			cnonce := params["cnonce"]
			if nc == "" || cnonce == "" {
				return a.sendChallenge(c)
			}
			expectedResponse = sha256Sum(fmt.Sprintf("%s:%s:%s:%s:%s:%s",
				a.a1Hash, params["nonce"], nc, cnonce, params["qop"], a2Hash))
		} else {
			// response = H(A1:nonce:A2) - RFC 2617 compatible
			expectedResponse = sha256Sum(fmt.Sprintf("%s:%s:%s",
				a.a1Hash, params["nonce"], a2Hash))
		}

		// Verify response
		if params["response"] != expectedResponse {
			return a.sendChallenge(c)
		}

		// Remove used nonce to prevent replay attacks
		a.mu.Lock()
		delete(a.nonceStore, params["nonce"])
		a.mu.Unlock()

		return c.Next()
	}
}

// sendChallenge sends a 401 response with Digest challenge
func (a *DigestAuthenticator) sendChallenge(c *fiber.Ctx) error {
	return a.sendChallengeWithStale(c, false)
}

// sendStaleChallenge sends a 401 response indicating the nonce is stale
func (a *DigestAuthenticator) sendStaleChallenge(c *fiber.Ctx) error {
	return a.sendChallengeWithStale(c, true)
}

// sendChallengeWithStale sends a 401 response with Digest challenge
func (a *DigestAuthenticator) sendChallengeWithStale(c *fiber.Ctx, stale bool) error {
	nonce := a.generateNonce()

	challenge := fmt.Sprintf(`Digest realm="%s", nonce="%s", algorithm=SHA-256, qop="auth"`,
		digestRealm, nonce)
	if stale {
		challenge += ", stale=true"
	}

	c.Set("WWW-Authenticate", challenge)
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "Authentication required",
	})
}

// generateNonce generates a unique nonce and stores it
func (a *DigestAuthenticator) generateNonce() string {
	b := make([]byte, nonceLength)
	_, _ = rand.Read(b)
	nonce := hex.EncodeToString(b)

	a.mu.Lock()
	a.nonceStore[nonce] = time.Now()
	// Clean up expired nonces
	a.cleanupExpiredNonces()
	a.mu.Unlock()

	return nonce
}

// validateNonce checks if a nonce is valid and not expired
func (a *DigestAuthenticator) validateNonce(nonce string) bool {
	a.mu.RLock()
	createdAt, exists := a.nonceStore[nonce]
	a.mu.RUnlock()

	if !exists {
		return false
	}

	return time.Since(createdAt) <= nonceValidityDuration
}

// cleanupExpiredNonces removes expired nonces from the store
// Must be called with lock held
func (a *DigestAuthenticator) cleanupExpiredNonces() {
	now := time.Now()
	for nonce, createdAt := range a.nonceStore {
		if now.Sub(createdAt) > nonceValidityDuration {
			delete(a.nonceStore, nonce)
		}
	}
}

// parseDigestAuth parses a Digest authorization header value
func parseDigestAuth(auth string) map[string]string {
	params := make(map[string]string)

	// Split by comma, but be careful with quoted strings
	parts := splitDigestParams(auth)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(part[:idx])
		value := strings.TrimSpace(part[idx+1:])

		// Remove quotes if present
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}

		params[key] = value
	}

	return params
}

// splitDigestParams splits Digest auth parameters handling quoted commas
func splitDigestParams(auth string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false

	for _, ch := range auth {
		switch ch {
		case '"':
			inQuotes = !inQuotes
			current.WriteRune(ch)
		case ',':
			if inQuotes {
				current.WriteRune(ch)
			} else {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// sha256Sum computes the SHA-256 hash of a string and returns hex-encoded result
func sha256Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
