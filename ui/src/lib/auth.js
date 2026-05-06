/**
 * auth.js — Svelte store encapsulating the ECDH P-256 key exchange and session
 * management for the DRL UI.
 *
 * Flow (v2 — out-of-band token):
 *  1. Read non-sensitive bootstrap data from <meta name="drl-bootstrap"> (serverPublicKey,
 *     clusterName, nodeId — no bootstrap token is embedded in the HTML).
 *  2. If no bootstrap token is in memory, set authStatus to 'awaiting_token' so the SPA
 *     renders the token modal.
 *  3. Operator retrieves the bootstrap token out-of-band:
 *       curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/ui/get-token
 *  4. User pastes the token into the modal; setBootstrapToken() stores it in session memory
 *     and resumes the handshake.
 *  5. Generate ephemeral ECDH P-256 key pair in the browser.
 *  6. POST /v1/ui/exchange with clientPublicKey + bootstrapToken.
 *  7. Derive shared secret; decrypt the AES-256-GCM encrypted session token.
 *  8. Store session token in module scope (never localStorage); provide apiFetch() wrapper.
 */

import { writable, get } from 'svelte/store';

// ── Public stores ────────────────────────────────────────────────────────────
/** 'loading' | 'awaiting_token' | 'authenticating' | 'ready' | 'error' */
export const authStatus = writable('loading');
export const authError = writable(null);
export const bootstrapInfo = writable(null);

// Session token stored in module scope (not a store — not needed in templates).
let _sessionToken = null;

// AES-256-GCM CryptoKey derived from the ECDH handshake.
// Used to transparently decrypt every API response body.
// Never written to localStorage; cleared when the session expires.
let _aesKey = null;

// Bootstrap token stored in session memory only (cleared on page reload).
let _bootstrapToken = null;

// ── Helpers ──────────────────────────────────────────────────────────────────
function b64ToBuffer(b64) {
  const bin = atob(b64);
  const buf = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
  return buf.buffer;
}

function getBootstrapData() {
  const meta = document.querySelector('meta[name="drl-bootstrap"]');
  if (!meta) throw new Error('Missing <meta name="drl-bootstrap"> — ensure the Go server injected it');
  return JSON.parse(meta.getAttribute('content'));
}

// ── setBootstrapToken ────────────────────────────────────────────────────────
/**
 * Store the bootstrap token provided by the user and proceed with the ECDH
 * handshake. Called by the token modal in App.svelte.
 *
 * @param {string} token  The bootstrap token obtained from GET /v1/ui/get-token
 */
export async function setBootstrapToken(token) {
  _bootstrapToken = token;
  await authenticate();
}

// ── authenticate ─────────────────────────────────────────────────────────────
/**
 * Initiate or resume authentication.
 *
 * If no bootstrap token is in memory the function sets authStatus to
 * 'awaiting_token' and returns — the SPA will render the token modal.
 * Once the user supplies the token via setBootstrapToken(), this function
 * is called again and proceeds with the ECDH handshake.
 */
export async function authenticate() {
  authStatus.set('loading');
  authError.set(null);
  _sessionToken = null;

  try {
    const bootstrap = getBootstrapData();
    bootstrapInfo.set(bootstrap);

    if (!_bootstrapToken) {
      authStatus.set('awaiting_token');
      return;
    }

    authStatus.set('authenticating');
    _aesKey = null;

    // Step 1 — generate ephemeral ECDH P-256 key pair
    const keyPair = await crypto.subtle.generateKey(
      { name: 'ECDH', namedCurve: 'P-256' },
      true,
      ['deriveBits']
    );
    const clientPubRaw = await crypto.subtle.exportKey('raw', keyPair.publicKey);
    const clientPubB64 = btoa(String.fromCharCode(...new Uint8Array(clientPubRaw)));

    // Step 2 — POST /v1/ui/exchange
    const exchResp = await fetch('/v1/ui/exchange', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        clientPublicKey: clientPubB64,
        bootstrapToken: _bootstrapToken,
      }),
    });

    // Clear bootstrap token from memory regardless of outcome.
    _bootstrapToken = null;

    if (!exchResp.ok) {
      const txt = await exchResp.text().catch(() => '');
      throw new Error(`Key exchange failed (${exchResp.status}): ${txt}`);
    }
    const exch = await exchResp.json();

    // Step 3 — derive shared secret
    const serverPub = await crypto.subtle.importKey(
      'raw',
      b64ToBuffer(exch.serverPublicKey),
      { name: 'ECDH', namedCurve: 'P-256' },
      false,
      []
    );
    const sharedBits = await crypto.subtle.deriveBits(
      { name: 'ECDH', public: serverPub },
      keyPair.privateKey,
      256
    );
    const aesKey = await crypto.subtle.importKey(
      'raw',
      sharedBits,
      { name: 'AES-GCM', length: 256 },
      false,
      ['decrypt']
    );

    // Step 4 — decrypt session token
    const iv = b64ToBuffer(exch.iv);
    const ct = b64ToBuffer(exch.encryptedSession);
    const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, aesKey, ct);
    _sessionToken = new TextDecoder().decode(plain);

    // Store the AES key for E2EE decryption of all subsequent API responses.
    // The server encrypts every 2xx response body with this same key.
    _aesKey = aesKey;

    authStatus.set('ready');
  } catch (err) {
    _bootstrapToken = null;
    _aesKey = null;
    authError.set(err.message || String(err));
    authStatus.set('error');
  }
}

// ── apiFetch ─────────────────────────────────────────────────────────────────
/**
 * Authenticated fetch wrapper. Adds Authorization: DRL-Session <token>.
 * On 401, clears the session and transitions back to 'awaiting_token' so the
 * user is prompted for a new bootstrap token.
 *
 * @param {string} url
 * @param {RequestInit} [options]
 * @returns {Promise<any>} parsed JSON response
 */
export async function apiFetch(url, options = {}, _retry = true) {
  if (!_sessionToken) throw new Error('Not authenticated — call authenticate() first');

  const headers = {
    'Content-Type': 'application/json',
    Authorization: `DRL-Session ${_sessionToken}`,
    ...(options.headers ?? {}),
  };

  const resp = await fetch(url, { ...options, headers });
  if (resp.status === 401 && _retry) {
    // Session expired; reset to awaiting_token so the user can supply a fresh bootstrap token.
    _sessionToken = null;
    _aesKey = null;
    authStatus.set('awaiting_token');
    throw new Error('Session expired — please enter a new access token');
  }
  if (!resp.ok) {
    const txt = await resp.text().catch(() => resp.statusText);
    throw new Error(`API ${resp.status}: ${txt}`);
  }

  const json = await resp.json();

  // Transparent E2EE decryption: the server wraps every 2xx response as
  // {"iv":"<base64>","data":"<base64>"} when a DRL-Session is active.
  // Decrypt here so all components always receive plain JSON objects.
  if (_aesKey && json && typeof json.iv === 'string' && typeof json.data === 'string') {
    const ivBuf = b64ToBuffer(json.iv);
    const ctBuf = b64ToBuffer(json.data);
    const plainBuf = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: ivBuf }, _aesKey, ctBuf);
    return JSON.parse(new TextDecoder().decode(plainBuf));
  }

  return json;
}
