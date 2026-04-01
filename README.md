# DRL - Distributed Rate Limiter

A high-performance, horizontally scalable rate-limiting service designed for Envoy sidecars. DRL eliminates the latency
of external databases by using a Peer-to-Peer (P2P) Hybrid Architecture.

## Features

- **Local Enforcement**: Fully replicated Blocklist for O(1) rejection
- **Shadow Accounting**: Hashed, asynchronous global quota tracking
- **State Sync**: Warm-bootstrapping to prevent "vulnerability windows" during rolling updates
- **Cluster Discovery**: Automatic peer discovery via DNS using Hashicorp Memberlist
- **Digest Authentication (SHA-256)**: Secure internal API with RFC 7616 compliant HTTP Digest authentication

## Quick Start

### Prerequisites

- Go 1.25+
- Docker & Docker Compose (for local testing)

### Building

```bash
mise run build
```

### Running

```bash
# Set the required API key (minimum 16 characters)
export DRL_PRIVATE_API_KEY="your-secure-api-key-here"

# Run with default configuration
./bin/drl

# Run with custom configuration file
./bin/drl --config config.kdl
```

## Configuration

DRL uses KDL configuration files with environment variable overrides. Configuration precedence (highest to lowest):

1. Environment variables (`DRL_*` pattern)
2. KDL configuration file
3. Default values

### Example Configuration (config.kdl)

```kdl
listen {
    grpc ":8081"
    metrics ":9091"
}

membership {
    service-name "drl"
    port 7946
    bind-addr "0.0.0.0"
    startup-delay "3s"
    gossip-interval "50ms"
    gossip-nodes 5
}

logging {
    level "info"
    format "json"
}

internal-api {
    enabled true
    address ":8082"
}
```

### Environment Variables

| Variable                      | Description                                                      | Default   |
|-------------------------------|------------------------------------------------------------------|-----------|
| `DRL_PRIVATE_API_KEY`         | API key for internal API authentication (required, min 16 chars) | -         |
| `DRL_NODE_NAME`               | Unique node identifier                                           | hostname  |
| `DRL_LISTEN_GRPC`             | gRPC server address                                              | `:8081`   |
| `DRL_LISTEN_METRICS`          | Prometheus metrics address                                       | `:9091`   |
| `DRL_MEMBERSHIP_SERVICE_NAME` | DNS name for peer discovery                                      | `drl`     |
| `DRL_MEMBERSHIP_PORT`         | Memberlist gossip port                                           | `7946`    |
| `DRL_MEMBERSHIP_BIND_ADDR`    | Address to bind memberlist                                       | `0.0.0.0` |
| `DRL_INTERNAL_API_ENABLED`    | Enable internal API                                              | `true`    |
| `DRL_INTERNAL_API_ADDRESS`    | Internal API server address                                      | `:8082`   |
| `DRL_LOGGING_LEVEL`           | Log level (debug, info, warn, error)                             | `info`    |
| `DRL_LOGGING_FORMAT`          | Log format (json, text)                                          | `json`    |

## Internal API

DRL exposes an internal API on port 8082 (configurable) protected by HTTP Digest Authentication with SHA-256.

### Endpoints

#### GET /status

Returns cluster status information including node ID, cluster name, active peers, and uptime.

**Response:**

```json
{
  "cluster_name": "drl",
  "node_id": "node-1",
  "active_peers": [
    "node-1",
    "node-2",
    "node-3"
  ],
  "uptime": "2h30m15s",
  "uptime_seconds": 9015.5
}
```

### Digest Authentication (SHA-256)

The internal API uses HTTP Digest Authentication (RFC 7616) with SHA-256 algorithm. This challenge-response mechanism
never transmits the password over the wire, making it suitable for secure API authentication.

#### Quick Testing with curl

The simplest way to test the API is using curl's built-in digest authentication support:

```bash
# Testing the Private API with Digest (empty username, API key as password)
curl --digest -u ":$DRL_PRIVATE_API_KEY" http://localhost:8082/status

# Or with explicit admin username
curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://localhost:8082/status

# Get the blocked entities 
curl --silent --digest -u "admin:$DRL_PRIVATE_API_KEY" drl-drl-1:8082/blocked-entity/ | jq

# Add an entitiy

```

#### Authentication Flow

