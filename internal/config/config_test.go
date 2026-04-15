package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig(t *testing.T) {
	cfg := NewConfig()
	assert.NotNil(t, cfg)
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
	assert.Equal(t, int64(64), cfg.Cache.BlocklistSizeMB)
	assert.Equal(t, int64(128), cfg.Cache.AccountingSizeMB)
	assert.Equal(t, 30, cfg.Cache.SyncTimeoutSeconds)
	assert.Equal(t, 300, cfg.Cache.BlocklistDefaultTTLSeconds)

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
		Cache: CacheConfig{
			BlocklistSizeMB:            64,
			AccountingSizeMB:           128,
			SyncTimeoutSeconds:         30,
			BlocklistDefaultTTLSeconds: 3600,
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
		Cache: CacheConfig{
			BlocklistSizeMB:            64,
			AccountingSizeMB:           128,
			SyncTimeoutSeconds:         30,
			BlocklistDefaultTTLSeconds: 3600,
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
				Cache: CacheConfig{
					BlocklistSizeMB:            64,
					AccountingSizeMB:           128,
					SyncTimeoutSeconds:         30,
					BlocklistDefaultTTLSeconds: 3600,
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
				Cache: CacheConfig{
					BlocklistSizeMB:            64,
					AccountingSizeMB:           128,
					SyncTimeoutSeconds:         30,
					BlocklistDefaultTTLSeconds: 3600,
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
		Cache: CacheConfig{
			BlocklistSizeMB:            64,
			AccountingSizeMB:           128,
			SyncTimeoutSeconds:         30,
			BlocklistDefaultTTLSeconds: 3600,
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
		Cache: CacheConfig{
			BlocklistSizeMB:            64,
			AccountingSizeMB:           128,
			SyncTimeoutSeconds:         30,
			BlocklistDefaultTTLSeconds: 3600,
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
		Cache: CacheConfig{
			BlocklistSizeMB:            64,
			AccountingSizeMB:           128,
			SyncTimeoutSeconds:         30,
			BlocklistDefaultTTLSeconds: 3600,
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
				Cache: CacheConfig{
					BlocklistSizeMB:            64,
					AccountingSizeMB:           128,
					SyncTimeoutSeconds:         30,
					BlocklistDefaultTTLSeconds: 3600,
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
				Cache: CacheConfig{
					BlocklistSizeMB:            64,
					AccountingSizeMB:           128,
					SyncTimeoutSeconds:         30,
					BlocklistDefaultTTLSeconds: 3600,
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
				Cache: CacheConfig{
					BlocklistSizeMB:            64,
					AccountingSizeMB:           128,
					SyncTimeoutSeconds:         30,
					BlocklistDefaultTTLSeconds: 3600,
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
				Cache: CacheConfig{
					BlocklistSizeMB:            64,
					AccountingSizeMB:           128,
					SyncTimeoutSeconds:         30,
					BlocklistDefaultTTLSeconds: 3600,
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
		Cache: CacheConfig{
			BlocklistSizeMB:    0,
			AccountingSizeMB:   0,
			SyncTimeoutSeconds: 0,
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
	assert.Contains(t, err.Error(), "cache.blocklist-size-mb must be at least 1 MB")
	assert.Contains(t, err.Error(), "cache.accounting-size-mb must be at least 1 MB")
	assert.Contains(t, err.Error(), "cache.sync-timeout-seconds must be at least 1")
	assert.Contains(t, err.Error(), "cache.blocklist-default-ttl-seconds must be at least 1")
}

func TestValidate_InvalidCacheConfig(t *testing.T) {
	tests := []struct {
		name     string
		cache    CacheConfig
		errorMsg string
	}{
		{
			name:     "blocklist size zero",
			cache:    CacheConfig{BlocklistSizeMB: 0, AccountingSizeMB: 64, SyncTimeoutSeconds: 30, BlocklistDefaultTTLSeconds: 3600},
			errorMsg: "cache.blocklist-size-mb must be at least 1 MB",
		},
		{
			name:     "accounting size zero",
			cache:    CacheConfig{BlocklistSizeMB: 64, AccountingSizeMB: 0, SyncTimeoutSeconds: 30, BlocklistDefaultTTLSeconds: 3600},
			errorMsg: "cache.accounting-size-mb must be at least 1 MB",
		},
		{
			name:     "sync timeout zero",
			cache:    CacheConfig{BlocklistSizeMB: 64, AccountingSizeMB: 128, SyncTimeoutSeconds: 0, BlocklistDefaultTTLSeconds: 3600},
			errorMsg: "cache.sync-timeout-seconds must be at least 1",
		},
		{
			name:     "blocklist default ttl zero",
			cache:    CacheConfig{BlocklistSizeMB: 64, AccountingSizeMB: 128, SyncTimeoutSeconds: 30, BlocklistDefaultTTLSeconds: 0},
			errorMsg: "cache.blocklist-default-ttl-seconds must be at least 1",
		},
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
					Port:        7946,
					BindAddr:    "0.0.0.0",
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
				Cache: tt.cache,
			}

			err := cfg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errorMsg)
		})
	}
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

func TestAccountingRule_WindowDuration(t *testing.T) {
	tests := []struct {
		name     string
		per      string
		expected time.Duration
	}{
		{"second", "second", time.Second},
		{"minute", "minute", time.Minute},
		{"SECOND uppercase", "SECOND", time.Second},
		{"MINUTE uppercase", "MINUTE", time.Minute},
		{"default to minute", "unknown", time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := AccountingRule{Per: tt.per}
			assert.Equal(t, tt.expected, rule.WindowDuration())
		})
	}
}

func TestAccountingConfig_KDLParsing(t *testing.T) {
	clearEnvVars(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "accounting.kdl")

	kdlConfig := `
accounting {
	rules {
		"api-v1" {
			path-prefix "/api/v1" 
			headers "X-API-KEY-ID" "X-Consumer-Type"
			limit 100 
			per "minute"
		}
		"health" {
			path-prefix "/health" 
			limit 500 
			per "second"
		}
	}
}
`
	err := os.WriteFile(configPath, []byte(kdlConfig), 0644)
	require.NoError(t, err)
	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Accounting.Rules, 2)
	assert.NotEmpty(t, lo.Values(cfg.Accounting.Rules))

	// First rule
	assert.Equal(t, "/api/v1", cfg.Accounting.Rules["api-v1"].PathPrefix)
	assert.Equal(t, []string{"X-API-KEY-ID", "X-Consumer-Type"}, cfg.Accounting.Rules["api-v1"].Headers)
	assert.Equal(t, int64(100), cfg.Accounting.Rules["api-v1"].Limit)
	assert.Equal(t, "minute", cfg.Accounting.Rules["api-v1"].Per)

	// Second rule
	assert.Equal(t, "/health", cfg.Accounting.Rules["health"].PathPrefix)
	assert.Empty(t, cfg.Accounting.Rules["health"].Headers)
	assert.Equal(t, int64(500), cfg.Accounting.Rules["health"].Limit)
	assert.Equal(t, "second", cfg.Accounting.Rules["health"].Per)
}

func TestAccountingConfig_EmptyBlock(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, cfg.Accounting.Rules)
}

func TestAccountingConfig_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		rule     AccountingRule
		errorMsg string
	}{
		{
			name:     "empty path prefix",
			rule:     AccountingRule{PathPrefix: "", Limit: 100, Per: "minute"},
			errorMsg: "path-prefix cannot be empty",
		},
		{
			name:     "zero limit",
			rule:     AccountingRule{PathPrefix: "/api", Limit: 0, Per: "minute"},
			errorMsg: "limit must be at least 1",
		},
		{
			name:     "invalid per",
			rule:     AccountingRule{PathPrefix: "/api", Limit: 100, Per: "hour"},
			errorMsg: "per must be one of second, minute",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := make(map[string]AccountingRule)
			rules[tt.name] = tt.rule
			cfg := &Config{
				Listen:     ListenConfig{GRPC: ":8081", Metrics: ":9091"},
				Membership: MembershipConfig{ServiceName: "drl", Port: 7946, BindAddr: "0.0.0.0"},
				Logging:    LoggingConfig{Level: "info", Format: "json"},
				Cache:      CacheConfig{BlocklistSizeMB: 64, AccountingSizeMB: 128, SyncTimeoutSeconds: 30, BlocklistDefaultTTLSeconds: 3600},
				Accounting: AccountingConfig{Rules: rules},
			}
			err := cfg.Validate()
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errorMsg)
		})
	}
}

func TestSecretKeys_KDLParsing(t *testing.T) {
	clearEnvVars(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "encryption.kdl")
	kdlContent := `
membership {
    secret-keys "12345678901234561234567890123456" "abcdefghijklmnopabcdefghijklmnop"
}
`
	require.NoError(t, os.WriteFile(configPath, []byte(kdlContent), 0644))

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Membership.SecretKeys, 2)
	assert.Equal(t, "12345678901234561234567890123456", cfg.Membership.SecretKeys[0])
	assert.Equal(t, "abcdefghijklmnopabcdefghijklmnop", cfg.Membership.SecretKeys[1])
}

func TestSecretKeys_EnvOverride(t *testing.T) {
	clearEnvVars(t)

	t.Setenv("DRL_MEMBERSHIP_PRIMARY_KEY", "PrimaryKey_32Bytes______________")
	t.Setenv("DRL_MEMBERSHIP_SECONDARY_KEYS", "SecondaryKey32Bytes_____________")

	cfg, err := Load("")
	require.NoError(t, err)
	require.Len(t, cfg.Membership.SecretKeys, 2)
	assert.Equal(t, "PrimaryKey_32Bytes______________", cfg.Membership.SecretKeys[0])
	assert.Equal(t, "SecondaryKey32Bytes_____________", cfg.Membership.SecretKeys[1])
}

func TestSecretKeys_EnvOverridesKDL(t *testing.T) {
	clearEnvVars(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "encryption.kdl")
	kdlContent := `
membership {
    secret-keys "KDLKey_32Bytes__________________"
}
`
	require.NoError(t, os.WriteFile(configPath, []byte(kdlContent), 0644))

	// Env should override KDL
	t.Setenv("DRL_MEMBERSHIP_PRIMARY_KEY", "EnvKey_32Bytes__________________")

	cfg, err := Load(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Membership.SecretKeys, 1)
	assert.Equal(t, "EnvKey_32Bytes__________________", cfg.Membership.SecretKeys[0])
}

func TestSecretKeys_Validation_InvalidLength(t *testing.T) {
	clearEnvVars(t)

	t.Setenv("DRL_MEMBERSHIP_PRIMARY_KEY", "too-short-15byt")

	_, err := Load("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "valid AES key length")
}

func TestSecretKeys_Validation_MismatchedLengths(t *testing.T) {
	clearEnvVars(t)

	// 16-byte primary, 32-byte secondary
	t.Setenv("DRL_MEMBERSHIP_PRIMARY_KEY", "1234567890123456")
	t.Setenv("DRL_MEMBERSHIP_SECONDARY_KEYS", "12345678901234561234567890123456")

	_, err := Load("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "same length")
}

func TestSecretKeys_Validation_ValidLengths(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"AES-128 (16 bytes)", "1234567890123456"},
		{"AES-192 (24 bytes)", "123456789012345678901234"},
		{"AES-256 (32 bytes)", "12345678901234561234567890123456"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnvVars(t)
			t.Setenv("DRL_MEMBERSHIP_PRIMARY_KEY", tt.key)

			cfg, err := Load("")
			require.NoError(t, err)
			require.Len(t, cfg.Membership.SecretKeys, 1)
			assert.Equal(t, tt.key, cfg.Membership.SecretKeys[0])
		})
	}
}

