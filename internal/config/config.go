package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sblinch/kdl-go"
)

// Config holds the DRL configuration
type Config struct {
	// NodeName is the unique identifier for this node
	NodeName string

	// Listen configuration
	Listen ListenConfig

	// Membership configuration
	Membership MembershipConfig

	// Logging configuration
	Logging LoggingConfig

	// ConfigSource indicates where the config was loaded from
	ConfigSource string
}

// ListenConfig holds listener configuration
type ListenConfig struct {
	// GRPC is the address for gRPC server
	GRPC string
	// Metrics is the address for Prometheus metrics endpoint
	Metrics string
}

// MembershipConfig holds cluster membership configuration
type MembershipConfig struct {
	// ServiceName is the DNS name to resolve for peer discovery
	ServiceName string
	// Port is the port for memberlist gossip
	Port int
	// BindAddr is the address to bind memberlist to
	BindAddr string
	// StartupDelay is the delay before attempting to join the cluster
	StartupDelay time.Duration
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	// Level is the log level (debug, info, warn, error)
	Level string
	// Format is the log format (json, text)
	Format string
}

// kdlConfig represents the KDL file structure for unmarshaling
type kdlConfig struct {
	Listen     kdlListen     `kdl:"listen"`
	Membership kdlMembership `kdl:"membership"`
	Logging    kdlLogging    `kdl:"logging"`
}

type kdlListen struct {
	GRPC    string `kdl:"grpc"`
	Metrics string `kdl:"metrics"`
}

type kdlMembership struct {
	ServiceName  string `kdl:"service-name"`
	Port         int    `kdl:"port"`
	BindAddr     string `kdl:"bind-addr"`
	StartupDelay string `kdl:"startup-delay"`
}

