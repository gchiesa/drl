---
title: DRL — Distributed Rate Limiter
description: >
  A high-performance, horizontally scalable rate-limiting service for Envoy
  sidecars using a P2P Hybrid Architecture.
weight: 1
---

DRL is a high-performance, horizontally scalable rate-limiting service designed to run alongside Envoy proxies. It
eliminates external-database round-trips by keeping all enforcement state in-process, distributed via a gossip mesh.

## How it works

DRL operates in three parallel planes:

| Plane                 | Mechanism                                          | Latency Impact                                           |
|-----------------------|----------------------------------------------------|----------------------------------------------------------|
| **Local enforcement** | Fully-replicated in-memory Blocklist (O(1) lookup) | Zero — rejection before the request reaches the upstream |
| **Shadow accounting** | Hashed, async counter ownership across the cluster | Zero — increments happen in a background goroutine       |
| **State sync**        | Warm-bootstrap via Memberlist Push/Pull on startup | One-time startup cost only                               |

## GitHub

Code for DRL is hosted on GitHub: https://github.com/gchiesa/drl

At this moment is in alpha state, and you are required to build it yourself or use one of the
[deployment configurations]({{< ref "deployments" >}}) provided in the repository.

## Quick start

### Prerequisites

- Go 1.25+
- Docker & Docker Compose (for local testing)

### Build

```bash
mise run build
```

### Run

```bash
# API key is required (minimum 16 characters)
export DRL_PRIVATE_API_KEY="your-secure-api-key-here"

# Start with built-in defaults
./bin/drl

# Start with a custom KDL config file
./bin/drl --config config.kdl
```

## Minimum viable configuration

```kdl
listen {
    grpc ":8081"
    metrics ":9091"
}

membership {
    service-name "drl"
    port 7946
    bind-addr "0.0.0.0"
}

internal-api {
    enabled true
    address ":8082"
}

accounting {
    settings {
        algorithm "sliding-window"
    }
    rules {
        api-v1 {
            path-prefix "/api/v1"
            headers "X-API-Key"
            limit 1000
            per "minute"
        }
    }
}
```

## Further reading

| Topic                                           | Description                                                      |
|-------------------------------------------------|------------------------------------------------------------------|
| [Configuration]({{< ref "configuration" >}})    | Complete KDL config reference and all environment variables      |
| [Membership]({{< ref "membership" >}})          | Cluster formation, gossip, warm-bootstrap, and block propagation |
| [Cache]({{< ref "cache" >}})                    | In-memory blocklist and accounting cache architecture            |
| [Accounting]({{< ref "accounting" >}})          | Shadow accounting model, entity hashing, and batched flush       |
| [gRPC API]({{< ref "api" >}})                   | Envoy `ratelimit.v3` service implementation                      |
| [Internal HTTP API]({{< ref "internal-api" >}}) | Management endpoints, digest authentication, and examples        |
| [Deployment Models]({{< ref "deployments" >}})  | Docker Compose, ECS Fargate, Kubernetes sidecar/fleet, and Istio |
