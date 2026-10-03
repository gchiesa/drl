# 021-cache-repository-abstraction-and-redis-backend

## Goal

Implement a high-performance abstraction layer (Repository pattern) for cache storage to support operating either with
the existing in-memory Otter cache or optionally with an external Redis backend. Ensure startup readiness gating,
configurable retry-with-backoff behavior on connection failures, dedicated Redis metrics, and proper degradation
handling (disabling state handover when Redis is active).

## Requirements

### 1. Cache Storage Repository Abstraction (`internal/cache`)

* Implement a `Repository` interface (or refactor `Manager`) in `internal/cache` to decouple caching interactions from
  the underlying store implementation.
* Provide two concrete implementations of the repository interface:
    * **In-Memory Store**: Wraps the current `Otter` cache implementation.
    * **Redis Store**: Implemented using `go-redis/redis/v9` (or a high-performance Redis client library).
* Support both **Blocklist** and **Shadow Accounting** operations through the abstraction layer.

### 2. Configuration Package (`internal/config`)

* Extend KDL schema and configuration structures under a `cache` block:
  ```kdl
  cache {
      provider "redis" // Options: "in-memory" (default), "redis"
      
      redis {
          addrs ["localhost:6379"] // Support standalone or cluster/sentinel endpoints
          password ""
          db 0
          max-retries 5
          retry-backoff-min "100ms"
          retry-backoff-max "2s"
          connect-timeout "5s"
          dial-timeout "3s"
          read-timeout "3s"
          write-timeout "3s"
          pool-size 10
      }
  }

  ```

* Support environment variable overrides following standard conventions (`DRL_CACHE_PROVIDER`, `DRL_CACHE_REDIS_ADDRS`,
  etc.).
* Implement strict validation in `Validate()` (e.g., verifying `provider` is valid and required Redis settings are
  present when selected).

### 3. Startup Readiness Gate & Connection Resilience

* **Readiness Block**: DRL must **not** accept traffic or set itself as ready until the cache connection to Redis is
  successfully established and verified (e.g., `Ping()`).
* **Retry with Backoff & Jitter**: If Redis is configured but unreachable on startup:
* Retry connection up to `max-retries` / `X` attempts.
* Use exponential backoff with randomized jitter between `retry-backoff-min` and `retry-backoff-max`.
* If all retry attempts fail, log detailed error messages stating the connection failure details and exit DRL
  immediately with a non-zero exit code (`log.Fatal`).


* **Logging**: Output clear structured logs showing connection attempts, backoff delays, and successful connection/ready
  state.

### 4. Behavior Adjustments & Shutdown Handover Control

* When `provider` is set to `"redis"`:
* State handover logic (Milestone 013 in `membership` pkg) **must be disabled** during `SIGTERM` / node shutdown, as
  shared external Redis maintains state persistence natively across node restarts.
* Warm state sync (TCP Push/Pull via memberlist delegate) for blocklist/accounting should bypass local-to-peer sync when
  Redis acts as the centralized source of truth.

### 5. Observability & Prometheus Metrics (`internal/metrics`)

* Expose dedicated Redis backend operational metrics via `:9091/metrics`:
* `drl_redis_command_duration_seconds` (Histogram with labels: `command`, `status`).
* `drl_redis_errors_total` (Counter with labels: `command`, `error_type`).
* `drl_redis_pool_connections_active` / `idle` (Gauges tracking connection pool health).

## Implementation Guidelines

* Maintain existing zero-GC allocation goals where possible by utilizing connection pooling and efficient binary
  serialization (e.g., Protobuf) for complex values stored in Redis.
* Ensure all unit and integration tests are isolated so tests pass when Redis is not running (skipping or using
  embedded/miniredis mock for unit test coverage).

## Validation Criteria

1. **Repository Abstraction**: Switching `cache.provider` between `in-memory` and `redis` requires zero changes to
   upstream consumers (`grpc`, `api`, `accounting`, `enforcement`).
2. **Fail-Fast Readiness**: Starting DRL with invalid Redis address configuration causes DRL to attempt retries with
   backoff/jitter, log proper error details, fail readiness check, and terminate cleanly.
3. **Successful Readiness**: With a valid Redis target, DRL passes readiness checks and transitions to accepting gRPC
   and HTTP API traffic.
4. **Shutdown Behavior**: When configured with Redis, shutting down DRL via `SIGTERM` skips memberlist state handover.
5. **Metrics**: `/metrics` reports active Redis commands, errors, and pool metrics during load testing.