func TestSecretKeys_EmptyList(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Empty(t, cfg.Membership.SecretKeys)
}

// ── Token Bucket configuration ────────────────────────────────────────────────

func TestAccountingSettings_TokenBucketKDL(t *testing.T) {
	clearEnvVars(t)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "tb.kdl")

	kdlConfig := `
accounting {
    settings {
        algorithm "token-bucket"
        capacity 100
        refill-rate 10
    }
    rules {
        "catch-all" {
            path-prefix "/"
            limit 100
            per "minute"
        }
    }
}
`
	require.NoError(t, os.WriteFile(configPath, []byte(kdlConfig), 0644))

	cfg, err := Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, "token-bucket", cfg.Accounting.Settings.Algorithm)
	assert.Equal(t, int64(100), cfg.Accounting.Settings.Capacity)
	assert.Equal(t, float64(10), cfg.Accounting.Settings.RefillRate)
}

func TestAccountingSettings_TokenBucketEnvOverride(t *testing.T) {
	clearEnvVars(t)

	t.Setenv("DRL_ACCOUNTING_SETTINGS_ALGORITHM", "token-bucket")
	t.Setenv("DRL_ACCOUNTING_SETTINGS_CAPACITY", "200")
	t.Setenv("DRL_ACCOUNTING_SETTINGS_REFILL_RATE", "20.5")

	// Load defaults only — token-bucket with capacity+refill-rate and no rules is valid
	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "token-bucket", cfg.Accounting.Settings.Algorithm)
	assert.Equal(t, int64(200), cfg.Accounting.Settings.Capacity)
	assert.InDelta(t, 20.5, cfg.Accounting.Settings.RefillRate, 0.001)
}

