# K8s Embedded Proxy Deployment

This Kustomize base deploys DRL in **embedded-proxy sidecar** mode on Kubernetes. DRL runs in the same pod as
echo-server and acts as the edge reverse proxy, replacing Envoy entirely — there is no Envoy container in this topology.

## Architecture

```
                    ┌─ drl namespace ─────────────────────────────────────────────┐
                    │                                                              │
  Client ──HTTP──►  │  Service (echo-server:80)                                   │
                    │       │                                                      │
                    │       ▼                                                      │
                    │  ┌─── Pod (×3) ──────────────────────────────────────────┐  │
                    │  │                                                        │  │
                    │  │  ┌─ drl sidecar ──────────────────────────────────┐   │  │
                    │  │  │  embedded proxy  :8080                         │   │  │
                    │  │  │    1. Auth0 OIDC Bearer token validation       │   │  │
                    │  │  │    2. Rate-limit blocklist check               │   │  │
                    │  │  │    3. Async P2P accounting                    │   │  │
                    │  │  │    4. Forward → localhost:18080               │   │  │
                    │  │  │                                                │   │  │
                    │  │  │  gRPC :8081 · metrics :9091 · gossip :7946   │   │  │
                    │  │  └────────────────────────────────────────────────┘   │  │
                    │  │                         │ localhost                    │  │
                    │  │  ┌─ echo-server ─────────▼──────────────────────┐    │  │
                    │  │  │  go-httpbin  :18080                          │    │  │
                    │  │  └──────────────────────────────────────────────┘    │  │
                    │  └────────────────────────────────────────────────────────┘  │
                    │                   │ gossip (drl-headless)                    │
                    │                   └──────► other pods                        │
                    └──────────────────────────────────────────────────────────────┘
```

## Key differences from k8s-sidecar

| Aspect            | k8s-sidecar                    | k8s-embedded-proxy                 |
|-------------------|--------------------------------|------------------------------------|
| Ingress proxy     | Envoy sidecar                  | DRL embedded proxy                 |
| Auth enforcement  | Envoy ext_authz → DRL gRPC     | Auth0 OIDC JWT validation in DRL   |
| DRL gRPC :8081    | Used by Envoy (ext_authz)      | Available but not used for ingress |
| Pod composition   | echo-server + envoy + drl      | echo-server + drl                  |
| echo-server port  | :8080                          | :18080 (loopback only)             |
| User-facing entry | Envoy :10000 → Service port 80 | DRL :8080 → Service port 80        |

## Services

| Service        | Type      | Port      | Selector           | Purpose                               |
|----------------|-----------|-----------|--------------------|---------------------------------------|
| `echo-server`  | ClusterIP | 80 → 8080 | `app: echo-server` | User-facing HTTP endpoint (DRL proxy) |
| `drl-headless` | Headless  | 7946      | `app: echo-server` | Memberlist peer discovery             |

## Auth0 OIDC configuration

DRL acts as an OIDC **Resource Server** — it validates Bearer tokens but never issues them. Clients obtain a JWT access
token from Auth0 and present it as `Authorization: Bearer <token>` on every request.

| Field                | Value                                                                       |
|----------------------|-----------------------------------------------------------------------------|
| OpenID Configuration | `https://dev-xxwr5gxe.eu.auth0.com/.well-known/openid-configuration`        |
| JWKS URI             | `https://dev-xxwr5gxe.eu.auth0.com/.well-known/jwks.json` (auto-discovered) |
| Token endpoint       | `https://dev-xxwr5gxe.eu.auth0.com/oauth/token`                             |
| Issuer               | `https://dev-xxwr5gxe.eu.auth0.com/`                                        |

> **Action required:** Open `base/configmap.yaml` and replace the `audience`
> value (`https://echo-server.example.com/api`) with the **Identifier** of the
> API you registered under *Auth0 Dashboard → Applications → APIs*.
> This value must match the `aud` claim in access tokens issued by Auth0.

## Prerequisites

Create the `drl` namespace and required Secret before applying:

```bash
kubectl create namespace drl

kubectl -n drl create secret generic drl-secrets \
  --from-literal=private-api-key="<random-32-char-string>" \
  --from-literal=membership-primary-key="<exactly-16-24-or-32-char-key>"
```

## Applying

```bash
kubectl apply -k deployments/k8s-embedded-proxy/base
```

Verify rollout:

```bash
kubectl -n drl rollout status deployment/echo-server
```

## Accessing the service

```bash
# Port-forward DRL's embedded proxy
kubectl -n drl port-forward svc/echo-server 8080:80

# Obtain an Auth0 access token via client credentials
TOKEN=$(curl -s -X POST https://dev-xxwr5gxe.eu.auth0.com/oauth/token \
  -H "Content-Type: application/json" \
  -d '{
    "client_id":     "<YOUR_AUTH0_CLIENT_ID>",
    "client_secret": "<YOUR_AUTH0_CLIENT_SECRET>",
    "audience":      "https://echo-server.example.com/api",
    "grant_type":    "client_credentials"
  }' | jq -r .access_token)

# Call the protected workload
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/anything

# Request without a token returns 401
curl -v http://localhost:8080/anything
```

## Runtime rule overrides

Override accounting rules per-deployment at runtime without a config reload:

```bash
kubectl -n drl set env deployment/echo-server \
  'DRL_RULE_catch-all_JSON={"path-prefix":"/","limit":200,"per":"minute"}'
```

## Customising OIDC per environment

To override the full embedded-proxy host configuration via environment variable (useful for overlays that target staging
vs production Auth0 tenants):

```bash
kubectl -n drl set env deployment/echo-server \
  'DRL_EMBEDDED_PROXY_HOSTS_JSON=[{"hostname":"echo-server","oidc":{"issuer":"https://prod.eu.auth0.com/","audience":"https://api.prod.example.com"},"routes":{"routes":[{"prefix":"/","upstream":"http://127.0.0.1:18080","require-auth":true}]}}]'
```
