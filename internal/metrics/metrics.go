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

	// gRPC metrics
	GRPCCheckTotal prometheus.Counter

	// Cache metrics
	CacheHitsTotal      *prometheus.CounterVec
	CacheMissesTotal    *prometheus.CounterVec
	CacheEvictionsTotal *prometheus.CounterVec
	CacheMemoryBytes    *prometheus.GaugeVec
	SyncDurationSeconds prometheus.Histogram

	// Accounting metrics
	AccountingLocalIncTotal  prometheus.Counter
	AccountingRemoteIncTotal prometheus.Counter
	AccountingFlushTotal     prometheus.Counter
	AccountingUDPRecvTotal   prometheus.Counter

	// Rate limiting metrics
	RateLimitBlocksTotal          *prometheus.CounterVec
	RateLimitPropagationLatencyMs prometheus.Histogram
	GRPCResponseCodeTotal         *prometheus.CounterVec

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

		// gRPC metrics
		GRPCCheckTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_grpc_check_total",
			Help: "Total number of gRPC Check requests received",
		}),

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

		// Accounting metrics
		AccountingLocalIncTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_local_increments_total",
			Help: "Total number of local accounting increments (this node is owner)",
		}),
		AccountingRemoteIncTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_remote_increments_total",
			Help: "Total number of remote accounting increments (forwarded to owner)",
		}),
		AccountingFlushTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_flush_total",
			Help: "Total number of UDP batch flushes sent",
		}),
		AccountingUDPRecvTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_udp_recv_total",
			Help: "Total number of UDP batch messages received",
		}),

		// Rate limiting metrics
		RateLimitBlocksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_ratelimit_blocks_total",
			Help: "Total number of entities blocked by the rate limiter",
		}, []string{"rule_name", "reason"}),
		RateLimitPropagationLatencyMs: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "drl_ratelimit_propagation_latency_ms",
			Help:    "Time from local block to cluster-wide propagation in milliseconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1ms to ~2s
		}),
		GRPCResponseCodeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_grpc_response_code_total",
			Help: "Total gRPC Check responses by code",
		}, []string{"code"}),

		registry: registry,
	}

	registry.MustRegister(m.ClusterSize)
	registry.MustRegister(m.EventsTotal)
	registry.MustRegister(m.GRPCCheckTotal)
	registry.MustRegister(m.CacheHitsTotal)
	registry.MustRegister(m.CacheMissesTotal)
	registry.MustRegister(m.CacheEvictionsTotal)
	registry.MustRegister(m.CacheMemoryBytes)
	registry.MustRegister(m.SyncDurationSeconds)
	registry.MustRegister(m.AccountingLocalIncTotal)
	registry.MustRegister(m.AccountingRemoteIncTotal)
	registry.MustRegister(m.AccountingFlushTotal)
	registry.MustRegister(m.AccountingUDPRecvTotal)
	registry.MustRegister(m.RateLimitBlocksTotal)
	registry.MustRegister(m.RateLimitPropagationLatencyMs)
	registry.MustRegister(m.GRPCResponseCodeTotal)

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

// IncGRPCCheck increments the gRPC Check request counter
func (m *Metrics) IncGRPCCheck() {
	m.GRPCCheckTotal.Inc()
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

// IncAccountingLocal increments the local accounting increment counter
func (m *Metrics) IncAccountingLocal() {
	m.AccountingLocalIncTotal.Inc()
}

// IncAccountingRemote increments the remote accounting increment counter
func (m *Metrics) IncAccountingRemote() {
	m.AccountingRemoteIncTotal.Inc()
}

// IncAccountingFlush increments the accounting flush counter
func (m *Metrics) IncAccountingFlush() {
	m.AccountingFlushTotal.Inc()
}

// IncAccountingUDPRecv increments the UDP receive counter
func (m *Metrics) IncAccountingUDPRecv() {
	m.AccountingUDPRecvTotal.Inc()
}

// IncRateLimitBlock increments the rate limit block counter for the given rule and reason
func (m *Metrics) IncRateLimitBlock(ruleName, reason string) {
	m.RateLimitBlocksTotal.WithLabelValues(ruleName, reason).Inc()
}

// ObservePropagationLatency records the propagation latency in milliseconds
func (m *Metrics) ObservePropagationLatency(ms float64) {
	m.RateLimitPropagationLatencyMs.Observe(ms)
}

// IncGRPCResponseCode increments the gRPC response code counter
func (m *Metrics) IncGRPCResponseCode(code string) {
	m.GRPCResponseCodeTotal.WithLabelValues(code).Inc()
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