1. **Client Request**: Client requests a protected resource
2. **Server Challenge**: Server responds with 401 and `WWW-Authenticate` header containing nonce, realm, and algorithm
3. **Client Response**: Client calculates digest response using password and sends `Authorization` header
4. **Server Verification**: Server verifies the digest and grants access

#### Manual Authentication Example

**Step 1: Get Challenge**

```bash
curl -v http://localhost:8082/status
```

Response (401 Unauthorized):

```
WWW-Authenticate: Digest realm="DRL Internal API", nonce="abc123...", algorithm=SHA-256, qop="auth"
```

**Step 2: Send Authenticated Request**

The digest response is calculated as:
- `A1 = SHA256(username:realm:password)`
- `A2 = SHA256(method:uri)`
- `response = SHA256(A1:nonce:nc:cnonce:qop:A2)`

#### Programmatic Example (Go)

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
	// Parse the challenge from WWW-Authenticate header
	realm := "DRL Internal API"
	nonce := "server-nonce-here"
	uri := "/status"
	method := "GET"
	username := "admin"
	password := "your-api-key"
	cnonce := "client-nonce"
	nc := "00000001"
	qop := "auth"

	// Calculate digest
	a1 := sha256Hash(fmt.Sprintf("%s:%s:%s", username, realm, password))
	a2 := sha256Hash(fmt.Sprintf("%s:%s", method, uri))
	response := sha256Hash(fmt.Sprintf("%s:%s:%s:%s:%s:%s", a1, nonce, nc, cnonce, qop, a2))

	// Build Authorization header
	auth := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=SHA-256, qop=%s, nc=%s, cnonce="%s", response="%s"`,
		username, realm, nonce, uri, qop, nc, cnonce, response)

	req, _ := http.NewRequest("GET", "http://localhost:8082/status", nil)
	req.Header.Set("Authorization", auth)
	// ... execute request
}
```

### Security Notes

- **Production Deployment**: The internal API should be bound to `localhost` or protected by mTLS/VPN
- **API Key Requirements**: Must be at least 16 characters
- **Credential Storage**: DRL never stores the raw API key. Only the A1 hash (`SHA256(username:realm:password)`) is kept in memory
- **Replay Protection**: Each nonce can only be used once and expires after 5 minutes
- **Error Messages**: Authentication errors do not reveal whether the username exists or if credentials are incorrect

## Docker Deployment

### Docker Compose

```bash
# Start the full stack (DRL cluster + Envoy + workload)
docker-compose up -d

# Scale DRL replicas
docker-compose up -d --scale drl=5
```

### Environment Variables for Docker

```yaml
services:
  drl:
    image: drl:latest
    environment:
      - DRL_PRIVATE_API_KEY=your-secure-api-key-minimum-16-chars
      - DRL_MEMBERSHIP_SERVICE_NAME=drl
      - DRL_LOGGING_LEVEL=info
```

## Metrics

DRL exposes Prometheus metrics on the configured metrics port (default: 9091).

### Available Metrics

| Metric                                  | Type    | Description                                       |
|-----------------------------------------|---------|---------------------------------------------------|
| `drl_membership_cluster_size`           | Gauge   | Number of active cluster members                  |
| `drl_membership_events_total`           | Counter | Membership events by type (join, leave, fail)     |
| `drl_membership_reliable_msgs_total`    | Counter | Reliable messages sent via memberlist              |
| `drl_membership_best_effort_msgs_total` | Counter | Best-effort messages sent via memberlist            |
| `drl_accounting_msg_recv_total`         | Counter | Accounting batch messages received                 |
| `drl_accounting_flush_total`            | Counter | Accounting batch flushes sent                      |
| `drl_accounting_local_increments_total` | Counter | Local accounting increments (this node is owner)   |
| `drl_accounting_remote_increments_total`| Counter | Remote accounting increments (forwarded to owner)  |
| `drl_ratelimit_blocks_total`            | Counter | Entities blocked by the rate limiter               |

### Endpoints

- `GET /metrics` - Prometheus metrics endpoint
- `GET /health` - Health check endpoint

## Development

### Running Tests

```bash
mise run test
```

### Running Linter

```bash
mise run lint
```

### Building

```bash
mise run build
```

## Internal Accounting

DRL uses a **shadow accounting** model: incoming requests are counted asynchronously in the background without
adding latency to the Envoy request path.

