package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds the DRL configuration
type Config struct {
	// NodeName is the unique identifier for this node
	NodeName string

	// BindAddr is the address to bind memberlist to
	BindAddr string

	// BindPort is the port for memberlist gossip
	BindPort int

	// DiscoveryServiceName is the DNS name to resolve for peer discovery
	DiscoveryServiceName string

	// MetricsPort is the port for the Prometheus metrics endpoint
	MetricsPort int

	// StartupDelay is the delay before attempting to join the cluster
	StartupDelay time.Duration
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		NodeName:             getEnvOrDefault("NODE_NAME", getHostname()),
		BindAddr:             getEnvOrDefault("BIND_ADDR", "0.0.0.0"),
		BindPort:             getEnvIntOrDefault("BIND_PORT", 7946),
		DiscoveryServiceName: getEnvOrDefault("DISCOVERY_SERVICE_NAME", "drl"),
		MetricsPort:          getEnvIntOrDefault("METRICS_PORT", 9091),
		StartupDelay:         getEnvDurationOrDefault("STARTUP_DELAY", 3*time.Second),
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
