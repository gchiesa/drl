# DRL ECS Sidecar (Embedded Proxy) Deployment

Terraform configuration for deploying DRL on AWS ECS Fargate **without Envoy**. DRL's built-in reverse
proxy is the edge listener — the ALB forwards traffic straight to DRL, which rate-limits and forwards to
the echo-server workload. This is the leanest way to put a **global rate limiter in front of an ECS
service**: an ALB and a DRL sidecar, nothing else.

## Architecture

```
                         ┌─────────────────────────────────────────────┐
  Internet               │  VPC (10.0.0.0/16)                          │
     │                   │                                             │
     ▼                   │  ┌──────────────────────────────────────┐   │
  ┌─────┐                │  │  ECS Service  (N tasks / Fargate)    │   │
  │ ALB │──────────────────▶│                                      │   │
  └─────┘  :8080          │  │  ┌────────────────────────────────┐ │   │
                          │  │  │  ECS Task (awsvpc network mode)│ │   │
                          │  │  │                                │ │   │
                          │  │  │  ┌────────────┐  localhost     │ │   │
                          │  │  │  │ echo-server│◀──:18080       │ │   │
                          │  │  │  └────────────┘                │ │   │
                          │  │  │        ▲                       │ │   │
                          │  │  │        │ forward                │ │   │
                          │  │  │  ┌────────────┐                │ │   │
                          │  │  │  │    drl     │◀──gossip──────▶│ │   │
                          │  │  │  │(embedded   │  drl.drl.local │ │   │
                          │  │  │  │ proxy :8080)│  (Cloud Map)   │ │   │
                          │  │  │  └────────────┘                │ │   │
                          │  │  └────────────────────────────────┘ │   │
                          │  └──────────────────────────────────────┘   │
                          └─────────────────────────────────────────────┘
```

Each ECS task contains **two** containers sharing a single network namespace — there is no Envoy:

| Container   | Role                                                        | Port(s)                            |
|-------------|---------------------------------------------------------------|--------------------------------------|
| drl         | Edge reverse proxy + rate limiter — ALB target directly       | 8080, 8081, 8082, 9091, 7946, 7956  |
| echo-server | Backend workload (`mccutchen/go-httpbin`), loopback-only       | 18080                               |

DRL instances across tasks discover each other via **AWS Cloud Map** (`drl.drl.local`), which resolves to all healthy task IPs, enabling the memberlist P2P gossip cluster.

### Cross-task peer traffic

The `tasks` security group must allow every port DRL nodes use to talk to **each other**, on top of the
ALB → DRL ingress rule. All of the following are opened as self-referencing rules in `networking.tf`:

| Port | Protocol | Purpose |
|------|----------|---------|
| 7946 | TCP + UDP | Memberlist gossip — cluster membership, failure detection, state push/pull |
| 7956 | TCP | Persistent gRPC channel for hi-priority Block/Unblock propagation (`membership.hiprio-channel-port`) — deliberately a different port from gossip |
| 8082 | TCP | Internal API — queried node-to-node by the control-plane UI proxy to aggregate blocklist/accounting data from peers |

## Why this flavour

Compared to [`ecs-sidecar-envoy`](../ecs-sidecar-envoy/README.md):

| Aspect            | ecs-sidecar-envoy                  | ecs-sidecar-embedded-proxy       |
|-------------------|-------------------------------------|------------------------------------|
| Edge proxy        | Envoy (`ext_authz` → DRL gRPC)      | DRL embedded proxy                 |
| Containers/task   | echo-server + envoy + drl           | echo-server + drl                  |
| ALB target        | Envoy `:10000`                      | DRL `:8080`                        |
| Moving parts       | 3 containers, 2 config files        | 2 containers, 1 config file        |

If all you need is a **global rate limiter sitting behind a plain ALB**, this flavour gets you there with
one fewer container per task and no Envoy config to maintain. Enable TLS termination and OIDC Bearer-token
validation directly in `config/drl/config.kdl` (see the [Embedded Proxy docs](https://drl.gchiesa.dev/docs/embedded-proxy/)) if you also want DRL to take over authentication at the edge.

## Prerequisites

- Terraform >= 1.5
- AWS CLI configured with permissions to create VPC, ECS, ALB, Cloud Map, IAM, SSM
- DRL Docker image pushed to ECR (or another accessible registry)

## Quick Start

```bash
# Initialise providers
terraform init

# Preview the plan
terraform plan \
  -var="drl_image=<ECR_URI>/drl:latest" \
  -var="drl_private_api_key=<SECRET>" \
  -var="drl_membership_primary_key=<16_BYTE_HEX>"

# Apply
terraform apply \
  -var="drl_image=<ECR_URI>/drl:latest" \
  -var="drl_private_api_key=<SECRET>" \
  -var="drl_membership_primary_key=<16_BYTE_HEX>"
```

## Variables

| Name                          | Description                              | Default          |
|-------------------------------|-------------------------------------------|------------------|
| `aws_region`                  | AWS region                               | `us-east-1`      |
| `environment`                 | Environment label                        | `dev`            |
| `cluster_name`                | ECS cluster name                         | `drl-cluster`    |
| `service_replicas`            | Number of ECS task replicas              | `3`              |
| `drl_image`                   | DRL container image URI                  | *required*       |
| `echo_server_image`           | Echo server image                        | go-httpbin:latest|
| `drl_private_api_key`         | DRL internal API key (sensitive)         | *required*       |
| `drl_membership_primary_key`  | Memberlist encryption key (sensitive)    | *required*       |
| `task_cpu`                    | Total CPU units for the task             | `1024`           |
| `task_memory`                 | Total memory (MiB)                       | `2048`           |

## Outputs

| Name               | Description                             |
|--------------------|-----------------------------------------|
| `alb_dns_name`     | Public DNS of the ALB — entry point     |
| `ecs_cluster_arn`  | ECS cluster ARN                         |
| `ecs_service_name` | ECS service name                        |
| `cloudmap_namespace` | Cloud Map namespace for DRL discovery |

## Configuration Files

- `config/drl/config.kdl` — DRL config. Enables `embedded-proxy` on `:8080` (the ALB target), forwards to
  `http://127.0.0.1:18080` (echo-server), and sets `join-addr "drl.drl.local"` for Cloud Map-based peer
  discovery.

## Tear Down

```bash
terraform destroy
```
