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

        // Optional: extract the real client IP from the X-Forwarded-For header.
        // use-x-forwarded-for           true
        // use-x-forwarded-for-direction "right"  // left | right
        // use-x-forwarded-for-index     1
    }

    rules {
        payments-api {
            path-prefix "/api/v1/payments"
            headers     "X-API-Key" "X-Tenant-ID"
            redactions {
                "X-API-Key"   "^(.{0,3}).*$"
                "X-Tenant-ID" "^(.{0,4}).*$"
            }
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

Global settings for the accounting engine. Each field can be overridden via its corresponding `DRL_ACCOUNTING_SETTINGS_*` environment variable.

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `algorithm` | `DRL_ACCOUNTING_SETTINGS_ALGORITHM` | `sliding-window` | Rate-limiting algorithm: `sliding-window` or `token-bucket` |
| `retry-after-type` | `DRL_ACCOUNTING_SETTINGS_RETRY_AFTER_TYPE` | `delay-seconds` | Format of the `Retry-After` response header: `delay-seconds` or `http-date` |
| `flush-interval` | `DRL_ACCOUNTING_SETTINGS_FLUSH_INTERVAL` | `200ms` | How often the Flusher drains per-owner buffers |
| `max-batch-size` | `DRL_ACCOUNTING_SETTINGS_MAX_BATCH_SIZE` | `1000` | Maximum entries per batch; triggers an immediate flush when reached |
| `capacity` | `DRL_ACCOUNTING_SETTINGS_CAPACITY` | — | Token-bucket burst size (required when `algorithm` is `token-bucket`) |
| `refill-rate` | `DRL_ACCOUNTING_SETTINGS_REFILL_RATE` | — | Token-bucket refill rate in tokens per second (required when `algorithm` is `token-bucket`) |
| `use-x-forwarded-for` | `DRL_ACCOUNTING_SETTINGS_USE_X_FORWARDED_FOR` | `false` | Extract the client IP from the `X-Forwarded-For` header instead of the socket remote address. See [X-Forwarded-For](#x-forwarded-for-xff-ip-extraction) below. |
| `use-x-forwarded-for-direction` | `DRL_ACCOUNTING_SETTINGS_USE_X_FORWARDED_FOR_DIRECTION` | `left` | Which end of the XFF chain to read from: `left` (client end) or `right` (proxy end). Only evaluated when `use-x-forwarded-for` is `true`. |
| `use-x-forwarded-for-index` | `DRL_ACCOUNTING_SETTINGS_USE_X_FORWARDED_FOR_INDEX` | `0` | Zero-based offset from the chosen direction. Only evaluated when `use-x-forwarded-for` is `true`. |

**Validation rules (when `use-x-forwarded-for` is `true`):**
- `use-x-forwarded-for-direction` must be `left` or `right` (case-insensitive); defaults to `left` if omitted
- `use-x-forwarded-for-index` must be ≥ 0

---

### X-Forwarded-For (XFF) IP extraction

When DRL runs behind one or more reverse proxies, the Envoy socket remote address is the proxy IP rather
than the originating client. Enabling `use-x-forwarded-for` tells the accounting engine to parse the
`X-Forwarded-For` header and use a specific entry as the effective source IP for rate-limit accounting and
entity key construction.

**How the header is structured**

Each proxy in the chain appends the address it received the connection from, left to right:

```
X-Forwarded-For: <client>, <proxy1>, <proxy2>
```

Index `0` from the **left** is the original client IP (cheapest to obtain, but trivially spoofable by the
client). Index `0` from the **right** is the last proxy.

#### Recipe 1 — Single trusted proxy, simple setup

Use the leftmost entry when there is exactly one proxy and you accept that a malicious client could
forge the header value:

```kdl
accounting {
    settings {
        use-x-forwarded-for           true
        use-x-forwarded-for-direction "left"
        use-x-forwarded-for-index     0
    }
}
```

#### Recipe 2 — Single trusted proxy, anti-spoofing

Use `direction "right"` with `index 1` to read the IP that **your** outermost controlled proxy recorded
as the upstream address. This entry is appended by your proxy — the client cannot forge it:

```
X-Forwarded-For: <client-or-spoofed>, <trusted-proxy-appended-this>
                                       └── right index 1 reads here
```

```kdl
accounting {
    settings {
        use-x-forwarded-for           true
        use-x-forwarded-for-direction "right"
        use-x-forwarded-for-index     1
    }
}
```

#### Fallback behaviour

When `use-x-forwarded-for` is `true` but the header is absent, empty, or the computed index is out of
range, DRL falls back silently to the socket remote address without logging a warning.

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
            redactions {
                "Header-Name-1" "^(.{0,3}).*$"
            }
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
| `redactions` | No | Map of header name → regex pattern. Applied when the internal API lists blocked entities. See [Header Redactions](#header-redactions) below. |
| `limit` | Yes | Request count threshold before the entity is blocked |
| `per` | Yes | Window unit: `second` or `minute` |

DRL evaluates rules in definition order and applies the **first matching rule** to an entity. Entities that
match no rule are passed through without accounting.

#### Environment variable override

Individual rules can be injected or overridden without a config file via `DRL_RULE_<rule-name>_JSON`.
The value is a JSON object matching the rule fields above.

```bash
DRL_RULE_payments_api_JSON='{"path-prefix":"/api/v1/payments","headers":["X-API-Key","X-Tenant-ID"],"redactions":{"X-API-Key":"^(.{0,3}).*$"},"limit":500,"per":"minute"}'
DRL_RULE_users_api_JSON='{"path-prefix":"/api/v1/users","limit":2000,"per":"minute"}'
```

**Merge semantics:** env-var rules are merged on top of any KDL-defined rules. A rule whose name appears
in an env var overwrites the KDL rule with the same name; rules not mentioned in env vars are kept
as-is. This lets you ship a base rule set in a KDL file and patch specific limits per environment
(e.g. lower limits in staging).

> **Note:** The `<rule-name>` portion of the env var key is used verbatim as the map key. It must
> exactly match the name used in the KDL `rules {}` block when you intend to override an existing rule.
> Because POSIX shell variable names cannot contain hyphens, prefer underscores in rule names
> (e.g. `payments_api` rather than `payments-api`) when env-var overrides are required.

---

### Header Redactions

The `redactions` block controls how sensitive header values are displayed when listing blocked entities via
the internal API (`GET /v1/blocked-entity`). It does **not** affect rate-limit accounting — headers are still
used in full for entity key construction.

Each entry is a `"Header-Name" "regex"` pair. The regex **must contain exactly one capturing group** `(...)`.
When a blocked entity is listed:

- The portion matched by the first capturing group is kept verbatim.
- Every other character in the value is replaced with `*`.
- If the regex does not match, the original value is returned unchanged.

The capture group `(...)` marks exactly the portion of the value to keep visible. Everything before and
after it is replaced with `*`. The table below shows the four supported patterns:

| Pattern | Example input        | Output                | Notes |
|---------|----------------------|-----------------------|-------|
| `^(.{0,3}).*$` | `sk_live_abc123`     | `sk_************`     | Keep first N chars |
| `^(Bearer .{0,3}).*$` | `Bearer tok_abc123`  | `Bearer to**********` | Keep scheme + first N token chars |
| `^glpat(.{0,7}).*$` | `glpat_abcdefgh1234` | `*****_abcdef******`  | Keep middle segment (group after a fixed prefix) |
| `^(\\w+).*$` | `tenant.example.com` | `tenant**********`    | Keep up to the first non-word character |

#### Examples

**Keep only the first 3 characters of an API key:**

```kdl
redactions {
    "X-Api-Key" "^(.{0,3}).*$"
}
```

`sk_live_abc123xyz` → `sk_**************`

---

**Preserve the Bearer scheme word and the first 3 token characters:**

```kdl
redactions {
    "Authorization" "^(Bearer .{0,3}).*$"
}
```

`Bearer tok_abc123` → `Bearer to**********`

---

**Expose only the middle segment of a structured token (group after a known prefix):**

The capture group sits in the middle; characters before it are also masked.

```kdl
redactions {
    "Private-Token" "^glpat(.{0,7}).*$"
}
```

`glpat_abcdefgh1234` → `*****_abcdef******`

---

**Keep only the first hostname label from a multi-part Host header:**

```kdl
redactions {
    "Host" "^(\\w+).*$"
}
```

`tenant.example.com` → `tenant************`

---

**Full rule example with redactions:**

```kdl
accounting {
    rules {
        payments-api {
            path-prefix "/api/v1/payments"
            headers     "X-Api-Key" "X-Tenant-ID"
            redactions {
                "X-Api-Key"   "^(.{0,3}).*$"
                "X-Tenant-ID" "^(.{0,4}).*$"
            }
            limit 500
            per   "minute"
        }
    }
}
```

**Validation rules:**
- Each regex value must be a valid Go regular expression (checked at startup).
- At least one capturing group is required for masking to take effect; a pattern with no group leaves
  the value unmodified.

### `embedded-proxy`

Enables DRL's optional built-in reverse proxy. See the [Embedded Proxy]({{< ref "embedded-proxy" >}}) page for the
full architecture and OIDC reference.

| KDL key | Env var | Default | Description |
|---------|---------|---------|-------------|
| `enabled` | `DRL_EMBEDDED_PROXY_ENABLED` | `false` | Enable the embedded reverse proxy |
| `listen` | `DRL_EMBEDDED_PROXY_LISTEN` | `:8443` | Bind address for the proxy listener |
| `tls.enabled` | `DRL_EMBEDDED_PROXY_TLS_ENABLED` | `false` | Enable TLS on the proxy listener |
| `tls.cert` | `DRL_EMBEDDED_PROXY_TLS_CERT` | — | Base64-encoded PEM certificate |
| `tls.key` | `DRL_EMBEDDED_PROXY_TLS_KEY` | — | Base64-encoded PEM private key |

#### Host and route override

The `host` / `routes` tree is too deeply nested for flat env vars. The entire hosts array can be
replaced via `DRL_EMBEDDED_PROXY_HOSTS_JSON` (full replace, not merge):

```bash
DRL_EMBEDDED_PROXY_HOSTS_JSON='[
  {
    "hostname": "api.example.com",
    "oidc": {
      "issuer": "https://auth.example.com/realms/myapp",
      "client-id": "drl-gateway",
      "audience": "https://api.example.com"
    },
    "routes": {
      "routes": [
        {
          "prefix": "/v1",
          "upstream": "http://backend:8080",
          "require-auth": true,
          "scopes": ["read"]
        },
        {
          "prefix": "/health",
          "upstream": "http://backend:8080",
          "require-auth": false
        }
      ]
    }
  }
]'
```

JSON field names follow the `json:"..."` struct tags (same as the keys reported by the internal API).
The `routes` wrapper is a nested object — see the example above for the correct shape.

**Terraform** users can produce the value with `jsonencode(...)`:

```hcl
environment = [
  {
    name  = "DRL_EMBEDDED_PROXY_HOSTS_JSON"
    value = jsonencode([{
      hostname = "api.example.com"
      routes   = { routes = [{ prefix = "/", upstream = "http://backend:8080", "require-auth" = false }] }
    }])
  }
]
```

#### `embedded-proxy.host.<hostname>.routes.route`

Each `route` node inside a `host` block maps a URI prefix to an upstream service.

| KDL key | Default | Description |
|---------|---------|-------------|
| `upstream` | — | Full upstream URL, e.g. `http://backend:8080` |
| `balance-strategy` | — | `dns-round-robin` rotates across all A-record IPs for the upstream hostname |
| `dns-refresh-interval` | `5s` | How often the DNS resolver refreshes the IP pool (only for `dns-round-robin`) |
| `require-auth` | `false` | Enforce OIDC Bearer-token verification on this route |
| `scopes` | — | One or more OAuth 2.0 scopes required (only evaluated when `require-auth true`) |

##### Connection pool settings (per route)

Each route maintains its own upstream connection pool, isolated from other routes and from internal DRL HTTP clients
(Memberlist, OIDC discovery). The defaults below are intentionally more generous than Go's `http.DefaultTransport`
(`MaxIdleConnsPerHost: 2`) to avoid connection churn under proxy workloads.

| KDL key | Default | Description |
|---------|---------|-------------|
| `max-idle-conns-per-host` | `32` | Maximum idle keep-alive connections held per upstream host |
| `max-conns-per-host` | `0` (unlimited) | Total connection cap per upstream host (dialing + active + idle) |
| `idle-conn-timeout` | `90s` | How long an idle keep-alive connection is kept open before closing |
| `response-header-timeout` | `30s` | Maximum wait for an upstream response header after the request is sent |
| `dial-timeout` | `30s` | Maximum time to establish a new TCP connection to the upstream |

---

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
