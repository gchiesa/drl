package proxy

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// RoundRobinDirector tracks a pool of IPs resolved from a DNS name and
// distributes requests across them using a lockless atomic counter.
type RoundRobinDirector struct {
	mu      sync.RWMutex
	ips     []string
	counter uint64
}

// NewRoundRobinDirector creates a new RoundRobinDirector.
func NewRoundRobinDirector() *RoundRobinDirector {
	return &RoundRobinDirector{}
}

// WatchDNS polls target addresses at the designated interval to avoid Go's
// built-in DNS cache so that all A/AAAA records are kept fresh.
func (rrd *RoundRobinDirector) WatchDNS(ctx context.Context, host string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Do an initial resolution before the first tick.
	if ips, err := net.DefaultResolver.LookupHost(ctx, host); err == nil && len(ips) > 0 {
		rrd.mu.Lock()
		rrd.ips = ips
		rrd.mu.Unlock()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newIPs, err := net.DefaultResolver.LookupHost(ctx, host)
			if err == nil && len(newIPs) > 0 {
				rrd.mu.Lock()
				rrd.ips = newIPs
				rrd.mu.Unlock()
			}
		}
	}
}

// SetIPs replaces the current IP pool. Useful for testing.
func (rrd *RoundRobinDirector) SetIPs(ips []string) {
	rrd.mu.Lock()
	rrd.ips = ips
	rrd.mu.Unlock()
}

// IPs returns a snapshot of the current IP pool.
func (rrd *RoundRobinDirector) IPs() []string {
	rrd.mu.RLock()
	defer rrd.mu.RUnlock()
	result := make([]string, len(rrd.ips))
	copy(result, rrd.ips)
	return result
}

// DirectRequest updates the outgoing HTTP target dynamically across the
// available pool using a lockless atomic index increment.
func (rrd *RoundRobinDirector) DirectRequest(req *http.Request) {
	rrd.mu.RLock()
	defer rrd.mu.RUnlock()

	if len(rrd.ips) == 0 {
		return
	}

	// Lockless index step – safe because we hold RLock (no concurrent write).
	idx := atomic.AddUint64(&rrd.counter, 1) % uint64(len(rrd.ips))
	targetIP := rrd.ips[idx]

	_, port, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		// Fallback to implicit port handling if missing.
		port = "80"
		if req.URL.Scheme == "https" {
			port = "443"
		}
	}

	req.URL.Host = net.JoinHostPort(targetIP, port)
}