### Entity Hashing & Ownership

Each rate-limiting entity (IP + path + configured headers) is hashed with xxHash64 to produce a deterministic key.
A consistent hash ring distributes key ownership across cluster nodes. The **owner node** is the single authority
for that entity's counter.

### Batched Accounting via Memberlist

When a non-owner node sees a request, it **enqueues** the increment into a per-owner buffer. A background goroutine
periodically flushes these buffers:

- **Flush Interval** (default `10s`): How often batches are sent.
- **Max Batch Size** (default `1000`): Triggers an immediate flush when the buffer grows past this threshold.

Both values are configurable via KDL (`accounting.settings.flush-interval`, `accounting.settings.max-batch-size`).

Batches are serialized as **Protobuf** `DrlMessage` envelopes (containing a `CounterBatch`) and sent using
`memberlist.SendBestEffort` — a UDP-based, fire-and-forget delivery that fits DRL's "Availability > Consistency"
philosophy. Packet loss is tolerable for shadow accounting.

### Zero-Copy Optimisation

`sync.Pool` is used to reuse `CounterBatch` protobuf objects, avoiding GC pressure on the hot path.

## Internal Membership

DRL uses [Hashicorp Memberlist](https://github.com/hashicorp/memberlist) for cluster formation, failure detection,
and inter-node messaging.

### P2P Discovery

Nodes discover peers via **DNS resolution** of a configurable service name (e.g. `drl`). On startup, each node
resolves the name, filters its own IP, and joins the cluster.

### State Sync (Warm Bootstrap)

When a new node joins, memberlist's **TCP Push/Pull** protocol transfers the full blocklist from an existing peer.
This prevents a "vulnerability window" where a freshly-started node would allow traffic that should be blocked.
The node reports **Ready** only after the initial sync completes (or a configurable timeout expires).

### Gossip Tuning

For low-latency convergence the gossip protocol is tuned with:

- **GossipInterval** (default `50ms`): Time between gossip rounds.
- **GossipNodes** (default `5`): Number of peers contacted per round.

Both are configurable via KDL (`membership.gossip-interval`, `membership.gossip-nodes`).

### Reliable Blocklist Propagation

When an entity exceeds its rate limit, the owner node blocks it locally and broadcasts a `BlockEvent` to every
peer using `memberlist.SendReliable` (TCP, guaranteed delivery). This ensures all nodes update their replicated
blocklist immediately, so the next request from any Envoy is rejected at the local blocklist check (O(1)).

All inter-node messages use a unified **Protobuf `DrlMessage` envelope** with a `oneof` discriminator:

| Message Type   | Delivery         | Use Case                  |
|---------------|------------------|---------------------------|
| `CounterBatch` | `SendBestEffort` | Shadow accounting batches |
| `BlockEvent`   | `SendReliable`   | Blocklist propagation     |
| `UnblockEvent` | `SendReliable`   | Blocklist removal         |

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Envoy Sidecar                        │
│  ┌─────────────┐    ┌─────────────────────────────────────┐ │
│  │   Request   │───▶│        DRL Rate Limiter             │ │
│  │   Traffic   │    │  ┌─────────────────────────────┐    │ │
│  │             │◀───│  │     Local Blocklist         │    │ │
│  └─────────────┘    │  │     (Replicated)            │    │ │
│                     │  └─────────────────────────────┘    │ │
│                     │             │                       │ │
│                     │             ▼                       │ │
│                     │  ┌─────────────────────────────┐    │ │
│                     │  │   Hashed Accounting         │    │ │
│                     │  │   (Async Background)        │    │ │
│                     │  └─────────────────────────────┘    │ │
│                     └───────────────┬─────────────────────┘ │
└─────────────────────────────────────┼───────────────────────┘
                                      │
                    ┌─────────────────┼─────────────────┐
                    │                 │                 │
                    ▼                 ▼                 ▼
              ┌──────────┐     ┌──────────┐     ┌──────────┐
              │  DRL-1   │◀───▶│  DRL-2   │◀───▶│  DRL-3   │
              │(Owner A) │     │(Owner B) │     │(Owner C) │
              └──────────┘     └──────────┘     └──────────┘
                    ▲                 ▲                 ▲
                    │                 │                 │
                    └─────────────────┴─────────────────┘
                           Memberlist Gossip
```

## License

MIT
