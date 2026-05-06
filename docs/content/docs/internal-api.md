---
title: Internal HTTP API
description: Versioned management endpoints (v1), digest authentication, ECDH session auth, OpenAPI docs, and usage examples for DRL's internal HTTP API.
weight: 7
---

DRL exposes an internal HTTP API on port `8082` (configurable) for cluster management, observability, and
manual blocklist operations. All management endpoints are protected by one of:

- **HTTP Digest Authentication (RFC 7616 SHA-256)** — for CLI / curl access
- **Bearer Token (ECDH session)** — for the browser SPA encrypted communication

## OpenAPI Documentation

Interactive Swagger UI and the raw OpenAPI spec are available without authentication:

| URL | Description |
|-----|-------------|
| `http://localhost:8082/v1/apidocs/` | Swagger UI — browse and try all endpoints interactively |
| `http://localhost:8082/v1/swagger.json` | Raw OpenAPI 3.0 JSON specification |

## Configuration

```kdl
internal-api {
    enabled true       // Enable the internal API (default: true)
    address ":8082"    // Bind address (default: :8082)
}
```

| Environment variable | Default | Description |
|---------------------|---------|-------------|
| `DRL_INTERNAL_API_ENABLED` | `true` | Enable/disable the API |
| `DRL_INTERNAL_API_ADDRESS` | `:8082` | Bind address |
| `DRL_PRIVATE_API_KEY` | — | API key (required, min 16 chars) |

## Authentication

### Digest Auth (CLI / curl)

All management endpoints require HTTP Digest Authentication (RFC 7616) with SHA-256. The challenge-response
mechanism **never transmits the password over the wire**:

{{< mermaid >}}
sequenceDiagram
    participant C as Client
    participant S as DRL Internal API

    C->>S: GET /v1/status
    S-->>C: 401 WWW-Authenticate: Digest realm="DRL Internal API",\nnonce="abc123", algorithm=SHA-256, qop="auth"
    Note over C: Compute:\nA1 = SHA256(username:realm:password)\nA2 = SHA256(method:uri)\nresponse = SHA256(A1:nonce:nc:cnonce:qop:A2)
    C->>S: GET /v1/status Authorization: Digest username="...", response="..."
    S-->>C: 200 OK
{{< /mermaid >}}

```bash
# curl handles the full challenge-response automatically
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/status

# With an explicit username
curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/status
```

### Bearer Token / ECDH Session (Browser SPA)

The browser SPA uses an ECDH P-256 key exchange to establish an end-to-end encrypted session:

1. **Get bootstrap token** (Digest auth required — out-of-band):
   ```bash
   curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/ui/get-token
   ```
2. **Exchange keys** — POST your ephemeral ECDH public key + bootstrap token to `/v1/ui/exchange`.
3. **Derive shared secret** — decrypt the AES-256-GCM encrypted session token from the response.
4. **Authenticate** — send `Authorization: DRL-Session <token>` on all subsequent requests.
   Responses are AES-256-GCM encrypted with the ECDH-derived key.

### Security notes

- The internal API should be bound to `localhost` or protected by mTLS/VPN in production
- DRL **never stores the raw API key** — only the A1 hash (`SHA256(username:realm:password)`) is kept in memory
- Each nonce can only be used once and expires after 5 minutes (replay protection)
- Authentication errors do not reveal whether a username exists

## Endpoints

All management endpoints are mounted under the `/v1` prefix.

### GET /v1/status

Returns cluster membership information for this node.

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/status
```

**Response:**

```json
{
  "cluster_name": "drl",
  "node_id": "drl-1",
  "active_peers": ["drl-1", "drl-2", "drl-3"],
  "active_peer_addresses": ["10.0.0.2:8082", "10.0.0.3:8082"],
  "uptime": "2h30m15s",
  "uptime_seconds": 9015.5
}
```

---

### GET /v1/accounting/stats

Returns local accounting statistics for this node.

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/accounting/stats
```

**Response:**

```json
{
  "local_node_id": "drl-1",
  "monitored_entities_count": 1234,
  "batched_updates_pending": 17,
  "estimated_entities_count": 1200
}
```

---

### GET /v1/blocked-entity

Returns all entities currently held in the local blocklist cache.

```bash
curl --silent --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/blocked-entity | jq
```

---

### POST /v1/blocked-entity/:ip/_path/*

Adds an entity to the blocklist on this node and gossips a `BlockEvent` to all peers.

**Path parameters:**

- `:ip` — source IP address of the entity
- `_path/*` — URI path; append `/_headers/<key:val,key2:val2>` to scope to a specific header tuple

**Query parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `ttl` | `cache.blocklist-default-ttl-seconds` | TTL in seconds for this block |

**Examples:**

