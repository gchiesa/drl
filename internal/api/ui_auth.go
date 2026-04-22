package api

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// bootstrapTokenTTL is how long a bootstrap token (embedded in the SPA HTML) is valid.
	bootstrapTokenTTL = 10 * time.Minute
	// sessionTokenTTL is how long a browser session remains valid after key exchange.
	sessionTokenTTL = 60 * time.Minute
)

// sessionInfo holds per-session state.
type sessionInfo struct {
	expiresAt time.Time
	sharedKey []byte // AES-256-GCM key derived from ECDH; used for response encryption
}

// uiAuthManager handles browser-based ECDH authentication for the UI.
//
// Authentication flow:
//  1. Server embeds a short-lived bootstrap token in the served SPA HTML.
//  2. Browser performs ECDH key exchange (POST /drl/ui/exchange), presenting the bootstrap token.
//  3. Server validates the bootstrap token, performs ECDH, and returns an AES-GCM-encrypted
//     session token using the derived shared key.
//  4. Browser decrypts the session token and includes it as
//     "Authorization: DRL-Session <token>" on subsequent API calls.
//  5. Server validates the DRL-Session token (HMAC-SHA256 signed, stored session ID).
type uiAuthManager struct {
	mu sync.RWMutex

	// serverPrivKey is the server's ephemeral ECDH P-256 key pair (generated at startup).
	serverPrivKey *ecdh.PrivateKey

	// bootstrapSigningKey is derived from the API key; shared by all nodes with the
	// same API key so bootstrap tokens cross-validate between peers.
	bootstrapSigningKey []byte

	// sessions maps sessionID -> sessionInfo.
	sessions map[string]sessionInfo
}

// newUIAuthManager creates a UIAuthManager derived from the given API key.
func newUIAuthManager(apiKey string) (*uiAuthManager, error) {
	privKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ECDH key: %w", err)
	}
	return &uiAuthManager{
		serverPrivKey:       privKey,
		bootstrapSigningKey: deriveUISigningKey(apiKey),
		sessions:            make(map[string]sessionInfo),
	}, nil
}

// deriveUISigningKey derives a deterministic HMAC key from the API key.
// All nodes sharing the same API key derive the same signing key, so
// bootstrap tokens issued by one node are valid on all peers.
func deriveUISigningKey(apiKey string) []byte {
	h := hmac.New(sha256.New, []byte(apiKey))
	h.Write([]byte("drl-ui-bootstrap-signer-v1"))
	return h.Sum(nil)
}

// ServerPublicKeyBase64 returns the server's ECDH P-256 public key as standard base64.
// The encoding is the 65-byte uncompressed point (04 || x || y).
func (m *uiAuthManager) ServerPublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(m.serverPrivKey.PublicKey().Bytes())
}

// GenerateBootstrapToken produces a short-lived, HMAC-signed token that is embedded
// in the SPA HTML at serve time.
//
// Token format: base64url(nonce:expiry_unix) "." hex(HMAC-SHA256(payload, signingKey))
func (m *uiAuthManager) GenerateBootstrapToken() string {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	payload := fmt.Sprintf("%s:%d", hex.EncodeToString(nonce), time.Now().Add(bootstrapTokenTTL).Unix())

	mac := hmac.New(sha256.New, m.bootstrapSigningKey)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return base64.URLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

// ValidateBootstrapToken returns true if the token has a valid HMAC and has not expired.
func (m *uiAuthManager) ValidateBootstrapToken(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	payload := string(payloadBytes)

	mac := hmac.New(sha256.New, m.bootstrapSigningKey)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expectedSig)) {
		return false
	}

	// payload = nonce:expiry_unix
	idx := strings.LastIndex(payload, ":")
	if idx < 0 {
		return false
	}
	var expiry int64
	if _, scanErr := fmt.Sscanf(payload[idx+1:], "%d", &expiry); scanErr != nil {
		return false
	}
	return time.Now().Unix() < expiry
}

// DeriveSharedKey performs ECDH with the supplied client public key (65-byte uncompressed
// P-256 point, base64-encoded) and returns the raw 32-byte shared secret.
// The shared secret is identical to what the browser derives via:
//
//	crypto.subtle.deriveBits({name:"ECDH", public: serverPubKey}, clientPrivKey, 256)
func (m *uiAuthManager) DeriveSharedKey(clientPubKeyB64 string) ([]byte, error) {
	clientPubKeyBytes, err := base64.StdEncoding.DecodeString(clientPubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("decoding client public key: %w", err)
	}
	clientPubKey, err := ecdh.P256().NewPublicKey(clientPubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing client public key: %w", err)
	}
	secret, err := m.serverPrivKey.ECDH(clientPubKey)
	if err != nil {
		return nil, fmt.Errorf("ECDH derivation: %w", err)
	}
	return secret, nil
}

