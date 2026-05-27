# DRL — Embedded Proxy Docker Compose

This deployment demonstrates **DRL without an Envoy sidecar**.  
DRL's embedded reverse proxy (`embedded-proxy`) handles edge ingress directly,
enforcing rate-limit rules before forwarding requests to the upstream service.

## Architecture

```
                   ┌─────────────────────────────────────────────┐
                   │  Docker Compose network: drl-network         │
                   │                                             │
  client / k6 ──► │  drl:8080  (3 replicas, embedded proxy)     │
                   │      │                                       │
                   │      │  dns-round-robin over echo-server     │
                   │      ▼                                       │
                   │  echo-server:8080  (3 replicas, go-httpbin) │
                   └─────────────────────────────────────────────┘
```

Compared to the `docker-compose` variant:

| Component | `docker-compose` | `docker-compose-embedded-proxy` |
|-----------|------------------|---------------------------------|
| Edge proxy | Envoy (ext_authz → DRL gRPC) | **DRL embedded proxy** |
| DRL role | Rate-limit decision engine only | Edge proxy + rate limiter |
| Extra containers | `envoy` | — |
| Inbound port | `10000` (Envoy) | `8080` (DRL) |

## Rate-limit rules

| Rule | Path | Limit |
|------|------|-------|
| `catch-all` | `/` | 100 req / minute |
| `anything` | `/anything` | **10 req / minute** |

The k6 load test hammers `/anything` and will start receiving `429 Too Many Requests`
once the 10 req/min rule fires on any DRL node.

## Quick start

```bash
# Build DRL image and start the fleet + echo-server
docker compose up --build

# In a separate terminal — watch DRL logs
docker compose logs -f drl

# Manual test from jumpbox (inside the Docker network)
docker exec -it jumpbox sh -c 'for i in $(seq 1 15); do
  echo -n "req $i: "
  wget -qO- --server-response http://drl:8080/anything 2>&1 | grep "HTTP/"
done'

# Run k6 load test
docker compose --profile loadtest up k6
```

## Observing rate limiting

```bash
# Watch the metrics endpoint of one DRL instance
docker exec -it jumpbox wget -qO- http://drl:9091/metrics | grep drl_proxy

# Check blocklist via internal API
docker exec -it jumpbox wget -qO- \
  --header "X-API-Key: Test5ecretPrivateAPIKey!" \
  http://drl:8082/v1/blocklist | jq .
```

## Configuration

`drl/config.kdl` — the embedded-proxy stanza:

```kdl
embedded-proxy {
    enabled true
    listen ":8080"
    tls { enabled false cert "" key "" }

    host "echo-server" {
        routes {
            route "/" {
                upstream           "http://echo-server:8080"
                balance-strategy   "dns-round-robin"
                dns-refresh-interval "5s"
                require-auth       false
            }
        }
    }
}
```

TLS can be enabled by setting `tls.enabled true` and providing base64-encoded
PEM cert and key in `tls.cert` / `tls.key` (or via `DRL_EMBEDDED_PROXY_TLS_CERT`
and `DRL_EMBEDDED_PROXY_TLS_KEY` environment variables), then changing
`listen` to `:8443`.