func TestAccountingSettings_SlidingWindowEnvOverride(t *testing.T) {
	clearEnvVars(t)
	t.Setenv("DRL_ACCOUNTING_SETTINGS_ALGORITHM", "sliding-window")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "sliding-window", cfg.Accounting.Settings.Algorithm)
}

func TestValidate_TokenBucketMissingCapacity(t *testing.T) {
	clearEnvVars(t)

	cfg := validBaseConfig()
	cfg.Accounting.Settings.Algorithm = "token-bucket"
	cfg.Accounting.Settings.Capacity = 0
	cfg.Accounting.Settings.RefillRate = 10

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accounting.settings.capacity must be > 0")
}

func TestValidate_TokenBucketMissingRefillRate(t *testing.T) {
	clearEnvVars(t)

	cfg := validBaseConfig()
	cfg.Accounting.Settings.Algorithm = "token-bucket"
	cfg.Accounting.Settings.Capacity = 100
	cfg.Accounting.Settings.RefillRate = 0

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accounting.settings.refill-rate must be > 0")
}

func TestValidate_TokenBucketValid(t *testing.T) {
	clearEnvVars(t)

	cfg := validBaseConfig()
	cfg.Accounting.Settings.Algorithm = "token-bucket"
	cfg.Accounting.Settings.Capacity = 100
	cfg.Accounting.Settings.RefillRate = 10

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_InvalidAlgorithm(t *testing.T) {
	clearEnvVars(t)

	cfg := validBaseConfig()
	cfg.Accounting.Settings.Algorithm = "leaky-bucket"

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "accounting.settings.algorithm must be one of sliding-window, token-bucket")
}

