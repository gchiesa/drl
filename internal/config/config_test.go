package config

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.BindAddr != "0.0.0.0" {
		t.Errorf("expected BindAddr to be 0.0.0.0, got %s", cfg.BindAddr)
	}

	if cfg.BindPort != 7946 {
		t.Errorf("expected BindPort to be 7946, got %d", cfg.BindPort)
	}

	if cfg.DiscoveryServiceName != "drl" {
		t.Errorf("expected DiscoveryServiceName to be drl, got %s", cfg.DiscoveryServiceName)
	}

	if cfg.MetricsPort != 9091 {
		t.Errorf("expected MetricsPort to be 9091, got %d", cfg.MetricsPort)
	}

	if cfg.StartupDelay != 3*time.Second {
		t.Errorf("expected StartupDelay to be 3s, got %v", cfg.StartupDelay)
	}
}

func TestConfigFromEnv(t *testing.T) {
	// Use t.Setenv for automatic cleanup
	t.Setenv("NODE_NAME", "test-node")
	t.Setenv("BIND_ADDR", "192.168.1.1")
	t.Setenv("BIND_PORT", "8000")
	t.Setenv("DISCOVERY_SERVICE_NAME", "test-service")
	t.Setenv("METRICS_PORT", "9999")
	t.Setenv("STARTUP_DELAY", "5s")

	cfg := DefaultConfig()

	if cfg.NodeName != "test-node" {
		t.Errorf("expected NodeName to be test-node, got %s", cfg.NodeName)
	}

	if cfg.BindAddr != "192.168.1.1" {
		t.Errorf("expected BindAddr to be 192.168.1.1, got %s", cfg.BindAddr)
	}

	if cfg.BindPort != 8000 {
		t.Errorf("expected BindPort to be 8000, got %d", cfg.BindPort)
	}

	if cfg.DiscoveryServiceName != "test-service" {
		t.Errorf("expected DiscoveryServiceName to be test-service, got %s", cfg.DiscoveryServiceName)
	}

	if cfg.MetricsPort != 9999 {
		t.Errorf("expected MetricsPort to be 9999, got %d", cfg.MetricsPort)
	}

	if cfg.StartupDelay != 5*time.Second {
		t.Errorf("expected StartupDelay to be 5s, got %v", cfg.StartupDelay)
	}
}

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_VAR", "test-value")

	if v := getEnvOrDefault("TEST_VAR", "default"); v != "test-value" {
		t.Errorf("expected test-value, got %s", v)
	}

	if v := getEnvOrDefault("NON_EXISTENT_VAR", "default"); v != "default" {
		t.Errorf("expected default, got %s", v)
	}
}

func TestGetEnvIntOrDefault(t *testing.T) {
	t.Setenv("TEST_INT", "42")

	if v := getEnvIntOrDefault("TEST_INT", 0); v != 42 {
		t.Errorf("expected 42, got %d", v)
	}

	if v := getEnvIntOrDefault("NON_EXISTENT_INT", 99); v != 99 {
		t.Errorf("expected 99, got %d", v)
	}

	t.Setenv("INVALID_INT", "not-a-number")

	if v := getEnvIntOrDefault("INVALID_INT", 100); v != 100 {
		t.Errorf("expected 100 for invalid int, got %d", v)
	}
}

func TestGetEnvDurationOrDefault(t *testing.T) {
	t.Setenv("TEST_DURATION", "10s")

	if v := getEnvDurationOrDefault("TEST_DURATION", time.Second); v != 10*time.Second {
		t.Errorf("expected 10s, got %v", v)
	}

	if v := getEnvDurationOrDefault("NON_EXISTENT_DURATION", 5*time.Second); v != 5*time.Second {
		t.Errorf("expected 5s, got %v", v)
	}

	t.Setenv("INVALID_DURATION", "not-a-duration")

	if v := getEnvDurationOrDefault("INVALID_DURATION", 3*time.Second); v != 3*time.Second {
		t.Errorf("expected 3s for invalid duration, got %v", v)
	}
}
