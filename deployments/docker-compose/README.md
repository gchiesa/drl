# DRL Docker Compose Deployment

Local development and manual testing environment using Docker Compose.

## Architecture

```
                    ┌──────────────────────────────────────────────┐
                    │              drl-network (bridge)             │
                    │                                               │
  ┌──────┐  :10000  │  ┌─────────┐     ┌──────────────────────┐   │
  │      │──────────┼─▶│  Envoy  │────▶│  echo-server (x3)    │   │
  │Client│          │  │         │     │  mccutchen/go-httpbin │   │
  └──────┘          │  └────┬────┘     └──────────────────────┘   │
                    │       │ ext_authz gRPC                        │
                    │       ▼                                       │
                    │  ┌──────────┐                                 │
                    │  │ DRL (x2) │◀──── memberlist P2P gossip      │
                    │  └──────────┘                                 │
                    └──────────────────────────────────────────────┘
```

## Services

| Service      | Image                        | Port(s)            | Description                      |
|--------------|------------------------------|--------------------|----------------------------------|
| echo-server  | mccutchen/go-httpbin:latest  | 8080 (internal)    | Backend workload (3 replicas)    |
| envoy        | envoyproxy/envoy:v1.30-latest| 10000 (exposed)    | Reverse proxy with ext_authz     |
| drl          | built from Dockerfile        | 8081, 8082, 9091   | Rate limiter (2 replicas)        |
| k6           | grafana/k6:latest            | —                  | Load test runner (profile-gated) |
| jumpbox      | apteno/alpine-jq:latest      | —                  | Debug utility container          |

## Usage

### Start the stack

```bash
# From this directory
docker compose up -d

# With load testing enabled
docker compose --profile loadtest up -d
```

### Run load test only

```bash
docker compose --profile loadtest run --rm k6
```

### Watch DRL logs

```bash
docker compose logs -f drl
```

### Stop the stack

```bash
docker compose down
```

## Configuration

- **DRL config**: `./drl/config.kdl` — rate limit rules, gRPC/metrics ports, membership settings
- **Envoy config**: `./envoy/envoy.yaml` — ext_authz filter wired to DRL on port 8081
- **K6 script**: `./k6/load-test.js` — ramp-up load test hitting `/anything`

## Environment Variables

| Variable                    | Default                    | Description                        |
|-----------------------------|----------------------------|------------------------------------|
| `DRL_PRIVATE_API_KEY`       | `Test5ecretPrivateAPIKey!` | Internal API authentication key    |
| `DRL_MEMBERSHIP_PRIMARY_KEY`| `abcdef0123456789`         | Memberlist encryption primary key  |
| `DRL_MEMBERSHIP_SECONDARY_KEYS` | `0123456789012345`     | Memberlist encryption secondary key|

> These defaults are for local testing only. Use strong secrets in production.