func TestGetConfigSection(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("")
	require.NoError(t, err)

	tests := []struct {
		section string
		wantOK  bool
	}{
		{"accounting", true},
		{"membership", true},
		{"cache", true},
		{"listen", true},
		{"logging", true},
		{"internal-api", true},
		{"ACCOUNTING", true}, // case-insensitive
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			data, ok := cfg.GetConfigSection(tt.section)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.NotNil(t, data)
			} else {
				assert.Nil(t, data)
			}
		})
	}
}

func TestGetConfigSection_AccountingContent(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("")
	require.NoError(t, err)

	data, ok := cfg.GetConfigSection("accounting")
	require.True(t, ok)

	ac, ok := data.(AccountingConfig)
	require.True(t, ok)
	assert.Equal(t, "sliding-window", ac.Settings.Algorithm)
}

// validBaseConfig returns a Config that passes validation with defaults.
func validBaseConfig() *Config {
	return &Config{
		Listen:     ListenConfig{GRPC: ":8081", Metrics: ":9091"},
		Membership: MembershipConfig{ServiceName: "drl", Port: 7946, BindAddr: "0.0.0.0"},
		Logging:    LoggingConfig{Level: "info", Format: "json"},
		Cache: CacheConfig{
			BlocklistSizeMB:            64,
			AccountingSizeMB:           128,
			SyncTimeoutSeconds:         30,
			BlocklistDefaultTTLSeconds: 300,
		},
	}
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
		"DRL_CACHE_BLOCKLIST_SIZE_MB",
		"DRL_CACHE_ACCOUNTING_SIZE_MB",
		"DRL_CACHE_SYNC_TIMEOUT_SECONDS",
		"DRL_CACHE_BLOCKLIST_DEFAULT_TTL_SECONDS",
		"DRL_MEMBERSHIP_PRIMARY_KEY",
		"DRL_MEMBERSHIP_SECONDARY_KEYS",
		"DRL_ACCOUNTING_SETTINGS_ALGORITHM",
		"DRL_ACCOUNTING_SETTINGS_CAPACITY",
		"DRL_ACCOUNTING_SETTINGS_REFILL_RATE",
		"DRL_ACCOUNTING_SETTINGS_RETRY_AFTER_TYPE",
		"DRL_ACCOUNTING_SETTINGS_FLUSH_INTERVAL",
		"DRL_ACCOUNTING_SETTINGS_MAX_BATCH_SIZE",
	}

	for _, env := range envVars {
		_ = os.Unsetenv(env)
	}
}