type kdlLogging struct {
	Level  string `kdl:"level"`
	Format string `kdl:"format"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		NodeName:     getHostname(),
		ConfigSource: "defaults",
		Listen: ListenConfig{
			GRPC:    ":8081",
			Metrics: ":9091",
		},
		Membership: MembershipConfig{
			ServiceName:  "drl",
			Port:         7946,
			BindAddr:     "0.0.0.0",
			StartupDelay: 3 * time.Second,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// Load loads configuration with precedence: Environment > KDL File > Defaults
func Load(configPath string) (*Config, error) {
	// Start with defaults
	cfg := DefaultConfig()

	// Load from KDL file if provided
	if configPath != "" {
		if err := cfg.loadFromKDL(configPath); err != nil {
			return nil, err
		}
		cfg.ConfigSource = configPath
	}

	// Apply environment variable overrides
	cfg.applyEnvOverrides()

	// Validate the final configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// loadFromKDL loads configuration from a KDL file
func (c *Config) loadFromKDL(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var kdlCfg kdlConfig
	if err := kdl.Unmarshal(data, &kdlCfg); err != nil {
		return fmt.Errorf("failed to parse KDL config: %w", err)
	}

	// Apply KDL values (only if set)
	if kdlCfg.Listen.GRPC != "" {
		c.Listen.GRPC = kdlCfg.Listen.GRPC
	}
	if kdlCfg.Listen.Metrics != "" {
		c.Listen.Metrics = kdlCfg.Listen.Metrics
	}

	if kdlCfg.Membership.ServiceName != "" {
		c.Membership.ServiceName = kdlCfg.Membership.ServiceName
	}
	if kdlCfg.Membership.Port != 0 {
		c.Membership.Port = kdlCfg.Membership.Port
	}
	if kdlCfg.Membership.BindAddr != "" {
		c.Membership.BindAddr = kdlCfg.Membership.BindAddr
	}
	if kdlCfg.Membership.StartupDelay != "" {
		if d, err := time.ParseDuration(kdlCfg.Membership.StartupDelay); err == nil {
			c.Membership.StartupDelay = d
		}
	}

	if kdlCfg.Logging.Level != "" {
		c.Logging.Level = kdlCfg.Logging.Level
	}
	if kdlCfg.Logging.Format != "" {
		c.Logging.Format = kdlCfg.Logging.Format
	}

	return nil
}

// applyEnvOverrides applies environment variable overrides
// Environment variables follow the pattern: DRL_<SECTION>_<KEY>
// e.g., DRL_LISTEN_GRPC, DRL_MEMBERSHIP_SERVICE_NAME, DRL_LOGGING_LEVEL
func (c *Config) applyEnvOverrides() {
	// Node name
	if v := os.Getenv("DRL_NODE_NAME"); v != "" {
		c.NodeName = v
	}
	// Legacy support
	if v := os.Getenv("NODE_NAME"); v != "" {
		c.NodeName = v
	}

	// Listen section
	if v := os.Getenv("DRL_LISTEN_GRPC"); v != "" {
		c.Listen.GRPC = v
	}
	if v := os.Getenv("DRL_LISTEN_METRICS"); v != "" {
		c.Listen.Metrics = v
	}

	// Membership section
	if v := os.Getenv("DRL_MEMBERSHIP_SERVICE_NAME"); v != "" {
		c.Membership.ServiceName = v
	}
	// Legacy support
	if v := os.Getenv("DISCOVERY_SERVICE_NAME"); v != "" {
		c.Membership.ServiceName = v
	}

	if v := os.Getenv("DRL_MEMBERSHIP_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			c.Membership.Port = port
		}
	}
	// Legacy support
	if v := os.Getenv("BIND_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			c.Membership.Port = port
		}
	}

	if v := os.Getenv("DRL_MEMBERSHIP_BIND_ADDR"); v != "" {
		c.Membership.BindAddr = v
	}
	// Legacy support
	if v := os.Getenv("BIND_ADDR"); v != "" {
		c.Membership.BindAddr = v
	}

	if v := os.Getenv("DRL_MEMBERSHIP_STARTUP_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Membership.StartupDelay = d
		}
	}
	// Legacy support
	if v := os.Getenv("STARTUP_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.Membership.StartupDelay = d
		}
	}

	// Logging section
	if v := os.Getenv("DRL_LOGGING_LEVEL"); v != "" {
		c.Logging.Level = v
	}
	if v := os.Getenv("DRL_LOGGING_FORMAT"); v != "" {
		c.Logging.Format = v
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	var errs []string

	// Validate membership
	if c.Membership.ServiceName == "" {
		errs = append(errs, "membership.service-name cannot be empty")
	}
	if c.Membership.Port < 1 || c.Membership.Port > 65535 {
		errs = append(errs, fmt.Sprintf("membership.port must be between 1 and 65535, got %d", c.Membership.Port))
	}
	if c.Membership.BindAddr == "" {
		errs = append(errs, "membership.bind-addr cannot be empty")
	}

	// Validate listen
	if c.Listen.GRPC == "" {
		errs = append(errs, "listen.grpc cannot be empty")
	}
	if c.Listen.Metrics == "" {
		errs = append(errs, "listen.metrics cannot be empty")
	}

	// Validate logging
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[strings.ToLower(c.Logging.Level)] {
		errs = append(errs, fmt.Sprintf("logging.level must be one of debug, info, warn, error; got %s", c.Logging.Level))
	}
	validFormats := map[string]bool{"json": true, "text": true}
	if !validFormats[strings.ToLower(c.Logging.Format)] {
		errs = append(errs, fmt.Sprintf("logging.format must be one of json, text; got %s", c.Logging.Format))
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation errors: %s", strings.Join(errs, "; "))
	}

	return nil
}

// BindAddr returns the bind address (for backward compatibility)
func (c *Config) BindAddr() string {
	return c.Membership.BindAddr
}

// BindPort returns the bind port (for backward compatibility)
func (c *Config) BindPort() int {
	return c.Membership.Port
}

// DiscoveryServiceName returns the discovery service name (for backward compatibility)
func (c *Config) DiscoveryServiceName() string {
	return c.Membership.ServiceName
}

// MetricsPort returns the metrics port extracted from the address
func (c *Config) MetricsPort() int {
	addr := c.Listen.Metrics
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
			return port
		}
	}
	return 9091
}

// StartupDelay returns the startup delay (for backward compatibility)
func (c *Config) StartupDelay() time.Duration {
	return c.Membership.StartupDelay
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
