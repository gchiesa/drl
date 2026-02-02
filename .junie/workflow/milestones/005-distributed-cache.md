# 005-distributed-cache.md

## Goal

Implement a dual-purpose, memory-bound caching layer to handle global blocklists (fully replicated) and shadow
accounting (partitioned). This system ensures DRL starts with immediate awareness of existing violations and manages
quota tracking with zero external database latency.

## Requirements

### 1. Dual-Cache Architecture (Ristretto)

* **Blocklist Cache**: A global, fully replicated in-memory store for banned IPs.
    - **Cost Management**: Configure using `MaxCost` in bytes to ensure it stays within the allocated MB limit.

* **Shadow Accounting Cache**: A partitioned store for tracking request counts.
    - **Partitioning**: Use **Consistent Hashing** (e.g., `xxHash` or `FNV-1a`) to determine which node "owns" an IP's
      counter.

* **Configuration**: Add `cache.blocklist_size_mb` and `cache.accounting_size_mb` to the KDL configuration engine.

### 2. Warm Bootstrapping (State Sync)

* **Protocol**: Implement the `memberlist.Delegate` interface, specifically utilizing `LocalState` and
  `MergeRemoteState` for **TCP Push/Pull**.
* **Bootstrap Flow**:

    1. On startup, the node joins the Memberlist cluster.
    2. It identifies a "sync peer" (the oldest or a random existing member).
    3. It performs a bulk transfer of the current **Blocklist Data** using a binary serialization format (e.g., *
       *MessagePack** or **Protobuf**) for efficiency.

* **Readiness Gate**: The node must block internal API and gRPC traffic until `MergeRemoteState` completes or a
  timeout (e.g., 30s) is reached.

### 3. Observability & Metrics

* **Prometheus Export**: Expose the following via the `:9091/metrics` endpoint:
    - `drl_cache_hits_total`: Total hits for blocklist/accounting.
    - `drl_cache_evictions_total`: Number of items evicted due to memory pressure.
    - `drl_cache_memory_bytes`: Current memory usage of each cache instance.
    - `drl_sync_duration_seconds`: Time taken for the initial state sync.


* **Logging**:
    - `INFO`: "State sync started from peer [IP]", "State sync complete: received [X] records".
    - `DEBUG`: Eviction events and hash partitioning decisions.

## Implementation Guidelines

* **Package Structure**: Create `internal/cache` for the Ristretto wrappers and `internal/membership/delegate.go` for
  the state sync logic.
* **Efficiency**: Use a `sync.Pool` for serialization buffers during state sync to minimize GC pressure during large
  transfers.

