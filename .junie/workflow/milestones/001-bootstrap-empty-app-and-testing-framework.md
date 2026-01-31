# 001-bootstrap-empty-app-and-testing-framework.md

## Goal

Establish the foundational project structure, a "no-op" Go application, and a fully functional Docker Compose
environment where traffic flows from **k6 → Envoy → Echo Server**.

## Requirements

### 1. Project Structure & Tooling

* Initialize a Go module `github.com/gchiesa/drl`.
* Configure `mise.toml` with tasks for `build`, `test`, and `lint`.
* Create a skeleton `main.go` that stays alive (e.g., listening for a signal or a dummy port).

### 2. Manual Testing Infrastructure (Docker Compose)

Setup a `docker-compose.yaml` with the following networking:

* **Service**: `echo-server`
* Image: `mccutchen/go-httpbin`
* Role: The protected backend.


* **Service**: `envoy`
* Image: `envoyproxy/envoy:v1.30-latest` (or latest stable).
* Config: A minimal `envoy.yaml` that performs a **direct cluster routing** to `echo-server` on port 8080.
* *Note:* Do not implement the `ext_authz` filter yet, but ensure the listener is ready for it.


* **Service**: `k6`
* Image: `grafana/k6`
* Script: Implement `load-test.js` using the following `options` stages:

```javascript
stages: [
    {duration: '5s', target: 1},  // 10% ramp-up
    {duration: '10s', target: 3}, // 30% ramp-up
    {duration: '15s', target: 5}, // 50% ramp-up
    {duration: '30s', target: 10},// 100% (10 VUs)
]

```

* Environment: `TARGET_URL` pointing to the Envoy listener.

### 3. DRL Skeleton

* Create a `Dockerfile` for the DRL service (multi-stage build recommended).
* Add `drl` to `docker-compose.yaml` but leave it **commented out** or in a "waiting" state to satisfy the "not started"
  requirement while ensuring the build context works.

## Validation Criteria

1. **Mise Check**: `mise run build` completes without errors.
2. **Connectivity**: Running `docker-compose up envoy echo-server k6` results in k6 logging `200 OK` responses from the
   echo server.
3. **Logs**: Envoy access logs should show traffic being proxied successfully.

---
