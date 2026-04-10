---
title: Metrics
description: Full reference for the Prometheus metrics exported by DRL, organized by subsystem with label definitions and operational guidance.
weight: 7
---

DRL exposes a Prometheus-compatible metrics endpoint on the standard port (default `9090`) at `/metrics`.
A `/health` liveness endpoint is available on the same port.

The endpoint uses a **dedicated private registry** — it does not expose Go runtime or process metrics by
default, keeping the surface clean and DRL-specific.

## Endpoint configuration

```kdl
metrics {
    port 9090
}
```

Scrape target for Prometheus:

```yaml
- job_name: drl
  static_configs:
    - targets: ["<drl-host>:9090"]
```

---

## Metric reference

### gRPC

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `drl_grpc_check_total` | Counter | — | Total `ShouldRateLimit` requests received from Envoy. This is the primary throughput signal. |
| `drl_grpc_response_code_total` | Counter | `code` | Responses broken down by decision. `code` values: `OK`, `OVER_LIMIT`. Use the ratio `OVER_LIMIT / (OK + OVER_LIMIT)` as the block rate. |

**Key alerts:**
- Sudden drop in `drl_grpc_check_total` → DRL is unreachable from Envoy.
- Sustained spike in `OVER_LIMIT` share → active attack or misconfigured rate limit.

---

### Rate limiting

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `drl_ratelimit_blocks_total` | Counter | `rule_name`, `reason` | Entities newly added to the blocklist. `reason` values: `rate_exceeded`, `manual`. |
| `drl_ratelimit_propagation_latency_ms` | Histogram | — | Time in milliseconds from the moment a block is decided on the owner node to the moment the gossip event is received cluster-wide. Buckets: 1 ms → ~2 s (exponential ×2, 12 steps). |

**Key alerts:**
- `drl_ratelimit_propagation_latency_ms` p99 > 500 ms → gossip is lagging; check cluster network and `drl_membership_cluster_size`.

---

### Cache

Both `blocklist` and `accounting` report under the same metric names, distinguished by the `cache_type` label.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `drl_cache_hits_total` | Counter | `cache_type` | Successful lookups. |
| `drl_cache_misses_total` | Counter | `cache_type` | Failed lookups (entry absent or expired). |
| `drl_cache_evictions_total` | Counter | `cache_type` | Entries evicted due to memory pressure (`MaximumWeight` reached). Non-zero evictions on the blocklist mean blocked entities are being silently dropped — increase `blocklist_max_size_mb`. |
| `drl_cache_memory_bytes` | Gauge | `cache_type` | Estimated current memory consumption reported by the cache (based on the weigher — see [Sizing Guide](../sizing/) for the relationship to actual RSS). |
| `drl_sync_duration_seconds` | Histogram | — | Time taken for the initial Push/Pull state sync on node startup. Buckets: 1 ms → ~16 s. High values indicate a large blocklist is being transferred or network latency between nodes is elevated. |

`cache_type` label values: `blocklist`, `accounting`.

**Key alerts:**
- `drl_cache_evictions_total{cache_type="blocklist"}` > 0 → blocklist is full; blocked entities are being silently dropped.
- `drl_cache_memory_bytes` approaching `max_size_mb × 1,048,576` → consider increasing the configured limit.

---

### Accounting

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `drl_accounting_local_increments_total` | Counter | — | Counter increments processed directly by this node (this node is the consistent-hash owner for the entity). |
| `drl_accounting_remote_increments_total` | Counter | — | Counter increments forwarded to a remote owner node via UDP batch. |
| `drl_accounting_flush_total` | Counter | — | Number of UDP `CounterBatch` flushes sent to peer nodes. Each flush bundles multiple increments. |
| `drl_accounting_msg_recv_total` | Counter | — | UDP `CounterBatch` messages received and processed. |
| `drl_accounting_bulk_load_total` | Counter | `result` | Entries processed via the private bulk-load API. `result` values: `no_match`, `accepted_local`, `accepted_remote`, `dropped`, `invalid`. |

The ratio `remote / (local + remote)` reflects how evenly traffic is distributed across the hash ring.
In a balanced N-node cluster this should be approximately `(N−1) / N`.

**Key alerts:**
- `drl_accounting_remote_increments_total` near zero on a multi-node cluster → the hash ring may not have converged; check `drl_membership_cluster_size`.

---

### Membership

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `drl_membership_cluster_size` | Gauge | — | Current number of live members as seen by this node's memberlist. Expected value equals the number of running DRL instances. |
| `drl_membership_events_total` | Counter | `event_type` | Membership change events. `event_type` values: `join`, `leave`, `update`, `reap`. |
| `drl_membership_reliable_msgs_total` | Counter | — | Messages sent via memberlist's reliable (TCP) transport (used for large payloads such as full-state Push/Pull). |
| `drl_membership_best_effort_msgs_total` | Counter | — | Messages sent via memberlist's best-effort (UDP) transport (used for gossip and block broadcast events). |

**Key alerts:**
- `drl_membership_cluster_size` < expected node count → a node has left or become unreachable; block propagation will be incomplete until it recovers or is replaced.

---

### Handover

Handover metrics track the graceful counter-migration that happens when a node leaves the cluster, ensuring
the departing node's owned accounting counters are transferred to a new owner before it shuts down.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `drl_accounting_handover_out_entities` | Counter | — | Entities exported by this node during a handover (leaving-node perspective). |
| `drl_accounting_handover_in_entities` | Counter | — | Entities received and imported by this node during a handover (adopter-node perspective). |
| `drl_accounting_handover_duration_ms` | Histogram | — | End-to-end handover time in milliseconds. Buckets: 10 ms → ~20 s. |
| `drl_accounting_handover_failed_total` | Counter | — | Failed handover attempts. A non-zero value means some counter state was lost during a rolling update. |

**Key alerts:**
- `drl_accounting_handover_failed_total` > 0 → investigate network connectivity between nodes at shutdown time.

---

## Recommended Grafana dashboard panels

| Panel | Query | Purpose |
|-------|-------|---------|
| Request rate | `rate(drl_grpc_check_total[1m])` | Overall throughput |
| Block rate | `rate(drl_grpc_response_code_total{code="OVER_LIMIT"}[1m]) / rate(drl_grpc_check_total[1m])` | Fraction of requests blocked |
| New blocks/s | `rate(drl_ratelimit_blocks_total[1m])` | Rate of new entity blocks |
| Propagation p99 | `histogram_quantile(0.99, rate(drl_ratelimit_propagation_latency_ms_bucket[5m]))` | Worst-case gossip convergence |
| Cluster size | `drl_membership_cluster_size` | Live node count |
| Blocklist evictions | `rate(drl_cache_evictions_total{cache_type="blocklist"}[5m])` | Memory pressure on blocklist |
| Blocklist memory | `drl_cache_memory_bytes{cache_type="blocklist"}` | Current blocklist footprint |
| Accounting balance | `rate(drl_accounting_remote_increments_total[1m]) / (rate(drl_accounting_local_increments_total[1m]) + rate(drl_accounting_remote_increments_total[1m]))` | Hash ring load balance (target ≈ `(N−1)/N`) |
