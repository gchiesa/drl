# 014-private-api-cache-loading.md

## Goal

Implement a private administrative endpoint `POST /accounting/load` to bulk-ingest entity request data into the
accounting engine. This bypasses the gRPC frontend to allow for rapid cache population during testing. Additionally,
consolidate and standardize the Internal API documentation in the `README.md`.

## Requirements

### 1. Newline JSON (ndjson) Ingestion

* **Endpoint**: `POST /accounting/load?distributionEnabled=true|false`
* **Format**: The request body must be processed as a stream of JSON objects, one per line.
* **Schema**:
    ```json
    { "sourceIP": "1.2.3.4", "path": "/anything/test2", "headers": {"User-Agent": "k6"}}
    ```
* **Processing Logic**:
    * Each line should be mapped to the `CheckRequest` logic used by the gRPC server.
    * It must resolve the applicable **Accounting Rule** based on the path.
    * It must calculate the **Entity Hash** and determine the **Owner Node** via the Consistent Hash Ring.
    * Metrics for accounting should be updated
    * A new metric for bulk update accounting entities should be added 
    * Blocking is not evaluated in this logic

### 2. Distribution Control

* **`distributionEnabled=true`**: If the current node is not the owner of the entity, the update must be enqueued in the
  `Flusher` to be sent to the correct owner (via `SendBestEffort`).
* **`distributionEnabled=false`**: If the current node is not the owner, the update is dropped. A `log.Warn` must be
  emitted stating: `"Skipping non-owned entity in bulk load: [entity_id] (distribution disabled)"`.
* **Local Ownership**: If the current node is the owner, the `AccountingCache` (Otter) is updated immediately.

### 3. Performance & Safety

* **Streaming Decoder**: Use `json.Decoder` with `Decode()` in a loop to process the body line-by-line rather than
  loading the entire payload into memory.
* **Authentication**: This endpoint must be protected by the existing **Digest Authentication** mechanism.
* **Validation**: Invalid JSON lines should be logged as errors, but the stream processing should continue for
  subsequent lines (Best Effort).

### 4. Documentation Consolidation (README.md)

Rewrite the "Private API" section of the `README.md` to be the "Single Source of Truth":

* **Overview**: Explain the role of the Internal API (Port 8082) and the Digest Auth security model.
* **Endpoint Reference Table**: 

| Method   | Path                  | Description                               | Milestone | 
|:---------|:----------------------|:------------------------------------------|:----------|
| `GET`    | `/accounting/stats`   | High-level engine metrics                 | 006       |
| `GET`    | `/blocked-entity`     | List all currently blocked entities       | 006v2/010 | 
| `DELETE` | `/blocked-entity/:id` | Manually unblock an entity (Cluster-wide) | 009       |
| `POST`   | `/accounting/load`    | Bulk ingest accounting data (Testing)     | 014       |


* **Curl Examples**: Provide a concrete example of how to handle the Digest Auth handshake:

    ```bash
    # Example for Bulk Load
    curl -v --digest -u admin:password -X POST \
      -H "Content-Type: application/x-ndjson" \
      --data-binary @entities.ndjson \
      "http://localhost:8082/accounting/load?distributionEnabled=true"
    ```

## Implementation Guidelines

* **Integration**: The handler should be implemented in `internal/api/handlers.go` and have access to the
  `AccountingEngine`.
* **Atomicity**: Increments should use the same atomic patterns defined in Milestone 010 (Otter/v2) to ensure thread
  safety during bulk loads.

