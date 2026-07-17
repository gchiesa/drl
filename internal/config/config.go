package config

import (
	"embed"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/samber/lo"
	"github.com/sblinch/kdl-go"
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
	// Listen configuration
	Listen ListenConfig `kdl:"listen" envPrefix:"DRL_LISTEN_" json:"listen"`

	// Membership configuration
	Membership MembershipConfig `kdl:"membership" envPrefix:"DRL_MEMBERSHIP_" json:"membership"`

	// Logging configuration
	Logging LoggingConfig `kdl:"logging" envPrefix:"DRL_LOGGING_" json:"logging"`

	// InternalAPI configuration
	InternalAPI InternalAPIConfig `kdl:"internal-api" envPrefix:"DRL_INTERNAL_API_" json:"internal-api"`

	// Cache configuration
	Cache CacheConfig `kdl:"cache" envPrefix:"DRL_CACHE_" json:"cache"`

	// Accounting configuration
	Accounting AccountingConfig `kdl:"accounting" envPrefix:"DRL_ACCOUNTING_" json:"accounting"`

	// EmbeddedProxy configuration
	EmbeddedProxy EmbeddedProxyConfig `kdl:"embedded-proxy" envPrefix:"DRL_EMBEDDED_PROXY_" json:"embedded-proxy,omitempty"`

	// meta
	externalConfigFilePath string
}

// AccountingConfig holds accounting rules for entity rate limiting
type AccountingConfig struct {
	Settings AccountingSettings        `kdl:"settings" envPrefix:"SETTINGS_" json:"settings"`
	Rules    map[string]AccountingRule `kdl:"rules"                          json:"rules,omitempty"`
}

// AccountingSettings holds global accounting settings
type AccountingSettings struct {
	// Algorithm is the rate limiting algorithm ("sliding-window" or "token-bucket")
	Algorithm string `kdl:"algorithm" env:"ALGORITHM" json:"algorithm"`
	// RetryAfterType determines the Retry-After header format: "delay-seconds" or "http-date"
	RetryAfterType string `kdl:"retry-after-type" env:"RETRY_AFTER_TYPE" json:"retry-after-type"`
	// FlushInterval is the interval between flusher batch sends
	FlushInterval time.Duration `kdl:"flush-interval" env:"FLUSH_INTERVAL" json:"flush-interval"`
	// MaxBatchSize is the maximum number of entries per batch before auto-flush
	MaxBatchSize int `kdl:"max-batch-size" env:"MAX_BATCH_SIZE" json:"max-batch-size"`
	// Capacity is the token bucket burst size (required when algorithm is "token-bucket")
	Capacity int64 `kdl:"capacity" env:"CAPACITY" json:"capacity,omitempty"`
	// RefillRate is the token bucket refill rate in tokens per second (required when algorithm is "token-bucket")
	RefillRate float64 `kdl:"refill-rate" env:"REFILL_RATE" json:"refill-rate,omitempty"`
	// UseXForwardedFor enables source IP extraction from the x-forwarded-for header
	// instead of the socket remote address. Useful when DRL sits behind one or more
	// reverse proxies and the Envoy remote_addr is a proxy IP rather than the
	// originating client.
	UseXForwardedFor bool `kdl:"use-x-forwarded-for" env:"USE_X_FORWARDED_FOR" json:"use-x-forwarded-for"`
	// UseXForwardedForDirection controls which end of the XFF chain to read from.
	// "left" reads from the client end (index 0 = original client IP, spoofable).
	// "right" reads from the proxy end (index 0 = last proxy, index 1 = first trusted
	// external IP — safer when you control the rightmost proxy). Default: "left".
	UseXForwardedForDirection string `kdl:"use-x-forwarded-for-direction" env:"USE_X_FORWARDED_FOR_DIRECTION" json:"use-x-forwarded-for-direction,omitempty"`
	// UseXForwardedForIndex is the zero-based offset from the chosen direction.
	// With direction "left" and index 0, the leftmost (client-supplied) IP is used.
	// With direction "right" and index 1, the second-from-right IP is used — the
	// one that your outermost trusted proxy recorded as the upstream address.
	UseXForwardedForIndex int `kdl:"use-x-forwarded-for-index" env:"USE_X_FORWARDED_FOR_INDEX" json:"use-x-forwarded-for-index,omitempty"`
}

