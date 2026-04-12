# DRL K8s Sidecar Deployment (Kustomize)

Kustomize manifests for deploying DRL as a **sidecar** inside each application pod, alongside the Envoy proxy, targeting an **existing Kubernetes cluster**.

## Architecture

```
                  ┌──────────────────────────────────────────────────────────┐
                  │  Namespace: drl                                           │
                  │                                                           │
  Ingress/:80     │  ┌─────────────────────────────────────────────────────┐ │
  ─────────────────▶ │  Pod: echo-server (x3)                              │ │
                  │  │                                                      │ │
                  │  │  ┌─────────────┐  localhost   ┌──────────────────┐  │ │
                  │  │  │   envoy     │─────:8080────▶│  echo-server     │  │ │
                  │  │  │  :10000     │               └──────────────────┘  │ │
                  │  │  └──────┬──────┘                                     │ │
                  │  │         │ ext_authz gRPC (localhost:8081)             │ │
                  │  │         ▼                                             │ │
                  │  │  ┌─────────────┐                                     │ │
                  │  │  │    drl      │◀──── memberlist gossip               │ │
                  │  │  │ :8081 :7946 │      drl-headless (DNS)              │ │
                  │  │  └─────────────┘                                     │ │
                  │  └─────────────────────────────────────────────────────┘ │
                  └──────────────────────────────────────────────────────────┘
```

Each pod runs three containers sharing localhost:

| Container   | Role                                            | Port(s)                     |
|-------------|-------------------------------------------------|-----------------------------|
| echo-server | Backend workload (`mccutchen/go-httpbin`)        | 8080                        |
| envoy       | Reverse proxy — calls DRL before upstream        | 10000, 9901 (admin)         |
| drl         | Rate limiter sidecar — ext_authz gRPC handler   | 8081, 8082, 9091, 7946      |

DRL instances across pods discover each other via the **`drl-headless`** Kubernetes headless Service, which resolves to all pod IPs.

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

> For production use an external secrets operator (External Secrets Operator, Vault Agent, Sealed Secrets).

### 2. Set your DRL image

Edit `base/kustomization.yaml` and update the `images` block:

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
kubectl -n drl get pods
kubectl -n drl logs -l app=echo-server -c drl
```

## Directory Layout

```
k8s-sidecar/
└── base/
    ├── kustomization.yaml   # entry point
    ├── namespace.yaml       # drl namespace
    ├── configmap.yaml       # envoy.yaml + drl config.kdl
    ├── deployment.yaml      # pod spec: echo-server + envoy + drl
    └── service.yaml         # ClusterIP + headless (memberlist)
```

## Peer Discovery

DRL uses **Hashicorp Memberlist** for P2P gossip. In this sidecar model, each pod has one DRL instance. The `drl-headless` headless Service makes all pod IPs available via DNS (`drl-headless.drl.svc.cluster.local`), enabling memberlist to bootstrap the cluster.

The `join-addr` in `config/drl/config.kdl` is pre-set to this DNS name.

## Customisation with Overlays

Create an overlay for environment-specific patches:

```bash
mkdir -p overlays/prod
cat > overlays/prod/kustomization.yaml <<EOF
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: drl
bases:
  - ../../base
patches:
  - path: replicas-patch.yaml
EOF
```
