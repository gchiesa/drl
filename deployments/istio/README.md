# DRL Istio Integration

This document describes how to configure **Istio** to use DRL as an external rate-limiting extension for Envoy sidecars managed by the service mesh.

> **Note:** This is a configuration guide only — no Kubernetes manifests are required beyond the `EnvoyFilter` and `ServiceEntry` resources shown below. DRL must already be deployed (e.g., via `k8s-fleet`) and reachable from the mesh.

## Overview

Istio manages Envoy sidecar proxies via xDS. The standard way to plug in an external rate limiter is through Envoy's **`ext_authz`** HTTP filter, injected via an `EnvoyFilter` resource. Istio's own `RateLimitPolicy` CRD can also drive the same flow, but `EnvoyFilter` gives full control and works without the Istio rate-limit service add-on.

```
┌──────────────────────────────────────────────────────────────────────┐
│  Istio mesh                                                           │
│                                                                       │
│   Workload Pod                                                        │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  ┌─────────────────┐   inbound   ┌────────────────────────┐   │  │
│  │  │  Envoy sidecar  │────────────▶│    app container       │   │  │
│  │  │  (istio-proxy)  │             └────────────────────────┘   │  │
│  │  │                 │                                           │  │
│  │  │  ext_authz ─────┼─────────── gRPC ──────────────────────────┼──▶ DRL fleet
│  │  │  (EnvoyFilter)  │            drl.drl.svc.cluster.local:8081 │  │
│  │  └─────────────────┘                                           │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

## Prerequisites

- Istio >= 1.17 installed in the cluster
- DRL fleet running and accessible (see `../k8s-fleet/`)
- `istioctl` and `kubectl` available

## Step 1 — Deploy the DRL Fleet

Deploy DRL using the fleet manifests before wiring Istio:

```bash
kubectl apply -k ../k8s-fleet/base/
```

Verify the fleet is healthy:

```bash
kubectl -n drl get pods -l app=drl
kubectl -n drl logs -l app=drl | grep "memberlist"
```

## Step 2 — Register DRL as a ServiceEntry (optional but recommended)

If DRL is deployed in a namespace that Istio manages, its `Service` is already known to the mesh and you can skip this step. If DRL lives outside the mesh or in a non-injected namespace, register it explicitly:

```yaml
# serviceentry-drl.yaml
apiVersion: networking.istio.io/v1alpha3
kind: ServiceEntry
metadata:
  name: drl-fleet
  namespace: istio-system
spec:
  hosts:
    - drl.drl.svc.cluster.local
  ports:
    - number: 8081
      name: grpc
      protocol: GRPC
  resolution: DNS
  location: MESH_INTERNAL
```

```bash
kubectl apply -f serviceentry-drl.yaml
```

## Step 3 — Inject the ext_authz Filter via EnvoyFilter

The `EnvoyFilter` below adds DRL's `ext_authz` gRPC check to the inbound HTTP filter chain of every sidecar in the `default` namespace. Adjust `workloadSelector` and `namespace` to target specific workloads.

```yaml
# envoyfilter-drl-ratelimit.yaml
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: drl-ext-authz
  namespace: default          # apply to workloads in this namespace
spec:
  # Remove workloadSelector to apply mesh-wide (all namespaces with injection)
  workloadSelector:
    labels:
      app: echo-server        # target specific app label

  configPatches:
    # ── Add DRL cluster definition ─────────────────────────────────────────
    - applyTo: CLUSTER
      match:
        context: SIDECAR_INBOUND
      patch:
        operation: ADD
        value:
          name: drl_ext_authz_cluster
          connect_timeout: 0.25s
          type: STRICT_DNS
          lb_policy: ROUND_ROBIN
          typed_extension_protocol_options:
            envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
              "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
              explicit_http_config:
                http2_protocol_options: {}
          load_assignment:
            cluster_name: drl_ext_authz_cluster
            endpoints:
              - lb_endpoints:
                  - endpoint:
                      address:
                        socket_address:
                          address: drl.drl.svc.cluster.local
                          port_value: 8081

    # ── Insert ext_authz filter BEFORE the router ─────────────────────────
    - applyTo: HTTP_FILTER
      match:
        context: SIDECAR_INBOUND
        listener:
          filterChain:
            filter:
              name: envoy.filters.network.http_connection_manager
              subFilter:
                name: envoy.filters.http.router
      patch:
        operation: INSERT_BEFORE
        value:
          name: envoy.filters.http.ext_authz
          typed_config:
            "@type": type.googleapis.com/envoy.extensions.filters.http.ext_authz.v3.ExtAuthz
            grpc_service:
              envoy_grpc:
                cluster_name: drl_ext_authz_cluster
              timeout: 0.25s
            transport_api_version: V3
            # fail_open: set to true if you prefer availability over strict rate limiting
            failure_mode_allow: false
            # Forward the original request headers to DRL for entity matching
            # (IP is extracted from x-forwarded-for by DRL)
            include_peer_certificate: false
