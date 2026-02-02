package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig(t *testing.T) {
	cfg := NewConfig()
	assert.NotNil(t, cfg)
	assert.Equal(t, "", cfg.NodeName)
	assert.Equal(t, "", cfg.Listen.GRPC)
	assert.Equal(t, "", cfg.Listen.Metrics)
}

func TestLoad_DefaultsOnly(t *testing.T) {
	// Clear any environment variables that might interfere
	clearEnvVars(t)

	cfg, err := Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Check default values from embedded KDL
	assert.Equal(t, ":8081", cfg.Listen.GRPC)
	assert.Equal(t, ":9091", cfg.Listen.Metrics)
	assert.Equal(t, "drl", cfg.Membership.ServiceName)
	assert.Equal(t, 7946, cfg.Membership.Port)
	assert.Equal(t, "0.0.0.0", cfg.Membership.BindAddr)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
	assert.True(t, cfg.InternalAPI.Enabled)
	assert.Equal(t, ":8082", cfg.InternalAPI.Address)

	// NodeName should be set to hostname when not specified
	hostname, _ := os.Hostname()
	if hostname != "" {
		assert.Equal(t, hostname, cfg.NodeName)
	}
}

func TestLoad_WithCustomKDLFile(t *testing.T) {
	clearEnvVars(t)

	// Create a temporary KDL config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "custom.kdl")

	customConfig := `
listen {
    grpc ":9000"
    metrics ":9100"
}

membership {
    service-name "custom-drl"
    port 8000
    bind-addr "127.0.0.1"
    startup-delay "5s"
}

logging {
    level "debug"
    format "text"
}

internal-api {
    enabled false
    address ":8083"
}
`
	err := os.WriteFile(configPath, []byte(customConfig), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Check overridden values
	assert.Equal(t, ":9000", cfg.Listen.GRPC)
	assert.Equal(t, ":9100", cfg.Listen.Metrics)
	assert.Equal(t, "custom-drl", cfg.Membership.ServiceName)
	assert.Equal(t, 8000, cfg.Membership.Port)
	assert.Equal(t, "127.0.0.1", cfg.Membership.BindAddr)
	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "text", cfg.Logging.Format)
	assert.False(t, cfg.InternalAPI.Enabled)
	assert.Equal(t, ":8083", cfg.InternalAPI.Address)
}

func TestLoad_PartialOverride(t *testing.T) {
	clearEnvVars(t)

	// Create a temporary KDL config with only partial overrides
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "partial.kdl")

	partialConfig := `
logging {
    level "warn"
}
`
	err := os.WriteFile(configPath, []byte(partialConfig), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Overridden value
	assert.Equal(t, "warn", cfg.Logging.Level)

	// Default values should remain
	assert.Equal(t, ":8081", cfg.Listen.GRPC)
	assert.Equal(t, ":9091", cfg.Listen.Metrics)
	assert.Equal(t, "drl", cfg.Membership.ServiceName)
}

func TestLoad_EnvironmentOverrides(t *testing.T) {
	clearEnvVars(t)

	// Set environment variables
	t.Setenv("DRL_LISTEN_GRPC", ":7000")
	t.Setenv("DRL_LISTEN_METRICS", ":7001")
	t.Setenv("DRL_MEMBERSHIP_SERVICE_NAME", "env-drl")
	t.Setenv("DRL_MEMBERSHIP_PORT", "9999")
	t.Setenv("DRL_MEMBERSHIP_BIND_ADDR", "192.168.1.1")
	t.Setenv("DRL_LOGGING_LEVEL", "error")
	t.Setenv("DRL_LOGGING_FORMAT", "text")
	t.Setenv("DRL_INTERNAL_API_ENABLED", "false")
	t.Setenv("DRL_INTERNAL_API_ADDRESS", ":7002")

	cfg, err := Load("")
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Environment should override defaults
	assert.Equal(t, ":7000", cfg.Listen.GRPC)
	assert.Equal(t, ":7001", cfg.Listen.Metrics)
	assert.Equal(t, "env-drl", cfg.Membership.ServiceName)
	assert.Equal(t, 9999, cfg.Membership.Port)
	assert.Equal(t, "192.168.1.1", cfg.Membership.BindAddr)
	assert.Equal(t, "error", cfg.Logging.Level)
	assert.Equal(t, "text", cfg.Logging.Format)
	assert.False(t, cfg.InternalAPI.Enabled)
	assert.Equal(t, ":7002", cfg.InternalAPI.Address)
}

