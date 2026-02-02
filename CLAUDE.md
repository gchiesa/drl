# DRL Distributed Rate Limiter

## Project Overview

DRL is a high-performance, horizontally scalable rate-limiting service designed for Envoy sidecars. It eliminates the
latency of external databases by using a Peer-to-Peer (P2P) Hybrid Architecture:

1. Local Enforcement: Fully replicated Blocklist for $O(1)$ rejection.
2. Shadow Accounting: Hashed, asynchronous global quota tracking.
3. State Sync: Warm-bootstrapping to prevent "vulnerability windows" during rolling updates. Core Architecture & Flow

DRL can be installed as a sidecar next to Envoy or as a separate fleet of instances dedicated to take decision on the
rate limit upon received Envoy requests

### 1 - The Request Path (Envoy → DRL)

* Step 1 (Check): DRL receives a gRPC CheckRequest (localhost or remote host)
* Step 2 (Local Blocklist): DRL queries its local, fully replicated in-memory Blocklist Cache.
    * If IP is present $\to$ Return DENIED (429) immediately.
* Step 3 (Optimistic OK): If not blocked, $\to$ Return OK to Envoy.
* Step 4 (Async Sync): In a background goroutine, DRL hashes the IP to find the Owner Node and sends an Increment (IP)
  signal via internal gRPC/UDP.

### 2 - The Accounting & Propagation Path

* Step 5 (Ownership): The Owner Node updates the counter in its Hashed Accounting Cache.
* Step 6 (Violation): If the limit is breached, the Owner Node:
    * Adds the IP to its local Blocklist.
    * Broadcasts a Block(IP, TTL) event to the cluster using Serf/Memberlist.
* Step 7 (Convergence): All nodes receive the event and update their local Blocklist, ensuring the next request from any
  Envoy is blocked at Step 2.

## Technology Stack

* **Language**: Go (Golang)
* **Interface**: gRPC (Envoy ratelimit.v3 proto)
* **Membership & Discovery**: Hashicorp Memberlist
* **Event Broadcast**: Hashicorp Serf
* **Consistency**: Consistent Hashing (stathat/consistent)
* **In-Memory Store**: Ristretto (High-concurrency cache)
* **Metrics**: The application should publish metrics in prometheus compatible format on the standard port and path

## Node Lifecycle & State Transfer Startup & Warm-up

To prevent malicious traffic spikes during rolling updates, new nodes must "learn" the current blocklist before serving
traffic:

* **Full State Sync**: On startup, the node performs a TCP Push/Pull sync with existing peers via the Memberlist
  Delegate interface.
* **Merge Strategy**: The node hydates its local cache with the received Blocklist Data.
* **Readiness**: The node only reports Ready to serve reequests after the initial state transfer is complete.

## Shutdown

* **Leave Intent**: Node notifies the cluster it is leaving so the Hash Ring rebalances smoothly.
* **Accounting Reset**: Hashed counters on the leaving node are discarded; new owners start fresh (Acceptable trade-off
  for speed).

## Implementation Guidelines

* **Concurrency**: Use ristretto for the caches to avoid GC overhead and lock contention.
* **Communication**: Internal peer increments should use a dedicated internal gRPC service or lightweight UDP packets.
* **Reliability**: Implement a Max Hops (1) for internal increments to prevent circular forwarding during hash ring
  transitions.
* **Serialization**: Use protobuf or msgpack for efficient state transfer in GetState() and MergeState(). Development
  Commands
* **Error Handling**: If a Peer Increment fails (network timeout), the node should log a warning but NOT fail the Envoy
  request. Availability > Consistency.
* **Protocols**: Use envoyproxy/go-control-plane for gRPC stubs to ensure 100% compatibility with the ratelimit.v3
  proto.

### CI/CD Guidelines

Use MISE tool for maintaining the build, test script

* **mise run task build**: Build binary with `-race` flag for dev.
* **mise run task lint**: Run linting.
* **mise run task tests**: Must include `go test -v ./...` and coverage reports.

All the required tooling should be installed via mise tools and maintained in the mise.toml file.

### Manual testing infrastructure

For the manual testing we will use a docker compose project that can be spawned up on the local laptop. DOcker compose
will have the following services

* **workload/echo-server**: should implement a simple echo-server workload based on
  `https://hub.docker.com/r/mccutchen/go-httpbin` that is protected by envoy reverse proxy
* **reverseproxy/envoy**: should implement in the same network and share resources with echo-server (acting like a
  sidecar). Envoy that has a YAML configuration to perform gRPC requests to a separate fleet of DRL instances
* **ratelimiter/drl**: should implement a fleet with min 3 instance of DRL that will receive envoy request and perform
  decisions
* **trafficproducer/k6s**: should implement a k6s instance which produces traffic against the protected workload in a
  ramp up model so that we can eventually see the DRL in action. The number of virtual clients, requests per minute,
  total duration and ramp-up should be configurable via environment variables in docker compose.

### Implementation approach with AI

This project uses a milestone-driven development process. For each milestone a set of unit test should be implemented.

The DRL tool should be constructed as a command and configurable via https://kdl.dev/ and possibly override by
environment variables. The structure of the code in DRL should resemble the following:

```txt
<root folder>
main.go <-- main entrypoint
internal/... <-- internal packages
internal/cmd <-- internal package for command line configuration 
pkg/ <-- external exported packages when these will be stable 
.golangci-lint.yaml <-- configuration for golangci-lint 
mise.toml <-- mise configuration
ci/scripts/ <-- mise scripts 
```

#### Workflow

1. Check `.junie/workflow/state/` for `*.completed` files
2. Find the first milestone in `.junie/workflow/milestones/` without a matching `.completed` file
3. Implement the milestone requirements
4. Run lint and testing with MISE. MISE should maintain the scripts in the dedicated folder with +x execution bit and
   the MISE toml file should be as clean as possible
5. If tests pass, create `<milestone_name>.completed` in `.junie/workflow/state/`
6. If tests fail, log error in `.junie/workflow/logs/session.log` and request human input

**Important:** Never re-process completed milestones. The `.completed` files are the source of truth
