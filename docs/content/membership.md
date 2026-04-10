---
title: Membership
description: Cluster formation, gossip protocol, warm-bootstrap state sync, and block propagation in DRL.
weight: 3
---

The `internal/membership` package manages everything related to cluster topology: peer discovery, failure detection,
full-state warm-bootstrap when a node joins, reliable block-event propagation, and graceful state handover when a
node leaves.

It wraps **Hashicorp Memberlist** and exposes a small, stable surface to the rest of DRL.

## Key types

| Type | Purpose |
|------|---------|
| `SerfManager` | Owns the Memberlist instance, drives gossip, broadcasts block/unblock events |
| `Ring` | Consistent hash ring — maps entity keys to owner node names |
| `PeerClient` | Outbound gRPC connections to peer DRL nodes for direct increments |
| `StateDelegate` | Memberlist delegate that handles Push/Pull state sync (warm-bootstrap) |
| `Handover` | Graceful counter evacuation when this node is shutting down |

## Peer discovery

DRL discovers cluster peers by **DNS resolution** of a configurable service name (default `drl`). This works
out-of-the-box with Kubernetes headless services and Docker Compose service names.

```kdl
membership {
    service-name "drl"   // DNS name to resolve
    port 7946            // Memberlist gossip port
    bind-addr "0.0.0.0"
    startup-delay "3s"   // Wait before joining (let DNS propagate)
}
```

On startup the node:
1. Resolves the service name → list of IP addresses
2. Filters out its own address
3. Calls `memberlist.Join(peers)` to enter the cluster

## Warm-bootstrap (state sync)

A freshly started node must learn the current blocklist **before** serving Envoy traffic. Without this, a rolling
update would open a "vulnerability window" where a blocked entity can slip through the new node.

The `StateDelegate` implements the Memberlist `Delegate` interface. When a new node joins, Memberlist
automatically initiates a **TCP Push/Pull** exchange with an existing peer:

{{< mermaid >}}
sequenceDiagram
    participant N as New Node
    participant DNS as DNS
    participant P as Existing Peer

    N->>DNS: Resolve service-name
    DNS-->>N: [10.0.0.1, 10.0.0.2]
    N->>P: Join (Memberlist TCP)
    P->>N: Push full Blocklist snapshot
    N->>N: MergeSnapshot → hydrate BlocklistCache
    Note over N: Node reports Ready
    N-->>N: Start serving gRPC
{{< /mermaid >}}

The node only reports **Ready** after the initial sync completes (or `cache.sync-timeout-seconds` expires — an
acceptable fallback to avoid blocking deployments).

## Gossip tuning

Gossip parameters directly affect how quickly block events converge across the cluster:

| KDL key | Env var | Default | Effect |
|---------|---------|---------|--------|
| `membership.gossip-interval` | `DRL_MEMBERSHIP_GOSSIP_INTERVAL` | `50ms` | How often gossip rounds fire |
| `membership.gossip-nodes` | `DRL_MEMBERSHIP_GOSSIP_NODES` | `5` | Peers contacted per round |

Lower intervals and higher fan-out increase convergence speed at the cost of network bandwidth. For a 10-node
cluster the defaults achieve sub-second convergence.

## Block event propagation

When a rate-limit threshold is breached, the owner node must propagate the block to all peers so they can enforce
it on their local Envoy at O(1) cost.

{{< mermaid >}}
sequenceDiagram
    participant O as Owner Node
    participant P1 as Peer 1
    participant P2 as Peer 2

    O->>O: Counter exceeds limit
    O->>O: Add entity to local BlocklistCache
    O->>P1: SendReliable(BlockEvent) [TCP]
    O->>P2: SendReliable(BlockEvent) [TCP]
    P1->>P1: Add entity to local BlocklistCache
    P2->>P2: Add entity to local BlocklistCache
    Note over O,P2: All nodes now reject this entity at O(1)
{{< /mermaid >}}

`SendReliable` uses TCP and guarantees delivery. This is intentionally more expensive than the best-effort UDP
used for accounting — correctness of blocklist propagation is non-negotiable.

## Message envelope

All inter-node messages share a single **Protobuf `DrlMessage`** envelope with a `oneof` discriminator. This
simplifies routing and avoids the overhead of separate message-type registrations.

| Message type | Transport | Purpose |
|-------------|-----------|---------|
| `CounterBatch` | `SendBestEffort` (UDP) | Batched accounting increments from non-owner to owner |
| `BlockEvent` | `SendReliable` (TCP) | Notify peers to add entity to blocklist |
| `UnblockEvent` | `SendReliable` (TCP) | Notify peers to remove entity from blocklist |
| `HandoverPayload` | `SendReliable` (TCP) | Counter evacuation during graceful shutdown |

## Gossip encryption

Memberlist traffic can be encrypted with AES. DRL supports key rotation — the first key is used for encryption,
additional keys are accepted for decryption only.

```kdl
membership {
    secret-keys "primary-aes-key-16b" "old-key-for-rotation"
}
```

Environment override for key rotation without a config file change:

```bash
export DRL_MEMBERSHIP_PRIMARY_KEY="new-key-16-bytes"
export DRL_MEMBERSHIP_SECONDARY_KEYS="old-key-16-bytes,older-key-16bytes"
```

All keys must be valid AES lengths: **16, 24, or 32 bytes**.

## Graceful state handover

When a DRL node receives `SIGTERM`, it evacuates its accounting counters to a healthy peer (the _Adopter_) so
that rate-limit counters are not lost during rolling updates.

{{< mermaid >}}
sequenceDiagram
    participant L as Leaving Node
    participant A as Adopter (peer)
    participant R as Hash Ring

    Note over L: SIGTERM received
    L->>L: Stop gRPC + API servers
    L->>L: Flush pending accounting batches
    L->>L: Snapshot local AccountingCache
    L->>L: Zstd-compress snapshot
    L->>A: SendReliable(HandoverPayload)
    A->>A: Decompress + unmarshal CounterBatch
    Note over A: Wait 2s settling period
    A->>R: Recalculate ownership after ring converges
    A->>A: Merge local entries into AccountingCache
    A->>A: Enqueue remote entries to Flusher
{{< /mermaid >}}

The **settling period** (2 seconds) exists because the consistent hash ring must converge across all remaining
nodes after the departing node leaves. If the adopter re-calculates ownership before the ring update propagates,
entries may be routed to the departed node.

If the adopter is also shutting down, it rejects the handover immediately and the sender retries with the next
available peer. A hard timeout of **10 seconds** prevents the handover from blocking the orchestrator.
