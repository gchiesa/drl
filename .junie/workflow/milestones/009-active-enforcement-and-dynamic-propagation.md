# 009-active-enforcement-and-dynamic-propagation.md

## Goal

Transform DRL into an active rate limiter by integrating the Accounting Engine with the Blocklist Cache. This milestone
implements threshold detection, immediate cluster-wide propagation of violations, and the transition of the gRPC server
from "Dry Run" to "Active Blocking" with 429 responses.

## Requirements

### 1. Rate Limiting Strategy (Interface & Implementation)

* **Abstraction**: Define a `RateLimiter` interface in `internal/ratelimit` to allow for multiple algorithms.
* **Default Algorithm**: Implement the **Sliding Window Log** or **Leaking Bucket**. Given the
  Ristretto-based accounting, a sliding window or fixed window is recommended as default for high concurrency.
* **Configuration**: Update KDL to support algorithm selection and `Retry-After` settings:

```kdl
accounting {
    settings {
        algorithm "sliding-window" // Default
        retry-after-type "delay-seconds" // or "http-date"
    }
    rules {
        "api-limit" {
            path-prefix "/api"
            limit 100
            per "minute"
        }
    }
}

```

### 2. Threshold Detection & Immediate Sync

* **Local Violation**: When an "Owner Node" detects a counter exceeding a rule's limit:

1. **Commit Local**: Immediately add the Entity to the local `BlocklistCache` with a TTL derived from the rule's `per`
   window.
2. **Broadcast**: Use the `Serf/Memberlist` event bus to broadcast a `BlockEvent` to all peers.


* **Global Convergence**: Upon receiving a `BlockEvent`, peers must update their local `BlocklistCache` immediately.
* **Refactor**: Rename/Move the current `accounting` engine logic into an `internal/enforcement` or `internal/ratelimit`
  package if necessary to clarify its role as a decision-maker.

### 3. gRPC Enforcement (Envoy Integration)

* **Active Check**: Update `internal/grpc/server.go` to query the `BlocklistCache` **before** any accounting logic.
* **429 Response**: If an entity is blocked:
* Return `code: UNAVAILABLE` or map to HTTP `429 Too Many Requests`.
* Inject the `Retry-After` header into the `CheckResponse` dynamic metadata/headers.


* **Short-Circuit**: If blocked, skip the asynchronous accounting increment to save resources.

### 4. Administrative API Extensions

* **Unblock Endpoint**: `DELETE /blocked-entity/:ip/_path/*`
* Removing a block via API must trigger a cluster-wide `UnblockEvent` to clear the cache on all nodes.


* **Listing**: Ensure `GET /blocked-entity` (from Milestone 006v2) correctly reflects entities blocked automatically by
  the engine vs. those added manually.

### 5. Observability & Metrics

* `drl_ratelimit_blocks_total`: Counter (labels: `rule_name`, `reason`).
* `drl_ratelimit_propagation_latency_ms`: Histogram measuring time from local block to cluster-wide convergence.
* `drl_grpc_response_code_total`: Counter (labels: `code`, e.g., `OK`, `DENIED`).

## Implementation Guidelines

* **Concurrency**: Ensure the transition from "Owner Node counter update" to "Cluster-wide broadcast" is non-blocking
  for the UDP flusher.
* **Circular Protection**: Ensure that a `BlockEvent` received from a peer does not trigger a re-broadcast (Max Hops =
  1).
* **Header Consistency**: For `Retry-After: <http-date>`, use `time.RFC1123` (e.g., `Sun, 06 Nov 1994 08:49:37 GMT`).


