# 006-internal-api-management-endpoint-block.md

## Goal

Extend the Internal API (Fiber) to provide an administrative CRUD interface for manually managing blocked entities. This
allows administrators to proactively block or unblock specific traffic patterns (IP + Path + Headers) across the entire
cluster without waiting for the automatic shadow accounting to trigger.

## Requirements

### 1. Administrative API Endpoints

The API must handle variable-length URI paths and optional header sets using the "Marker" routing strategy to avoid
ambiguity.

* **POST** `/blocked-entity/<IP>/_path/<uriPath>/_headers/<key:val,key:val>`
* **Action**: Adds the entity to the local **Blocklist Cache** (Ristretto).
* **Logic**: If headers are omitted, the block applies to the IP + Path only.


* **DELETE** `/blocked-entity/<IP>/_path/<uriPath>/_headers/<key:val,key:val>`
* **Action**: Removes the entity from the local **Blocklist Cache**.


* **Response Format**:

```json
{
  "id": "uuid-or-timestamp-id",
  "ip": "<IP>",
  "uriPath": "<uriPath>",
  "headers": {
    "key": "val",
    "key2": "val2"
  },
  "message": "Entity added to blocklist / Entity removed from blocklist",
  "errors": []
}

```

### 2. Entity Representation

An "Entity" in DRL is a composite key. For this management endpoint, the IP is derived from the context or treated as a
wildcard/global if not specified, but the identity must be unique.

* **Key Construction**: Generate a hash (e.g., `xxHash`) of the concatenated `IP + Path + SortedHeaders` to use as the
  internal cache key.

### 3. Asynchronous Cluster Propagation

Manual blocks must be synchronized across all nodes to ensure cluster-wide enforcement.

* **Mechanism**: Utilize the existing `Memberlist/Serf` broadcast layer.
* **Non-Blocking Flow**:

1. The API node updates its local Ristretto cache immediately.
2. The node spins up a background goroutine to broadcast a `BlockEvent` or `UnblockEvent` to peers.
3. The API returns a `200 OK` to the admin immediately; propagation happens with **eventual consistency**.


* **Efficiency**: Use the `sync.Pool` for serialization buffers as established in the state sync guidelines.

### 4. Security & Authentication

* **Digest Auth**: These endpoints **must** be protected by the Digest Authentication (SHA-256) implemented in Milestone
  004v2.
* **Validation**: The API must return `400 Bad Request` if the `<uriPath>` is empty or the `<header>` format is
  malformed.

## Implementation Guidelines

* **Routing**: Use Fiber’s wildcard or "greedy" parameter (e.g., `/_path/*`) to capture the URI path, then manually
  split the string at the `/_headers/` marker.
* **Package**: Implement the controller logic in `internal/api/handlers/blocklist.go`.
* **Event Handling**: In `internal/membership/delegate.go`, extend the `NotifyMsg` handler to process incoming manual
  block/unblock events and update the local Ristretto store.

## Validation Criteria

1. **Creation**: `POST /blocked-entity/_path/api/v1/payments/_headers/User-Agent:ScraperBot` returns `200 OK`.
2. **Immediate Local Enforcement**: A subsequent internal check for that specific path/header combo returns `DENIED`.
3. **Propagation**: After 1-2 seconds, a `GET /status` on a **different** node shows the updated blocklist count or
   shows the entity in its local cache.
4. **Deletion**: `DELETE` on the same URI removes the block cluster-wide.
5. **Auth Check**: Unauthenticated requests to these endpoints return `401 Unauthorized`.

