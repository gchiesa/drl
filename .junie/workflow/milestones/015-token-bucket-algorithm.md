# 015-token-bucket-algorithm.md

## Goal

Implement the **Token Bucket** algorithm as a high-performance alternative to the existing "sliding-window" counter.
This milestone also adds a new configuration introspection endpoint to the Internal API to verify the active algorithm
and system settings at runtime.

## Requirements

### 1. Token Bucket Implementation (`internal/ratelimit`)

* **Algorithm**: Implement a stateful Token Bucket where:
    * `tokens = min(capacity, current_tokens + (elapsed_time * refill_rate))`
    * A request is allowed if `tokens >= 1`, consuming exactly one token.
* **State Management**: Each entity must track its `last_refill_timestamp` and current `token_count` within the
  `AccountingCache`.
* **Concurrency**: Ensure thread-safe updates to bucket state using atomic operations or mutexes, consistent with
  `internal/accounting/engine.go` patterns.

### 2. Configuration & Validation (`internal/config`)

* **KDL Schema**: Update the `accounting` block to support the new algorithm and its parameters:
    ```kdl
    accounting {
        settings {
            algorithm "token-bucket"
            capacity 100        // Burst size
            refill-rate 10      // Tokens per second
        }
    }
    ```
* **Validation**: Add logic to `config.go` to ensure `capacity > 0` and `refill-rate > 0` when the token-bucket
  algorithm is selected.
* **Precedence**: Ensure environment variables like `DRL_ACCOUNTING_SETTINGS_ALGORITHM` correctly override KDL settings.
  Use the struct tag (e.g. ` env:"GOSSIP_INTERVAL"`) to automatically implement retrieval of configuration from the
  environment variable, similarly to `MembershipConfig` (`internal/config/config.go:91`). Update the accounting
  structure to use the env tags and align with the other config struct implementation.

### 3. Observability & Metrics (`internal/metrics`)

* **Metrics Extension**: Extend `internal/metrics/metrics.go` to track:
    * `drl_ratelimit_tokens_consumed_total`: Counter for total tokens spent.
    * `drl_ratelimit_bucket_exhausted_total`: Counter for requests denied due to empty buckets.
    * `drl_ratelimit_tokens_current`: Gauge (optional/sampled) for remaining tokens per active entity.

### 4. Private API: Configuration Discovery

* **Endpoint**: `GET /configuration/static/:section`
* **Access Control**: Protected by **Digest Authentication (SHA-256)**.
    * **Functionality**:
        * Returns the JSON representation of the requested top-level KDL section (e.g., `accounting`, `membership`,
          `cache`).
    * **Example**: `GET /configuration/static/accounting` returns:
      ```json
      {
        "settings": {
          "algorithm": "sliding-window",
          "retry-after-type": "delay-seconds"
        },
        "rules": {
          "catch-all": {
            "path-prefix": "/",
            "limit": 100,
            "per": "minute"
          },
          "anything": {
            "path-prefix": "/anything",
            "limit": 10,
            "per": "minute"
          }
        }
      }
      ```
* **Implementation**: Add the handler to `internal/api/handlers_config.go` and wire it in `internal/api/api.go`.

### 5. Documentation

* **README.md**: Add the Token Bucket to the "Algorithm" section and document the new `/configuration/static` endpoint.
* **Hugo Documentation**: Update the Hugo based markdown documents where necessary.
* **Architecture**: Update `CLAUDE.md` to reflect the move toward supporting multiple rate-limiting strategies. Add API
  example for internal-api docs.
