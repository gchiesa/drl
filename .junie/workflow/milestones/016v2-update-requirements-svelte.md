# 016v2-update-requirements-svelte.md

## Goal

Convert the current single page UI implementation to a Svelte framework for high maintainability.

Implement a high-performance, Single Page Application (SPA) built with Svelte to provide a real-time "Control Plane"
dashboard. The UI will aggregate data from cluster nodes via the Private API, featuring a secure, zero-trust
communication layer using ECDH for session encryption.

## Requirements

### 1. Svelte SPA & Embedded Assets

* **Framework**: Svelte 4/5 (via Vite) configured in **SPA Mode** (no SSR).
* **Single-File Build**: Use `vite-plugin-singlefile` to bundle all CSS and JS into `index.html` for easy embedding.
* **Embedding**: The single `index.html` must be embedded into the Go binary using `embed.FS`.
* **Endpoint**: Serve the SPA at `GET /drl/ui/` via the Private API port (`8082`).

### 2. Svelte Component Decomposition (NEW)

To maintain project health, the page must be split into the following logical units:

* **Lib/Auth (Logic-only)**: A Svelte **Store** (`auth.ts/js`) that encapsulates the Web Crypto API logic. It handles
  the ephemeral key generation, the `POST /drl/ui/handshake`, and provides an `encryptedFetch()` wrapper that
  automatically handles AES-GCM encryption/decryption.
* **Layout/Shell**: The primary container handling the "Bootstrap Token" extraction from the `<meta>` tag and managing
  the global "Loading/Auth" state.
* **View/ClusterOverview**:
    * `NodeCard.svelte`: A reusable component representing a single cluster node (Status, IP, Role).
    * `MetricsGraph.svelte`: A wrapper for Chart.js/LayerCake to visualize `requests_total`.
* **View/Configuration**:
    * `RuleTable.svelte`: Decodes and displays the KDL/JSON ruleset.
    * `SearchFilter.svelte`: Client-side filtering for large rulesets.
* **Shared/UI**: Atomic components for Status Dots, Buttons, and encrypted-state indicators.

### 3. Hybrid Authentication Middleware

* **Dual-Auth Strategy**:
    * **CLI/Bot**: Support **Digest Access Authentication**.
    * **Browser (SPA)**: Implement **Session-based ECDH** flow.
* **The "Bake-in" Mechanism**: On `GET /drl/ui/`, the Go backend generates a short-lived **Bootstrap Token** and injects
  it into the HTML: `<meta name="drl-bootstrap" content="...">`.

### 4. Key Workflow for E2EE (Diffie-Hellman)

1. **Key Generation**: SPA generates ephemeral ECDH P-256 key pair (Web Crypto API).
2. **Handshake**: SPA calls `POST /drl/ui/handshake` with its Public Key + Bootstrap Token.
3. **Derivation**: Both sides derive a shared secret.
4. **Encryption**: Subsequent calls use **AES-GCM (256-bit)**. The Svelte Store should automatically decrypt the
   `Response` body before passing it to components.
5. **Rotation**: Secret is held in Svelte memory; server invalidates after 1 hour or on node restart.

### 5. Cross-Node Aggregation (Proxy Mode)

* **Internal Proxy**: Node receiving the SPA request acts as a coordinator.
* **Endpoint**: `GET /drl/ui/proxy/:node_id/:endpoint`.
* The proxy must forward the encrypted payload or re-encrypt it specifically for the client session.

### 6. Private API Updates

* **Middleware**: Update `internal/api/middleware.go` to support `Bearer <ECDH_SESSION_ID>`.
* **Metrics**: Add `drl_ui_sessions_active` gauge to monitor UI load.

## Technical Considerations

* **State Management**: Use Svelte Stores for real-time metrics to avoid "prop-drilling."
* **Reactivity**: Use Svelte's `$:` (or snippets in Svelte 5) to trigger chart updates automatically when the metrics
  store updates.
* **Bundle Size**: Target < 200KB gzipped. Avoid heavy dependencies; use UMD/CDN for Chart.js if necessary, or
  tree-shake it heavily.
* **Content Security Policy (CSP)**: The server must emit a CSP header allowing `script-src 'self'` and
  `connect-src 'self'`.

## UI important aspects

* **Modern and captivating UI**: UI should be modern and captivating with the graphical widgets visualizing the most
  important metrics.
* **Aggregation of distributed metrics**: metrics coming from accounting api are per each node. The client application,
  given it knows the cluster nodes, should use the Cross-Node Aggregation (Proxy Mode) to request in parallel the
  information from the other nodes, via the proxy endpoint. The resulting response must be aggregated by sum together
  before presenting them in the dashboard. Ensure the dashboard widget explains this is the sum across all nodes of the
  cluster.
* **Blocklist Searchable**: blocklist can grow and should be a searchable table with static height, with filter by
  columns. Use the simplest and smallest implementation of filterable table.
* **Separate visualization for rulesets**: The configuration block should be separated from the ruleset, and the ruleset
  should be presented as a flat table with parameters, one row per each rule.
* **Static select menu with refresh time**: the autorefresh of the page should be 30 sec by default but offers the
  possibility to set also 1, 2, 5, 10, 15
* **Cluster peers sorted**: to prevent that the list of peers change with the refresh, it should be sorted before
  visualization
* **Handover metrics**: the handover metrics should also be reported.
* **Light/Dark Mode**: nice to have, if possible the switch to light dark mode for the UI

## Build process

* **Prebuild the page before packaging**: Being a single-page application, the result should be built before packaging
  it with golang in the embedded fs. Ensure the build process in golang first builds the single page (e.g. with
  appropriate npm build vite-plugin-singlefile), then build the golang binary embedding the generated file.
* **Embed only the generated file**: Do not embed any svelte unnecessary file.

## Validation Criteria

1. **Zero-Touch Access**: `http://localhost:8082/drl/ui/` loads and authenticates without a login prompt.
2. **Encapsulation**: Verify that UI components are modular (e.g., modifying `NodeCard.svelte` does not break
   `auth.js`).
3. **Security**: Verify via DevTools that API responses are encrypted strings, not plain JSON.
4. **Resilience**: Page refresh triggers a clean key-exchange handshake.