```bash
# Block by IP + path
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X POST \
  "http://localhost:8082/v1/blocked-entity/192.168.1.10/_path/api/v1/payments"

# Block by IP + path + headers
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X POST \
  "http://localhost:8082/v1/blocked-entity/192.168.1.10/_path/api/v1/payments/_headers/User-Agent:ScraperBot"

# Block with custom TTL (1 hour)
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X POST \
  "http://localhost:8082/v1/blocked-entity/192.168.1.10/_path/api/v1/payments?ttl=3600"
```

---

### DELETE /v1/blocked-entity/:ip/_path/*

Removes the matching entity from the blocklist and gossips an `UnblockEvent` to all peers.

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X DELETE \
  "http://localhost:8082/v1/blocked-entity/192.168.1.10/_path/api/v1/payments"
```

---

### POST /v1/accounting/load

Bulk-ingests entities into the accounting cache **without rate-limit evaluation**. This endpoint is intended
for load-testing the cache and warming nodes; entities loaded this way will not be blocked even if their counts
exceed the configured rule limit.

**Request:**

- Content-Type: `application/x-ndjson`
- Body: one JSON object per line

```json
{"sourceIP": "10.0.0.1", "path": "/api/v1/users", "headers": {"X-API-Key": "abc"}}
{"sourceIP": "10.0.0.2", "path": "/api/v1/orders"}
```

`sourceIP` and `path` are required; `headers` is optional. Blank lines are skipped; malformed lines are counted as `invalid` but processing continues.

**Query parameters:**

| Parameter | Default | Description |
|-----------|---------|-------------|
| `distributionEnabled` | `false` | When `true`, entities owned by remote nodes are forwarded via the Flusher; when `false`, they are dropped |

**Response:**

```json
{
  "id": "1712534400000000000",
  "total": 1000,
  "accepted_local": 920,
  "accepted_remote": 60,
  "dropped": 0,
  "no_match": 15,
  "invalid": 5,
  "errors": ["line 7: unexpected end of JSON input"]
}
```

**Example:**

```bash
cat <<'EOF' > /tmp/load.ndjson
{"sourceIP":"10.0.0.1","path":"/api/v1/users"}
{"sourceIP":"10.0.0.2","path":"/api/v1/users"}
{"sourceIP":"10.0.0.3","path":"/api/v1/orders","headers":{"X-API-Key":"abc"}}
EOF

curl --digest -u ":$DRL_PRIVATE_API_KEY" \
  -X POST \
  -H "Content-Type: application/x-ndjson" \
  --data-binary @/tmp/load.ndjson \
  "http://localhost:8082/v1/accounting/load?distributionEnabled=true"
```

---

### GET /v1/configuration/static/:section

Returns the JSON representation of a named configuration section.
Sensitive fields (encryption keys) are redacted.

Valid sections: `accounting`, `membership`, `cache`, `listen`, `logging`, `internal-api`

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/configuration/static/accounting
```

---

### GET /v1/ui/get-token

Issues a short-lived (10-minute) bootstrap token for initiating the ECDH key exchange.
**Requires Digest authentication.**

```bash
curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/v1/ui/get-token
```

**Response:**

```json
{
  "bootstrap_token": "eyJ..."
}
```

## Error Responses

All endpoints return a standardized error body on failure:

```json
{
  "error": "Short description",
  "code": 400,
  "details": "Technical details for debugging"
}
```

## Programmatic authentication (Go example)

```go
package main

import (
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "net/http"
)

func sha256Hash(s string) string {
    h := sha256.Sum256([]byte(s))
    return hex.EncodeToString(h[:])
}

func main() {
    realm    := "DRL Internal API"
    nonce    := "server-nonce-here"  // from WWW-Authenticate header
    uri      := "/v1/status"
    method   := "GET"
    username := "admin"
    password := "your-api-key"
    cnonce   := "client-nonce"
    nc       := "00000001"
    qop      := "auth"

    a1       := sha256Hash(fmt.Sprintf("%s:%s:%s", username, realm, password))
    a2       := sha256Hash(fmt.Sprintf("%s:%s", method, uri))
    response := sha256Hash(fmt.Sprintf("%s:%s:%s:%s:%s:%s", a1, nonce, nc, cnonce, qop, a2))

    auth := fmt.Sprintf(
        `Digest username="%s", realm="%s", nonce="%s", uri="%s", `+
        `algorithm=SHA-256, qop=%s, nc=%s, cnonce="%s", response="%s"`,
        username, realm, nonce, uri, qop, nc, cnonce, response,
    )

    req, _ := http.NewRequest("GET", "http://localhost:8082/v1/status", nil)
    req.Header.Set("Authorization", auth)
    // ... execute request
}
```
