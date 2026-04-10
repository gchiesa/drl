---
title: Deployment Models
description: Overview of the available DRL deployment flavours — Docker Compose, ECS Fargate, Kubernetes sidecar, Kubernetes fleet, and Istio.
weight: 9
---

DRL ships with five deployment configurations, each targeting a different infrastructure model. All
flavours configure the same core components — an echo-server workload, an Envoy reverse proxy, and
one or more DRL instances — but differ in how Envoy reaches DRL and how DRL nodes form a cluster.

## Flavour comparison

| Flavour | Envoy → DRL path | DRL topology | Infrastructure | Best for |
|---------|-----------------|--------------|----------------|----------|
| [Docker Compose](#docker-compose) | `drl:8081` (DNS) | 2-replica service, bridge network | Local machine | Local dev, manual QA |
| [ECS Sidecar](#ecs-sidecar-terraform) | `127.0.0.1:8081` (loopback) | One DRL container per Fargate task; peers via Cloud Map DNS | AWS ECS Fargate + ALB | AWS-native workloads |
| [K8s Sidecar](#kubernetes-sidecar-kustomize) | `127.0.0.1:8081` (loopback) | One DRL pod per app pod; peers via headless Service DNS | Any Kubernetes cluster | Sidecar-injection pattern |
| [K8s Fleet](#kubernetes-fleet-kustomize) | `drl.drl.svc.cluster.local:8081` (ClusterIP) | Dedicated DRL Deployment; peers via headless Service DNS | Any Kubernetes cluster | Shared rate-limit tier, independent scaling |
| [Istio](#istio-extension) | `drl.drl.svc.cluster.local:8081` (ClusterIP) | Any of the above | Istio service mesh | Mesh-wide policy without re-configuring workloads |

---

## Docker Compose

The simplest way to run the full stack locally. All services share a single bridge network; Envoy
resolves DRL by container service name.

**Topology**

```
Client → Envoy :10000 → echo-server :8080
                  ↓ ext_authz gRPC
               DRL (x2) ← memberlist gossip
```

**Start**

```bash
cd deployments/docker-compose
docker compose up -d

# With load testing
docker compose --profile loadtest up -d
```

**Source**  
{{< icon "github" >}} [deployments/docker-compose/README.md](https://github.com/gchiesa/drl/blob/main/deployments/docker-compose/README.md)

---

## ECS Sidecar (Terraform)

Deploys the full stack on **AWS ECS Fargate** using Terraform. Each ECS task runs three containers
(`echo-server`, `envoy`, `drl`) in the same network namespace, so all inter-container traffic
resolves over `127.0.0.1`. DRL instances across tasks discover each other via **AWS Cloud Map** DNS
(`drl.drl.local`).

**Topology**

```
Internet → ALB :80 → Envoy :10000 (per-task sidecar)
                         ↓ ext_authz gRPC (127.0.0.1)
                      DRL sidecar :8081
                         ↕ memberlist gossip (Cloud Map DNS)
                      DRL sidecar (other tasks)
```

**Resources created**

- VPC, public/private subnets, NAT gateways
- Application Load Balancer
- ECS cluster (Fargate), task definition, service
- AWS Cloud Map private DNS namespace for peer discovery
- SSM Parameter Store entries for secrets

**Deploy**

```bash
cd deployments/ecs-sidecar
terraform init
terraform apply \
  -var="drl_image=<ECR_URI>/drl:latest" \
  -var="drl_private_api_key=<SECRET>" \
  -var="drl_membership_primary_key=<16_BYTE_HEX>"
```

**Source**  
{{< icon "github" >}} [deployments/ecs-sidecar/README.md](https://github.com/gchiesa/drl/blob/main/deployments/ecs-sidecar/README.md)

---

## Kubernetes Sidecar (Kustomize)

Kustomize manifests for an **existing Kubernetes cluster**. DRL runs as a third container inside
every application pod alongside `echo-server` and `envoy`. All three containers share `localhost`.
A headless Service (`drl-headless`) makes all pod IPs available via DNS so memberlist can bootstrap
the gossip cluster across pods.

**Topology**

```
Ingress → echo-server Service :80
              ↓
         Pod: [echo-server :8080] + [envoy :10000] + [drl :8081]
                                        ↓ ext_authz (127.0.0.1)
                                       drl :8081
                                        ↕ memberlist gossip
                                  drl-headless (all pod IPs)
```

**Apply**

```bash
# Create secrets first
kubectl create secret generic drl-secrets --namespace drl \
  --from-literal=private-api-key='<KEY>' \
  --from-literal=membership-primary-key='<16_BYTE_HEX>'

# Apply base manifests
kubectl apply -k deployments/k8s-sidecar/base/
```

**Trade-offs vs fleet model**

| | Sidecar | Fleet |
|---|---------|-------|
| Envoy→DRL latency | ~0 µs (loopback) | +network hop |
| DRL scaling | Coupled to app replicas | Independent |
| Resource isolation | Shared pod budget | Own quota |
| Gossip mesh size | Grows with app replicas | Fixed fleet size |

**Source**  
{{< icon "github" >}} [deployments/k8s-sidecar/README.md](https://github.com/gchiesa/drl/blob/main/deployments/k8s-sidecar/README.md)

---

## Kubernetes Fleet (Kustomize)

Kustomize manifests for an **existing Kubernetes cluster** where DRL runs as a **dedicated
Deployment** separate from the application. Envoy sidecars inside the `echo-server` pods resolve
DRL via the `drl` ClusterIP Service. This model lets you scale the DRL fleet independently and
allocate dedicated node resources.

**Topology**

```
Ingress → echo-server Service :80
              ↓
         Pod: [echo-server :8080] + [envoy :10000]
                                        ↓ ext_authz gRPC
                              drl.drl.svc.cluster.local:8081
                                        ↓ (ClusterIP load-balanced)
                               DRL Deployment (x3)
                                 [drl-0] [drl-1] [drl-2]
                                   ↕ memberlist gossip
                               drl-headless (all DRL pod IPs)
```

**Apply**

```bash
# Create secrets first
kubectl create secret generic drl-secrets --namespace drl \
  --from-literal=private-api-key='<KEY>' \
  --from-literal=membership-primary-key='<16_BYTE_HEX>'

# Apply base manifests
kubectl apply -k deployments/k8s-fleet/base/
```

**Source**  
{{< icon "github" >}} [deployments/k8s-fleet/README.md](https://github.com/gchiesa/drl/blob/main/deployments/k8s-fleet/README.md)

---

## Istio Extension

When **Istio** manages your Envoy sidecars, you do not need to redeploy workloads. Instead, an
`EnvoyFilter` resource injects the `ext_authz` HTTP filter into the existing sidecar filter chains,
and DRL is registered as an extension provider in the Istio `MeshConfig`.

This approach lets you apply rate-limiting policy mesh-wide or per-namespace without touching
application manifests.

**Configuration path**

1. Deploy the DRL fleet (`k8s-fleet` or equivalent).
2. Register DRL as an `extensionProvider` in `MeshConfig`:

```yaml
extensionProviders:
  - name: drl-provider
    envoyExtAuthzGrpc:
      service: drl.drl.svc.cluster.local
      port: 8081
      timeout: 250ms
      failOpen: false
```

3. Apply an `AuthorizationPolicy` to target specific workloads:

```yaml
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: drl-ratelimit
  namespace: default
spec:
  selector:
    matchLabels:
      app: echo-server
  action: CUSTOM
  provider:
    name: drl-provider
  rules:
    - {}
```

For the full walkthrough — including the raw `EnvoyFilter` alternative, scoping options, failure-mode
settings, and troubleshooting — see the source linked below.

**Source**  
{{< icon "github" >}} [deployments/istio/README.md](https://github.com/gchiesa/drl/blob/main/deployments/istio/README.md)
