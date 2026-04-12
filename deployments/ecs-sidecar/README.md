# DRL ECS Sidecar Deployment

Terraform configuration for deploying DRL as a **sidecar** alongside Envoy and the echo-server workload on AWS ECS Fargate.

## Architecture

```
                         ┌─────────────────────────────────────────────┐
  Internet               │  VPC (10.0.0.0/16)                          │
     │                   │                                             │
     ▼                   │  ┌──────────────────────────────────────┐   │
  ┌─────┐                │  │  ECS Service  (N tasks / Fargate)    │   │
  │ ALB │──────────────────▶│                                      │   │
  └─────┘  :10000         │  │  ┌────────────────────────────────┐ │   │
                          │  │  │  ECS Task (awsvpc network mode)│ │   │
                          │  │  │                                │ │   │
                          │  │  │  ┌────────────┐  localhost     │ │   │
                          │  │  │  │ echo-server│◀──:8080        │ │   │
                          │  │  │  └────────────┘                │ │   │
                          │  │  │                                │ │   │
                          │  │  │  ┌────────────┐                │ │   │
                          │  │  │  │   envoy    │──ext_authz──▶  │ │   │
                          │  │  │  │  :10000    │  localhost:8081 │ │   │
                          │  │  │  └────────────┘                │ │   │
                          │  │  │                                │ │   │
                          │  │  │  ┌────────────┐                │ │   │
                          │  │  │  │    drl     │◀──gossip──────▶│ │   │
                          │  │  │  │ :8081 :7946│  drl.drl.local │ │   │
                          │  │  │  └────────────┘  (Cloud Map)   │ │   │
                          │  │  └────────────────────────────────┘ │   │
                          │  └──────────────────────────────────────┘   │
                          └─────────────────────────────────────────────┘
```

Each ECS task contains three containers sharing a single network namespace:

| Container   | Role                                              | Port(s)              |
|-------------|---------------------------------------------------|----------------------|
| echo-server | Backend workload (`mccutchen/go-httpbin`)          | 8080                 |
| envoy       | Reverse proxy — forwards traffic, calls DRL first | 10000, 9901 (admin)  |
| drl         | Rate limiter sidecar — receives ext_authz gRPC    | 8081, 8082, 9091     |

DRL instances across tasks discover each other via **AWS Cloud Map** (`drl.drl.local`), which resolves to all healthy task IPs, enabling the memberlist P2P gossip cluster.

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
|-------------------------------|------------------------------------------|------------------|
| `aws_region`                  | AWS region                               | `us-east-1`      |
| `environment`                 | Environment label                        | `dev`            |
| `cluster_name`                | ECS cluster name                         | `drl-cluster`    |
| `service_replicas`            | Number of ECS task replicas              | `3`              |
| `drl_image`                   | DRL container image URI                  | *required*       |
| `echo_server_image`           | Echo server image                        | go-httpbin:latest|
| `envoy_image`                 | Envoy image                              | v1.30-latest     |
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

- `config/envoy/envoy.yaml` — Envoy static config. Uses `127.0.0.1` for echo-server and DRL (sidecar model).
- `config/drl/config.kdl` — DRL config. Sets `join-addr "drl.drl.local"` for Cloud Map-based peer discovery.

## Tear Down

```bash
terraform destroy
```
