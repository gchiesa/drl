---
title: gRPC Rate Limit API
description: Envoy ext_authz v3 gRPC service implementation in DRL.
weight: 6
---

The `internal/grpc` package implements the Envoy **External Authorization** (`ext_authz.v3`) gRPC service.
Envoy calls `Check` for every request it processes; DRL responds with `OK` or `PERMISSION_DENIED` (429).

## Interface

DRL implements the standard Envoy ext_authz proto:

```protobuf
service Authorization {
  rpc Check(CheckRequest) returns (CheckResponse);
}
```

The server is registered with `authv3.RegisterAuthorizationServer` from
`github.com/envoyproxy/go-control-plane/envoy/service/auth/v3`.

## Request processing flow

{{< mermaid >}}
sequenceDiagram
    participant E as Envoy
    participant D as DRL gRPC Server
    participant B as BlocklistCache
    participant A as Accounting Engine

    E->>D: Check(CheckRequest{attributes})
    D->>D: Extract IP from attributes.source.address
    D->>D: Extract path + headers from attributes.request.http
    D->>B: IsBlockedWithExpiration(entityKey)?
    alt Entity is blocked
        B-->>D: true, expiresAt
        D-->>E: PERMISSION_DENIED (429) + Retry-After header
    else Not blocked
        B-->>D: false
        D-->>E: OK (200)
        D-)A: Process(ip, path, headers) [async goroutine]
    end
{{< /mermaid >}}

The response is returned to Envoy **before** the accounting goroutine completes. This is the core of the
shadow accounting model — no counter write ever sits on the Envoy request path.

## Entity extraction

DRL extracts rate-limiting entity components from the `CheckRequest.Attributes` fields:

| `CheckRequest` field | Mapped to |
|---|---|
| `attributes.source.address.socket_address.address` | Source IP |
| `attributes.request.http.path` | URI path |
| `attributes.request.http.headers` | Header map (filtered per rule) |

The entity key is built by the accounting engine using rule-based header filtering (only headers named in
the matching `AccountingRule.Headers` list are included). The key is then hashed with xxHash64 and looked
up in the blocklist. See [Accounting]({{< ref "accounting" >}}) for details.

## Envoy configuration

Envoy must use the `ext_authz` HTTP filter pointed at DRL. The filter type is
`envoy.extensions.filters.http.ext_authz.v3.ExtAuthz`.

### HTTP filter

```yaml
http_filters:
  - name: envoy.filters.http.ext_authz
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz
      grpc_service:
        envoy_grpc:
          cluster_name: drl_cluster
        timeout: 0.25s
      transport_api_version: V3
      failure_mode_allow: false
  - name: envoy.filters.http.router
    typed_config:
      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
```

`failure_mode_allow: false` ensures that if DRL is unreachable, requests are denied rather than passed
through. Set to `true` if you prefer fail-open behaviour.

### DRL cluster

The cluster must be configured with HTTP/2 (`http2_protocol_options`) because gRPC requires it:

```yaml
clusters:
  - name: drl_cluster
    connect_timeout: 0.25s
    type: STRICT_DNS
    lb_policy: ROUND_ROBIN
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    load_assignment:
      cluster_name: drl_cluster
      endpoints:
        - lb_endpoints:
            - endpoint:
                address:
                  socket_address:
                    address: drl      # DNS name of the DRL service / pod
                    port_value: 8081  # DRL gRPC port
```

### Complete `envoy.yaml` example

```yaml
static_resources:
  listeners:
    - name: listener_0
      address:
        socket_address:
          address: 0.0.0.0
          port_value: 10000
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: ingress_http
                http_filters:
                  - name: envoy.filters.http.ext_authz
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz
                      grpc_service:
                        envoy_grpc:
                          cluster_name: drl_cluster
                        timeout: 0.25s
                      transport_api_version: V3
                      failure_mode_allow: false
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
                route_config:
                  name: local_route
                  virtual_hosts:
                    - name: backend
                      domains: ["*"]
                      routes:
                        - match:
                            prefix: "/"
                          route:
                            cluster: echo-server

  clusters:
    - name: drl_cluster
      connect_timeout: 0.25s
      type: STRICT_DNS
      lb_policy: ROUND_ROBIN
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http2_protocol_options: {}
      load_assignment:
        cluster_name: drl_cluster
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address:
                      address: drl
                      port_value: 8081
```

## gRPC server address

```kdl
listen {
    grpc ":8081"   // gRPC bind address (default: :8081)
}
```

Environment variable: `DRL_LISTEN_GRPC`

## Responses

| Scenario | gRPC status code | HTTP status | Extra headers |
|---|---|---|---|
| Entity not blocked | `OK` (0) | 200 | — |
| Entity blocked | `PERMISSION_DENIED` (7) | 429 | `Retry-After: <seconds>` |

The `Retry-After` value is the number of seconds until the blocklist entry expires for the entity.

## Max hops

To prevent circular forwarding during hash ring transitions (e.g. when a node is joining or leaving), DRL
enforces a **max hops of 1** on internal peer increments. If a forwarded increment cannot be delivered to
the owner, the increment is dropped with a warning log. This keeps the failure domain local and prevents
cascade retries.
