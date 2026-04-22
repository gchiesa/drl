package api

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

//go:embed resources/index.html
var uiIndexHTML []byte

// bootstrapMeta holds the non-sensitive values injected into the SPA as a <meta> tag.
// The bootstrap token is no longer embedded here; it must be retrieved out-of-band
// via GET /drl/ui/get-token (Digest-authenticated).
type bootstrapMeta struct {
	ServerPubKey string `json:"serverPublicKey"`
	ClusterName  string `json:"clusterName"`
	NodeID       string `json:"nodeId"`
}

// getTokenResponse is returned by GET /drl/ui/get-token.
type getTokenResponse struct {
	BootstrapToken string `json:"bootstrap_token"`
}

// keyExchangeRequest is the body of POST /drl/ui/exchange.
type keyExchangeRequest struct {
	// ClientPublicKey is the browser's ephemeral ECDH P-256 public key:
	// standard base64 of the 65-byte uncompressed point (04 || x || y).
	// Obtain in the browser with:
	//   const raw = await crypto.subtle.exportKey("raw", keyPair.publicKey);
	//   clientPublicKey = btoa(String.fromCharCode(...new Uint8Array(raw)));
	ClientPublicKey string `json:"clientPublicKey"`

	// BootstrapToken is the short-lived token injected into the SPA HTML at serve time.
	BootstrapToken string `json:"bootstrapToken"`
}

// keyExchangeResponse is the JSON response of POST /drl/ui/exchange.
type keyExchangeResponse struct {
	// ServerPublicKey is the server's ECDH P-256 public key (same format as client).
	ServerPublicKey string `json:"serverPublicKey"`

	// IV is the base64-encoded AES-GCM nonce (12 bytes) used to encrypt the session token.
	IV string `json:"iv"`

	// EncryptedSession is the base64-encoded AES-256-GCM ciphertext of the session token.
	EncryptedSession string `json:"encryptedSession"`

	// ExpiresIn is the session lifetime in seconds.
	ExpiresIn int `json:"expiresIn"`
}

