# 013-state-handover-on-sigterm.md

## Goal

Implement a "Graceful State Handover" mechanism. When a DRL node receives a `SIGTERM`, it must evacuate its local
accounting state to a healthy peer (the "Adopter") before exiting. This ensures that global rate-limit counters remain
accurate during rolling updates and scale-down events.

## Requirements

### 1. The Exit Sequence (Sender/Leaver)

* **Signal Interception**: Update `main.go` to intercept `SIGTERM`. Instead of an immediate shutdown, trigger the
  `Handover` process.
* **Adopter Selection**: Use the `consistent` hash ring to find the "next" node in the ring (the neighbor) to act as the
  Adopter.
* **State Serialization**:
    * Leverage Otter's native serialization if available, or iterate through the `AccountingCache`.
    * **Compression**: Use `github.com/klauspost/compress/zstd` for the fastest possible compression/decompression
      ratio.
* **Reliable Transfer**: Use `memberlist.SendReliableMsg` to transmit the compressed payload to the Adopter.
    * If the receiver rejects the message immediately, then another adopter will be selected and the operation retried.
* **Timeout**: Implement a hard timeout (e.g., 10 seconds) for the handover. If it fails, the node must log the error
  and exit to avoid blocking the orchestrator (Kubernetes/Docker).

### 2. The Adoption Logic (Receiver)

* **Protocol Handling**: Update the `NotifyMsg` handler in `internal/membership/delegate.go` to recognize a new
  DrlMessage message type: `HandoverPayload`.
* **Adopter Health Check**: If a node receives a handover while it is *also* in a shutting-down state, it must reject
  the message.
* **Decompression & Merging**:
    * Decompress the Zstd payload.
    * Load the data into a temporary Otter cache or a structured buffer.
* **Redistribution Phase**:
    * Wait for a short "Settling Period" (e.g., 2000ms) to allow the `memberlist` cluster state to converge and
      recognize the sender has left.
    * For each entity in the received payload:
        1. Re-calculate the "Owner" using the updated Hash Ring.
        2. If the current node is the new owner, merge the counter into the local `AccountingCache`.
        3. If another node is the owner, enqueue the counter for the next `Flusher` cycle.
* **Safety**: Handover processing must **not** trigger new `BlockEvents` to avoid "double-blocking" or gossip storms.

### 3. Observability & Metrics

* `drl_accounting_handover_out_entities`: Counter for entities sent by the leaving node.
* `drl_accounting_handover_in_entities`: Counter for entities received and processed by the adopter.
* `drl_accounting_handover_duration_ms`: Histogram of the total time taken for the handover.
* `drl_accounting_handover_failed_total`: Counter for failed handover attempts.

### 4. Documentation

Update `README.md` with a "Lifecycle & Resilience" section:

* Explain the **Sender -> Adopter** workflow.
* Define the "Settling Period" and why it is necessary for consistent hashing convergence.
* Document the use of Zstd for high-speed state evacuation.

## Implementation Guidelines

* **Payload Structure**: Since you are using Protobuf for other messages, define a `HandoverWrapper` proto that contains
  the compressed bytes and metadata (sender ID, timestamp).
* **Memory Management**: Use `sync.Pool` for the Zstd writers/readers to keep GC pressure low during the high-stress
  shutdown period.
* **Memberlist Leave**: Ensure `memberlist.Leave()` is called **after** the `SendReliableMsg` has finished or timed out.

---

### Technical Note: Otter Serialization

Otter is designed for performance, but if the native serialization doesn't satisfy the "Redistribution" requirement (
which requires inspecting each key to find the new owner), you should implement a custom iterator-based serializer:

```go
// Proposed approach
iter := cache.Iter()
for iter.Next() {
// pack key/value into protobuf batch
}
```

However, we should try to use the persistence and load capabilities as much as possible. And use protobuf only as a
wrapper for the native otter format.