func TestLoad_EnvironmentOverridesKDL(t *testing.T) {
	clearEnvVars(t)

	// Create a temporary KDL config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.kdl")

	kdlConfig := `
logging {
    level "debug"
}
`
	err := os.WriteFile(configPath, []byte(kdlConfig), 0644)
	require.NoError(t, err)

	// Environment should override KDL
	t.Setenv("DRL_LOGGING_LEVEL", "error")

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Environment takes precedence over KDL
	assert.Equal(t, "error", cfg.Logging.Level)
}

func TestLoad_InvalidConfigFilePath(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("/nonexistent/path/config.kdl")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to read user config file")
}

func TestLoad_InvalidKDLSyntax(t *testing.T) {
	clearEnvVars(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.kdl")

	// Write invalid KDL syntax
	invalidKDL := `
listen {
    grpc ":8081"
    this is not valid KDL syntax!!!
}
`
	err := os.WriteFile(configPath, []byte(invalidKDL), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to parse KDL config")
}

func TestLoad_ValidationFailure(t *testing.T) {
	clearEnvVars(t)

	// Set invalid values via environment
	t.Setenv("DRL_MEMBERSHIP_SERVICE_NAME", "")
	t.Setenv("DRL_MEMBERSHIP_PORT", "70000") // Invalid port

	cfg, err := Load("")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "configuration validation failed")
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		NodeName: "test-node",
		Listen: ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: MembershipConfig{
			ServiceName: "drl",
			Port:        7946,
			BindAddr:    "0.0.0.0",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_EmptyServiceName(t *testing.T) {
	cfg := &Config{
		Listen: ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: MembershipConfig{
			ServiceName: "",
			Port:        7946,
			BindAddr:    "0.0.0.0",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "membership.service-name cannot be empty")
}

func TestValidate_InvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"port zero", 0},
		{"port negative", -1},
		{"port too high", 65536},
		{"port way too high", 100000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					GRPC:    ":8081",
					Metrics: ":9091",
				},
				Membership: MembershipConfig{
					ServiceName: "drl",
					Port:        tt.port,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			}

			err := cfg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "membership.port must be between 1 and 65535")
		})
	}
}

func TestValidate_ValidPortBoundaries(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"port 1 (minimum)", 1},
		{"port 65535 (maximum)", 65535},
		{"port 80 (common)", 80},
		{"port 443 (common)", 443},
		{"port 8080 (common)", 8080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					GRPC:    ":8081",
					Metrics: ":9091",
				},
				Membership: MembershipConfig{
					ServiceName: "drl",
					Port:        tt.port,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			}

			err := cfg.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestValidate_EmptyBindAddr(t *testing.T) {
	cfg := &Config{
		Listen: ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: MembershipConfig{
			ServiceName: "drl",
			Port:        7946,
			BindAddr:    "",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "membership.bind-addr cannot be empty")
}

func TestValidate_EmptyGRPC(t *testing.T) {
	cfg := &Config{
		Listen: ListenConfig{
			GRPC:    "",
			Metrics: ":9091",
		},
		Membership: MembershipConfig{
			ServiceName: "drl",
			Port:        7946,
			BindAddr:    "0.0.0.0",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "listen.grpc cannot be empty")
}

func TestValidate_EmptyMetrics(t *testing.T) {
	cfg := &Config{
		Listen: ListenConfig{
			GRPC:    ":8081",
			Metrics: "",
		},
		Membership: MembershipConfig{
			ServiceName: "drl",
			Port:        7946,
			BindAddr:    "0.0.0.0",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "listen.metrics cannot be empty")
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	invalidLevels := []string{"trace", "verbose", "critical", "fatal", ""}

	for _, level := range invalidLevels {
		t.Run("level_"+level, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					GRPC:    ":8081",
					Metrics: ":9091",
				},
				Membership: MembershipConfig{
					ServiceName: "drl",
					Port:        7946,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  level,
					Format: "json",
				},
			}

			err := cfg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "logging.level must be one of debug, info, warn, error")
		})
	}
}

