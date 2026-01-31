package metrics

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()

	if m.ClusterSize == nil {
		t.Error("ClusterSize gauge should not be nil")
	}

	if m.EventsTotal == nil {
		t.Error("EventsTotal counter should not be nil")
	}

	if m.registry == nil {
		t.Error("registry should not be nil")
	}
}

func TestSetClusterSize(t *testing.T) {
	m := NewMetrics()

	m.SetClusterSize(5)
	// The gauge is set - we can't easily read it back without the registry,
	// but we verify no panic occurs
}

func TestIncEvent(t *testing.T) {
	m := NewMetrics()

	// Verify no panic for different event types
	m.IncEvent("join")
	m.IncEvent("leave")
	m.IncEvent("fail")
}

func TestStartServer(t *testing.T) {
	m := NewMetrics()

	// Set some metric values so they appear in output
	m.SetClusterSize(3)
	m.IncEvent("join")

	// Use a random high port to avoid conflicts
	port := 19091
	err := m.StartServer(port)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer func() {
		_ = m.Stop()
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

	// Test /health endpoint
	resp, err := http.Get("http://localhost:19091/health")
	if err != nil {
		t.Fatalf("failed to reach health endpoint: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "OK" {
		t.Errorf("expected body 'OK', got '%s'", string(body))
	}

	// Test /metrics endpoint
	resp2, err := http.Get("http://localhost:19091/metrics")
	if err != nil {
		t.Fatalf("failed to reach metrics endpoint: %v", err)
	}
	defer func() {
		_ = resp2.Body.Close()
	}()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp2.StatusCode)
	}

	metricsBody, _ := io.ReadAll(resp2.Body)
	metricsStr := string(metricsBody)

	// Verify our custom metrics are exposed
	if !strings.Contains(metricsStr, "drl_membership_cluster_size") {
		t.Error("expected drl_membership_cluster_size metric in output")
	}

	if !strings.Contains(metricsStr, "drl_membership_events_total") {
		t.Error("expected drl_membership_events_total metric in output")
	}
}

func TestStop(t *testing.T) {
	m := NewMetrics()

	port := 19092
	err := m.StartServer(port)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	err = m.Stop()
	if err != nil {
		t.Errorf("failed to stop server: %v", err)
	}

	// Verify server is stopped
	time.Sleep(100 * time.Millisecond)
	_, err = http.Get("http://localhost:19092/health")
	if err == nil {
		t.Error("expected error after server stopped")
	}
}

func TestStopWithoutStart(t *testing.T) {
	m := NewMetrics()

	// Should not panic or error
	err := m.Stop()
	if err != nil {
		t.Errorf("unexpected error stopping unstarted server: %v", err)
	}
}
