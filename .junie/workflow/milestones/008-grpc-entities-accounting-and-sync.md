# 008-grpc-entities-accounting-and-sync.md

This milestone defines the core analytical engine of DRL. It transitions the service from a passive gRPC receiver to an
active, distributed accounting system that identifies and tracks request patterns across the cluster using the shadow
accounting cache.

## Goal

Implement the distributed accounting engine to track request frequencies based on configurable entity definitions (IP,
Path, and Headers). This milestone enables DRL to maintain a high-granularity view of traffic across the cluster by
hashing entities to specific "Owner Nodes" and synchronizing counters asynchronously via a lightweight protocol.

## Requirements

### 1. Accounting Configuration Schema

Update the KDL configuration to define "Rules" for monitoring. DRL will only track entities that match these
definitions.

* **Structure**:

```kdl
accounting {
    rule "/api/v1" {
        headers "X-API-KEY-ID" "X-Consumer-Type"
        limit 100 per="minute"
    }
    rule "/static/images" {
        limit 500 per="second"
    }
}

```

* **Logic**:
* **Path Matching**: Uses `startswith` logic (e.g., `/api/v1` matches `/api/v1/users`).
* **Header Selection**: If headers are specified, the entity unique ID must include the specific values of those header
  keys from the request.

### 2. Entity Hashing & Ownership

* **Composite Key Generation**: For every incoming gRPC `CheckRequest`, if a rule matches, DRL generates a key based on
  the rule's criteria:
* **Case A (Path only)**: `Hash(IP + uriPath)`.
* **Case B (Path + Headers)**: `Hash(IP + uriPath + HeaderKey1:Val1 + ...)`.


* **Owner Determination**: Use the **Consistent Hashing** ring to identify which cluster node "owns" the resulting hash.

### 3. Asynchronous Peer Synchronization

To minimize latency on the Envoy request path, counter increments are propagated out-of-band.

* **Lightweight Protocol**: Use **UDP** for inter-node increment signals to reduce overhead.
* **Batching**: Nodes must buffer increments locally and flush them to the respective "Owner Nodes" in batches (e.g.,
  every 10 seconds or every 500 increments).
* **Storage**: The Owner Node updates the value in its **Shadow Accounting Cache** (Ristretto).

### 4.UDP Sync Protocol (Protobuf)

UDP Propagation Logic Buffering: Each node maintains a local buffer of increments categorized by the "Owner Node" (
determined via the hash ring).

**Flush Trigger**: A background worker (the CounterFlusher) monitors the buffers. It triggers a UDP send when the buffer
reaches the size limit or the 10-second timer expires.

**Zero-Copy Serialization**: Use sync.Pool for the Protobuf message objects and the byte buffers to ensure the accounting
engine does not trigger heavy Garbage Collection (GC) during traffic spikes.

**Loss Tolerance**: Because DRL prioritizes availability and low latency, no ACK/Retry logic is implemented for these UDP
packets. Lost packets result in a temporary, slight under-counting, which is acceptable for "eventual consistency" in
shadow accounting.

To ensure high performance and maintainability, we will use **Protocol Buffers (Protobuf)** as the off-the-shelf binary
format for the UDP accounting synchronization.

### 5. UDP Sync Protocol: `CounterBatch`

The synchronization follows a "fire-and-forget" model. Multiple increments are packed into a single UDP datagram to
maximize throughput and minimize the number of syscalls.

#### 1. Protobuf Schema (`internal/proto/accounting.proto`)

This schema defines the structure of the data sent over the wire.

```proto
syntax = "proto3";
package drl.v1;

message CounterEntry {
  // Use fixed64 for hashes to avoid varint overhead on random-looking bits
  fixed64 entity_hash = 1; 
  uint32 hits = 2;
}

message CounterBatch {
  uint64 sender_id = 1;
  uint64 timestamp = 2; // Unix epoch in seconds
  repeated CounterEntry entries = 3;
}

```

#### 2. Packet Characteristics

| Characteristic      | Specification                                                          |
|---------------------|------------------------------------------------------------------------|
| **Transport**       | UDP                                                                    |
| **Default Port**    | `7947` (Internal DRL Sync)                                             |
| **Max Packet Size** | **1400 Bytes** (Safely below standard 1500 MTU to avoid fragmentation) |
| **Endianness**      | Handled automatically by Protobuf (standardized)                       |
| **Batching Window** | 10 seconds or 1000 entries (whichever comes first)                     |

### 6. Observability: Accounting API

Extend the Internal API to allow inspection of the local accounting state.

* **Endpoint**: `GET /accounting/stats`
* **Security**: Protected by Digest Auth.
* **Response**:

```json
{
  "local_node_id": "drl-node-1",
  "monitored_entities_count": 1240,
  "batched_updates_pending": 45
}

```

## Implementation Guidelines

* **Hashing**: Use `xxHash` for consistent and high-performance key generation.
* **Background Worker**: Implement a `CounterFlusher` goroutine that manages the UDP batching logic.
* **Fail-Safe**: If a UDP update is lost, the system accepts the temporary inconsistency to prioritize availability.
* **Package**: Implement logic in `internal/accounting/engine.go` and `internal/accounting/sync.go`.

## Validation Criteria

1. **Rule Matching**: A request to `/api/v1/test` with header `X-API-KEY-ID: abc` results in a specific entity being
   tracked, while a request to `/other` does not.
2. **Distributed Logic**: Verify (via logs) that if Node A receives a request but Node B is the "Owner," Node A sends an
   asynchronous update to Node B.
3. **Batching**: Verify that UDP traffic between nodes occurs in intervals (e.g., 10s) rather than per-request.
4. **Cache Integration**: `GET /accounting/stats` reflects the growth of the shadow accounting cache during a load test.
5. **Performance**: `k6` load tests confirm that adding accounting logic adds < 1ms of latency to the Envoy `Check`
   response.
