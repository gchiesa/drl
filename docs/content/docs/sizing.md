---
title: Memory Footprint & Deployment Sizing
description: Per-entry memory cost of the blocklist and accounting caches, capacity tables for common RAM budgets, and deployment sizing recommendations based on real-world traffic scales.
weight: 8
---

Choosing the right `blocklist_max_size_mb` and `accounting_max_size_mb` values requires understanding how
much RAM each cache entry actually consumes. This page breaks down the per-entry cost from first principles,
translates it into entry counts for common RAM budgets, and provides concrete deployment recommendations
derived from publicly documented traffic figures for well-known web services.

## How the caches work

DRL maintains two in-memory caches per node. Both are backed by
[otter v2](https://github.com/maypok86/otter) with weight-based eviction
(`MaximumWeight = MaxSizeMB × 1,048,576`; each entry is assigned a fixed weight of 100):

| Cache | Contents | Replication | Configured via |
|-------|----------|-------------|----------------|
| **Blocklist** | Banned entity hashes + optional metadata | **Fully replicated** on every node | `blocklist.max_size_mb` |
| **Accounting** | Per-entity request counters (`*atomic.Int64`) | **Sharded** by consistent hash ring | `accounting.max_size_mb` |

Because the blocklist is fully replicated, adding more DRL instances does **not** reduce per-node blocklist
memory — every node holds the complete set of banned entities. The accounting cache scales the other way:
with N nodes, each node owns roughly 1/N of the counter keyspace.

## Per-entry memory cost

Every cache key is a 16-character lowercase hex string encoding the 64-bit xxhash of the entity's canonical
form (`IP|Path[|Header:Value]...`). The key length is **always 32 bytes** (16-byte string header + 16 bytes
of data), regardless of how long the original IP, path, or headers are.

### Blocklist

Each blocklist value is a `*blocklistEntryData`, a heap-allocated struct holding a `time.Time` expiration
and an optional `*model.Entity` pointer. The entity pointer is `nil` for automatic rate-limiter blocks
(the common case) and non-nil only for admin-API blocks that need human-readable listing.

| Component | Automatic block | Admin block (no headers) | Admin block (2 headers) |
|-----------|:--------------:|:------------------------:|:-----------------------:|
| Cache key (`string` — 16-char hex) | 32 B | 32 B | 32 B |
| Otter v2 node overhead (S3-FIFO metadata) | ~64 B | ~64 B | ~64 B |
| `*blocklistEntryData` heap (`time.Time` + pointer) | 32 B | 32 B | 32 B |
| `*model.Entity` pointer | — | 8 B | 8 B |
| `Entity.IP` string (IPv4) | — | ~31 B | ~31 B |
| `Entity.Path` string (~20 chars) | — | ~36 B | ~36 B |
| `Entity.Headers` (nil map vs 2-pair map) | 8 B | 8 B | ~400 B |
| **Total per entry** | **~136 B** | **~211 B** | **~603 B** |

> **Note:** The otter weigher assigns a fixed cost of 100 per entry. Actual heap usage is 1.4×–6× higher
> depending on whether entity metadata is stored. For operational sizing, use the actual figures in the
> table above, not the configured `max_size_mb` value directly.

### Accounting

Accounting entries are simpler: the key is the same 16-char hex and the value is a `*atomic.Int64` — a
pointer to a heap-allocated 64-bit atomic counter. The pointer indirection is required because
`atomic.Int64` must not be copied after first use; storing it by value in the cache would violate
Go's atomic no-copy contract and introduce data races.

**Total per accounting entry: ~128 B** (fixed, regardless of entity profile).

## Blocklist capacity by RAM budget

| RAM budget | Entry profile | Est. bytes/entry | Max entries |
|:----------:|---------------|:----------------:|:-----------:|
| **64 MB** | Automatic blocks (no entity meta) | ~136 B | ~496 k |
| **64 MB** | Admin blocks, no headers | ~211 B | ~319 k |
| **64 MB** | Admin blocks + 2 header pairs | ~603 B | ~112 k |
| **128 MB** | Automatic blocks | ~136 B | ~993 k (~1 M) |
| **128 MB** | Admin blocks, no headers | ~211 B | ~638 k |
| **128 MB** | Admin blocks + 2 header pairs | ~603 B | ~224 k |
| **256 MB** | Automatic blocks | ~136 B | ~1.99 M |
| **256 MB** | Admin blocks, no headers | ~211 B | ~1.28 M |
| **256 MB** | Admin blocks + 2 header pairs | ~603 B | ~449 k |
| **1 GB** | Automatic blocks | ~136 B | ~7.90 M |
| **1 GB** | Admin blocks, no headers | ~211 B | ~5.09 M |
| **1 GB** | Admin blocks + 2 header pairs | ~603 B | ~1.78 M |

The **automatic blocks** profile is the common production case: the rate-limiter fires a block event
storing only the 64-bit entity hash as the key, with no original IP/path/headers retained in the value.
Admin-API blocks optionally carry the full entity struct for human-readable listing via the management
endpoints.

## Accounting cache capacity by RAM budget

Accounting entries are always ~128 B each. Because the accounting cache is sharded, the per-node memory
requirement scales with `total_unique_entities / N` for an N-node cluster.

| RAM budget per node | Entries per node | 3-node cluster | 5-node cluster | 10-node cluster |
|:-------------------:|:----------------:|:--------------:|:--------------:|:---------------:|
| 64 MB | ~524 k | ~1.57 M total | ~2.62 M total | ~5.24 M total |
| 128 MB | ~1.05 M | ~3.15 M total | ~5.24 M total | ~10.5 M total |
| 256 MB | ~2.10 M | ~6.29 M total | ~10.5 M total | ~21.0 M total |
| 1 GB | ~8.39 M | ~25.2 M total | ~41.9 M total | ~83.9 M total |

## Real-world deployment recommendations

The table below uses publicly documented traffic figures. Attack scenarios assume a volumetric DDoS with a
distributed botnet of unique source IPs, which drives the worst-case blocklist size.

| Site (scale reference) | Public traffic data | Peak blocked entities (attack) | Recommended blocklist RAM | Recommended instances | Notes |
|------------------------|--------------------|---------------------------------|:-------------------------:|:---------------------:|-------|
| **Stack Overflow / Stack Exchange** (~55 M page views/month, ~200 req/s peak — Stack Exchange blog) | ~20 req/s avg | ~5 k–20 k | 16 MB | 3 | Typical enterprise-grade API service; 64 MB is already 3× overkill |
| **Wikipedia / Wikimedia** (~15 B page views/month, ~6 k req/s avg — Wikimedia Stats) | ~6 k req/s | ~50 k–200 k | 64 MB | 3–5 | Block events propagate via gossip in < 1 s; 64 MB holds ~496 k automatic blocks |
| **Reddit** (~1.7 B page views/month, ~700 req/s API avg — Reddit Transparency) | ~700 req/s | ~200 k–1 M | 256 MB | 5 | Aggressive scraping bots drive large blocklists; accounting sharding across 5 nodes keeps per-node counter pressure manageable |
| **Twitter / X** (~200 M daily active users, ~500 M tweets/day — SEC filings) | ~100 k req/s | ~1 M–10 M | 1 GB | 10 | Fully replicated 1 GB blocklist on 10 nodes; accounting sharded to ~1/10th per node |
| **Netflix** (~220 M subscribers, ~15 % of peak US internet — Sandvine reports) | ~1 M+ req/s | ~5 M–50 M | 4 GB | 20 | Multiple independent DRL clusters per region recommended; 4 GB holds ~7.9 M automatic blocks per node |

## Configuration recipe

```kdl
cache {
    // Blocklist: fully replicated. Size it for the peak attack blocklist across
    // ALL nodes (every node holds the full set).
    blocklist_max_size_mb 256

    // Accounting: sharded. Size it for total_unique_entities / num_nodes.
    // E.g. 10 M total entities across a 5-node cluster → 2 M per node → ~256 MB.
    accounting_max_size_mb 256
}
```

> Because the otter weigher assigns a fixed cost of 100 per entry while actual heap usage is higher,
> monitor process RSS in production and tune `*_max_size_mb` to keep RSS within your pod/VM memory limit.
