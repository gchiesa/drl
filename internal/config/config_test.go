package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Listen.GRPC != ":8081" {
		t.Errorf("expected Listen.GRPC to be :8081, got %s", cfg.Listen.GRPC)
	}

	if cfg.Listen.Metrics != ":9091" {
		t.Errorf("expected Listen.Metrics to be :9091, got %s", cfg.Listen.Metrics)
	}

	if cfg.Membership.BindAddr != "0.0.0.0" {
		t.Errorf("expected Membership.BindAddr to be 0.0.0.0, got %s", cfg.Membership.BindAddr)
	}

	if cfg.Membership.Port != 7946 {
		t.Errorf("expected Membership.Port to be 7946, got %d", cfg.Membership.Port)
	}

	if cfg.Membership.ServiceName != "drl" {
		t.Errorf("expected Membership.ServiceName to be drl, got %s", cfg.Membership.ServiceName)
	}

	if cfg.Membership.StartupDelay != 3*time.Second {
		t.Errorf("expected Membership.StartupDelay to be 3s, got %v", cfg.Membership.StartupDelay)
	}

	if cfg.Logging.Level != "info" {
		t.Errorf("expected Logging.Level to be info, got %s", cfg.Logging.Level)
	}

	if cfg.Logging.Format != "json" {
		t.Errorf("expected Logging.Format to be json, got %s", cfg.Logging.Format)
	}

	if cfg.ConfigSource != "defaults" {
		t.Errorf("expected ConfigSource to be defaults, got %s", cfg.ConfigSource)
	}
}

func TestLoadWithNoFile(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error loading config with no file: %v", err)
	}

	if cfg.ConfigSource != "defaults" {
		t.Errorf("expected ConfigSource to be defaults, got %s", cfg.ConfigSource)
	}
}

func TestLoadWithMissingFile(t *testing.T) {
	_, err := Load("/non/existent/path/config.kdl")
	if err == nil {
		t.Error("expected error when loading non-existent config file")
	}
}

