---
title: Accounting
description: Shadow accounting model, entity hashing, consistent-ring ownership, and batched flushing in DRL.
weight: 5
---

The `internal/accounting` package implements the **shadow accounting** model: request counters are updated
_asynchronously_ in the background so that the Envoy response path never blocks on a counter write.

## Entity model

A rate-limiting entity is not just an IP address. DRL computes a composite key from:

```
entity_key = xxHash64(sourceIP + "|" + uriPath + "|" + sortedHeaders)
```

Headers included in the hash are **explicitly enumerated per rule** in the configuration:

```kdl
accounting {
    rules {
        payments-api {
            path-prefix "/api/v1/payments"
            headers "X-API-Key" "X-Tenant-ID"
            limit 500
            per "minute"
        }
    }
}
```

Only the listed header names are considered; their values are included in the hash. This gives fine-grained
control: a single IP can operate under different rate limits depending on which API key it uses.

## Ownership via consistent hashing

The entity key is mapped to an **owner node** using a consistent hash ring (backed by
[buraksezer/consistent](https://github.com/buraksezer/consistent)). The owner is the single source of truth
for that entity's counter.

{{< mermaid >}}
flowchart TD
    R[gRPC Request\nIP + Path + Headers] --> H[xxHash64\nentity key]
    H --> C{Consistent Ring\nGetOwner}
    C -->|This node| L[Increment\nAccountingCache]
    C -->|Remote node| Q[Enqueue to\nFlusher buffer]
    L --> T{Counter ≥ Limit?}
    T -->|Yes| B[Add to BlocklistCache\n+ BroadcastBlock]
    T -->|No| OK[OK]
    Q --> F[Batch flush\nvia UDP to owner]
{{< /mermaid >}}

When the hash ring changes (node join or leave), ownership of some keys shifts to different nodes.
The graceful handover mechanism (see [Membership]({{< ref "membership" >}})) transfers counters to new owners before a
node exits.

## Batched flushing

Non-owner nodes accumulate increments in a per-owner in-memory buffer. A background goroutine (the _Flusher_)
drains these buffers on a configurable schedule:

```kdl
accounting {
    settings {
        flush-interval "200ms"   // How often to drain buffers
        max-batch-size 1000      // Immediate flush when buffer reaches this size
    }
}
```

Batches are serialised as **Protobuf `CounterBatch`** messages and sent via `memberlist.SendBestEffort` (UDP).
UDP fits DRL's "Availability > Consistency" philosophy: if a batch is dropped, the counter on the owner will
be slightly low for one flush interval — an acceptable trade-off that keeps the request path clean.

### Zero-copy optimization

`sync.Pool` reuses `CounterBatch` objects so that high-throughput paths do not generate GC pressure.

### Flusher failure handling

If a UDP send to the owner fails (network timeout, node unreachable), the Flusher **logs a warning and
discards the batch**. It does not retry and does not fail the originating Envoy request. The next flush
cycle will deliver fresh increments.

## Bulk load

The Internal HTTP API exposes a `POST /accounting/load` endpoint that injects entities directly into the
accounting cache for testing and cache warm-up purposes. Entities loaded via bulk load never trigger blocking,
even if their counts exceed the configured rule limit.

| Outcome | Meaning |
|---------|---------|
| `accepted_local` | Entity owned by this node; counter incremented locally |
| `accepted_remote` | Entity owned by a remote node; forwarded via the Flusher (requires `distributionEnabled=true`) |
| `dropped` | Entity is non-local and `distributionEnabled=false`, or Flusher not configured |
| `no_match` | No accounting rule matched the entity's path |
| `invalid` | JSON parsing failed or required fields missing |

Bulk-load outcomes are exported as the `drl_accounting_bulk_load_total{result=...}` Prometheus counter.

## Rule configuration reference

```kdl
accounting {
    settings {
        algorithm "sliding-window"      // Rate limiting algorithm (only "sliding-window" currently)
        retry-after-type "delay-seconds" // Retry-After header format: "delay-seconds" or "http-date"
        flush-interval "200ms"
        max-batch-size 1000
    }

    rules {
        <rule-name> {
            path-prefix "/api/v1/..."   // URI path prefix to match
            headers "Header-Name"       // Zero or more headers to include in the entity key
            limit 1000                  // Request count threshold
            per "minute"                // Window unit: "second" or "minute"
        }
    }
}
```

Multiple rules can co-exist. DRL evaluates rules in order and applies the **first matching** rule to an entity.

## Overriding rules via environment variables

Individual rules can be injected or overridden without a config file using `DRL_RULE_<rule-name>_JSON`.
The variable value is a JSON object with the same fields as the KDL rule block.

```bash
# Override a single rule limit for staging
DRL_RULE_payments_api_JSON='{"path-prefix":"/api/v1/payments","limit":50,"per":"minute"}'

# Add a rule that does not exist in the base KDL config
DRL_RULE_health_JSON='{"path-prefix":"/health","limit":10,"per":"second"}'
```

**Merge semantics:** env-var rules are applied after the KDL file is parsed. A rule name present in an
env var overwrites the corresponding KDL rule; all other KDL rules remain unchanged. This makes it
straightforward to ship a shared base config and tune per-environment limits without duplicating the
full rule set.

**Field reference (JSON keys):**

| JSON key | Required | Description |
|----------|----------|-------------|
| `path-prefix` | Yes | URI path prefix to match |
| `headers` | No | Array of header names to include in the entity key |
| `redactions` | No | Object mapping header name → redaction regex |
| `limit` | Yes | Request count threshold |
| `per` | Yes | Window unit: `"second"` or `"minute"` |