// uiMetricsResponse is returned by GET /drl/ui/api/metrics.
type uiMetricsResponse struct {
	NodeID    string             `json:"nodeId"`
	Timestamp string             `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}

// handleUIIndex serves the Svelte single-page application.
//
// The built index.html is embedded at compile time. Non-sensitive metadata
// (server public key, cluster name, node ID) is injected just before </head>
// as a <meta name="drl-bootstrap"> tag. The bootstrap token is intentionally
// omitted; the SPA must retrieve it out-of-band via GET /drl/ui/get-token.
func (s *Server) handleUIIndex(c *fiber.Ctx) error {
	if s.uiAuth == nil {
		return c.Status(fiber.StatusServiceUnavailable).
			SendString("UI authentication not configured")
	}

	meta := bootstrapMeta{
		ServerPubKey: s.uiAuth.ServerPublicKeyBase64(),
		ClusterName:  s.clusterName,
		NodeID:       s.nodeID,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshalling bootstrap meta: %w", err)
	}

	metaTag := fmt.Sprintf(`<meta name="drl-bootstrap" content='%s'>`, string(metaJSON))

	// Inject meta tag before </head> in the embedded HTML.
	html := bytes.Replace(uiIndexHTML, []byte("</head>"), []byte(metaTag+"\n</head>"), 1)

	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-store")
	// CSP: allow self scripts (Svelte inlines everything) and connect-src self.
	c.Set("Content-Security-Policy",
		"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:")
	return c.Send(html)
}

// handleUIGetToken handles GET /drl/ui/get-token.
//
// Protected by Digest authentication. Returns a short-lived bootstrap token
// that the browser SPA can use to initiate the ECDH key exchange. The caller
// must obtain this token out-of-band (e.g. via curl) and paste it into the
// token modal presented by the SPA on first load.
//
// Example:
//
//	curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/drl/ui/get-token
func (s *Server) handleUIGetToken(c *fiber.Ctx) error {
	if s.uiAuth == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "UI authentication not configured",
		})
	}
	return c.JSON(getTokenResponse{
		BootstrapToken: s.uiAuth.GenerateBootstrapToken(),
	})
}

// handleUIExchange handles POST /drl/ui/exchange.
//
// The browser posts its ephemeral ECDH public key and the bootstrap token
// obtained from the SPA HTML. The server:
//  1. Validates the bootstrap token (HMAC + expiry).
//  2. Derives the ECDH shared secret.
//  3. Creates a server-side session.
//  4. Encrypts the session token with AES-256-GCM using the shared secret.
//  5. Returns the encrypted session token and server public key.
func (s *Server) handleUIExchange(c *fiber.Ctx) error {
	if s.uiAuth == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "UI authentication not configured",
		})
	}

	var req keyExchangeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.ClientPublicKey == "" || req.BootstrapToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "clientPublicKey and bootstrapToken are required",
		})
	}

	if !s.uiAuth.ValidateBootstrapToken(req.BootstrapToken) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid or expired bootstrap token",
		})
	}

	sharedKey, err := s.uiAuth.DeriveSharedKey(req.ClientPublicKey)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key exchange failed: " + err.Error(),
		})
	}

	sessionToken, err := s.uiAuth.CreateSession(sharedKey)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create session",
		})
	}

	iv, encrypted, err := encryptWithSharedKey(sharedKey, []byte(sessionToken))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "encryption failed",
		})
	}

	return c.JSON(keyExchangeResponse{
		ServerPublicKey:  s.uiAuth.ServerPublicKeyBase64(),
		IV:               iv,
		EncryptedSession: encrypted,
		ExpiresIn:        int(sessionTokenTTL.Seconds()),
	})
}

// handleUIMetrics handles GET /drl/ui/api/metrics.
// Returns a JSON snapshot of the most relevant Prometheus metrics.
func (s *Server) handleUIMetrics(c *fiber.Ctx) error {
	resp := uiMetricsResponse{
		NodeID:    s.nodeID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Metrics:   make(map[string]float64),
	}

	if s.metricsGatherer != nil {
		resp.Metrics = s.metricsGatherer.GatherForUI()
	}

	return c.JSON(resp)
}

// handleUIProxy handles GET /drl/ui/proxy/:nodeAddr/*endpoint.
//
// Acts as an authenticated cross-node proxy: forwards the request to the
// specified peer's API endpoint using the caller's DRL-Session token.
// This allows the SPA to aggregate metrics from all cluster nodes without
// CORS issues or token sharing.
//
// :nodeAddr  — URL-encoded "host:port" of the target node's private API.
// *endpoint  — the API path to forward, e.g. "drl/ui/api/metrics".
func (s *Server) handleUIProxy(c *fiber.Ctx) error {
	rawAddr := c.Params("nodeAddr")
	if rawAddr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "nodeAddr is required",
		})
	}

	// URL-decode the node address.
	nodeAddr, err := url.PathUnescape(rawAddr)
	if err != nil || nodeAddr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid nodeAddr",
		})
	}

	endpoint := c.Params("*")
	if endpoint == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "endpoint path is required",
		})
	}

	// Validate endpoint — allow only paths under known safe prefixes.
	if !strings.HasPrefix(endpoint, "drl/ui/api/") &&
		!strings.HasPrefix(endpoint, "status") &&
		!strings.HasPrefix(endpoint, "accounting/") {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "endpoint not proxiable",
		})
	}

	// Prevent proxying to non-cluster addresses (basic SSRF guard).
	if strings.Contains(nodeAddr, "..") || strings.Contains(nodeAddr, "//") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid nodeAddr",
		})
	}

	target := fmt.Sprintf("http://%s/%s", nodeAddr, endpoint)

	// Build the outbound request with the caller's session token.
	outReq, err := http.NewRequestWithContext(c.Context(), http.MethodGet, target, nil)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "building proxy request: " + err.Error(),
		})
	}
	outReq.Header.Set("Authorization", c.Get("Authorization"))

	client := &http.Client{Timeout: 5 * time.Second}
	peerResp, err := client.Do(outReq)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "peer unreachable: " + err.Error(),
		})
	}
	defer func() { _ = peerResp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(peerResp.Body, 1<<20)) // 1 MiB max
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "reading peer response: " + err.Error(),
		})
	}

	c.Set("Content-Type", peerResp.Header.Get("Content-Type"))
	return c.Status(peerResp.StatusCode).Send(body)
}