func TestLoadFromKDL(t *testing.T) {
	// Create a temporary KDL config file
	kdlContent := `
listen {
    grpc ":9000"
    metrics ":9100"
}

membership {
    service-name "test-service"
    port 8000
    bind-addr "192.168.1.1"
    startup-delay "5s"
}

logging {
    level "debug"
    format "text"
}
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.kdl")
	if err := os.WriteFile(configPath, []byte(kdlContent), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading KDL config: %v", err)
	}

	if cfg.Listen.GRPC != ":9000" {
		t.Errorf("expected Listen.GRPC to be :9000, got %s", cfg.Listen.GRPC)
	}

	if cfg.Listen.Metrics != ":9100" {
		t.Errorf("expected Listen.Metrics to be :9100, got %s", cfg.Listen.Metrics)
	}

	if cfg.Membership.ServiceName != "test-service" {
		t.Errorf("expected Membership.ServiceName to be test-service, got %s", cfg.Membership.ServiceName)
	}

	if cfg.Membership.Port != 8000 {
		t.Errorf("expected Membership.Port to be 8000, got %d", cfg.Membership.Port)
	}

	if cfg.Membership.BindAddr != "192.168.1.1" {
		t.Errorf("expected Membership.BindAddr to be 192.168.1.1, got %s", cfg.Membership.BindAddr)
	}

	if cfg.Membership.StartupDelay != 5*time.Second {
		t.Errorf("expected Membership.StartupDelay to be 5s, got %v", cfg.Membership.StartupDelay)
	}

	if cfg.Logging.Level != "debug" {
		t.Errorf("expected Logging.Level to be debug, got %s", cfg.Logging.Level)
	}

	if cfg.Logging.Format != "text" {
		t.Errorf("expected Logging.Format to be text, got %s", cfg.Logging.Format)
	}

	if cfg.ConfigSource != configPath {
		t.Errorf("expected ConfigSource to be %s, got %s", configPath, cfg.ConfigSource)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	// Set environment variables
	t.Setenv("DRL_NODE_NAME", "env-node")
	t.Setenv("DRL_LISTEN_GRPC", ":7000")
	t.Setenv("DRL_LISTEN_METRICS", ":7100")
	t.Setenv("DRL_MEMBERSHIP_SERVICE_NAME", "env-service")
	t.Setenv("DRL_MEMBERSHIP_PORT", "6000")
	t.Setenv("DRL_MEMBERSHIP_BIND_ADDR", "10.0.0.1")
	t.Setenv("DRL_MEMBERSHIP_STARTUP_DELAY", "10s")
	t.Setenv("DRL_LOGGING_LEVEL", "error")
	t.Setenv("DRL_LOGGING_FORMAT", "text")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.NodeName != "env-node" {
		t.Errorf("expected NodeName to be env-node, got %s", cfg.NodeName)
	}

	if cfg.Listen.GRPC != ":7000" {
		t.Errorf("expected Listen.GRPC to be :7000, got %s", cfg.Listen.GRPC)
	}

	if cfg.Listen.Metrics != ":7100" {
		t.Errorf("expected Listen.Metrics to be :7100, got %s", cfg.Listen.Metrics)
	}

	if cfg.Membership.ServiceName != "env-service" {
		t.Errorf("expected Membership.ServiceName to be env-service, got %s", cfg.Membership.ServiceName)
	}

	if cfg.Membership.Port != 6000 {
		t.Errorf("expected Membership.Port to be 6000, got %d", cfg.Membership.Port)
	}

	if cfg.Membership.BindAddr != "10.0.0.1" {
		t.Errorf("expected Membership.BindAddr to be 10.0.0.1, got %s", cfg.Membership.BindAddr)
	}

	if cfg.Membership.StartupDelay != 10*time.Second {
		t.Errorf("expected Membership.StartupDelay to be 10s, got %v", cfg.Membership.StartupDelay)
	}

	if cfg.Logging.Level != "error" {
		t.Errorf("expected Logging.Level to be error, got %s", cfg.Logging.Level)
	}

	if cfg.Logging.Format != "text" {
		t.Errorf("expected Logging.Format to be text, got %s", cfg.Logging.Format)
	}
}

func TestEnvironmentOverridesKDL(t *testing.T) {
	// Create a KDL config file with info level
	kdlContent := `
listen {
    grpc ":8081"
    metrics ":9091"
}

membership {
    service-name "kdl-service"
    port 7946
    bind-addr "0.0.0.0"
    startup-delay "3s"
}

logging {
    level "info"
    format "json"
}
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.kdl")
	if err := os.WriteFile(configPath, []byte(kdlContent), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	// Set environment variable to override logging level
	t.Setenv("DRL_LOGGING_LEVEL", "debug")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	// Environment should override KDL
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected Logging.Level to be debug (env override), got %s", cfg.Logging.Level)
	}

	// KDL value should be preserved where not overridden
	if cfg.Membership.ServiceName != "kdl-service" {
		t.Errorf("expected Membership.ServiceName to be kdl-service (from KDL), got %s", cfg.Membership.ServiceName)
	}
}

