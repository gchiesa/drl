# DRL K8s Fleet Deployment (Kustomize)

Kustomize manifests for deploying DRL as a **dedicated fleet** — a separate Deployment with its own Service — while the echo-server workload has Envoy as a sidecar that connects to the DRL fleet over the cluster network.

## Architecture

```
                  ┌──────────────────────────────────────────────────────────────┐
                  │  Namespace: drl                                               │
                  │                                                               │
  Ingress/:80     │  ┌─────────────────────────────────────────────────────────┐ │
  ─────────────────▶ │  echo-server Deployment (x3)                            │ │
                  │  │                                                          │ │
                  │  │  ┌─────────────────────────────────────────────────┐    │ │
                  │  │  │  Pod                                             │    │ │
                  │  │  │  ┌────────────┐  localhost   ┌───────────────┐  │    │ │
                  │  │  │  │   envoy    │────:8080─────▶│  echo-server  │  │    │ │
                  │  │  │  │  :10000    │               └───────────────┘  │    │ │
                  │  │  │  └──────┬─────┘                                  │    │ │
                  │  │  └─────────┼───────────────────────────────────────┘    │ │
                  │  └────────────┼────────────────────────────────────────────┘ │
                  │               │ ext_authz gRPC                                │
                  │               │ drl.drl.svc.cluster.local:8081                │
                  │               ▼                                               │
                  │  ┌────────────────────────────────────────────────────────┐  │
                  │  │  DRL Fleet Deployment (x3)                             │  │
                  │  │                                                        │  │
                  │  │  ┌──────────┐  ┌──────────┐  ┌──────────┐             │  │
                  │  │  │  drl-0   │  │  drl-1   │  │  drl-2   │             │  │
                  │  │  │ :8081    │◀▶│ :8081    │◀▶│ :8081    │  P2P gossip │  │
                  │  │  │ :7946    │  │ :7946    │  │ :7946    │  via        │  │
                  │  │  └──────────┘  └──────────┘  └──────────┘  drl-      │  │
                  │  │                                              headless  │  │
                  │  └────────────────────────────────────────────────────────┘  │
                  └──────────────────────────────────────────────────────────────┘
```

| Component        | Role                                                              |
|------------------|-------------------------------------------------------------------|
| echo-server      | Backend workload (`mccutchen/go-httpbin`)                         |
| envoy (sidecar)  | Reverse proxy — calls DRL fleet before forwarding upstream        |
| drl (fleet)      | Dedicated rate-limiting service — ext_authz gRPC + P2P gossip    |

Envoy resolves the DRL fleet via the **`drl` ClusterIP Service** (`drl.drl.svc.cluster.local:8081`), which load-balances across all DRL pods. DRL pods discover each other for gossip via the **`drl-headless`** headless Service.

## Prerequisites

- `kubectl` >= 1.25
- `kustomize` >= 5.0 (or `kubectl apply -k`)
- Access to an existing Kubernetes cluster
- DRL image pushed to a registry accessible from the cluster

## Quick Start

### 1. Create the secrets

```bash
kubectl create secret generic drl-secrets \
  --namespace drl \
  --from-literal=private-api-key='<YOUR_KEY>' \
  --from-literal=membership-primary-key='<16_BYTE_HEX>' \
  --from-literal=membership-secondary-keys=''
```

### 2. Set your DRL image

Edit `base/kustomization.yaml`:

```yaml
images:
  - name: ghcr.io/your-org/drl
    newName: <YOUR_REGISTRY>/drl
    newTag: <YOUR_TAG>
```

### 3. Apply

```bash
# Preview
kubectl kustomize base/

# Apply
kubectl apply -k base/
```

### 4. Verify

```bash
# All pods running
kubectl -n drl get pods

# DRL fleet formed a gossip cluster (look for "memberlist: joining" messages)
kubectl -n drl logs -l app=drl

# DRL metrics
kubectl -n drl port-forward svc/drl 9091:9091
curl http://localhost:9091/metrics
```

## Directory Layout

```
k8s-fleet/
└── base/
    ├── kustomization.yaml         # entry point
    ├── namespace.yaml             # drl namespace
    ├── echo-server/
    │   ├── configmap.yaml         # envoy.yaml (connects to drl Service)
    │   ├── deployment.yaml        # echo-server + envoy sidecar
    │   └── service.yaml           # ClusterIP :80 → envoy :10000
    └── drl/
        ├── configmap.yaml         # drl config.kdl
        ├── deployment.yaml        # DRL fleet (3 replicas, anti-affinity)
        └── service.yaml           # ClusterIP :8081 + headless (memberlist)
```

## Differences from k8s-sidecar

| Aspect            | k8s-sidecar                        | k8s-fleet (this)                       |
|-------------------|------------------------------------|----------------------------------------|
| DRL topology      | One DRL per app pod (sidecar)       | Dedicated DRL deployment               |
| Envoy→DRL path    | `localhost:8081`                   | `drl.drl.svc.cluster.local:8081`       |
| Scaling DRL       | Coupled to app replica count        | Independent — scale DRL separately     |
| Resource isolation| Shared pod resource budget          | DRL has its own resource quota         |
| Gossip overhead   | One instance per app pod            | Fixed fleet — gossip traffic is bounded|

## Customisation with Overlays

```bash
mkdir -p overlays/prod
cat > overlays/prod/kustomization.yaml <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: drl
resources:
  - ../../base
patches:
  - path: drl-replicas.yaml   # scale DRL fleet independently
EOF
```
