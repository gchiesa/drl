---
title: Embedded Proxy
weight: 55
---

# Embedded Proxy

DRL includes an optional embedded reverse proxy that lets you run DRL as a complete edge gateway without a separate
Envoy sidecar. When enabled, DRL listens on a configurable port, enforces rate-limit rules, and forwards requests to one
or more upstream services.

## Architecture

```
                     ┌──────────────────────────────────────────┐
  client request ──► │  DRL :8443  (embedded proxy)             │
                     │    │                                      │
                     │    ├─ OIDC Bearer-token verification      │
                     │    ├─ Rate-limit accounting               │
                     │    └─► upstream service                   │
                     └──────────────────────────────────────────┘
```

DRL acts exclusively as an **OAuth 2.0 Resource Server (RS)**: it validates tokens presented by clients but never issues
them. Token issuance remains the responsibility of your IdP.

---

## Basic Configuration

```kdl
embedded-proxy {
    enabled true
    listen ":8080"

    tls {
        enabled false
        cert ""   // base64-encoded PEM; or env DRL_EMBEDDED_PROXY_TLS_CERT
        key ""   // base64-encoded PEM; or env DRL_EMBEDDED_PROXY_TLS_KEY
    }

    host "api.example.com" {
        routes {
            route "/" {
                upstream "http://backend:8080"
                balance-strategy "dns-round-robin"
                dns-refresh-interval "5s"
                require-auth false

                // Connection pool (all optional — sensible defaults apply)
                // max-idle-conns-per-host 32
                // max-conns-per-host      0      // 0 = unlimited
                // idle-conn-timeout       "90s"
                // response-header-timeout "30s"
                // dial-timeout            "30s"
            }
        }
    }
}
```

---

## Connection Pool

Each route maintains its own upstream HTTP connection pool. This provides two guarantees:

- **Isolation** — a slow or saturated upstream cannot exhaust the pool shared with other routes or
  with DRL's internal HTTP clients (Memberlist gossip, OIDC JWKS fetching).
- **Correct sizing** — Go's `http.DefaultTransport` caps idle connections per host at **2**, which
  causes constant TCP churn under proxy workloads. DRL defaults to **32** idle connections per host.

### Pool settings reference

| KDL key | Default | Description |
|---------|---------|-------------|
| `max-idle-conns-per-host` | `32` | Idle keep-alive connections held per upstream host |
| `max-conns-per-host` | `0` (unlimited) | Hard cap on total connections per host (dialing + active + idle). Set this to protect upstream services from overload. |
| `idle-conn-timeout` | `90s` | How long an idle connection is kept before being closed |
| `response-header-timeout` | `30s` | Maximum wait for an upstream to send response headers after the request body is written. Prevents slow upstreams from holding goroutines indefinitely. |
| `dial-timeout` | `30s` | Maximum time allowed to establish a new TCP connection |

### Tuning example

High-throughput route with a bounded connection cap to protect the upstream:

```kdl
route "/api" {
    upstream "http://api-service:8080"
    require-auth true
    scopes "read"

    max-idle-conns-per-host 64
    max-conns-per-host      200
    idle-conn-timeout       "60s"
    response-header-timeout "10s"
    dial-timeout            "5s"
}
```

### TLS

Set `tls.enabled true` and supply base64-encoded PEM material in `tls.cert` / `tls.key`
(or the corresponding environment variables). Certificate material is never written to disk.

```bash
# Encode an existing cert/key:
DRL_EMBEDDED_PROXY_TLS_CERT=$(base64 -w0 server.crt)
DRL_EMBEDDED_PROXY_TLS_KEY=$(base64 -w0 server.key)
```

---

## OIDC JWT Verification

The `oidc {}` block is declared at the **host** level because identity realms typically govern a specific virtual-host
footprint. Individual routes enable enforcement via `require-auth true` and optionally restrict access to specific OAuth
2.0 scopes.

### Full Schema

```kdl
host "api.example.com" {
    oidc {
    // Required: OIDC discovery endpoint (must expose /.well-known/openid-configuration)
        issuer "https://auth.example.com/realms/myapp"

    // Optional: validated against the "client_id" / "azp" claim
        client-id "drl-gateway"

    // Optional: validated against the token "aud" claim (required by Auth0 and Entra ID)
        audience "https://api.example.com"

    // Optional: override non-standard claim field names
        claims {
            scopes "scp"     // Okta / Entra ID use "scp" instead of "scope"
            roles "groups"  // Keycloak uses "groups"; default is "roles"
        }

    // Optional: how long JWKS public keys are cached before re-fetching
        jwks-cache-ttl "10m"
    }

    routes {
        route "/v1/admin" {
            upstream "http://admin-service:8080"
            require-auth true
            scopes "admin"
        }

        route "/v1/data" {
            upstream "http://data-service:8080"
            require-auth true
            scopes "read" "write"
        }

        route "/health" {
            upstream "http://data-service:8080"
            require-auth false
        }
    }
}
```

### Verification Pipeline

For each request landing on a route with `require-auth true`:

| Step | Action                                                                  | On failure         |
|------|-------------------------------------------------------------------------|--------------------|
| 1    | Extract `Authorization: Bearer <token>`                                 | `401 Unauthorized` |
| 2    | Verify RS256/ES256 signature and `exp` claim via JWKS                   | `401 Unauthorized` |
| 3    | Validate `aud` claim (if `audience` is configured)                      | `403 Forbidden`    |
| 4    | Unmarshal claims and extract scopes/roles                               | `401 Unauthorized` |
| 5    | Check all `scopes` listed on the route are present                      | `403 Forbidden`    |
| 6    | Inject `OIDCClaims{Subject, Scopes, Roles}` into `http.Request` context | —                  |
| 7    | Forward to rate-limit accounting → upstream                             | —                  |

### Claims Injection

Verified token identity is available to any in-process middleware via `OIDCClaimsFromContext`:

```go
import "github.com/gchiesa/drl/internal/proxy"

func myHandler(w http.ResponseWriter, r *http.Request) {
claims, ok := proxy.OIDCClaimsFromContext(r.Context())
if ok {
log.Printf("subject: %s, scopes: %v", claims.Subject, claims.Scopes)
}
}
```

### Scope Claim Formats

DRL handles all common scope formats automatically:

| Format                 | Example token value  | Parsed as                  |
|------------------------|----------------------|----------------------------| 
| Space-delimited string | `"read write admin"` | `["read","write","admin"]` |
| Comma-delimited string | `"read,write"`       | `["read","write"]`         |
| JSON array             | `["read","write"]`   | `["read","write"]`         |

---

## Claims Mapping by Identity Provider

| Provider            | `scopes` claim field | `roles` claim field | Notes                                              |
|---------------------|----------------------|---------------------|----------------------------------------------------|
| Keycloak            | `scope` (default)    | `roles` (default)   | realm roles                                        |
| Okta                | `scp`                | `groups`            | configure `claims { scopes "scp" roles "groups" }` |
| Auth0               | `scope` (default)    | custom namespace    | see Auth0 guide below                              |
| Entra ID (Azure AD) | `scp`                | `roles`             | configure `claims { scopes "scp" }`                |

---

## Prometheus Metrics

| Metric                                         | Type      | Labels                   | Description                     |
|------------------------------------------------|-----------|--------------------------|---------------------------------|
| `drl_proxy_oidc_requests_total`                | Counter   | `host`, `path`, `status` | Auth attempts by outcome        |
| `drl_proxy_oidc_verification_duration_seconds` | Histogram | `host`, `path`           | JWT crypto-verification latency |

**Status label values:** `success`, `missing_token`, `token_expired`, `invalid_signature`,
`forbidden_scope`, `invalid_token`

---

## Auth0 Step-by-Step Guide

### 1 — Create an API in Auth0

1. Open your Auth0 dashboard → **Applications → APIs → Create API**.
2. Set a **Name** (e.g. `DRL Gateway`) and an **Identifier** (this becomes the `audience`, e.g.
   `https://api.example.com`).
3. Select **RS256** as the signing algorithm.

### 2 — Add Custom Scopes

In the API settings → **Permissions** tab, add the scopes your routes require
(e.g. `read`, `write`, `admin`).

### 3 — Retrieve the Discovery Endpoint

Your Auth0 tenant exposes the OIDC discovery document at:

```
https://<your-tenant>.auth0.com/.well-known/openid-configuration
```

The `issuer` field in that document is what you configure in DRL (e.g.
`https://your-tenant.auth0.com/`).

### 4 — Configure Custom Token Claims (optional)

Auth0 uses `scope` (space-delimited string) by default. If you use custom claim namespaces via Rules or Actions, map
them in the `claims {}` block accordingly.

### 5 — Write the DRL KDL Configuration

```kdl
embedded-proxy {
    enabled true
    listen ":8443"

    tls {
        enabled true
        cert "BASE64_CERT_HERE"
        key "BASE64_KEY_HERE"
    }

    host "api.example.com" {
        oidc {
            issuer "https://your-tenant.auth0.com/"
            client-id "YOUR_AUTH0_API_CLIENT_ID"
            audience "https://api.example.com"
        // Auth0 uses "scope" by default — no claims override needed
            jwks-cache-ttl "10m"
        }

        routes {
            route "/v1" {
                upstream "http://backend:8080"
                require-auth true
                scopes "read"
            }
            route "/health" {
                upstream "http://backend:8080"
                require-auth false
            }
        }
    }
}
```

### 6 — Test with a Client Credentials Token

```bash
# Obtain a token using the client credentials flow
TOKEN=$(curl -s -X POST \
  "https://your-tenant.auth0.com/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{
    "client_id":     "YOUR_CLIENT_ID",
    "client_secret": "YOUR_CLIENT_SECRET",
    "audience":      "https://api.example.com",
    "grant_type":    "client_credentials"
  }' | jq -r '.access_token')

# Call the protected endpoint
curl -H "Authorization: Bearer $TOKEN" https://api.example.com/v1/resource
```

---

## Non-fatal OIDC Provider Failures

If DRL cannot reach the OIDC discovery endpoint at startup (e.g. the IdP is temporarily unavailable), it logs a warning
and continues without enforcing authentication for that host. Routes with `require-auth true` will be served *
*unauthenticated** until the provider is reachable and DRL is restarted/reloaded.

This is an intentional availability-over-security trade-off consistent with DRL's design philosophy. For hard-fail
behaviour, ensure your IdP is healthy before starting DRL.
