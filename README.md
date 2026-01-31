# DRL - Distributed Rate Limiter

A high-performance, horizontally scalable rate-limiting service designed for Envoy sidecars. DRL eliminates the latency
of external databases by using a Peer-to-Peer (P2P) Hybrid Architecture.

## Features

- **Local Enforcement**: Fully replicated Blocklist for O(1) rejection
- **Shadow Accounting**: Hashed, asynchronous global quota tracking
- **State Sync**: Warm-bootstrapping to prevent "vulnerability windows" during rolling updates
- **Cluster Discovery**: Automatic peer discovery via DNS using Hashicorp Memberlist
- **SCRAM-SHA-256 Authentication**: Secure internal API with RFC 5802/7677 compliant authentication

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

DRL exposes an internal API on port 8082 (configurable) protected by SCRAM-SHA-256 authentication.

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

### SCRAM-SHA-256 Authentication

The internal API uses SCRAM-SHA-256 (RFC 5802/7677) for authentication. This is a challenge-response mechanism that
never transmits the password over the wire.

#### Authentication Flow

1. **Client First Message**: Client sends username and nonce
2. **Server First Message**: Server responds with combined nonce, salt, and iteration count
3. **Client Final Message**: Client sends proof of password knowledge
4. **Server Final Message**: Server verifies and sends its own proof

#### Example Using curl

**Step 1: Initial Request (Client First)**

```bash
curl -v http://localhost:8082/status \
     -H "Authorization: SCRAM-SHA-256 n,,n=admin,r=fyko+d2lbbFgONRv9qkxdawL"
```

Response (401 Unauthorized):

```
WWW-Authenticate: SCRAM-SHA-256 r=fyko+d2lbbFgONRv9qkxdawL<server-nonce>,s=<salt>,i=4096
```

**Step 2: Final Request (Client Final)**

```bash
curl -v http://localhost:8082/status \
     -H "Authorization: SCRAM-SHA-256 c=biws,r=<full-nonce>,p=<client-proof>"
```

Response (200 OK):

```
Authentication-Info: v=<server-signature>

{"cluster_name":"drl","node_id":"node-1",...}
```

#### Programmatic Example (Go)

```go
package main

import (
	"fmt"
	"github.com/xdg-go/scram"
)

func main() {
	client, _ := scram.SHA256.NewClient("admin", "your-api-key", "")
	conv := client.NewConversation()

	// Step 1: Generate client-first message
	clientFirst, _ := conv.Step("")

	// Step 2: Send to server, receive server-first
	// serverFirst := sendToServer(clientFirst)

	// Step 3: Generate client-final message
	// clientFinal, _ := conv.Step(serverFirst)

	// Step 4: Verify server-final
	// _, _ = conv.Step(serverFinal)

	fmt.Println("Client first:", clientFirst)
}
```

### Security Notes

- **Production Deployment**: The internal API should be bound to `localhost` or protected by mTLS/VPN
- **API Key Requirements**: Must be at least 16 characters
- **Credential Storage**: DRL never stores the raw API key. Only derived `StoredKey` and `ServerKey` are kept in memory
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

| Metric                        | Type    | Description                                   |
|-------------------------------|---------|-----------------------------------------------|
| `drl_membership_cluster_size` | Gauge   | Number of active cluster members              |
| `drl_membership_events_total` | Counter | Membership events by type (join, leave, fail) |

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
