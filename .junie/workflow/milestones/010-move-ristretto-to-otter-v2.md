# 010-move-ristretto-to-otter-v2.md

## Goal

Replace the existing Ristretto-based caching layer with `github.com/maypok86/otter/v2`. This migration aims to simplify
the implementation of the management API by utilizing Otter's native key-iteration support while maintaining or
improving high-concurrency performance and zero-GC overhead.

## Requirements

### 1. Cache Layer Refactoring (`internal/cache`)

* **Library Migration**: Replace `github.com/dgraph-io/ristretto/v2` with `github.com/maypok86/otter/v2` throughout the
  project.
* **Blocklist Cache**: Migrate the global replicated store to use Otter's `Builder` with S3-FIFO eviction policy.
* **Accounting Cache**: Refactor the partitioned store to utilize Otter's high-performance expiration and cost-based
  eviction.
* **Iteration Support**: Implement a `Range` or `List` method in the cache wrapper that leverages Otter’s thread-safe
  iteration to replace any secondary metadata indexes used in Milestone 006v2.

### 2. Configuration & Resource Management

* **Builder Patterns**: Update the initialization logic to use Otter's fluent API.
* **Cost Management**: Map existing `cache.blocklist_size_mb` and `cache.accounting_size_mb` settings to Otter's
  `MaxCost`.
* **Statistics**: Adapt the Prometheus metrics to collect data from Otter's `Stats()` (hits, misses, evictions, etc.).

### 3. API & Interface Stability

* **Internal Stability**: Keep the `internal/cache/manager.go` interface consistent to avoid cascading changes in the
  `grpc`, `api`, and `membership` packages.
* **Entity Listing**: Refactor `GET /blocked-entity` to use the new native iteration, ensuring the response still
  includes `id`, `ip`, `uriPath`, `headers`, and `expires_at`.

### 4. Testing & Validation

* **Unit Tests**: Update all tests in `internal/cache/*_test.go` to verify Otter-specific behavior, such as expiration
  precision and iteration.
* **Race Detection**: Run all tests with the `-race` flag (via `mise run task build`) to ensure the lock-free nature of
  Otter is correctly integrated.

## Implementation Guidelines

* **S3-FIFO Utilization**: Take advantage of Otter's default S3-FIFO algorithm, which often provides better hit ratios
  than the TinyLFU used in Ristretto.
* **Cleanup**: Remove all Ristretto-related dependencies from `go.mod` and `go.sum`.
* **Efficiency**: Maintain the `sync.Pool` usage for serialization during state sync to keep the "zero-GC" goal intact.
