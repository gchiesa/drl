package proxy

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// ──────────────────────────────────────────────────────────────────────────────
// Mock OIDC provider
// ──────────────────────────────────────────────────────────────────────────────

// testOIDCServer is a minimal httptest server that acts as an OIDC provider.
// It exposes:
//   - GET /.well-known/openid-configuration  (discovery document)
//   - GET /jwks                              (JSON Web Key Set)
//
// Tokens are RS256 JWTs signed with an in-memory RSA key generated at creation.
type testOIDCServer struct {
	*httptest.Server
	privateKey *rsa.PrivateKey
	keyID      string
}

// newTestOIDCServer creates and starts a mock OIDC provider.
func newTestOIDCServer(t *testing.T) *testOIDCServer {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	ts := &testOIDCServer{
		privateKey: privateKey,
		keyID:      "test-key-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]interface{}{
			"issuer":   ts.URL,
			"jwks_uri": ts.URL + "/jwks",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		jwks := map[string]interface{}{
			"keys": []interface{}{
				rsaPublicKeyToJWK(&privateKey.PublicKey, ts.keyID),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// MakeToken creates a signed RS256 JWT with the given extra claims merged with
// the required standard claims (iss, iat, exp, sub).
func (ts *testOIDCServer) MakeToken(t *testing.T, extraClaims map[string]interface{}) string {
	t.Helper()

	now := time.Now()
	claims := map[string]interface{}{
		"iss": ts.URL,
		"sub": "test-user",
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
	for k, v := range extraClaims {
		claims[k] = v
	}

	return ts.signJWT(t, claims)
}

// MakeExpiredToken creates a JWT whose exp is in the past.
func (ts *testOIDCServer) MakeExpiredToken(t *testing.T) string {
	t.Helper()
	now := time.Now()
	claims := map[string]interface{}{
		"iss": ts.URL,
		"sub": "test-user",
		"iat": now.Add(-10 * time.Minute).Unix(),
		"exp": now.Add(-5 * time.Minute).Unix(),
	}
	return ts.signJWT(t, claims)
}

// signJWT produces a compact RS256 JWT from arbitrary claims.
func (ts *testOIDCServer) signJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()

	header := map[string]interface{}{
		"alg": "RS256",
		"typ": "JWT",
		"kid": ts.keyID,
	}
	hdrJSON, err := json.Marshal(header)
	require.NoError(t, err)
	payJSON, err := json.Marshal(claims)
	require.NoError(t, err)

	msg := b64url(hdrJSON) + "." + b64url(payJSON)

	h := sha256.New()
	h.Write([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, ts.privateKey, crypto.SHA256, h.Sum(nil))
	require.NoError(t, err)

	return msg + "." + b64url(sig)
}

// rsaPublicKeyToJWK returns a minimal JWK representation of an RSA public key.
func rsaPublicKeyToJWK(pub *rsa.PublicKey, kid string) map[string]interface{} {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())

	// Encode the public exponent as a big-endian byte slice, trimming leading zeros.
	eb := make([]byte, 4)
	binary.BigEndian.PutUint32(eb, uint32(pub.E)) //nolint:gosec // exponent is always small
	i := 0
	for i < len(eb)-1 && eb[i] == 0 {
		i++
	}
	e := base64.RawURLEncoding.EncodeToString(eb[i:])

	return map[string]interface{}{
		"kty": "RSA",
		"use": "sig",
		"kid": kid,
		"alg": "RS256",
		"n":   n,
		"e":   e,
	}
}

// b64url encodes data as unpadded base64url.
func b64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// ──────────────────────────────────────────────────────────────────────────────
// Helper: build a Server with OIDC on one host
// ──────────────────────────────────────────────────────────────────────────────

func proxyConfigWithOIDC(backendURL, issuerURL, audience string, scopes []string) config.EmbeddedProxyConfig {
	return config.EmbeddedProxyConfig{
		Enabled: true,
		Listen:  "127.0.0.1:0",
		Hosts: []config.ProxyHostConfig{
			{
				Hostname: "api.local",
				OIDC: config.ProxyOIDCConfig{
					Issuer:   issuerURL,
					Audience: audience,
				},
				Routes: config.ProxyRoutesWrapper{
					Routes: []config.ProxyRouteConfig{
						{
							Prefix:      "/protected",
							Upstream:    backendURL,
							RequireAuth: true,
							Scopes:      scopes,
						},
						{
							Prefix:      "/public",
							Upstream:    backendURL,
							RequireAuth: false,
						},
					},
				},
			},
		},
	}
}

// buildRouterWithOIDC is the shared setup for OIDC middleware integration tests.
// It discovers the OIDC provider and returns a ready-to-use router and a cancel func.
func buildRouterWithOIDC(t *testing.T, oidcServer *testOIDCServer, audience string, scopes []string) (http.Handler, context.CancelFunc) {
	t.Helper()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Reached-Backend", "yes")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := proxyConfigWithOIDC(backend.URL, oidcServer.URL, audience, scopes)
	srv, err := NewServer(cfg, nil, nil, metrics.NewMetrics())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	router, err := srv.buildRouter(ctx)
	require.NoError(t, err)
	return router, cancel
}

// ──────────────────────────────────────────────────────────────────────────────
// Integration tests — OIDC middleware
// ──────────────────────────────────────────────────────────────────────────────

func TestOIDCMiddleware_MissingToken(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", nil)
	defer cancel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	// No Authorization header
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestOIDCMiddleware_InvalidSignature(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", nil)
	defer cancel()

	// Generate a different RSA key and sign with it — signature will not match JWKS
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	impostor := &testOIDCServer{privateKey: otherKey, keyID: oidcSrv.keyID, Server: oidcSrv.Server}
	token := impostor.MakeToken(t, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestOIDCMiddleware_ExpiredToken(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", nil)
	defer cancel()

	token := oidcSrv.MakeExpiredToken(t)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestOIDCMiddleware_AudienceMismatch(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "expected-audience", nil)
	defer cancel()

	// Token has a different audience
	token := oidcSrv.MakeToken(t, map[string]interface{}{
		"aud": "wrong-audience",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestOIDCMiddleware_MissingRequiredScope(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	// Route requires "write" scope; token only has "read"
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", []string{"write"})
	defer cancel()

	token := oidcSrv.MakeToken(t, map[string]interface{}{
		"scope": "read",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestOIDCMiddleware_ValidToken_PassThrough(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", []string{"read"})
	defer cancel()

	token := oidcSrv.MakeToken(t, map[string]interface{}{
		"scope": "read write",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "yes", rr.Header().Get("X-Reached-Backend"))
}

func TestOIDCMiddleware_RequireAuthFalse_NoCheck(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", nil)
	defer cancel()

	rr := httptest.NewRecorder()
	// /public has require-auth false — no token needed
	req := httptest.NewRequest(http.MethodGet, "/public/resource", nil)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "yes", rr.Header().Get("X-Reached-Backend"))
}

// ──────────────────────────────────────────────────────────────────────────────
// Metrics registration
// ──────────────────────────────────────────────────────────────────────────────

func TestOIDCMetrics_Registered(t *testing.T) {
	m := metrics.NewMetrics()

	require.NotNil(t, m.OIDCRequestsTotal)
	require.NotNil(t, m.OIDCVerificationDuration)

	assert.NotPanics(t, func() { m.IncOIDCRequest("h", "/p", "success") })
	assert.NotPanics(t, func() { m.ObserveOIDCLatency("h", "/p", 0.001) })
}

// ──────────────────────────────────────────────────────────────────────────────
// Unit tests — helper functions
// ──────────────────────────────────────────────────────────────────────────────

func TestExtractStringSliceClaim_SpaceDelimited(t *testing.T) {
	claims := map[string]interface{}{"scope": "read write admin"}
	got := extractStringSliceClaim(claims, "scope")
	assert.Equal(t, []string{"read", "write", "admin"}, got)
}

func TestExtractStringSliceClaim_CommaDelimited(t *testing.T) {
	claims := map[string]interface{}{"scp": "read,write"}
	got := extractStringSliceClaim(claims, "scp")
	assert.Equal(t, []string{"read", "write"}, got)
}

func TestExtractStringSliceClaim_Array(t *testing.T) {
	claims := map[string]interface{}{"roles": []interface{}{"admin", "user"}}
	got := extractStringSliceClaim(claims, "roles")
	assert.Equal(t, []string{"admin", "user"}, got)
}

func TestExtractStringSliceClaim_MissingField(t *testing.T) {
	claims := map[string]interface{}{}
	got := extractStringSliceClaim(claims, "scope")
	assert.Nil(t, got)
}

func TestHasRequiredScopes_AllPresent(t *testing.T) {
	assert.True(t, hasRequiredScopes([]string{"read", "write", "admin"}, []string{"read", "write"}))
}

func TestHasRequiredScopes_Missing(t *testing.T) {
	assert.False(t, hasRequiredScopes([]string{"read"}, []string{"read", "write"}))
}

func TestHasRequiredScopes_EmptyRequired(t *testing.T) {
	assert.True(t, hasRequiredScopes(nil, nil))
	assert.True(t, hasRequiredScopes([]string{"read"}, nil))
}

// TestOIDCMiddleware_ClaimsInjectedInContext verifies that sub and scopes are
// available to downstream handlers via OIDCClaimsFromContext.
// Note: claims live in the in-process context chain (OIDC → next), not in the
// upstream HTTP request, so this test calls the middleware directly with an
// in-process handler rather than routing through a backend httptest.Server.
func TestOIDCMiddleware_ClaimsInjectedInContext(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v, err := newOIDCVerifier(ctx, config.ProxyOIDCConfig{
		Issuer: oidcSrv.URL,
	})
	require.NoError(t, err)

	// In-process next handler — context is preserved across the in-process call.
	var capturedClaims OIDCClaims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedClaims, _ = OIDCClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	srv, err := NewServer(config.EmbeddedProxyConfig{}, nil, nil, nil)
	require.NoError(t, err)

	wrapped := srv.oidcMiddleware("api.local", v, []string{"read"}, next)

	token := oidcSrv.MakeToken(t, map[string]interface{}{
		"sub":   "alice@example.com",
		"scope": "read write",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	wrapped.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "alice@example.com", capturedClaims.Subject)
	assert.Contains(t, capturedClaims.Scopes, "read")
	assert.Contains(t, capturedClaims.Scopes, "write")
}

// TestOIDCMiddleware_ArrayScopesClaim verifies that a token with a JSON-array
// scope claim satisfies scope requirements correctly.
func TestOIDCMiddleware_ArrayScopesClaim(t *testing.T) {
	oidcSrv := newTestOIDCServer(t)
	router, cancel := buildRouterWithOIDC(t, oidcSrv, "", []string{"write"})
	defer cancel()

	token := oidcSrv.MakeToken(t, map[string]interface{}{
		"scope": []interface{}{"read", "write"},
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected/resource", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

// ──────────────────────────────────────────────────────────────────────────────
// Verify classifyOIDCError labels
// ──────────────────────────────────────────────────────────────────────────────

func TestClassifyOIDCError(t *testing.T) {
	cases := []struct {
		msg      string
		expected string
	}{
		{"token is expired by 5m0s", "token_expired"},
		{"failed to verify signature", "invalid_signature"},
		{"malformed token", "invalid_token"},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			got := classifyOIDCError(fmt.Errorf("%s", tc.msg))
			assert.Equal(t, tc.expected, got)
		})
	}
}
