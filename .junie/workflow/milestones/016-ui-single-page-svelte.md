# 016-ui-single-page-svelte.md

## Goal

Implement a high-performance, Single Page Application (SPA) built with Svelte to provide a real-time "Control Plane"
dashboard. The UI will aggregate data from the cluster nodes via the Private API, featuring a secure, zero-trust
communication layer using ECDH for session encryption.

## Requirements

### 1. Svelte SPA & Embedded Assets

* **Framework**: Use Svelte (or SvelteKit in SPA mode) for a lightweight footprint.
* **Embedding**: The frontend assets (HTML, JS, CSS) must be embedded into the Go binary using `embed.FS`.
* **Endpoint**: Serve the SPA at `GET /drl/ui/` via the Private API port (`8082`).
* **Visuals**:
    * **Cluster Overview**: List all nodes (retrieved via `membership` gossip state).
    * **Configuration**: Display the active ruleset and algorithm (Token Bucket/Sliding Window) using the
      `/configuration/static` endpoint.
    * **Metrics Dashboard**: Real-time charts for `requests_total`, `blocked_total`, and `tokens_consumed` (polling or
      WebSockets).

### 2. Hybrid Authentication Middleware

* **Dual-Auth Strategy**:
    * **CLI/Bot**: Continue supporting **Digest Access Authentication** (as per Milestone 004v2).
    * **Browser (SPA)**: Implement a **Session-based ECDH** flow.
* **The "Bake-in" Mechanism**:
    * When the user first accesses `/drl/ui/`, the Go backend generates a short-lived **Bootstrap Token** valid for 60
      seconds and embeds it in a `<meta>` tag or a global JS variable.
    * The SPA uses this token to initiate the ECDH Key Exchange.

### 3. Key Workflow for E2EE (Diffie-Hellman)

To ensure end-to-end security between the browser and the Private API without a central CA:

1. **Key Generation**: Upon loading, the SPA generates an ephemeral ECDH P-256 key pair using the **Web Crypto API**.
2. **Handshake**: The SPA calls `POST /api/auth/handshake` with its Public Key and the **Bootstrap Token**.
3. **Derivation**: The Go backend responds with its own Public Key. Both sides derive a shared secret using their
   private key and the peer's public key.
4. **Encryption**: Subsequent data calls (e.g., `/accounting/stats`) are encrypted using **AES-GCM** with the derived
   secret.
5. **Rotation**: The shared secret is held in memory (SPA) and invalidated on the server after 1 hour or on node
   restart.

### 4. Cross-Node Aggregation (Proxy Mode)

* Because the SPA is served from one node but needs data from the entire cluster:
* Implement a **Internal Proxy** in the Private API.
* **Endpoint**: `GET /api/proxy/:node_id/:endpoint`.
* The node receiving the SPA request acts as a coordinator, fetching metrics from peer nodes and returning an aggregated
  JSON to the UI.

### 5. Private API Updates

* **Middleware**: Update `internal/api/middleware.go` to detect the `Authorization` header.
    * If `Digest ...`, use existing logic.
    * If `Bearer <ECDH_SESSION_ID>`, verify the AES-GCM signature.
* **Metrics**: Add `drl_ui_sessions_active` gauge.

## Technical Considerations

* **Storage**: Store the ECDH session keys in a thread-safe map in the `internal/api` package with a TTL-based cleanup (
  similar to Otter patterns used in accounting).
* **Bundle Size**: Keep the SPA under 500KB (gzipped) to ensure the Go binary remains lean.
* **Browser Security**: Set `Content-Security-Policy` headers to only allow connections to the DRL cluster nodes.

## Validation Criteria

1. **Zero-Touch Access**: Opening `http://localhost:8082/drl/ui/` in a browser successfully loads the dashboard without
   manual password entry (using the baked-in bootstrap).
2. **Security**: Verify via Browser DevTools that API responses are encrypted (AES-GCM) and unreadable as plain JSON.
3. **Interoperability**: Verify that `curl --digest` still works for existing endpoints while the UI is active.
4. **Resilience**: Ensure that refreshing the page regenerates a new ECDH session and invalidates the old one.

