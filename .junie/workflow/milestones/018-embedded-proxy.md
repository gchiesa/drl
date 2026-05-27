# 018-embedded-proxy.md

## Goal

DRL is focused specifically on rate limiting and usually requires Envoy to work via gRPC check filters. However, in
simpler architectures or small workloads, maintaining an additional Envoy sidecar introduces unneeded operational
overhead.

The goal of this milestone is to introduce an opinionated, ultra-lightweight reverse proxy engine called
`embedded-proxy`. When activated, it enables DRL to handle edge or sidecar ingress directly over HTTP/HTTPS,
transparently piping requests through the DRL local accounting rules before proxying traffic down to the target upstream
service.

---

## 1. Architectural Requirements & Core Technologies

To maintain sidecar performance constraints, the implementation must use native, highly performant Go constructs:

* **Core Proxy:** Must be based entirely on standard library `net/http/httputil.ReverseProxy`. This guarantees streaming
  bodies, chunked transfers, context propagation, connection pooling, and standard header management with zero extra
  dependencies.
* **Routing Engine:** Use `github.com/go-chi/chi/v5` for lightweight inline middleware management, host-based
  multiplexing, and route prefix evaluation.
* **Metrics Integration:** Proxy metrics (Total request counts, error counters, latency histograms) must be exposed via
  the official `prometheus/client_golang` package. These metrics must be available on the configured metrics endpoint
  matching the rest of the application's reporting structures.
* **Rate Limiter Linkage:** Incoming proxy traffic must automatically invoke the local DRL accounting rules based on
  matching request contexts (`IP + uriPath + Headers`) before routing upstream.

---

## 2. Configuration Schema & KDL Validation

The embedded proxy configuration must integrate cleanly into the existing KDL parsing core, supporting implicit default
fallbacks and override injections via environment variables (e.g., matching the `DRL_` prefix convention).

### Production-Ready KDL Schema Definition

```kdl
// Embedded Proxy Configuration (Alternative to running an Envoy sidecar)
embedded-proxy {
    enabled true
    listen ":8443" // Active listening port for inbound traffic

// Explicit TLS control block
    tls {
        enabled true // Set to false if terminating at Cloud Load Balancer level
        cert "base64-encoded-certificate-pem-here..."
        key "base64-encoded-private-key-pem-here..."
    }

// Host-based routing blocks (SNI matching or Host header checking)
    host "api.example.com" {
        routes {
        // Configuration 1: Standard Single Target Upstream (Sidecar Pattern)
            route "/local-api" {
                upstream "http://127.0.0.1:8082"
                require-auth false
            }

        // Configuration 2: Fleet Upstream via Headless K8s Service
            route "/fleet-service" {
                upstream "http://backend-headless.production.svc.cluster.local:8080"
                balance-strategy "dns-round-robin"
                dns-refresh-interval "5s"
                require-auth false
            }
        }
    }

// Secondary virtual host allocation
    host "admin.example.com" {
        routes {
            route "/" {
                upstream "http://127.0.0.1:9000"
                require-auth false
            }
        }
    }
}

```

### Configuration Behaviors:

1. **TLS Optionality:** If `tls.enabled` is `false`, the proxy must start using `http.ListenAndServe`. If `true`, the
   proxy must decode the inline base64 strings directly into memory via `crypto/tls.X509KeyPair` and hook them into
   `http.ListenAndServeTLS`. Files must never be written to disk.
2. **Authentication Flag:** For this milestone, the `require-auth` property must be safely parsed and structured into
   the memory routing maps to allow downstream expansion (such as future OIDC token filters). Implementation of
   authentication is out of scope.

---

## 3. Runtime Features & Dynamic Load Balancing

### Client-Side DNS Round-Robin Fleet Balancing

When `balance-strategy` is set to `"dns-round-robin"`, the proxy must bypass Go's single-IP connection-caching behavior
to ensure even distribution across a headless service pool.

* **Background Worker:** Spin up an internal thread-safe resolver tracking a background ticker set to the
  `dns-refresh-interval` value.
* **Resolution Engine:** Consume `net.DefaultResolver.LookupHost(ctx, host)` to query the authoritative DNS core A/AAAA
  records.
* **Director Execution:** Implement an atomic index lookup counter via `sync/atomic` inside the
  `httputil.ReverseProxy.Director` function to step across live endpoints sequentially without locks.

```go
package proxy

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type RoundRobinDirector struct {
	sync.RWMutex
	ips     []string
	counter uint64
}

// WatchDNS polls target addresses at the designated interval to avoid Go's client cache
func (rrd *RoundRobinDirector) WatchDNS(ctx context.Context, host string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newIPs, err := net.DefaultResolver.LookupHost(ctx, host)
			if err == nil && len(newIPs) > 0 {
				rrd.Lock()
				rrd.ips = newIPs
				rrd.Unlock()
			}
		}
	}
}

// DirectRequest updates the outgoing HTTP target dynamically across available pool records
func (rrd *RoundRobinDirector) DirectRequest(req *http.Request) {
	rrd.RLock()
	defer rrd.RUnlock()

	if len(rrd.ips) == 0 {
		return
	}

	// Lockless index modification
	idx := atomic.AddUint64(&rrd.counter, 1) % uint64(len(rrd.ips))
	targetIP := rrd.ips[idx]

	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		// Fallback to implicit port handling if missing
		port = "80"
		if req.URL.Scheme == "https" {
			port = "443"
		}
	}

	req.URL.Host = net.JoinHostPort(targetIP, port)
}

```

---

## 4. Implementation & Testing Guidelines

Consistent with the milestone-driven lifecycle defined in `CLAUDE.md`, this addition must compile with zero global lint
errors and enforce testing validation before being marked complete.

### Code Layout Additions:

```txt
<root folder>
internal/proxy/             <-- New proxy core package
internal/proxy/proxy.go     <-- Proxy server execution & middleware loop
internal/proxy/balancer.go  <-- DNS Round-robin tracking structures
internal/config/            <-- Update parsing logic for 'embedded-proxy' stanza

```

### Verification Criteria (Mise Automation Run):

1. **Unit Tests:** Implement comprehensive test tables validating:

* Base64 conversion parsing exceptions during invalid TLS block initialization.
* Virtual host routing separation (Request to `hostA` does not reach `hostB` paths).
* Atomic correctness of the `RoundRobinDirector` when managing single-node versus multi-IP lookups.


2. **Lint Check:** Running `mise run lint` must cleanly complete without failures matching the system configuration
   profile `.golangci-lint.yaml`.
3. **Completion Hook:** Once local unit validations successfully clear, the automation creates the indicator log file:
   `.junie/workflow/state/018-embedded-proxy.completed`.