func TestValidate_ValidLogLevels(t *testing.T) {
	validLevels := []string{"debug", "info", "warn", "error"}

	for _, level := range validLevels {
		t.Run("level_"+level, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					GRPC:    ":8081",
					Metrics: ":9091",
				},
				Membership: MembershipConfig{
					ServiceName: "drl",
					Port:        7946,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  level,
					Format: "json",
				},
			}

			err := cfg.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	invalidFormats := []string{"xml", "csv", "yaml", ""}

	for _, format := range invalidFormats {
		t.Run("format_"+format, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					GRPC:    ":8081",
					Metrics: ":9091",
				},
				Membership: MembershipConfig{
					ServiceName: "drl",
					Port:        7946,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: format,
				},
			}

			err := cfg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "logging.format must be one of json, text")
		})
	}
}

func TestValidate_ValidLogFormats(t *testing.T) {
	validFormats := []string{"json", "text"}

	for _, format := range validFormats {
		t.Run("format_"+format, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					GRPC:    ":8081",
					Metrics: ":9091",
				},
				Membership: MembershipConfig{
					ServiceName: "drl",
					Port:        7946,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: format,
				},
			}

			err := cfg.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &Config{
		Listen: ListenConfig{
			GRPC:    "",
			Metrics: "",
		},
		Membership: MembershipConfig{
			ServiceName: "",
			Port:        0,
			BindAddr:    "",
		},
		Logging: LoggingConfig{
			Level:  "invalid",
			Format: "invalid",
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)

	// Should contain multiple error messages
	assert.Contains(t, err.Error(), "membership.service-name cannot be empty")
	assert.Contains(t, err.Error(), "membership.port must be between 1 and 65535")
	assert.Contains(t, err.Error(), "membership.bind-addr cannot be empty")
	assert.Contains(t, err.Error(), "listen.grpc cannot be empty")
	assert.Contains(t, err.Error(), "listen.metrics cannot be empty")
	assert.Contains(t, err.Error(), "logging.level must be one of")
	assert.Contains(t, err.Error(), "logging.format must be one of")
}

func TestMetricsPort_StandardPort(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected int
	}{
		{"port only", ":9091", 9091},
		{"with host", "localhost:8080", 8080},
		{"with ip", "127.0.0.1:3000", 3000},
		{"ipv6 style", "[::1]:9091", 9091},
		{"zero port", ":0", 0},
		{"high port", ":65535", 65535},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					Metrics: tt.addr,
				},
			}
			assert.Equal(t, tt.expected, cfg.MetricsPort())
		})
	}
}

func TestMetricsPort_DefaultOnInvalidAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{"no port separator", "localhost"},
		{"empty string", ""},
		{"invalid port", ":abc"},
		{"port with letters", ":123abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Listen: ListenConfig{
					Metrics: tt.addr,
				},
			}
			// Should return default port 9091 for invalid addresses
			assert.Equal(t, 9091, cfg.MetricsPort())
		})
	}
}

func TestGetPrivateAPIKey_Set(t *testing.T) {
	t.Setenv("DRL_PRIVATE_API_KEY", "my-secret-api-key-12345")

	key, exists := GetPrivateAPIKey()
	assert.True(t, exists)
	assert.Equal(t, "my-secret-api-key-12345", key)
}

func TestGetPrivateAPIKey_NotSet(t *testing.T) {
	_ = os.Unsetenv("DRL_PRIVATE_API_KEY")

	key, exists := GetPrivateAPIKey()
	assert.False(t, exists)
	assert.Equal(t, "", key)
}

func TestGetPrivateAPIKey_EmptyValue(t *testing.T) {
	t.Setenv("DRL_PRIVATE_API_KEY", "")

	key, exists := GetPrivateAPIKey()
	assert.False(t, exists)
	assert.Equal(t, "", key)
}

func TestValidatePrivateAPIKey_Valid(t *testing.T) {
	t.Setenv("DRL_PRIVATE_API_KEY", "this-is-a-valid-key-16chars")

	err := ValidatePrivateAPIKey()
	assert.NoError(t, err)
}

