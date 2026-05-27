package proxy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// --- Fakes ---

type fakeBlocklist struct {
	blocked map[string]bool
}

func (f *fakeBlocklist) IsBlockedWithExpiration(key string) (time.Time, bool) {
	if f.blocked[key] {
		return time.Now().Add(time.Minute), true
	}
	return time.Time{}, false
}

type fakeAccounting struct {
	processedKeys []string
	keyPrefix     string
}

func (f *fakeAccounting) Process(sourceIP, path string, headers map[string]string) {
	f.processedKeys = append(f.processedKeys, sourceIP+path)
}

func (f *fakeAccounting) BuildEntityKey(sourceIP, path string, headers map[string]string) string {
	return f.keyPrefix + sourceIP + path
}

// --- Helper: minimal proxy config ---

func simpleProxyConfig(upstreamURL, routePrefix string) config.EmbeddedProxyConfig {
	return config.EmbeddedProxyConfig{
		Enabled: true,
		Listen:  "127.0.0.1:0",
		Hosts: []config.ProxyHostConfig{
			{
				Hostname: "test.local",
				Routes: config.ProxyRoutesWrapper{
					Routes: []config.ProxyRouteConfig{
						{
							Prefix:   routePrefix,
							Upstream: upstreamURL,
						},
					},
				},
			},
		},
	}
}

// --- Tests ---

func TestBuildTLSConfig_InvalidBase64Cert(t *testing.T) {
	cfg := config.EmbeddedProxyTLSConfig{
		Enabled: true,
		Cert:    "!!!not-base64!!!",
		Key:     base64.StdEncoding.EncodeToString([]byte("somekey")),
	}
	_, err := buildTLSConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode base64 cert")
}

func TestBuildTLSConfig_InvalidBase64Key(t *testing.T) {
	cfg := config.EmbeddedProxyTLSConfig{
		Enabled: true,
		Cert:    base64.StdEncoding.EncodeToString([]byte("somecert")),
		Key:     "!!!not-base64!!!",
	}
	_, err := buildTLSConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode base64 key")
}

func TestBuildTLSConfig_InvalidKeyPair(t *testing.T) {
	// Valid base64 but not valid PEM cert/key pair.
	cfg := config.EmbeddedProxyTLSConfig{
		Enabled: true,
		Cert:    base64.StdEncoding.EncodeToString([]byte("not-a-cert")),
		Key:     base64.StdEncoding.EncodeToString([]byte("not-a-key")),
	}
	_, err := buildTLSConfig(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse X509 key pair")
}

func TestBuildTLSConfig_ValidCertKey(t *testing.T) {
	certPEM, keyPEM := generateSelfSignedCert(t)

	cfg := config.EmbeddedProxyTLSConfig{
		Enabled: true,
		Cert:    base64.StdEncoding.EncodeToString(certPEM),
		Key:     base64.StdEncoding.EncodeToString(keyPEM),
	}
	tlsCfg, err := buildTLSConfig(cfg)
	require.NoError(t, err)
	require.NotNil(t, tlsCfg)
	assert.Len(t, tlsCfg.Certificates, 1)
	assert.Equal(t, uint16(tls.VersionTLS12), tlsCfg.MinVersion)
}

// TestVirtualHostIsolation verifies that a request matching hostA's routes
// does not accidentally reach hostB.
func TestVirtualHostIsolation(t *testing.T) {
	// Set up two independent echo backends.
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "A")
		w.WriteHeader(http.StatusOK)
	}))
	defer backendA.Close()

	backendB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "B")
		w.WriteHeader(http.StatusOK)
	}))
	defer backendB.Close()

	cfg := config.EmbeddedProxyConfig{
		Enabled: true,
		Listen:  "127.0.0.1:0",
		Hosts: []config.ProxyHostConfig{
			{
				Hostname: "host-a.local",
				Routes: config.ProxyRoutesWrapper{
					Routes: []config.ProxyRouteConfig{
						{Prefix: "/a", Upstream: backendA.URL},
					},
				},
			},
			{
				Hostname: "host-b.local",
				Routes: config.ProxyRoutesWrapper{
					Routes: []config.ProxyRouteConfig{
						{Prefix: "/b", Upstream: backendB.URL},
					},
				},
			},
		},
	}

	srv, err := NewServer(cfg, nil, nil, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	router, err := srv.buildRouter(ctx)
	require.NoError(t, err)

	// A request to /a/* should reach backend A.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/a/resource", nil)
	router.ServeHTTP(rr, req)
	assert.Equal(t, "A", rr.Header().Get("X-Backend"))

	// A request to /b/* should reach backend B.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/b/resource", nil)
	router.ServeHTTP(rr2, req2)
	assert.Equal(t, "B", rr2.Header().Get("X-Backend"))
}

// TestRateLimitMiddleware_BlockedEntity tests that a blocked entity receives 429.
func TestRateLimitMiddleware_BlockedEntity(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// The fakeAccounting.BuildEntityKey returns sourceIP+path.
	// Request path is "/blocked/resource", RemoteAddr is "127.0.0.1:12345".
	blockedKey := "127.0.0.1" + "/blocked/resource"
	acct := &fakeAccounting{keyPrefix: ""}
	bl := &fakeBlocklist{blocked: map[string]bool{blockedKey: true}}

	cfg := simpleProxyConfig(backend.URL, "/blocked")
	srv, err := NewServer(cfg, bl, acct, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	router, err := srv.buildRouter(ctx)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/blocked/resource", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
}

// TestRateLimitMiddleware_AllowedEntity verifies non-blocked entities pass through.
func TestRateLimitMiddleware_AllowedEntity(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	acct := &fakeAccounting{keyPrefix: ""}
	bl := &fakeBlocklist{blocked: map[string]bool{}}

	cfg := simpleProxyConfig(backend.URL, "/api")
	srv, err := NewServer(cfg, bl, acct, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	router, err := srv.buildRouter(ctx)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/resource", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestMetrics_ProxyFieldsRegistered verifies that the three proxy metric families
// are registered inside metrics.NewMetrics() and callable without panic.
func TestMetrics_ProxyFieldsRegistered(t *testing.T) {
	m := metrics.NewMetrics()

	require.NotNil(t, m.ProxyRequestsTotal)
	require.NotNil(t, m.ProxyErrorsTotal)
	require.NotNil(t, m.ProxyLatencySeconds)

	// Accessor methods must not panic.
	assert.NotPanics(t, func() { m.IncProxyRequest("h", "r", "200") })
	assert.NotPanics(t, func() { m.IncProxyError("h", "r", "upstream") })
	assert.NotPanics(t, func() { m.ObserveProxyLatency("h", "r", 0.01) })
}

// TestNewServer_NilMetricsManager verifies the server is usable without a metrics manager.
func TestNewServer_NilMetricsManager(t *testing.T) {
	cfg := config.EmbeddedProxyConfig{Enabled: true, Listen: "127.0.0.1:0"}
	srv, err := NewServer(cfg, nil, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, srv.metrics)
}

// --- Self-signed cert generator for TLS tests ---

func generateSelfSignedCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	require.NoError(t, err)

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return
}
