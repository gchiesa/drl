# DRL — Distributed Rate Limiter

[![CI](https://github.com/gchiesa/drl/actions/workflows/ci.yml/badge.svg)](https://github.com/gchiesa/drl/actions/workflows/ci.yml)
[![Performance](https://github.com/gchiesa/drl/actions/workflows/performance.yml/badge.svg)](https://github.com/gchiesa/drl/actions/workflows/performance.yml)

A high-performance, horizontally scalable rate-limiting service designed for Envoy sidecars. DRL eliminates
the latency of external databases by using a **Peer-to-Peer Hybrid Architecture**:

- **Local enforcement** — fully-replicated in-memory Blocklist for O(1) rejection
- **Shadow accounting** — hashed, asynchronous global quota tracking
- **Warm-bootstrap** — state sync on startup prevents vulnerability windows during rolling updates

## Infrastructure overview

```mermaid
graph TB
    subgraph pod-a ["Pod A"]
        WA["Workload"] <--> EA["Envoy\nsidecar"]
    end
    subgraph pod-b ["Pod B"]
        WB["Workload"] <--> EB["Envoy\nsidecar"]
    end
    subgraph pod-c ["Pod C"]
        WC["Workload"] <--> EC["Envoy\nsidecar"]
    end

    EA -->|"gRPC ShouldRateLimit"| DRL1
    EB -->|"gRPC ShouldRateLimit"| DRL2
    EC -->|"gRPC ShouldRateLimit"| DRL3

    subgraph drl-fleet ["DRL Fleet"]
        DRL1["DRL-1\nOwns keys A–F"]
        DRL2["DRL-2\nOwns keys G–M"]
        DRL3["DRL-3\nOwns keys N–Z"]
        DRL1 <-->|"Memberlist gossip\n+ block events"| DRL2
        DRL2 <-->|"Memberlist gossip\n+ block events"| DRL3
        DRL1 <-->|"Memberlist gossip\n+ block events"| DRL3
    end

    DRL1 -->|"OK / OVER_LIMIT"| EA
    DRL2 -->|"OK / OVER_LIMIT"| EB
    DRL3 -->|"OK / OVER_LIMIT"| EC

    DRL1 -.->|"Async UDP CounterBatch\nto owner node"| DRL2
    DRL2 -.->|"Async UDP CounterBatch\nto owner node"| DRL3
```

## Documentation

| Topic | Description |
|-------|-------------|
| [Getting Started](https://drl.gchiesa.dev/) | Quick start and overview |
| [Configuration](https://drl.gchiesa.dev/configuration/) | Complete KDL config reference and environment variables |
| [Membership](https://drl.gchiesa.dev/membership/) | Cluster formation, gossip, warm-bootstrap, block propagation |
| [Cache](https://drl.gchiesa.dev/cache/) | In-memory blocklist and accounting cache architecture |
| [Accounting](https://drl.gchiesa.dev/accounting/) | Shadow accounting, entity hashing, batched flushing |
| [gRPC API](https://drl.gchiesa.dev/api/) | Envoy `ratelimit.v3` service implementation |
| [Internal HTTP API](https://drl.gchiesa.dev/internal-api/) | Management endpoints and digest authentication |

## CI reports

Reports are published to GitHub Pages after each successful run on `main`.

| Job | Report |
|-----|--------|
| Functional (1 replica) | [functional-single-instance](https://drl.gchiesa.dev/reports/functional-single-instance/) |
| Functional (5 replicas) | [functional-5-instances](https://drl.gchiesa.dev/reports/functional-5-instances/) |
| Functional (10 replicas) | [functional-10-instances](https://drl.gchiesa.dev/reports/functional-10-instances/) |
| Handover | [handover](https://drl.gchiesa.dev/reports/handover/) |
| Performance | [performance](https://drl.gchiesa.dev/reports/performance/) |

## License

MIT