// AccountingRule defines a rate-limiting rule for a path prefix
type AccountingRule struct {
	PathPrefix string   `kdl:"path-prefix" json:"path-prefix"`
	Headers    []string `kdl:"headers"     json:"headers,omitempty"`
	// HeaderRedactions rules for headers. It uses capturing regexp and capturing groups
	// so that you can extract a part of the value and omit the non-matching
	// part.
	// Examples:
	// 1 - redact an API Key, e.g. {"Authorization": "^(.{0,3}).*$"}
	// 2 - extract only the first portion of a FQDN {"Host": "^(\w+).*$"}
	HeaderRedactions map[string]string `kdl:"redactions"  json:"redactions,omitempty"`
	Limit            int64             `kdl:"limit"       json:"limit"`
	Per              string            `kdl:"per"         json:"per"`
}

// WindowDuration returns the time.Duration for the rule's rate window
func (r AccountingRule) WindowDuration() time.Duration {
	if strings.ToLower(r.Per) == "second" {
		return time.Second
	}
	return time.Minute
}

// ListenConfig holds listener configuration
type ListenConfig struct {
	// GRPC is the address for gRPC server
	GRPC string `kdl:"grpc" env:"GRPC" json:"grpc"`
	// Metrics is the address for Prometheus metrics endpoint
	Metrics string `kdl:"metrics" env:"METRICS" json:"metrics"`
}

// MembershipConfig holds cluster membership configuration
type MembershipConfig struct {
	// ServiceName is the DNS name to resolve for peer discovery
	ServiceName string `kdl:"service-name" env:"SERVICE_NAME" json:"service-name"`
	// Port is the port for memberlist gossip
	Port int `kdl:"port" env:"PORT" json:"port"`
	// BindAddr is the address to bind memberlist to
	BindAddr string `kdl:"bind-addr" env:"BIND_ADDR" json:"bind-addr"`
	// StartupDelay is the delay before attempting to join the cluster
	StartupDelay time.Duration `kdl:"startup-delay" env:"STARTUP_DELAY" json:"startup-delay"`
	// GossipInterval is the interval between gossip rounds
	GossipInterval time.Duration `kdl:"gossip-interval" env:"GOSSIP_INTERVAL" json:"gossip-interval"`
	// GossipNodes is the number of nodes to gossip to per round
	GossipNodes int `kdl:"gossip-nodes" env:"GOSSIP_NODES" json:"gossip-nodes"`
	// SecretKeys is a list of AES encryption keys for memberlist.
	// The first key is the primary (used for encryption); additional keys
	// are secondary (decryption only, for key rotation). All keys must be
	// valid AES lengths (16, 24, or 32 bytes).
	// Override via env: DRL_MEMBERSHIP_PRIMARY_KEY + DRL_MEMBERSHIP_SECONDARY_KEYS
	SecretKeys []string `kdl:"secret-keys" json:"-"` // never serialised — contains sensitive key material
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	// Level is the log level (debug, info, warn, error)
	Level string `kdl:"level" env:"LEVEL" json:"level"`
	// Format is the log format (json, text)
	Format string `kdl:"format" env:"FORMAT" json:"format"`
}

// InternalAPIConfig holds internal API configuration
type InternalAPIConfig struct {
	// Enabled indicates if the internal API is enabled
	Enabled bool `kdl:"enabled" env:"ENABLED" json:"enabled"`
	// Address is the address for the internal API server
	Address string `kdl:"address" env:"ADDRESS" json:"address"`
}

// CacheConfig holds cache configuration
type CacheConfig struct {
	// BlocklistSizeMB is the maximum size in MB for the blocklist cache
	BlocklistSizeMB int64 `kdl:"blocklist-size-mb" env:"BLOCKLIST_SIZE_MB" json:"blocklist-size-mb"`
	// AccountingSizeMB is the maximum size in MB for the accounting cache
	AccountingSizeMB int64 `kdl:"accounting-size-mb" env:"ACCOUNTING_SIZE_MB" json:"accounting-size-mb"`
	// SyncTimeoutSeconds is the timeout in seconds for initial state sync
	SyncTimeoutSeconds int `kdl:"sync-timeout-seconds" env:"SYNC_TIMEOUT_SECONDS" json:"sync-timeout-seconds"`
	// BlocklistDefaultTTLSeconds is the default TTL in seconds for admin-API blocks
	BlocklistDefaultTTLSeconds int `kdl:"blocklist-default-ttl-seconds" env:"BLOCKLIST_DEFAULT_TTL_SECONDS" json:"blocklist-default-ttl-seconds"`
}

