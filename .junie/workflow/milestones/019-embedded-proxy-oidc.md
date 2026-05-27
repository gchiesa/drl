# 018-embedded-proxy-oidc.md

## Goal

Now that the DRL supports the `embedded-proxy` feature, the proxy must provide opinionated, secure, and production-ready
authentication and authorization mechanisms. This transforms DRL into a complete, lightweight edge gateway for simpler
deployments where an external Envoy instance is overkill.

The goal of this milestone is to add **OpenID Connect (OIDC) JWT Token Verification** to the embedded proxy. The proxy
acts strictly as a **Resource Server (RS)**; it intercepts incoming requests, validates bearer tokens using dynamically
discovered JWKS endpoints, verifies configured scope/audience parameters, and pipes authenticated context forward into
DRL's rate-limiting accounting layer.

---

## 1. Enterprise-Ready OIDC Schema & KDL Validation

The `oidc` block is configured at the `host` level because identity boundaries (realms or domains) typically govern a
specific virtual host API footprint. Individual routes underneath can toggle their specific auth visibility and scope
requirements.

### Production-Ready KDL Schema Definition

```kdl
embedded-proxy {
    enabled true
    listen ":8443"

    tls {
        enabled true
        cert "base64-encoded-certificate-here..."
        key "base64-encoded-private-key-here..."
    }

    host "api.example.com" {
    // Expanded OIDC configuration supporting Okta, Auth0, Keycloak, and Entra ID
        oidc {
            issuer "https://auth.example.com/realms/drl"
            client-id "drl-sidecar-proxy"

        // Optional audience validation (required by Auth0 / Entra ID)
            audience "https://api.example.com/v1"

        // Customize claim mappings for deployments that do not use standard names
            claims {
                scopes "scp"       // Default is "scope" or "scopes"
                roles "groups"     // Default is "roles" or "resource_access"
            }

        // Crypto caching configuration for high-performance sidecar operations
            jwks-cache-ttl "10m"
        }

        routes {
            route "/anything" {
                upstream "http://127.0.0.1:8080"
                require-auth true
                scopes "read" "write" // Explicit scope validation
            }

            route "/public" {
                upstream "http://127.0.0.1:8080"
                require-auth false
            }
        }
    }
}

```

### Configuration Semantics:

1. **`audience`:** Validates the `aud` claim in the incoming JWT. Essential for platforms like Auth0 (where APIs require
   specific audience values) and Microsoft Entra ID.
2. **`claims`:** Provides flexibility for token claim structure variations (e.g., Okta and Azure often put scopes inside
   a space-delimited `"scp"` string rather than an array named `"scope"`).
3. **`jwks-cache-ttl`:** Defines how long the OIDC verifier retains public keys in memory before making a downstream
   network call to the `.well-known/openid-configuration` public keyset (JWKS).

---

## 2. Core Architecture & Verification Lifecycle

The OIDC implementation must follow a strictly internal initialization lifecycle that hooks into the existing
`internal/proxy` framework via inline Go-Chi middleware. It should not pollute `cmd/` configuration root definitions.

### Technical Stack

## 2. Technical Stack & Verification Loop

To optimize sidecar performance and minimize memory footprints, the authentication loop uses a single, highly focused
dependency rather than stacking multiple token-parsing utilities:

* **`github.com/coreos/go-oidc/v3/oidc`**: Handles background OIDC discovery mapping, thread-safe key synchronization (
  JWKS caching via `RemoteKeySet`), and cryptographic signature checking.
* **Native Claim Unmarshaling**: Rather than introducing secondary JWT packages, standard/custom claims (such as scope
  verification and audience checking) are extracted directly into target memory structs via the native
  `idToken.Claims()` JSON unmarshal integration layer. This keeps allocation profiles predictable and lightweight.

### The Verification Lifecycle Pipeline:

1. **Discovery:** During proxy initialization or dynamic config reload, a background `context.Context` invokes
   `oidc.NewProvider` using the configured `issuer` endpoint. This fetches and stores the JWKS keyset.
2. **Middleware Interception:** When a request lands on a path flagged with `require-auth true`:

* It checks for an `Authorization: Bearer <token>` header. If missing, it rejects with `401 Unauthorized`.
* It passes the raw token to the host's initialized `oidc.IDTokenVerifier`. If signature verification or token
  expiration (`exp`) validation fails, it aborts with `401 Unauthorized`.


3. **Authorization Evaluation:** If the route specifies an explicit `scopes` array, the middleware extracts the claims
   via `idToken.Claims()`. If the required values do not exist within the token’s scope collection, it drops the
   connection with
   `403 Forbidden`.
4. **Context Enrichment:** The parsed user identifier (`sub`), client application identity, and roles are injected into
   the standard `http.Request` context. This allows DRL's accounting module to rate-limit against the validated token
   identity instead of relying solely on raw source IPs.

---

## 3. Metrics Collection

To monitor authentication health without introducing performance regressions, collect and expose the following
Prometheus metrics on the shared application instrumentation loop:

* `drl_proxy_oidc_requests_total{host, path, status}` (Counter): Tracks general authentication request traffic results (
  `success`, `missing_token`, `invalid_signature`, `token_expired`, `forbidden_scope`).
* `drl_proxy_oidc_verification_duration_seconds` (Histogram): Measures the inner execution latency of the crypto
  validation cycle to monitor caching health.

---

## 4. UI Adjustments (Internal Management App)

1. **OIDC Definitions Grid:** Introduce a clean data summary table inside the Configuration Section labeled **"OIDC
   Authentication Handlers"**. Each row lists the active configurations, keyed by their parent `host` name, displaying
   the `Issuer URL`, `Client ID`, and `Audience`. Assign a uniquely colored badge or tag component next to each host
   identity row.
2. **Proxy Routes Grid Integration:** Modify the existing **"Embedded Proxy Routes"** interface grid by appending an **"
   Authentication"** column.

* Routes with `require-auth false` display a subtle gray `public` badge.
* Routes with `require-auth true` display a badge that matches the specific color tag of its associated configuration in
  the OIDC Handlers grid, explicitly printing the required scopes (e.g., `OIDC: api.example.com [read, write]`).

---

## 5. Documentation Requirements

Add clear, standardized engineering guides inside the documentation paths:

* **Location:** `docs/content/docs/embedded-proxy.md` (or append to the file created in milestone 018).
* **Reference Specifications:** Include clear explanations detailing token claims mapping, architectural setup blocks,
  and metrics listings.
* **Auth0 Step-by-Step How-To:** Provide a concrete recipe demonstrating how to declare an API inside the Auth0
  dashboard, retrieve the `.well-known` configuration, configure custom token claims, and write the matching DRL KDL
  deployment variables.

---

## 6. Verification & Implementation Blueprint

Ensure code structures fall directly under your internal module layout conventions:

```txt
internal/proxy/
internal/proxy/middleware_oidc.go  <-- New token extraction and cryptographic verifier layer
internal/config/                   <-- Extend parsing structures for 'oidc' KDL stanzas

```

### Automation & Testing Metrics:

* **Validation Suites:** Test cases must assert token extraction edge cases, invalid signature failure behaviors,
  expired tokens, audience mismatch exceptions, and token parsing variations (comma-separated vs array-based scope claim
  variants).
* **Execution Validation:** Running `mise run lint` and `mise run test` must complete cleanly with zero errors.
* **Completion Indicator:** On successful execution of all verification stages, create the tracking artifact file at:
  `.junie/workflow/state/018-embedded-proxy-oidc.completed`.
