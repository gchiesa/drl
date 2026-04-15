/**
 * auth.js — Svelte store encapsulating the ECDH P-256 key exchange and session
 * management for the DRL UI.
 *
 * Flow:
 *  1. Read bootstrap data from <meta name="drl-bootstrap"> injected by Go server.
 *  2. Generate ephemeral ECDH P-256 key pair in the browser.
 *  3. POST /drl/ui/exchange with clientPublicKey + bootstrapToken.
 *  4. Derive shared secret; decrypt the AES-256-GCM encrypted session token.
 *  5. Store session token; provide apiFetch() wrapper that adds Authorization header.
 */

import { writable, get } from 'svelte/store';

// ── Public stores ────────────────────────────────────────────────────────────
/** 'loading' | 'authenticating' | 'ready' | 'error' */
export const authStatus = writable('loading');
export const authError = writable(null);
export const bootstrapInfo = writable(null);

// Session token stored in module scope (not a store — not needed in templates).
let _sessionToken = null;

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

// ── authenticate ─────────────────────────────────────────────────────────────
/**
 * Perform the ECDH handshake with the server. Called once on page load and
 * again on refresh (each page load generates a fresh key pair and bootstrap
 * token is re-embedded by the server).
 */
export async function authenticate() {
  authStatus.set('authenticating');
  authError.set(null);
  _sessionToken = null;

  try {
    const bootstrap = getBootstrapData();
    bootstrapInfo.set(bootstrap);

    // Step 1 — generate ephemeral ECDH P-256 key pair
    const keyPair = await crypto.subtle.generateKey(
      { name: 'ECDH', namedCurve: 'P-256' },
      true,
      ['deriveBits']
    );
    const clientPubRaw = await crypto.subtle.exportKey('raw', keyPair.publicKey);
    const clientPubB64 = btoa(String.fromCharCode(...new Uint8Array(clientPubRaw)));

    // Step 2 — POST /drl/ui/exchange
    const exchResp = await fetch('/drl/ui/exchange', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        clientPublicKey: clientPubB64,
        bootstrapToken: bootstrap.bootstrapToken,
      }),
    });
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

    authStatus.set('ready');
  } catch (err) {
    authError.set(err.message || String(err));
    authStatus.set('error');
  }
}

// ── apiFetch ─────────────────────────────────────────────────────────────────
/**
 * Authenticated fetch wrapper. Adds Authorization: DRL-Session <token>.
 * On 401, attempts one re-authentication before propagating the error.
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
    // Session expired; re-authenticate once and retry.
    await authenticate();
    return apiFetch(url, options, false);
  }
  if (!resp.ok) {
    const txt = await resp.text().catch(() => resp.statusText);
    throw new Error(`API ${resp.status}: ${txt}`);
  }
  return resp.json();
}
