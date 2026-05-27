package proxy

import (
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoundRobinDirector_EmptyPool(t *testing.T) {
	rrd := NewRoundRobinDirector()
	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "example.com:80"}}
	original := req.URL.Host

	// With an empty pool DirectRequest must not touch the URL.
	rrd.DirectRequest(req)
	assert.Equal(t, original, req.URL.Host, "empty pool should leave URL unchanged")
}

func TestRoundRobinDirector_SingleIP(t *testing.T) {
	rrd := NewRoundRobinDirector()
	rrd.SetIPs([]string{"10.0.0.1"})

	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "svc.local:8080"}}
	rrd.DirectRequest(req)

	assert.Equal(t, "10.0.0.1:8080", req.URL.Host, "single-IP pool should always target that IP")
}

func TestRoundRobinDirector_MultipleIPs_Distribution(t *testing.T) {
	ips := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	rrd := NewRoundRobinDirector()
	rrd.SetIPs(ips)

	hits := map[string]int{}
	const rounds = 300
	for i := 0; i < rounds; i++ {
		req := &http.Request{URL: &url.URL{Scheme: "http", Host: "svc.local:9000"}}
		rrd.DirectRequest(req)
		host := hostOnly(t, req.URL.Host)
		hits[host]++
	}

	// Each IP should receive some requests.
	for _, ip := range ips {
		assert.Greater(t, hits[ip], 0, "IP %s should have received requests", ip)
	}
}

func TestRoundRobinDirector_AtomicCounter_NoDataRace(t *testing.T) {
	rrd := NewRoundRobinDirector()
	rrd.SetIPs([]string{"192.168.1.1", "192.168.1.2"})

	var wg sync.WaitGroup
	var ops int64

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				req := &http.Request{URL: &url.URL{Scheme: "http", Host: "svc:80"}}
				rrd.DirectRequest(req)
				atomic.AddInt64(&ops, 1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(5000), atomic.LoadInt64(&ops))
}

func TestRoundRobinDirector_SetIPs_ReplacesPool(t *testing.T) {
	rrd := NewRoundRobinDirector()
	rrd.SetIPs([]string{"1.1.1.1"})
	assert.Equal(t, []string{"1.1.1.1"}, rrd.IPs())

	rrd.SetIPs([]string{"2.2.2.2", "3.3.3.3"})
	assert.Equal(t, []string{"2.2.2.2", "3.3.3.3"}, rrd.IPs())
}

func TestRoundRobinDirector_MissingPort_HTTP(t *testing.T) {
	rrd := NewRoundRobinDirector()
	rrd.SetIPs([]string{"10.0.0.5"})

	// URL without explicit port – SplitHostPort will fail and we fall back to 80.
	req := &http.Request{URL: &url.URL{Scheme: "http", Host: "no-port-host"}}
	rrd.DirectRequest(req)
	assert.Equal(t, "10.0.0.5:80", req.URL.Host)
}

func TestRoundRobinDirector_MissingPort_HTTPS(t *testing.T) {
	rrd := NewRoundRobinDirector()
	rrd.SetIPs([]string{"10.0.0.5"})

	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "no-port-host"}}
	rrd.DirectRequest(req)
	assert.Equal(t, "10.0.0.5:443", req.URL.Host)
}

// hostOnly extracts the host portion from a "host:port" string.
func hostOnly(t *testing.T, hostport string) string {
	t.Helper()
	for i := len(hostport) - 1; i >= 0; i-- {
		if hostport[i] == ':' {
			return hostport[:i]
		}
	}
	require.Fail(t, "expected host:port, got: "+hostport)
	return ""
}