// EmbeddedProxyConfig holds configuration for the embedded reverse proxy.
type EmbeddedProxyConfig struct {
	Enabled bool                   `kdl:"enabled" env:"ENABLED" json:"enabled"`
	Listen  string                 `kdl:"listen"  env:"LISTEN"  json:"listen"`
	TLS     EmbeddedProxyTLSConfig `kdl:"tls"                   json:"tls"`
	// ",multiple" tells kdl-go to append one ProxyHostConfig per "host" node
	// rather than treating the node's positional arguments as slice elements.
	Hosts []ProxyHostConfig `kdl:"host,multiple" json:"hosts,omitempty"`
}

// EmbeddedProxyTLSConfig holds TLS settings for the embedded proxy.
type EmbeddedProxyTLSConfig struct {
	Enabled bool   `kdl:"enabled" env:"TLS_ENABLED" json:"enabled"`
	Cert    string `kdl:"cert"    env:"TLS_CERT"    json:"cert,omitempty"`
	Key     string `kdl:"key"     env:"TLS_KEY"     json:"key,omitempty"`
}

// ProxyHostConfig maps a virtual hostname to its routing rules.
type ProxyHostConfig struct {
	Hostname string             `kdl:",arg"   json:"hostname"`
	OIDC     ProxyOIDCConfig    `kdl:"oidc"   json:"oidc,omitempty"`
	Routes   ProxyRoutesWrapper `kdl:"routes" json:"routes"`
}

// ProxyRoutesWrapper is the "routes { }" block wrapping individual route entries.
type ProxyRoutesWrapper struct {
	// ",multiple" appends one ProxyRouteConfig per "route" node.
	Routes []ProxyRouteConfig `kdl:"route,multiple" json:"routes,omitempty"`
}

// ProxyRouteConfig defines a single upstream route entry.
type ProxyRouteConfig struct {
	Prefix             string        `kdl:",arg"                  json:"prefix"`
	Upstream           string        `kdl:"upstream"              json:"upstream"`
	BalanceStrategy    string        `kdl:"balance-strategy"      json:"balance-strategy,omitempty"`
	DNSRefreshInterval time.Duration `kdl:"dns-refresh-interval"  json:"dns-refresh-interval,omitempty"`
	RequireAuth        bool          `kdl:"require-auth"          json:"require-auth"`
	// Scopes lists the OAuth2 scopes required to access this route (used with require-auth true).
	// Populated from a single KDL node with positional args: scopes "read" "write"
	Scopes []string `kdl:"scopes" json:"scopes,omitempty"`

	// Connection pool settings for the upstream transport.
	// Zero values fall back to sensible proxy-oriented defaults (not http.DefaultTransport).

	// MaxIdleConnsPerHost caps the number of idle keep-alive connections held open per
	// upstream host. Defaults to 32 when zero.
	MaxIdleConnsPerHost int `kdl:"max-idle-conns-per-host" json:"max-idle-conns-per-host,omitempty"`
	// MaxConnsPerHost limits the total number of connections per host (dialing + active +
	// idle). Zero means no limit.
	MaxConnsPerHost int `kdl:"max-conns-per-host" json:"max-conns-per-host,omitempty"`
	// IdleConnTimeout is the maximum time an idle keep-alive connection stays open before
	// being closed. Defaults to 90s when zero.
	IdleConnTimeout time.Duration `kdl:"idle-conn-timeout" json:"idle-conn-timeout,omitempty"`
	// ResponseHeaderTimeout is the maximum time to wait for an upstream response header
	// after the request has been fully written. Defaults to 30s when zero.
	ResponseHeaderTimeout time.Duration `kdl:"response-header-timeout" json:"response-header-timeout,omitempty"`
	// DialTimeout is the maximum time allowed to establish a new TCP connection to the
	// upstream. Defaults to 30s when zero.
	DialTimeout time.Duration `kdl:"dial-timeout" json:"dial-timeout,omitempty"`
}