func TestValidatePrivateAPIKey_NotSet(t *testing.T) {
	_ = os.Unsetenv("DRL_PRIVATE_API_KEY")

	err := ValidatePrivateAPIKey()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "DRL_PRIVATE_API_KEY environment variable is not set")
}

func TestValidatePrivateAPIKey_TooShort(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"one char", "a"},
		{"15 chars", "123456789012345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DRL_PRIVATE_API_KEY", tt.key)

			err := ValidatePrivateAPIKey()
			assert.Error(t, err)
			if tt.key == "" {
				assert.Contains(t, err.Error(), "DRL_PRIVATE_API_KEY environment variable is not set")
			} else {
				assert.Contains(t, err.Error(), "DRL_PRIVATE_API_KEY must be at least 16 characters")
			}
		})
	}
}

func TestValidatePrivateAPIKey_ExactlyMinLength(t *testing.T) {
	t.Setenv("DRL_PRIVATE_API_KEY", "1234567890123456") // exactly 16 chars

	err := ValidatePrivateAPIKey()
	assert.NoError(t, err)
}

func TestLoadFromKDL_ValidKDL(t *testing.T) {
	cfg := NewConfig()

	kdlData := []byte(`
listen {
    grpc ":5000"
    metrics ":5001"
}
`)

	err := cfg.loadFromKDL(kdlData)
	assert.NoError(t, err)
	assert.Equal(t, ":5000", cfg.Listen.GRPC)
	assert.Equal(t, ":5001", cfg.Listen.Metrics)
}

func TestLoadFromKDL_InvalidKDL(t *testing.T) {
	cfg := NewConfig()

	kdlData := []byte(`this is not { valid KDL syntax`)

	err := cfg.loadFromKDL(kdlData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse KDL config")
}

func TestLoadFromEnvironment(t *testing.T) {
	cfg := NewConfig()

	t.Setenv("DRL_LISTEN_GRPC", ":6000")
	t.Setenv("DRL_LISTEN_METRICS", ":6001")

	err := cfg.loadFromEnvironment()
	assert.NoError(t, err)
	assert.Equal(t, ":6000", cfg.Listen.GRPC)
	assert.Equal(t, ":6001", cfg.Listen.Metrics)
}

func TestConfig_InternalAPIDefaults(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("")
	require.NoError(t, err)

	assert.True(t, cfg.InternalAPI.Enabled)
	assert.Equal(t, ":8082", cfg.InternalAPI.Address)
}

func TestConfig_InternalAPIDisabledViaEnv(t *testing.T) {
	clearEnvVars(t)
	t.Setenv("DRL_INTERNAL_API_ENABLED", "false")

	cfg, err := Load("")
	require.NoError(t, err)

	assert.False(t, cfg.InternalAPI.Enabled)
}

func TestConfig_StartupDelayFromKDL(t *testing.T) {
	clearEnvVars(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "delay.kdl")

	kdlConfig := `
membership {
    service-name "test"
    port 7946
    bind-addr "0.0.0.0"
    startup-delay "10s"
}
`
	err := os.WriteFile(configPath, []byte(kdlConfig), 0644)
	require.NoError(t, err)

	cfg, err := Load(configPath)
	require.NoError(t, err)

	// Note: The startup-delay is stored as a string in KDL but may be parsed as duration
	// This tests that the config loads without error
	assert.NotNil(t, cfg)
}

// clearEnvVars clears all DRL_ environment variables to ensure test isolation
func clearEnvVars(t *testing.T) {
	t.Helper()
	envVars := []string{
		"DRL_LISTEN_GRPC",
		"DRL_LISTEN_METRICS",
		"DRL_MEMBERSHIP_SERVICE_NAME",
		"DRL_MEMBERSHIP_PORT",
		"DRL_MEMBERSHIP_BIND_ADDR",
		"DRL_MEMBERSHIP_STARTUP_DELAY",
		"DRL_LOGGING_LEVEL",
		"DRL_LOGGING_FORMAT",
		"DRL_INTERNAL_API_ENABLED",
		"DRL_INTERNAL_API_ADDRESS",
		"DRL_PRIVATE_API_KEY",
	}

	for _, env := range envVars {
		_ = os.Unsetenv(env)
	}
}
