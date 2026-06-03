---
title: DRL — Distributed Rate Limiter
description: >
  A high-performance, horizontally scalable rate-limiting service. Runs as an Envoy
  ext_authz sidecar or as a standalone reverse proxy with TLS and OIDC JWT validation.
weight: 1
---

DRL is a high-performance, horizontally scalable rate-limiting service that eliminates external-database
round-trips by keeping all enforcement state in-process, distributed via a self-organising peer-to-peer
gossip mesh.

It supports two deployment models:

- **Envoy / gRPC ext_authz** — the primary model; DRL runs as a sidecar and Envoy calls
  `ShouldRateLimit` over `localhost:8081`.
- **Embedded reverse proxy** — DRL itself handles ingress: TLS termination, OIDC JWT validation,
  rate-limiting, and upstream forwarding in a single binary.

---

## How it works

DRL operates in three parallel planes:

| Plane | Mechanism | Latency impact |
|-------|-----------|----------------|
| **Local enforcement** | Fully-replicated in-memory Blocklist (O(1) lookup) | Zero — rejection before the request reaches the upstream |
| **Shadow accounting** | Hashed async counter ownership across the cluster | Zero — increments happen in a background goroutine |
| **State sync** | Warm-bootstrap via Memberlist Push/Pull on startup | One-time startup cost only |

### Request path (Envoy mode)

1. Envoy calls `Check(CheckRequest)` over localhost gRPC.
2. DRL extracts `IP + URI path + configured headers` to form an entity key.
3. Local Blocklist lookup (O(1)): if the key is present → `PERMISSION_DENIED (429)`, done.
4. If not blocked → `OK`, returned to Envoy immediately.
5. Background goroutine forwards a `CounterBatch` UDP packet to the consistent-hash owner for that key.
6. Owner updates its Accounting Cache. When the limit is exceeded the owner adds the entity to its local
   Blocklist and broadcasts a `Block` event via Serf/Memberlist gossip.
7. All nodes receive the event, update their local Blocklists, and the entity is blocked cluster-wide.

### Request path (embedded proxy mode)

1. Client sends an HTTP or HTTPS request to DRL's embedded proxy listener.
2. If TLS is enabled, DRL terminates the connection (cert/key loaded from base64-encoded config or
   environment variables — never written to disk).
3. If the matching route has `require-auth true`, DRL validates the `Authorization: Bearer` token:
   - Fetches JWKS from the OIDC discovery endpoint (cached per `jwks-cache-ttl`).
   - Verifies the RS256/ES256 signature and `exp` claim.
   - Checks the `aud` claim (if `audience` is configured).
   - Enforces that all `scopes` declared on the route are present in the token.
4. DRL checks the Blocklist (same O(1) in-process lookup as Envoy mode).
5. Request is forwarded to the upstream. If `balance-strategy "dns-round-robin"` is set, a background
   DNS watcher maintains a live IP pool and rotates across it per request.
6. Async accounting and gossip proceed identically to Envoy mode.

---

## Entity model

Rate limits are not IP-only. Each tracked entity is a composite key:

```
entity key = hash(source_IP + URI_path + [header_k1=v1, header_k2=v2, ...])
```

Headers included in the key are configured per rule. This allows per-client, per-route, per-API-key
rate limiting with a single rule set.

---

## Cluster internals

| Mechanism | Technology | Purpose |
|-----------|-----------|---------|
| Membership & discovery | Hashicorp Memberlist | Node join/leave, failure detection |
| Block event broadcast | Hashicorp Serf user events | Cluster-wide blocklist convergence |
| Counter ownership | `stathat/consistent` hash ring | Deterministic owner election — no coordination needed |
| Counter forwarding | UDP `CounterBatch` (protobuf) | Low-overhead off-path increment delivery |
| State transfer | Memberlist Push/Pull delegate | Warm-bootstrap on new node startup |
| In-memory store | Otter (high-concurrency cache) | GC-friendly O(1) blocklist and accounting |

All inter-node communication is strictly **off the request path**. A network partition between DRL nodes
degrades accounting accuracy but never delays a rate-limit decision.

---

## GitHub

Code for DRL is hosted on GitHub: https://github.com/gchiesa/drl

The project is currently in alpha state. You can build it yourself or use one of the
[deployment configurations]({{< ref "deployments" >}}) provided in the repository.

---

## Quick start

### Prerequisites

- Go 1.22+
- Docker & Docker Compose (for local stack testing)

### Build

```bash
mise run build
```

### Run

```bash
# API key is required (minimum 16 characters)
export DRL_PRIVATE_API_KEY="your-secure-api-key-here"

# Start with built-in defaults (Envoy gRPC mode)
./bin/drl

# Start with a custom KDL config file
./bin/drl --config config.kdl
```

---

## Minimum viable configuration (Envoy mode)

```kdl
listen {
    grpc ":8081"
    metrics ":9091"
}

membership {
    service-name "drl"
    port 7946
    bind-addr "0.0.0.0"
}

internal-api {
    enabled true
    address ":8082"
}

accounting {
    settings {
        algorithm "sliding-window"
    }
    rules {
        api-v1 {
            path-prefix "/api/v1"
            headers "X-API-Key"
            limit 1000
            per "minute"
        }
    }
}
```

## Minimum viable configuration (embedded proxy mode)

```kdl
embedded-proxy {
    enabled true
    listen ":8443"

    tls {
        enabled true
        cert ""   // base64-encoded PEM; or env DRL_EMBEDDED_PROXY_TLS_CERT
        key ""    // base64-encoded PEM; or env DRL_EMBEDDED_PROXY_TLS_KEY
    }

    host "api.example.com" {
        oidc {
            issuer "https://auth.example.com/realms/myapp"
            audience "https://api.example.com"
        }

        routes {
            route "/v1" {
                upstream "http://backend:8080"
                balance-strategy "dns-round-robin"
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

membership {
    service-name "drl"
    port 7946
    bind-addr "0.0.0.0"
}

accounting {
    settings {
        algorithm "sliding-window"
    }
    rules {
        api-v1 {
            path-prefix "/v1"
            limit 500
            per "minute"
        }
    }
}
```

---

## Further reading

| Topic | Description |
|-------|-------------|
| [Configuration]({{< ref "configuration" >}}) | Complete KDL config reference and all environment variables |
| [Embedded Proxy]({{< ref "embedded-proxy" >}}) | TLS, OIDC JWT validation, virtual hosts, upstream routing, IdP guides |
| [Membership]({{< ref "membership" >}}) | Cluster formation, gossip, warm-bootstrap, and block propagation |
| [Cache]({{< ref "cache" >}}) | In-memory blocklist and accounting cache architecture |
| [Accounting]({{< ref "accounting" >}}) | Shadow accounting model, entity hashing, and batched flush |
| [gRPC API]({{< ref "api" >}}) | Envoy `ratelimit.v3` / `ext_authz.v3` service implementation |
| [Internal HTTP API]({{< ref "internal-api" >}}) | Management endpoints, digest authentication, and examples |
| [Control Plane UI]({{< ref "ui" >}}) | Built-in web dashboard, ECDH session auth, cross-node aggregation |
| [Metrics]({{< ref "metrics" >}}) | Prometheus metrics reference and Grafana panel queries |
| [Sizing Guide]({{< ref "sizing" >}}) | Memory footprint, capacity tables, and deployment recommendations |
| [Deployment Models]({{< ref "deployments" >}}) | Docker Compose, ECS Fargate, Kubernetes sidecar/fleet, and Istio |
