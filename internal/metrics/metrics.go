package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus metrics for DRL
type Metrics struct {
	// Membership metrics
	ClusterSize prometheus.Gauge
	EventsTotal *prometheus.CounterVec

	// Cache metrics
	CacheHitsTotal      *prometheus.CounterVec
	CacheMissesTotal    *prometheus.CounterVec
	CacheEvictionsTotal *prometheus.CounterVec
	CacheMemoryBytes    *prometheus.GaugeVec
	SyncDurationSeconds prometheus.Histogram

	registry *prometheus.Registry
	server   *http.Server
}

// NewMetrics creates a new Metrics instance
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		// Membership metrics
		ClusterSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "drl_membership_cluster_size",
			Help: "Current number of active members in the cluster",
		}),
		EventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_membership_events_total",
			Help: "Total number of membership events",
		}, []string{"event_type"}),

		// Cache metrics
		CacheHitsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_cache_hits_total",
			Help: "Total number of cache hits",
		}, []string{"cache_type"}),
		CacheMissesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_cache_misses_total",
			Help: "Total number of cache misses",
		}, []string{"cache_type"}),
		CacheEvictionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_cache_evictions_total",
			Help: "Total number of cache evictions due to memory pressure",
		}, []string{"cache_type"}),
		CacheMemoryBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "drl_cache_memory_bytes",
			Help: "Current memory usage of cache instances in bytes",
		}, []string{"cache_type"}),
		SyncDurationSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "drl_sync_duration_seconds",
			Help:    "Time taken for initial state sync",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~16s
		}),

		registry: registry,
	}

	registry.MustRegister(m.ClusterSize)
	registry.MustRegister(m.EventsTotal)
	registry.MustRegister(m.CacheHitsTotal)
	registry.MustRegister(m.CacheMissesTotal)
	registry.MustRegister(m.CacheEvictionsTotal)
	registry.MustRegister(m.CacheMemoryBytes)
	registry.MustRegister(m.SyncDurationSeconds)

	return m
}

// SetClusterSize updates the cluster size gauge
func (m *Metrics) SetClusterSize(size int) {
	m.ClusterSize.Set(float64(size))
}

// IncEvent increments the event counter for the given event type
func (m *Metrics) IncEvent(eventType string) {
	m.EventsTotal.WithLabelValues(eventType).Inc()
}

// IncCacheHit increments the cache hit counter for the given cache type
func (m *Metrics) IncCacheHit(cacheType string) {
	m.CacheHitsTotal.WithLabelValues(cacheType).Inc()
}

// IncCacheMiss increments the cache miss counter for the given cache type
func (m *Metrics) IncCacheMiss(cacheType string) {
	m.CacheMissesTotal.WithLabelValues(cacheType).Inc()
}

// IncCacheEviction increments the cache eviction counter for the given cache type
func (m *Metrics) IncCacheEviction(cacheType string) {
	m.CacheEvictionsTotal.WithLabelValues(cacheType).Inc()
}

// SetCacheMemory sets the memory usage for the given cache type
func (m *Metrics) SetCacheMemory(cacheType string, bytes int64) {
	m.CacheMemoryBytes.WithLabelValues(cacheType).Set(float64(bytes))
}

// ObserveSyncDuration records the duration of a state sync operation
func (m *Metrics) ObserveSyncDuration(seconds float64) {
	m.SyncDurationSeconds.Observe(seconds)
}

// StartServer starts the HTTP server for metrics
func (m *Metrics) StartServer(port int) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	m.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash - metrics are non-critical
			fmt.Printf("Metrics server error: %v\n", err)
		}
	}()

	return nil
}

// Stop stops the metrics server
func (m *Metrics) Stop() error {
	if m.server != nil {
		return m.server.Close()
	}
	return nil
}

// CacheType constants for metrics labels
const (
	CacheTypeBlocklist  = "blocklist"
	CacheTypeAccounting = "accounting"
)
