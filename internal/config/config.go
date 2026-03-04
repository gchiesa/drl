package config

import (
	"embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/sblinch/kdl-go"
	"github.com/sblinch/kdl-go/document"
)

//go:embed resources/*.kdl
var res embed.FS

const (
	defaultConfigSource     = "defaults"
	kdlConfigSource         = "kdl"
	environmentConfigSource = "environment"
	defaultConfigFile       = "resources/default.kdl"
)

type Config struct {
	// NodeName is the unique identifier for this node
	NodeName string

	// Listen configuration
	Listen ListenConfig `kdl:"listen" envPrefix:"DRL_LISTEN_"`

	// Membership configuration
	Membership MembershipConfig `kdl:"membership" envPrefix:"DRL_MEMBERSHIP_"`

	// Logging configuration
	Logging LoggingConfig `kdl:"logging" envPrefix:"DRL_LOGGING_"`

	// InternalAPI configuration
	InternalAPI InternalAPIConfig `kdl:"internal-api" envPrefix:"DRL_INTERNAL_API_"`

	// Cache configuration
	Cache CacheConfig `kdl:"cache" envPrefix:"DRL_CACHE_"`

	// Accounting configuration
	Accounting AccountingConfig `kdl:"accounting"`
}

// AccountingConfig holds accounting rules for entity rate limiting
type AccountingConfig struct {
	Rules []AccountingRule
}

// AccountingRule defines a rate-limiting rule for a path prefix
type AccountingRule struct {
	PathPrefix string
	Headers    []string
	Limit      int64
	Per        string // "second" or "minute"
}

// WindowDuration returns the time.Duration for the rule's rate window
func (r AccountingRule) WindowDuration() time.Duration {
	if strings.ToLower(r.Per) == "second" {
		return time.Second
	}
	return time.Minute
}

// UnmarshalKDL implements kdl.Unmarshaler for custom KDL parsing of accounting config
func (a *AccountingConfig) UnmarshalKDL(node *document.Node) error {
	a.Rules = nil
	for _, child := range node.Children {
		if child.Name.String() != "rule" {
			continue
		}

		rule := AccountingRule{}

		// Path prefix from first argument
		if len(child.Arguments) > 0 {
			rule.PathPrefix = fmt.Sprintf("%v", child.Arguments[0].Value)
		}

		// Parse rule children: headers and limit
		for _, ruleChild := range child.Children {
			switch ruleChild.Name.String() {
			case "headers":
				for _, arg := range ruleChild.Arguments {
					rule.Headers = append(rule.Headers, fmt.Sprintf("%v", arg.Value))
				}
			case "limit":
				if len(ruleChild.Arguments) > 0 {
					switch v := ruleChild.Arguments[0].Value.(type) {
					case int64:
						rule.Limit = v
					case float64:
						rule.Limit = int64(v)
					default:
						rule.Limit, _ = strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64)
					}
				}
				if perVal, ok := ruleChild.Properties.Get("per"); ok {
					rule.Per = fmt.Sprintf("%v", perVal.Value)
				}
			}
		}

		a.Rules = append(a.Rules, rule)
	}
	return nil
}

// ListenConfig holds listener configuration
type ListenConfig struct {
	// GRPC is the address for gRPC server
	GRPC string `kdl:"grpc" env:"GRPC"`
	// Metrics is the address for Prometheus metrics endpoint
	Metrics string `kdl:"metrics" env:"METRICS"`
}

