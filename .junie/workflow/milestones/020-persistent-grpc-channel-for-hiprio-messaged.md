# 020-persistent-grpc-channel-for-hiprio-messaged.md

## Context

When a request is calculated to be blocked by rate-limiting, DRL currently establishes an on-demand TCP connection to
each cluster member to propagate the block message. This overhead is inefficient when the server is under heavy load.

## Architectural Decision

After evaluating the options:

1. **Option A (2 unidirectional gRPC connections per node pair):** Creates a spaghetti web of independent connections
   that increases resource footprint, connection management complexity, and lock contention across sockets.
2. **Option B (1 single bidirectional gRPC connection per node pair):** Reduces open file descriptors by half,
   simplifies lifecycle management on node join/leave, and enables full HTTP/2 multiplexing for concurrent high-priority
   event streams (e.g., blocking/unblocking events) across the same connection.

**Decision:** Adopt **Option B** (a single bidirectional gRPC connection established between each unique pair of cluster
nodes).

## Goal

Replace the on-demand high-priority messaging propagation for blocking events with a single persistent bidirectional
gRPC channel between each cluster member pair.

The channel will be established on port `7956`. When enabled, this channel will handle immediate high-priority events
(such as rate-limiting blocks) across cluster members.

To preserve current package organization, extend the `membership` package with additional files to manage the gRPC
persistent channel lifecycle and event transport.

## Requirements

### 1. Configuration & Feature Flag

- Add feature flag environment variable: `DRL_MEMBERSHIP_USE_HIPRIO_PERSISTENT_CHANNEL` (boolean, default: `true`).
- Extend KDL configuration schema under the `membership` block to include:
  ```kdl
  membership {
      use-hiprio-persistent-channel true
      hiprio-channel-port 7956
  }
  ```

* Wire all configuration options cleanly in `internal/config` with proper environment variable overrides and default
  values.

### 2. Extensible Message Protocol

* Implement a forward-compatible gRPC message structure in `internal/proto/hiprio.proto` (or within the existing proto
  framework).
* Design the message format to support multiple high-priority event types via an event identifier or `oneof` payload
  structure (similar to `internal/proto/accounting.proto`), starting with `BlockEvent` and `UnblockEvent`.

### 3. gRPC Persistent Channel Implementation

* Use TCP port `7956` for inter-node persistent gRPC messaging.
* Maintain a single bidirectional gRPC channel between each unique node pair in the cluster.
* **Node Join:** When a new member joins the cluster, establish/accept the gRPC connection with the node and log an
  `INFO` event (e.g., `"Established persistent gRPC channel with peer [NodeID / IP:7956]"`).
* **Joiner Readiness**: When a new member joins the cluster, it's ready only when all the persistent gRPC channel
  connections are established.
* **Node Leave:** When a member leaves or fails health checks, cleanly close the gRPC channel and clear associated
  client connections from memory. Log a `WARN`/`INFO` event accordingly.

### 4. Integration with Membership Package

* Place persistent channel logic inside the `membership` package (e.g., `internal/membership/grpc_channel.go`).
* When `DRL_MEMBERSHIP_USE_HIPRIO_PERSISTENT_CHANNEL` is set to `true`, route high-priority blocking and unblocking
  events through the new gRPC persistent channel instead of short-lived on-demand TCP connections.
* Ensure fallback behavior or fallback errors are logged gracefully if a channel suffers a temporary transport failure.
* Ensure when old TCP on demand model is used there is a log message level WARN for the event.

## Outcome

* **State Report:** Generate the report of all state file changes in Markdown format upon completion.
* **Configuration Update:** DRL configuration updated to parse and validate `use-hiprio-persistent-channel` and
  `hiprio-channel-port`.
* **Protocol Definition:** Protobuf definitions compiled and wired for extensible event transport.
* **Runtime Execution:** Clustered nodes maintain a single bidirectional gRPC channel on port `7956` for blocking
  propagation when the feature flag is enabled.
* **Implement necessary testings:** Unit tests for the new gRPC persistent channel implementation.
