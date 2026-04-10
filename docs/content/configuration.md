---
title: Configuration
description: Complete KDL configuration reference and environment variable overrides for DRL.
weight: 2
---

DRL uses [KDL](https://kdl.dev/) configuration files. Configuration is applied in three layers (highest wins):

1. **Environment variables** (`DRL_*` prefix)
2. **KDL configuration file** (path passed via `--config`)
3. **Built-in defaults** (embedded in the binary)

## Full example

```kdl
// DRL configuration file

listen {
    grpc    ":8081"
    metrics ":9091"
}

membership {
    service-name    "drl"
    port            7946
    bind-addr       "0.0.0.0"
    startup-delay   "3s"
    gossip-interval "50ms"
    gossip-nodes    5
    // Optional AES encryption (16, 24, or 32 byte keys)
    secret-keys "primary-key-16b" "old-key-16-bytes"
}

logging {
    level  "info"    // debug | info | warn | error
    format "json"    // json | text
}

internal-api {
    enabled true
    address ":8082"
}

cache {
    blocklist-size-mb             64
    accounting-size-mb           128
    sync-timeout-seconds          30
    blocklist-default-ttl-seconds 300
}

accounting {
    settings {
        algorithm        "sliding-window"
        retry-after-type "delay-seconds"
        flush-interval   "200ms"
        max-batch-size   1000
    }

    rules {
        payments-api {
            path-prefix "/api/v1/payments"
            headers     "X-API-Key" "X-Tenant-ID"
            limit        500
            per          "minute"
        }
        users-api {
            path-prefix "/api/v1/users"
            limit        2000
            per          "minute"
        }
    }
}
```

## Reference

### `listen`

Controls the addresses DRL binds its public servers to.

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `grpc` | `DRL_LISTEN_GRPC` | `:8081` | gRPC server address (Envoy rate-limit service) |
| `metrics` | `DRL_LISTEN_METRICS` | `:9091` | Prometheus metrics HTTP endpoint |

---

### `membership`

Controls cluster formation and gossip.

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `service-name` | `DRL_MEMBERSHIP_SERVICE_NAME` | `drl` | DNS name resolved to discover peers |
| `port` | `DRL_MEMBERSHIP_PORT` | `7946` | Memberlist gossip UDP/TCP port |
| `bind-addr` | `DRL_MEMBERSHIP_BIND_ADDR` | `0.0.0.0` | Address to bind the Memberlist listener |
| `startup-delay` | `DRL_MEMBERSHIP_STARTUP_DELAY` | `3s` | Delay before joining the cluster (allows DNS to propagate) |
| `gossip-interval` | `DRL_MEMBERSHIP_GOSSIP_INTERVAL` | `50ms` | Interval between gossip rounds |
| `gossip-nodes` | `DRL_MEMBERSHIP_GOSSIP_NODES` | `5` | Number of peers contacted per gossip round |
| `secret-keys` | See note below | — | AES encryption keys (16, 24, or 32 bytes) |

**Encryption key environment variables** (special handling — override the full `secret-keys` list):

| Env var | Description |
|---------|-------------|
| `DRL_MEMBERSHIP_PRIMARY_KEY` | Primary encryption key (replaces all KDL-configured keys) |
| `DRL_MEMBERSHIP_SECONDARY_KEYS` | Comma-separated secondary keys accepted for decryption only |

All keys must be the same length and a valid AES size (16, 24, or 32 bytes). The first key is used for
encryption; additional keys are decryption-only (key rotation support).

**Validation rules:**
- `service-name` must not be empty
- `port` must be 1–65535
- `bind-addr` must not be empty

---

### `logging`

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `level` | `DRL_LOGGING_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error` |
| `format` | `DRL_LOGGING_FORMAT` | `json` | Log format: `json`, `text` |

---

### `internal-api`

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `enabled` | `DRL_INTERNAL_API_ENABLED` | `true` | Enable the internal HTTP management API |
| `address` | `DRL_INTERNAL_API_ADDRESS` | `:8082` | Bind address for the internal API |

The internal API requires an API key set via the `DRL_PRIVATE_API_KEY` environment variable (minimum 16
characters). This variable has no KDL equivalent — it is always sourced from the environment.

| Env var | Description |
|---------|-------------|
| `DRL_PRIVATE_API_KEY` | API authentication key (required, min 16 chars) |
| `DRL_NODE_NAME` | Override the node name (defaults to the system hostname) |

---

### `cache`

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `blocklist-size-mb` | `DRL_CACHE_BLOCKLIST_SIZE_MB` | `64` | Maximum RAM (MB) for the blocklist cache |
| `accounting-size-mb` | `DRL_CACHE_ACCOUNTING_SIZE_MB` | `128` | Maximum RAM (MB) for the accounting cache |
| `sync-timeout-seconds` | `DRL_CACHE_SYNC_TIMEOUT_SECONDS` | `30` | Max wait (s) for initial Memberlist state sync |
| `blocklist-default-ttl-seconds` | `DRL_CACHE_BLOCKLIST_DEFAULT_TTL_SECONDS` | `300` | Default TTL (s) for manual admin-API blocks |

**Validation rules:**
- `blocklist-size-mb` must be ≥ 1
- `accounting-size-mb` must be ≥ 1
- `sync-timeout-seconds` must be ≥ 1
- `blocklist-default-ttl-seconds` must be ≥ 1

---

### `accounting.settings`

Global settings for the accounting engine. These do **not** have individual environment variable overrides.

| KDL key | Default | Description |
|---------|---------|-------------|
| `algorithm` | `sliding-window` | Rate-limiting algorithm (`sliding-window` is the only supported value) |
| `retry-after-type` | `delay-seconds` | Format of the `Retry-After` header: `delay-seconds` or `http-date` |
| `flush-interval` | `200ms` | How often the Flusher drains per-owner buffers |
| `max-batch-size` | `1000` | Maximum entries per batch; triggers an immediate flush when reached |

---

### `accounting.rules`

Rate-limiting rules are defined as named children under the `rules` node. Each rule matches a URI path
prefix and optionally a set of headers.

```kdl
accounting {
    rules {
        <rule-name> {
            path-prefix "/api/v1/..."
            headers     "Header-Name-1" "Header-Name-2"
            limit        1000
            per          "minute"
        }
    }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `path-prefix` | Yes | URI path prefix to match. Matched using longest-prefix (radix tree). |
| `headers` | No | One or more header names whose values are included in the entity key |
| `limit` | Yes | Request count threshold before the entity is blocked |
| `per` | Yes | Window unit: `second` or `minute` |

DRL evaluates rules in definition order and applies the **first matching rule** to an entity. Entities that
match no rule are passed through without accounting.

## Docker Compose example

```yaml
services:
  drl:
    image: drl:latest
    environment:
      - DRL_PRIVATE_API_KEY=your-secure-api-key-minimum-16-chars
      - DRL_MEMBERSHIP_SERVICE_NAME=drl
      - DRL_LISTEN_GRPC=:8081
      - DRL_LISTEN_METRICS=:9091
      - DRL_LOGGING_LEVEL=info
      - DRL_LOGGING_FORMAT=json
    volumes:
      - ./config.kdl:/etc/drl/config.kdl:ro
    command: ["./drl", "--config", "/etc/drl/config.kdl"]
```
