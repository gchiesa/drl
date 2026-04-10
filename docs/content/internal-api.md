---
title: Internal HTTP API
description: Management endpoints, digest authentication, and usage examples for DRL's internal HTTP API.
weight: 7
---

DRL exposes an internal HTTP API on port `8082` (configurable) for cluster management, observability, and
manual blocklist operations. All endpoints are protected by **RFC 7616 HTTP Digest Authentication with SHA-256**.

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

### How it works

The API uses HTTP Digest Authentication (RFC 7616) with SHA-256. The challenge-response mechanism **never
transmits the password over the wire**:

{{< mermaid >}}
sequenceDiagram
    participant C as Client
    participant S as DRL Internal API

    C->>S: GET /status
    S-->>C: 401 WWW-Authenticate: Digest realm="DRL Internal API",\nnonce="abc123", algorithm=SHA-256, qop="auth"
    Note over C: Compute:\nA1 = SHA256(username:realm:password)\nA2 = SHA256(method:uri)\nresponse = SHA256(A1:nonce:nc:cnonce:qop:A2)
    C->>S: GET /status Authorization: Digest username="...", response="..."
    S-->>C: 200 OK
{{< /mermaid >}}

### Using curl (recommended)

```bash
# curl handles the full challenge-response automatically
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/status

# With an explicit username
curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/status
```

### Security notes

- The internal API should be bound to `localhost` or protected by mTLS/VPN in production
- DRL **never stores the raw API key** — only the A1 hash (`SHA256(username:realm:password)`) is kept in memory
- Each nonce can only be used once and expires after 5 minutes (replay protection)
- Authentication errors do not reveal whether a username exists

## Endpoints

### GET /status

Returns cluster membership information for this node.

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/status
```

**Response:**

```json
{
  "cluster_name": "drl",
  "node_id": "drl-1",
  "active_peers": ["drl-1", "drl-2", "drl-3"],
  "uptime": "2h30m15s",
  "uptime_seconds": 9015.5
}
```

---

### GET /accounting/stats

Returns local accounting statistics for this node.

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/accounting/stats
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

### GET /blocked-entity

Returns all entities currently held in the local blocklist cache.

```bash
curl --silent --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/blocked-entity | jq
```

---

### POST /blocked-entity/:ip/_path/*

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
  "http://localhost:8082/blocked-entity/192.168.1.10/_path/api/v1/payments"

# Block by IP + path + headers
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X POST \
  "http://localhost:8082/blocked-entity/192.168.1.10/_path/api/v1/payments/_headers/User-Agent:ScraperBot"

# Block with custom TTL (1 hour)
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X POST \
  "http://localhost:8082/blocked-entity/192.168.1.10/_path/api/v1/payments?ttl=3600"
```

---

### DELETE /blocked-entity/:ip/_path/*

Removes the matching entity from the blocklist and gossips an `UnblockEvent` to all peers.

```bash
curl --digest -u ":$DRL_PRIVATE_API_KEY" -X DELETE \
  "http://localhost:8082/blocked-entity/192.168.1.10/_path/api/v1/payments"
```

---

### POST /accounting/load

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
  "http://localhost:8082/accounting/load?distributionEnabled=true"
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
    uri      := "/status"
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

    req, _ := http.NewRequest("GET", "http://localhost:8082/status", nil)
    req.Header.Set("Authorization", auth)
    // ... execute request
}
```
