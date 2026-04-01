# 011-native-udp-flusher-to-membership.md

## Goal

Decommission the custom UDP flusher implementation in favor of `memberlist` native messaging. The system will use
`SendBestEffort` for high-frequency accounting updates and `SendReliable` for critical, immediate blocklist propagation.
This unification simplifies the network architecture and aligns DRL with its P2P core.

## Requirements

### 1. Unified Communication Layer

* **Accounting Propagation**: Refactor `internal/accounting/flusher.go` to remove direct UDP socket management.
    * Replace the UDP send logic with `memberlist.SendBestEffort(node, payload)`.
    * Continue using the 10-second/1000-entry bucket and interval logic to batch updates before sending.
    * Ensure these values are configurable via KDL but default to the high-performance settings above.
* **Blocklist Propagation**: Update the `BlockBroadcaster` in the accounting engine and the manual API handlers to use
  `memberlist.SendReliable(node, payload)` instead of the standard Serf broadcast for events requiring immediate,
  guaranteed delivery.
* **Efficiency**: Retain the **Protobuf** (`CounterBatch`) serialization for all accounting updates to ensure wire
  compatibility and low CPU overhead.

### 2. Membership Configuration Tuning

* Update the `internal/config` and `internal/membership` initialization to tune the gossip protocol for lower latency:
    * **GossipInterval**: Set to `50ms` (default was higher in `DefaultLANConfig`).
    * **GossipNodes**: Increase to `5` nodes to speed up convergence in larger clusters.
* Ensure these values are configurable via KDL but default to the high-performance settings above.

### 3. Implementation Details

* **Receiver Logic**: Update the `memberlist.Delegate` in `internal/membership/delegate.go` to handle incoming messages
  from both `SendBestEffort` (Accounting) and `SendReliable` (Blocklist).
* **Metrics**:
    * Update `drl_accounting_udp_recv` to a more generic `drl_accounting_msg_recv_total`.
    * Add `drl_membership_reliable_msgs_total` and `drl_membership_best_effort_msgs_total`.

### 4. Documentation

Update the root `README.md` to include dedicated architectural sections:

* **Internal Accounting**: Describe the batching logic, the hashing of entities to "Owner Nodes," and the transition
  from UDP to Gossip-based best-effort delivery.
* **Internal Membership**: Describe the P2P discovery mechanism, the State Sync (TCP Push/Pull) during warm-up, and the
  reliable messaging used for blocklist propagation.

## Implementation Guidelines

* **Zero-Copy**: Continue using `sync.Pool` for Protobuf objects and byte buffers during the `SendBestEffort` calls to
  maintain the "zero-GC" goal.
* **Cleanup**: Remove the internal UDP port (`7947`) and related configuration settings from the KDL schema and
  `internal/config`.
* **Reliability**: Note that `SendBestEffort` is packet-loss-tolerant, which fits the DRL philosophy of "Availability >
  Consistency" for shadow accounting.

