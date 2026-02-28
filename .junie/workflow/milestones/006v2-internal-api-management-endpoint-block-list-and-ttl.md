# 006v2-internal-api-management-endpoint-block-list-and-ttl.md

## Goal

Implement a retrieval mechanism to inspect the active blocklist and introduce Time-To-Live (TTL) management for blocked
entities. This ensures the cluster remains self-cleaning and provides administrators with the necessary observability to
verify current enforcement states.

## Requirements

### 1. Blocklist Inspection (GET)

* **Endpoint**: `GET /blocked-entity`
* **Functionality**: Returns a JSON array of all currently blocked entities stored in the local Ristretto cache.
* **Data per Entry**:
* `id`: The unique hash (xxHash) of the entity.
* `ip`: The blocked IP address.
* `uriPath`: The blocked URI path.
* `headers`: Map of the key-value pairs used for the block.
* `expires_at`: RFC3339 timestamp indicating when the block will be purged.

### 2. TTL Implementation & Housekeeping

* **Configurable Default**: Introduce `cache.blocklist_default_ttl` in the KDL configuration (suggested default:
  `3600s`).
* **Local Purging**: Utilize Ristretto’s native TTL support to handle item expiration.
* **Eventual Consistency**: Each node is responsible for its own cache eviction based on the TTL; there is no global "
  clock" synchronization required for purging.
* **Propagation Update**: The `BlockEvent` broadcast to peers must now include the `TTL` or `ExpirationTimestamp` so
  peers can set the same expiration locally.

### 3. API Updates (POST)

* **Optional TTL Parameter**: Update the POST endpoint to optionally accept a TTL in the query string (e.g.,
  `?ttl=300`).
* **Pattern**: `POST /blocked-entity/<IP>/_path/<uriPath>/_headers/<key:val>?ttl=600`

## Implementation Guidelines

* **Cache Iteration**: Since Ristretto is optimized for performance rather than iteration, the `GET` handler might
  require maintaining a secondary "metadata" index or using a controlled iteration if the blocklist size allows.
* **Serialization**: Update the Protobuf/MessagePack schema for cluster events to include the `Duration` or `Expiration`
  field.
* **Concurrency**: Use `internal/cache` wrappers to ensure the `GET` operation doesn't block high-frequency write
  operations or gRPC checks.

## Validation Criteria

1. **List Retrieval**: `GET /blocked-entity` returns a JSON list matching the entities previously added via `POST`.
2. **Expiration Test**: Add an entity with a 10s TTL. Verify it appears in the `GET` list initially and disappears after
   10-15 seconds.
3. **Cluster Expiration**: Verify that an entity added to Node A with a specific TTL also expires and disappears from
   Node B’s memory at approximately the same time.
4. **Formatting**: The `expires_at` field in the JSON response is a valid ISO/RFC3339 timestamp.