// ProxyOIDCClaimsConfig overrides the JWT claim field names used for scope/role extraction.
// Useful for providers like Okta or Azure that use non-standard claim names.
type ProxyOIDCClaimsConfig struct {
	Scopes string `kdl:"scopes" json:"scopes,omitempty"` // default: "scope"
	Roles  string `kdl:"roles"  json:"roles,omitempty"`  // default: "roles"
}

// ProxyOIDCConfig declares the OIDC Resource Server settings for a virtual host.
// DRL acts as a Resource Server only — it validates Bearer tokens but never issues them.
type ProxyOIDCConfig struct {
	Issuer       string                `kdl:"issuer"         json:"issuer,omitempty"`
	ClientID     string                `kdl:"client-id"      json:"client-id,omitempty"`
	Audience     string                `kdl:"audience"       json:"audience,omitempty"`
	Claims       ProxyOIDCClaimsConfig `kdl:"claims"         json:"claims,omitempty"`
	JWKSCacheTTL time.Duration         `kdl:"jwks-cache-ttl" json:"jwks-cache-ttl,omitempty"`
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
		cfg.externalConfigFilePath = configPath
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

	// Apply encryption key environment variable overrides
	// (DRL_MEMBERSHIP_PRIMARY_KEY + DRL_MEMBERSHIP_SECONDARY_KEYS)
	cfg.applyEncryptionKeyEnvOverrides()

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

// applyEncryptionKeyEnvOverrides checks DRL_MEMBERSHIP_PRIMARY_KEY and
// DRL_MEMBERSHIP_SECONDARY_KEYS environment variables. If the primary key
// env var is set, it overrides SecretKeys entirely (env > KDL precedence).
func (c *Config) applyEncryptionKeyEnvOverrides() {
	primaryKey := os.Getenv("DRL_MEMBERSHIP_PRIMARY_KEY")
	if primaryKey == "" {
		return
	}
	keys := []string{primaryKey}
	if secondary := os.Getenv("DRL_MEMBERSHIP_SECONDARY_KEYS"); secondary != "" {
		for _, k := range strings.Split(secondary, ",") {
			if trimmed := strings.TrimSpace(k); trimmed != "" {
				keys = append(keys, trimmed)
			}
		}
	}
	c.Membership.SecretKeys = keys
}

// GetConfigFilePath returns the path to the configuration file.
// If an external path is specified, it is returned; otherwise, the default path is used.
func (c *Config) GetConfigFilePath() string {
	if c.externalConfigFilePath != "" {
		return c.externalConfigFilePath
	}
	return defaultConfigFile
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

	// Validate secret keys for encryption
	if len(c.Membership.SecretKeys) > 0 {
		for i, key := range c.Membership.SecretKeys {
			keyLen := len(key)
			if keyLen != 16 && keyLen != 24 && keyLen != 32 {
				errs = append(errs, fmt.Sprintf(
					"membership.secret-keys[%d] must be 16, 24, or 32 bytes (valid AES key length), got %d bytes",
					i, keyLen))
			}
		}
		// All keys must be the same length (memberlist requirement)
		if len(c.Membership.SecretKeys) > 1 {
			firstLen := len(c.Membership.SecretKeys[0])
			for i := 1; i < len(c.Membership.SecretKeys); i++ {
				if len(c.Membership.SecretKeys[i]) != firstLen {
					errs = append(errs, fmt.Sprintf(
						"membership.secret-keys: all keys must have the same length; key[0] is %d bytes but key[%d] is %d bytes",
						firstLen, i, len(c.Membership.SecretKeys[i])))
				}
			}
		}
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

	// Apply membership defaults
	if c.Membership.GossipInterval == 0 {
		c.Membership.GossipInterval = 50 * time.Millisecond
	}
	if c.Membership.GossipNodes == 0 {
		c.Membership.GossipNodes = 5
	}

	// Apply accounting settings defaults
	if c.Accounting.Settings.Algorithm == "" {
		c.Accounting.Settings.Algorithm = "sliding-window"
	}
	if c.Accounting.Settings.RetryAfterType == "" {
		c.Accounting.Settings.RetryAfterType = "delay-seconds"
	}
	if c.Accounting.Settings.FlushInterval == 0 {
		c.Accounting.Settings.FlushInterval = 200 * time.Millisecond
	}
	if c.Accounting.Settings.MaxBatchSize == 0 {
		c.Accounting.Settings.MaxBatchSize = 1000
	}

	// Validate accounting settings
	validAlgorithms := map[string]bool{"sliding-window": true, "token-bucket": true}
	if !validAlgorithms[strings.ToLower(c.Accounting.Settings.Algorithm)] {
		errs = append(errs, fmt.Sprintf("accounting.settings.algorithm must be one of sliding-window, token-bucket; got %q", c.Accounting.Settings.Algorithm))
	}
	if strings.ToLower(c.Accounting.Settings.Algorithm) == "token-bucket" {
		if c.Accounting.Settings.Capacity <= 0 {
			errs = append(errs, fmt.Sprintf("accounting.settings.capacity must be > 0 when algorithm is token-bucket, got %d", c.Accounting.Settings.Capacity))
		}
		if c.Accounting.Settings.RefillRate <= 0 {
			errs = append(errs, fmt.Sprintf("accounting.settings.refill-rate must be > 0 when algorithm is token-bucket, got %g", c.Accounting.Settings.RefillRate))
		}
	}
	validRetryAfter := map[string]bool{"delay-seconds": true, "http-date": true}
	if !validRetryAfter[strings.ToLower(c.Accounting.Settings.RetryAfterType)] {
		errs = append(errs, fmt.Sprintf("accounting.settings.retry-after-type must be one of delay-seconds, http-date; got %q", c.Accounting.Settings.RetryAfterType))
	}

	// Validate x-forwarded-for settings (only when the feature is enabled)
	if c.Accounting.Settings.UseXForwardedFor {
		if c.Accounting.Settings.UseXForwardedForDirection == "" {
			c.Accounting.Settings.UseXForwardedForDirection = "left"
		}
		validDirs := map[string]bool{"left": true, "right": true}
		if !validDirs[strings.ToLower(c.Accounting.Settings.UseXForwardedForDirection)] {
			errs = append(errs, fmt.Sprintf(
				"accounting.settings.use-x-forwarded-for-direction must be one of left, right; got %q",
				c.Accounting.Settings.UseXForwardedForDirection))
		}
		if c.Accounting.Settings.UseXForwardedForIndex < 0 {
			errs = append(errs, fmt.Sprintf(
				"accounting.settings.use-x-forwarded-for-index must be >= 0; got %d",
				c.Accounting.Settings.UseXForwardedForIndex))
		}
	}

	// Validate accounting rules
	for key, rule := range c.Accounting.Rules {
		if rule.PathPrefix == "" {
			errs = append(errs, fmt.Sprintf("accounting.rules[%s].path-prefix cannot be empty", key))
		}
		if rule.Limit < 1 {
			errs = append(errs, fmt.Sprintf("accounting.rules[%s].limit must be at least 1, got %d", key, rule.Limit))
		}
		validPer := map[string]bool{"second": true, "minute": true}
		if !validPer[strings.ToLower(rule.Per)] {
			errs = append(errs, fmt.Sprintf("accounting.rules[%s].per must be one of second, minute; got %q", key, rule.Per))
		}
		lo.MapValues(rule.HeaderRedactions, func(v string, k string) error {
			_, err := regexp.Compile(v)
			if err != nil {
				errs = append(errs, fmt.Sprintf("accounting.rules[%s].header-redactions[%s] is not a valid regular expression: %s", key, k, err))
			}
			return nil
		})
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

// GetConfigSection returns the JSON-serialisable representation of the named
// top-level configuration section. Recognised names (case-insensitive):
// "accounting", "membership", "cache", "listen", "logging", "internal-api".
// Returns (nil, false) for unknown section names.
func (c *Config) GetConfigSection(section string) (any, bool) {
	switch strings.ToLower(section) {
	case "accounting":
		return c.Accounting, true
	case "membership":
		return c.Membership, true
	case "cache":
		return c.Cache, true
	case "listen":
		return c.Listen, true
	case "logging":
		return c.Logging, true
	case "internal-api":
		return c.InternalAPI, true
	case "embedded-proxy":
		return c.EmbeddedProxy, true
	default:
		return nil, false
	}
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
