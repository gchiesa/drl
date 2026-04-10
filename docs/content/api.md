---
title: gRPC Rate Limit API
description: Envoy ratelimit.v3 gRPC service implementation in DRL.
weight: 6
---

The `internal/api` package implements the Envoy `ratelimit.v3` gRPC service, which is the primary interface
between Envoy and DRL. Envoy calls `ShouldRateLimit` for every request it processes; DRL responds with
`OK` or `OVER_LIMIT`.

## Interface

DRL implements the standard Envoy rate-limit proto:

```protobuf
service RateLimitService {
  rpc ShouldRateLimit(RateLimitRequest) returns (RateLimitResponse);
}
```

The server is created with references to the cache, accounting engine, and membership ring:

```go
func NewRateLimitServer(
    cfg      *config.Config,
    blocklist *cache.BlocklistCache,
    accounting *accounting.AccountingCache,
    ring      *membership.Ring,
    peers     *membership.PeerClient,
) *RateLimitServer
```

## Request processing flow

{{< mermaid >}}
sequenceDiagram
    participant E as Envoy
    participant D as DRL gRPC Server
    participant B as BlocklistCache
    participant A as Accounting Engine

    E->>D: ShouldRateLimit(domain, descriptors)
    D->>D: Extract IP + path + headers from descriptors
    D->>B: Contains(entityKey)?
    alt Entity is blocked
        B-->>D: true
        D-->>E: OVER_LIMIT (code 429)
    else Not blocked
        B-->>D: false
        D-->>E: OK (code 200)
        D-)A: Increment(entityKey) [async goroutine]
    end
{{< /mermaid >}}

The response is returned to Envoy **before** the accounting goroutine completes. This is the core of the
shadow accounting model — no counter write ever sits on the Envoy request path.

## Envoy configuration

Envoy must be configured to use the external rate-limit service. Relevant snippet for `envoy.yaml`:

```yaml
http_filters:
  - name: envoy.filters.http.ratelimit
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.ratelimit.v3.RateLimit
      domain: drl
      request_type: external
      rate_limit_service:
        grpc_service:
          envoy_grpc:
            cluster_name: drl_cluster
        transport_api_version: V3

clusters:
  - name: drl_cluster
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: drl_cluster
      endpoints:
        - lb_endpoints:
            - endpoint:
                address:
                  socket_address:
                    address: drl         # service DNS name
                    port_value: 8081     # DRL gRPC port
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
```

## gRPC server address

```kdl
listen {
    grpc ":8081"   // gRPC bind address (default: :8081)
}
```

Environment variable: `DRL_LISTEN_GRPC`

## Entity extraction

DRL extracts rate-limiting entity components from the Envoy `RateLimitDescriptor` entries:

| Descriptor key | Mapped to |
|---------------|-----------|
| `remote_address` | Source IP |
| `path` | URI path |
| Any other key | Header value (if the key matches a configured header name) |

The entity key is then hashed with xxHash64 and looked up in the blocklist. See [Accounting]({{< ref "accounting" >}})
for details on how header matching works in accounting rules.

## Max hops

To prevent circular forwarding during hash ring transitions (e.g. when a node is joining or leaving), DRL
enforces a **max hops of 1** on internal peer increments. If a forwarded increment cannot be delivered to
the owner, the increment is dropped with a warning log. This keeps the failure domain local and prevents
cascade retries.