// CreateSession generates a new session (stored in memory) with the given shared key,
// and returns the HMAC-signed session token string.
// The sharedKey is stored alongside the session so it can be retrieved later
// for per-session operations.
func (m *uiAuthManager) CreateSession(sharedKey []byte) (string, error) {
	idBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("generating session ID: %w", err)
	}
	sessionID := hex.EncodeToString(idBytes)
	expiresAt := time.Now().Add(sessionTokenTTL)

	m.mu.Lock()
	m.sessions[sessionID] = sessionInfo{
		expiresAt: expiresAt,
		sharedKey: sharedKey,
	}
	m.cleanExpiredSessions()
	m.mu.Unlock()

	// Token payload: sessionID:expiry_unix
	payload := fmt.Sprintf("%s:%d", sessionID, expiresAt.Unix())

	mac := hmac.New(sha256.New, m.bootstrapSigningKey)
	mac.Write([]byte("session-v1:"))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return base64.URLEncoding.EncodeToString([]byte(payload)) + "." + sig, nil
}

// ValidateSession returns true if the token is well-formed, HMAC-valid, and
// has not expired.
//
// Validation is intentionally stateless: the expiry is embedded in the
// HMAC-signed payload, so no in-memory session lookup is required. This
// allows tokens to be validated on any cluster node that shares the same
// API key (and therefore the same bootstrapSigningKey) — which is essential
// for cross-node proxy authentication.
func (m *uiAuthManager) ValidateSession(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payloadBytes, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	payload := string(payloadBytes)

	mac := hmac.New(sha256.New, m.bootstrapSigningKey)
	mac.Write([]byte("session-v1:"))
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expectedSig)) {
		return false
	}

	// payload = sessionID:expiry_unix — parse expiry directly from the signed payload.
	idx := strings.LastIndex(payload, ":")
	if idx < 0 {
		return false
	}
	var expiry int64
	if _, scanErr := fmt.Sscanf(payload[idx+1:], "%d", &expiry); scanErr != nil {
		return false
	}
	return time.Now().Unix() < expiry
}

// GetSessionSharedKey validates the session token and returns the AES-256
// shared key that was stored when the session was created by this node.
//
// A peer node can verify the HMAC (stateless) but cannot return the shared key
// because the session entry lives only on the node that performed the ECDH
// exchange. This is intentional: response encryption only applies to the
// browser ↔ originating-node leg; the originating node re-encrypts peer
// responses before forwarding them to the browser.
//
// Returns (nil, false) when the token is invalid, expired, or not found in the
// local session map.
func (m *uiAuthManager) GetSessionSharedKey(token string) ([]byte, bool) {
	if !m.ValidateSession(token) {
		return nil, false
	}

	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, false
	}
	payloadBytes, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, false
	}
	payload := string(payloadBytes)
	// payload = sessionID:expiry_unix
	idx := strings.LastIndex(payload, ":")
	if idx < 0 {
		return nil, false
	}
	sessionID := payload[:idx]

	m.mu.RLock()
	info, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok || time.Now().After(info.expiresAt) {
		return nil, false
	}
	return info.sharedKey, true
}

// ActiveSessions returns the count of currently active (non-expired) sessions.
func (m *uiAuthManager) ActiveSessions() int {
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, info := range m.sessions {
		if now.Before(info.expiresAt) {
			count++
		}
	}
	return count
}

// EncryptWithSharedKey encrypts plaintext using AES-256-GCM with the provided 32-byte key.
// Returns (base64(nonce), base64(ciphertext+tag), error).
// The IV/nonce is prepended to the result for convenience: base64(nonce || ciphertext+tag).
func encryptWithSharedKey(key, plaintext []byte) (ivB64, ciphertextB64 string, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", "", fmt.Errorf("creating AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("creating GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", "", fmt.Errorf("generating nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(ciphertext),
		nil
}

// cleanExpiredSessions removes expired sessions. Caller must hold m.mu write lock.
func (m *uiAuthManager) cleanExpiredSessions() {
	now := time.Now()
	for id, info := range m.sessions {
		if now.After(info.expiresAt) {
			delete(m.sessions, id)
		}
	}
}
