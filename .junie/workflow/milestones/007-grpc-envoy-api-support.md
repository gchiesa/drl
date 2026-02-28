# 007-grpc-envoy-api-support.md

## Goal

Establish the gRPC server within DRL to handle `CheckRequest` calls from Envoy's `ext_authz` filter. The system will
correctly parse the incoming request metadata (IP, Path, and Headers) but will initially operate in a "Dry Run" mode,
returning `OK` for all requests to ensure connectivity and baseline performance before the accounting logic is fully
integrated.

## Requirements

### 1. gRPC Server Implementation

* **Protocol**: Use the `envoyproxy/go-control-plane` library to implement the `ratelimit.v3` or `ext_authz.v3` gRPC
  interface.
* **Port**: Listen on the configured gRPC port (default `:8081`).
* **Logic (No-Op)**: For this milestone, every `Check` call must return an `OK` response to Envoy.
* **Request Parsing**:
* Extract the **Source IP** from the peer attributes or headers.
* Extract the **URI Path** from the request attributes.
* Extract relevant **Headers** as defined in the entity model.

### 2. Envoy Configuration Update

Update the `envoy.yaml` in the `docker-compose` environment to activate the `ext_authz` HTTP filter.

* **Cluster**: Define a new cluster named `drl-cluster` pointing to the `drl` service on port `8081`.
* **Filter Configuration**:
* Type: `envoy.filters.http.ext_authz`.
* Transport: gRPC.
* Failure Mode: `allow_at_failure: true` (ensures traffic flows even if DRL is down, adhering to "Availability >
  Consistency").

### 3. Observability

* **Metrics**: Increment `drl_grpc_check_total` for every incoming request.
* **Logging**: Log `DEBUG` info for each request: `Received Check: IP=[...], Path=[...], Headers=[...]`.

## Implementation Guidelines

* **Package Structure**: Create `internal/grpc/server.go` to house the Envoy-compatible gRPC service implementation.
* **Concurrency**: Ensure the gRPC server handles requests concurrently without blocking the main event loop.
* **Mise Integration**: Update `mise.toml` to include any necessary protobuf generation tasks if you customize the
  stubs.

## Validation Criteria

1. **Connectivity**: Envoy successfully forwards requests to DRL, and DRL logs the incoming metadata (IP, Path,
   Headers).
2. **No-Op Pass**: `k6` load tests show `200 OK` for all traffic, confirming that DRL is not blocking yet.
3. **Metrics**: The `/metrics` endpoint shows an increasing count for `drl_grpc_check_total` during the load test.
4. **Resilience**: Shutting down the DRL container does not stop the `echo-server` traffic (due to
   `allow_at_failure: true` in Envoy).