```

```bash
kubectl apply -f envoyfilter-drl-ratelimit.yaml
```

## Step 4 — Verify

### Check filter is injected

```bash
# Inspect sidecar config for a target pod
POD=$(kubectl get pod -l app=echo-server -o jsonpath='{.items[0].metadata.name}')
istioctl proxy-config listeners $POD --port 80 -o json | \
  jq '.[].filterChains[].filters[].typedConfig.httpFilters[] | select(.name | contains("ext_authz"))'
```

### Send test traffic

```bash
# Normal request — should pass
curl http://echo-server/anything

# Flood with requests — rate limit should kick in (HTTP 403 from ext_authz)
for i in $(seq 1 50); do curl -s -o /dev/null -w "%{http_code}\n" http://echo-server/anything; done
```

### Check DRL metrics

```bash
kubectl -n drl port-forward svc/drl 9091:9091 &
curl http://localhost:9091/metrics | grep drl_
```

## Step 5 — Tuning

### Scope

| Scope                        | EnvoyFilter `namespace` + `workloadSelector`               |
|------------------------------|------------------------------------------------------------|
| Single workload              | workload namespace + `workloadSelector.labels`             |
| All workloads in a namespace | workload namespace, no `workloadSelector`                  |
| Mesh-wide                    | `istio-system`, no `workloadSelector`                      |

### Failure mode

```yaml
failure_mode_allow: true   # fail open — DRL unavailability does NOT block traffic
failure_mode_allow: false  # fail closed — DRL unavailability blocks traffic (safer default)
```

### Timeout

The default `timeout: 0.25s` is intentionally low to minimise added latency. Increase if DRL pods are under heavy load:

```yaml
timeout: 0.5s
```

### Propagating client IP

Istio sets `x-forwarded-for` by default. Ensure DRL is configured to read the client IP from this header (or from the Envoy peer principal) for correct entity matching.

## Istio AuthorizationPolicy vs EnvoyFilter

Istio's `AuthorizationPolicy` can call an external authorizer (including DRL) through the `action: CUSTOM` extension point, which is cleaner than a raw `EnvoyFilter`:

```yaml
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: drl-ratelimit
  namespace: default
spec:
  selector:
    matchLabels:
      app: echo-server
  action: CUSTOM
  provider:
    name: drl-provider     # defined in MeshConfig
  rules:
    - {}                   # match all requests
```

To use this approach, register DRL as an extension provider in the Istio `MeshConfig`:

```yaml
# meshconfig patch (istioctl install --set values.meshConfig.extensionProviders=...)
extensionProviders:
  - name: drl-provider
    envoyExtAuthzGrpc:
      service: drl.drl.svc.cluster.local
      port: 8081
      timeout: 250ms
      failOpen: false
```

Apply via:

```bash
istioctl install --set profile=default \
  --set 'meshConfig.extensionProviders[0].name=drl-provider' \
  --set 'meshConfig.extensionProviders[0].envoyExtAuthzGrpc.service=drl.drl.svc.cluster.local' \
  --set 'meshConfig.extensionProviders[0].envoyExtAuthzGrpc.port=8081'
```

This approach is preferred when possible because it integrates with Istio's RBAC model and makes the policy visible in `istioctl analyze`.

## Troubleshooting

| Symptom                          | Likely cause                                    | Fix                                                          |
|----------------------------------|-------------------------------------------------|--------------------------------------------------------------|
| All requests return 403          | DRL blocklist triggered or filter misconfigured | Check DRL logs: `kubectl -n drl logs -l app=drl`             |
| Requests hang (timeout)          | DRL unreachable or `connect_timeout` too low    | Verify `drl` Service exists; check cluster DNS               |
| EnvoyFilter not applied          | Namespace or label mismatch                     | `istioctl analyze` for validation errors                     |
| DRL pods not forming cluster     | Memberlist can't reach headless Service DNS     | Confirm `drl-headless` resolves: `kubectl -n drl exec ... -- nslookup drl-headless.drl.svc.cluster.local` |
| High latency on all requests     | `failure_mode_allow: false` + slow DRL          | Increase timeout or switch to `fail_open: true` temporarily  |