// MembershipConfig holds cluster membership configuration
type MembershipConfig struct {
	// ServiceName is the DNS name to resolve for peer discovery
	ServiceName string `kdl:"service-name" env:"SERVICE_NAME"`
	// Port is the port for memberlist gossip
	Port int `kdl:"port" env:"PORT"`
	// BindAddr is the address to bind memberlist to
	BindAddr string `kdl:"bind-addr" env:"BIND_ADDR"`
	// StartupDelay is the delay before attempting to join the cluster
	StartupDelay time.Duration `kdl:"startup-delay" env:"STARTUP_DELAY"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	// Level is the log level (debug, info, warn, error)
	Level string `kdl:"level" env:"LEVEL"`
	// Format is the log format (json, text)
	Format string `kdl:"format" env:"FORMAT"`
}

// InternalAPIConfig holds internal API configuration
type InternalAPIConfig struct {
	// Enabled indicates if the internal API is enabled
	Enabled bool `kdl:"enabled" env:"ENABLED"`
	// Address is the address for the internal API server
	Address string `kdl:"address" env:"ADDRESS"`
}

// CacheConfig holds cache configuration
type CacheConfig struct {
	// BlocklistSizeMB is the maximum size in MB for the blocklist cache
	BlocklistSizeMB int64 `kdl:"blocklist-size-mb" env:"BLOCKLIST_SIZE_MB"`
	// AccountingSizeMB is the maximum size in MB for the accounting cache
	AccountingSizeMB int64 `kdl:"accounting-size-mb" env:"ACCOUNTING_SIZE_MB"`
	// SyncTimeoutSeconds is the timeout in seconds for initial state sync
	SyncTimeoutSeconds int `kdl:"sync-timeout-seconds" env:"SYNC_TIMEOUT_SECONDS"`
	// BlocklistDefaultTTLSeconds is the default TTL in seconds for admin-API blocks
	BlocklistDefaultTTLSeconds int `kdl:"blocklist-default-ttl-seconds" env:"BLOCKLIST_DEFAULT_TTL_SECONDS"`
}

func NewConfig() *Config {
	return &Config{}
}

// Load loads configuration with precedence: Environment > KDL File > Defaults
func Load(configPath string) (*Config, error) {
	// start with defaults
	cfg := NewConfig()

	// load defaults kdl first
	var configData []byte
	var err error
	if configData, err = res.ReadFile(defaultConfigFile); err != nil {
		return nil, fmt.Errorf("failed to read default embedded config file: %w", err)
	}
	if err = cfg.loadFromKDL(configData); err != nil {
		return nil, err
	}

	// load overrides from user KDL if provided
	if configPath != "" {
		configData, err = os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read user config file: %w", err)
		}
		if err = cfg.loadFromKDL(configData); err != nil {
			return nil, err
		}
	}

	// apply the environment variable overrides
	if err = cfg.loadFromEnvironment(); err != nil {
		return nil, err
	}

	// if hostname is not set we use the current one
	if cfg.NodeName == "" {
		cfg.NodeName = getHostname()
	}

	// Validate the final configuration
	if err = cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return cfg, nil
}

// loadFromKDL loads configuration from a KDL file
func (c *Config) loadFromKDL(data []byte) error {
	if err := kdl.Unmarshal(data, c); err != nil {
		return fmt.Errorf("failed to parse KDL config: %w", err)
	}
	return nil
}

// loadFromEnvironment loads configuration values from environment variables and overrides existing config values in the struct.
func (c *Config) loadFromEnvironment() error {
	if err := env.Parse(c); err != nil {
		return fmt.Errorf("failed to parse Environment config: %w", err)
	}
	return nil
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

	// Validate cache
	if c.Cache.BlocklistSizeMB < 1 {
		errs = append(errs, fmt.Sprintf("cache.blocklist-size-mb must be at least 1 MB, got %d", c.Cache.BlocklistSizeMB))
	}
	if c.Cache.AccountingSizeMB < 1 {
		errs = append(errs, fmt.Sprintf("cache.accounting-size-mb must be at least 1 MB, got %d", c.Cache.AccountingSizeMB))
	}
	if c.Cache.SyncTimeoutSeconds < 1 {
		errs = append(errs, fmt.Sprintf("cache.sync-timeout-seconds must be at least 1, got %d", c.Cache.SyncTimeoutSeconds))
	}
	if c.Cache.BlocklistDefaultTTLSeconds < 1 {
		errs = append(errs, fmt.Sprintf("cache.blocklist-default-ttl-seconds must be at least 1, got %d", c.Cache.BlocklistDefaultTTLSeconds))
	}

	// Validate accounting rules
	for i, rule := range c.Accounting.Rules {
		if rule.PathPrefix == "" {
			errs = append(errs, fmt.Sprintf("accounting.rules[%d].path-prefix cannot be empty", i))
		}
		if rule.Limit < 1 {
			errs = append(errs, fmt.Sprintf("accounting.rules[%d].limit must be at least 1, got %d", i, rule.Limit))
		}
		validPer := map[string]bool{"second": true, "minute": true}
		if !validPer[strings.ToLower(rule.Per)] {
			errs = append(errs, fmt.Sprintf("accounting.rules[%d].per must be one of second, minute; got %q", i, rule.Per))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("validation errors: %s", strings.Join(errs, "; "))
	}

	return nil
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

// GetPrivateAPIKey returns the private API key from environment variable
// Returns the key and a boolean indicating if it was set
func GetPrivateAPIKey() (string, bool) {
	key := os.Getenv("DRL_PRIVATE_API_KEY")
	return key, key != ""
}

// ValidatePrivateAPIKey validates the private API key meets security requirements
// Returns an error if the key is not set or is shorter than 16 characters
func ValidatePrivateAPIKey() error {
	key, exists := GetPrivateAPIKey()
	if !exists {
		return fmt.Errorf("DRL_PRIVATE_API_KEY environment variable is not set")
	}
	if len(key) < 16 {
		return fmt.Errorf("DRL_PRIVATE_API_KEY must be at least 16 characters, got %d", len(key))
	}
	return nil
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
