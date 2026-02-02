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
	"github.com/huandu/xstrings"
	"github.com/samber/lo"
)

const (
	// MinAPIKeyLength is the minimum length for the API key
	MinAPIKeyLength = 16
	// digestRealm is the realm for Digest authentication
	digestRealm = "DRL Internal API"
	// digestUsername is the fixed username for internal API authentication
	digestUsername = "admin"
	// nonceValidityDuration is how long nonce is valid
	nonceValidityDuration = 5 * time.Minute
	// nonceLength is the length of the random nonce
	nonceLength = 32
)

// DigestAuthenticator handles HTTP Digest authentication with SHA-256
type DigestAuthenticator struct {
	mu sync.RWMutex
	// hashedCredentials stores H(username:realm:password) - never the raw password
	hashedCredentials string
	// nonceStore tracks issued nonces and their creation time
	nonceStore map[string]time.Time
}

// NewDigestAuthenticator creates a new Digest authenticator from the API key
func NewDigestAuthenticator(apiKey string) (*DigestAuthenticator, error) {
	if len(apiKey) < MinAPIKeyLength {
		return nil, fmt.Errorf("API key must be at least %d characters", MinAPIKeyLength)
	}
	// Calculate the hashedCredentials
	// This ensures the raw API key is never stored in memory after initialization
	credentials := fmt.Sprintf("%s:%s:%s", digestUsername, digestRealm, apiKey)

	return &DigestAuthenticator{
		hashedCredentials: sha256Sum(credentials),
		nonceStore:        make(map[string]time.Time),
	}, nil
}

// Middleware creates the middleware for Digest authentication for Fiber
func (a *DigestAuthenticator) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// get the Header for authorization digest
		auth := c.Get("Authorization")
		if auth == "" {
			return a.sendChallenge(c)
		}

		// Check for Digest scheme
		if !strings.HasPrefix(auth, "Digest ") {
			return a.sendChallenge(c)
		}

		// Parse the Digest response
		params := parseDigestAuth(auth)

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
		method := c.Method()
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
				a.hashedCredentials, params["nonce"], nc, cnonce, params["qop"], a2Hash))
		} else {
			// response = H(A1:nonce:A2) - RFC 2617 compatible
			expectedResponse = sha256Sum(fmt.Sprintf("%s:%s:%s",
				a.hashedCredentials, params["nonce"], a2Hash))
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

// parseDigestAuth parses a Digest authorization header value into a map of
// parameters Header is in the form:
// ```
// Authorization: Digest username="admin",
// realm="Access Restricted", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c0",
// uri="/index.html", qop=auth, nc=00000001, cnonce="0a4f113b",
// response="6629fae49393a05397450978507c4ef1",
// opaque="5ccc069c403ebaf9f0171e9517f40e41"
// ```
func parseDigestAuth(auth string) map[string]string {
	// Remove the initial `Digest`
	auth = strings.TrimPrefix(auth, "Digest ")

	parts := lo.Map(strings.Split(auth, ","), func(s string, _ int) string {
		return strings.Trim(s, " ")
	})
	digestParams := lo.SliceToMap(parts, func(part string) (key, value string) {
		// e.g. username="admin"
		key, _, value = xstrings.Partition(part, "=")
		return key, strings.TrimPrefix(strings.TrimSuffix(value, `"`), `"`)
	})
	return digestParams
}

// sha256Sum computes the SHA-256 hash of a string and returns hex-encoded result
func sha256Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