func TestLegacyEnvironmentVariables(t *testing.T) {
	// Test legacy environment variable support
	t.Setenv("NODE_NAME", "legacy-node")
	t.Setenv("DISCOVERY_SERVICE_NAME", "legacy-service")
	t.Setenv("BIND_PORT", "5000")
	t.Setenv("BIND_ADDR", "172.16.0.1")
	t.Setenv("STARTUP_DELAY", "7s")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.NodeName != "legacy-node" {
		t.Errorf("expected NodeName to be legacy-node, got %s", cfg.NodeName)
	}

	if cfg.Membership.ServiceName != "legacy-service" {
		t.Errorf("expected Membership.ServiceName to be legacy-service, got %s", cfg.Membership.ServiceName)
	}

	if cfg.Membership.Port != 5000 {
		t.Errorf("expected Membership.Port to be 5000, got %d", cfg.Membership.Port)
	}

	if cfg.Membership.BindAddr != "172.16.0.1" {
		t.Errorf("expected Membership.BindAddr to be 172.16.0.1, got %s", cfg.Membership.BindAddr)
	}

	if cfg.Membership.StartupDelay != 7*time.Second {
		t.Errorf("expected Membership.StartupDelay to be 7s, got %v", cfg.Membership.StartupDelay)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name        string
		modifyFunc  func(*Config)
		expectError bool
	}{
		{
			name:        "valid config",
			modifyFunc:  func(c *Config) {},
			expectError: false,
		},
		{
			name: "empty service name",
			modifyFunc: func(c *Config) {
				c.Membership.ServiceName = ""
			},
			expectError: true,
		},
		{
			name: "invalid port - too low",
			modifyFunc: func(c *Config) {
				c.Membership.Port = 0
			},
			expectError: true,
		},
		{
			name: "invalid port - too high",
			modifyFunc: func(c *Config) {
				c.Membership.Port = 70000
			},
			expectError: true,
		},
		{
			name: "empty bind address",
			modifyFunc: func(c *Config) {
				c.Membership.BindAddr = ""
			},
			expectError: true,
		},
		{
			name: "empty grpc address",
			modifyFunc: func(c *Config) {
				c.Listen.GRPC = ""
			},
			expectError: true,
		},
		{
			name: "empty metrics address",
			modifyFunc: func(c *Config) {
				c.Listen.Metrics = ""
			},
			expectError: true,
		},
		{
			name: "invalid log level",
			modifyFunc: func(c *Config) {
				c.Logging.Level = "invalid"
			},
			expectError: true,
		},
		{
			name: "invalid log format",
			modifyFunc: func(c *Config) {
				c.Logging.Format = "invalid"
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modifyFunc(cfg)
			err := cfg.Validate()

			if tt.expectError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestInvalidKDLSyntax(t *testing.T) {
	// Create a file with invalid KDL syntax
	kdlContent := `this is not valid kdl {{{`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.kdl")
	if err := os.WriteFile(configPath, []byte(kdlContent), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("expected error when loading invalid KDL file")
	}
}

func TestKDLWithComments(t *testing.T) {
	// KDL supports comments
	kdlContent := `
// This is a comment
listen {
    grpc ":8081"    // inline comment
    metrics ":9091"
}

/* Block comment */
membership {
    service-name "test"
    port 7946
    bind-addr "0.0.0.0"
    startup-delay "3s"
}

logging {
    level "info"
    format "json"
}
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.kdl")
	if err := os.WriteFile(configPath, []byte(kdlContent), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading KDL with comments: %v", err)
	}

	if cfg.Listen.GRPC != ":8081" {
		t.Errorf("expected Listen.GRPC to be :8081, got %s", cfg.Listen.GRPC)
	}
}

func TestMetricsPortExtraction(t *testing.T) {
	tests := []struct {
		metricsAddr string
		expected    int
	}{
		{":9091", 9091},
		{"0.0.0.0:8080", 8080},
		{"localhost:3000", 3000},
		{"invalid", 9091}, // fallback to default
	}

	for _, tt := range tests {
		t.Run(tt.metricsAddr, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Listen.Metrics = tt.metricsAddr
			if got := cfg.MetricsPort(); got != tt.expected {
				t.Errorf("MetricsPort() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestPartialKDLConfig(t *testing.T) {
	// KDL file that only specifies some values
	kdlContent := `
membership {
    service-name "partial-test"
    port 9999
    bind-addr "0.0.0.0"
    startup-delay "1s"
}

logging {
    level "warn"
    format "json"
}
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "partial.kdl")
	if err := os.WriteFile(configPath, []byte(kdlContent), 0644); err != nil {
		t.Fatalf("failed to write temp config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error loading partial KDL config: %v", err)
	}

	// Specified values should be from KDL
	if cfg.Membership.ServiceName != "partial-test" {
		t.Errorf("expected Membership.ServiceName to be partial-test, got %s", cfg.Membership.ServiceName)
	}

	if cfg.Membership.Port != 9999 {
		t.Errorf("expected Membership.Port to be 9999, got %d", cfg.Membership.Port)
	}

	// Unspecified values should use defaults
	if cfg.Listen.GRPC != ":8081" {
		t.Errorf("expected Listen.GRPC to be :8081 (default), got %s", cfg.Listen.GRPC)
	}

	if cfg.Listen.Metrics != ":9091" {
		t.Errorf("expected Listen.Metrics to be :9091 (default), got %s", cfg.Listen.Metrics)
	}
}
