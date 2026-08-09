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
	AccountingMsgRecvTotal   prometheus.Counter
	AccountingBulkLoadTotal  *prometheus.CounterVec

	// Membership messaging metrics
	MembershipReliableMsgsTotal   prometheus.Counter
	MembershipBestEffortMsgsTotal prometheus.Counter

	// Persistent gRPC channel metrics
	MembershipChannelMsgsSentTotal     prometheus.Counter
	MembershipChannelMsgsRecvTotal     prometheus.Counter
	MembershipChannelConnectionsActive prometheus.Gauge
	MembershipChannelErrorsTotal       prometheus.Counter

	// Handover metrics
	HandoverOutEntities prometheus.Counter
	HandoverInEntities  prometheus.Counter
	HandoverDurationMs  prometheus.Histogram
	HandoverFailedTotal prometheus.Counter

	// Rate limiting metrics
	RateLimitBlocksTotal          *prometheus.CounterVec
	RateLimitPropagationLatencyMs prometheus.Histogram
	GRPCResponseCodeTotal         *prometheus.CounterVec

	// Token bucket metrics
	RateLimitTokensConsumedTotal  *prometheus.CounterVec
	RateLimitBucketExhaustedTotal *prometheus.CounterVec
	RateLimitTokensCurrent        *prometheus.GaugeVec

	// Embedded proxy metrics
	ProxyRequestsTotal  *prometheus.CounterVec
	ProxyErrorsTotal    *prometheus.CounterVec
	ProxyLatencySeconds *prometheus.HistogramVec

	// OIDC authentication metrics
	OIDCRequestsTotal        *prometheus.CounterVec   // labels: host, path, status
	OIDCVerificationDuration *prometheus.HistogramVec // labels: host, path

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
		AccountingMsgRecvTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_msg_recv_total",
			Help: "Total number of accounting batch messages received",
		}),
		AccountingBulkLoadTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_accounting_bulk_load_total",
			Help: "Total number of bulk-load entries processed via the private API, by outcome",
		}, []string{"result"}),
		MembershipReliableMsgsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_membership_reliable_msgs_total",
			Help: "Total number of reliable messages sent via memberlist",
		}),
		MembershipBestEffortMsgsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_membership_best_effort_msgs_total",
			Help: "Total number of best-effort messages sent via memberlist",
		}),

		// Persistent gRPC channel metrics
		MembershipChannelMsgsSentTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_membership_channel_msgs_sent_total",
			Help: "Total number of hi-priority messages sent via the persistent gRPC channel",
		}),
		MembershipChannelMsgsRecvTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_membership_channel_msgs_recv_total",
			Help: "Total number of hi-priority messages received via the persistent gRPC channel",
		}),
		MembershipChannelConnectionsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "drl_membership_channel_connections_active",
			Help: "Current number of active persistent gRPC channel connections to peers",
		}),
		MembershipChannelErrorsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_membership_channel_errors_total",
			Help: "Total number of persistent gRPC channel errors (dial failures, send/recv failures)",
		}),

		// Handover metrics
		HandoverOutEntities: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_handover_out_entities",
			Help: "Total number of entities sent by leaving node during handover",
		}),
		HandoverInEntities: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_handover_in_entities",
			Help: "Total number of entities received and processed by adopter during handover",
		}),
		HandoverDurationMs: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "drl_accounting_handover_duration_ms",
			Help:    "Total time taken for the handover in milliseconds",
			Buckets: prometheus.ExponentialBuckets(10, 2, 12), // 10ms to ~20s
		}),
		HandoverFailedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "drl_accounting_handover_failed_total",
			Help: "Total number of failed handover attempts",
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

		// Token bucket metrics
		RateLimitTokensConsumedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_ratelimit_tokens_consumed_total",
			Help: "Total number of tokens consumed by allowed requests (token-bucket algorithm)",
		}, []string{"rule_name"}),
		RateLimitBucketExhaustedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_ratelimit_bucket_exhausted_total",
			Help: "Total number of requests denied due to an empty token bucket",
		}, []string{"rule_name"}),
		RateLimitTokensCurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "drl_ratelimit_tokens_current",
			Help: "Remaining tokens in the bucket after the most recent request (sampled, token-bucket algorithm)",
		}, []string{"rule_name"}),

		// Embedded proxy metrics
		ProxyRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_proxy_requests_total",
			Help: "Total number of requests handled by the embedded proxy",
		}, []string{"host", "route", "status"}),
		ProxyErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_proxy_errors_total",
			Help: "Total number of errors encountered by the embedded proxy",
		}, []string{"host", "route", "reason"}),
		ProxyLatencySeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "drl_proxy_latency_seconds",
			Help:    "Latency of upstream round-trips handled by the embedded proxy",
			Buckets: prometheus.DefBuckets,
		}, []string{"host", "route"}),

		// OIDC authentication metrics
		OIDCRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_proxy_oidc_requests_total",
			Help: "Total OIDC authentication attempts, labelled by outcome (success, missing_token, invalid_signature, token_expired, forbidden_scope, invalid_token)",
		}, []string{"host", "path", "status"}),
		OIDCVerificationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "drl_proxy_oidc_verification_duration_seconds",
			Help:    "Latency of the OIDC JWT crypto-verification cycle (JWKS cache hit path)",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 12), // 100µs → ~400ms
		}, []string{"host", "path"}),

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
	registry.MustRegister(m.AccountingMsgRecvTotal)
	registry.MustRegister(m.AccountingBulkLoadTotal)
	registry.MustRegister(m.MembershipReliableMsgsTotal)
	registry.MustRegister(m.MembershipBestEffortMsgsTotal)
	registry.MustRegister(m.MembershipChannelMsgsSentTotal)
	registry.MustRegister(m.MembershipChannelMsgsRecvTotal)
	registry.MustRegister(m.MembershipChannelConnectionsActive)
	registry.MustRegister(m.MembershipChannelErrorsTotal)
	registry.MustRegister(m.HandoverOutEntities)
	registry.MustRegister(m.HandoverInEntities)
	registry.MustRegister(m.HandoverDurationMs)
	registry.MustRegister(m.HandoverFailedTotal)
	registry.MustRegister(m.RateLimitBlocksTotal)
	registry.MustRegister(m.RateLimitPropagationLatencyMs)
	registry.MustRegister(m.GRPCResponseCodeTotal)
	registry.MustRegister(m.RateLimitTokensConsumedTotal)
	registry.MustRegister(m.RateLimitBucketExhaustedTotal)
	registry.MustRegister(m.RateLimitTokensCurrent)
	registry.MustRegister(m.ProxyRequestsTotal)
	registry.MustRegister(m.ProxyErrorsTotal)
	registry.MustRegister(m.ProxyLatencySeconds)
	registry.MustRegister(m.OIDCRequestsTotal)
	registry.MustRegister(m.OIDCVerificationDuration)

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

// IncAccountingMsgRecv increments the accounting message receive counter
func (m *Metrics) IncAccountingMsgRecv() {
	m.AccountingMsgRecvTotal.Inc()
}

// IncAccountingBulkLoad increments the bulk-load counter for the given outcome.
// Result must be one of: "no_match", "accepted_local", "accepted_remote",
// "dropped", or "invalid".
func (m *Metrics) IncAccountingBulkLoad(result string) {
	m.AccountingBulkLoadTotal.WithLabelValues(result).Inc()
}

// IncMembershipReliable increments the reliable message counter
func (m *Metrics) IncMembershipReliable() {
	m.MembershipReliableMsgsTotal.Inc()
}

// IncMembershipBestEffort increments the best-effort message counter
func (m *Metrics) IncMembershipBestEffort() {
	m.MembershipBestEffortMsgsTotal.Inc()
}

// IncMembershipChannelMsgsSent increments the persistent gRPC channel sent-message counter
func (m *Metrics) IncMembershipChannelMsgsSent() {
	m.MembershipChannelMsgsSentTotal.Inc()
}

// IncMembershipChannelMsgsRecv increments the persistent gRPC channel received-message counter
func (m *Metrics) IncMembershipChannelMsgsRecv() {
	m.MembershipChannelMsgsRecvTotal.Inc()
}

// IncMembershipChannelConnections increments the active persistent gRPC channel connections gauge
func (m *Metrics) IncMembershipChannelConnections() {
	m.MembershipChannelConnectionsActive.Inc()
}

// DecMembershipChannelConnections decrements the active persistent gRPC channel connections gauge
func (m *Metrics) DecMembershipChannelConnections() {
	m.MembershipChannelConnectionsActive.Dec()
}

// IncMembershipChannelErrors increments the persistent gRPC channel error counter
func (m *Metrics) IncMembershipChannelErrors() {
	m.MembershipChannelErrorsTotal.Inc()
}

// AddHandoverOut adds to the handover out entities counter
func (m *Metrics) AddHandoverOut(n float64) {
	m.HandoverOutEntities.Add(n)
}

// AddHandoverIn adds to the handover in entities counter
func (m *Metrics) AddHandoverIn(n float64) {
	m.HandoverInEntities.Add(n)
}

// ObserveHandoverDuration records the handover duration in milliseconds
func (m *Metrics) ObserveHandoverDuration(ms float64) {
	m.HandoverDurationMs.Observe(ms)
}

// IncHandoverFailed increments the failed handover counter
func (m *Metrics) IncHandoverFailed() {
	m.HandoverFailedTotal.Inc()
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

// IncRateLimitTokensConsumed increments the token-consumed counter for a rule.
func (m *Metrics) IncRateLimitTokensConsumed(ruleName string) {
	m.RateLimitTokensConsumedTotal.WithLabelValues(ruleName).Inc()
}

// IncRateLimitBucketExhausted increments the bucket-exhausted counter for a rule.
func (m *Metrics) IncRateLimitBucketExhausted(ruleName string) {
	m.RateLimitBucketExhaustedTotal.WithLabelValues(ruleName).Inc()
}

// SetRateLimitTokensCurrent updates the remaining-tokens gauge for a rule.
// This is sampled on each token-bucket decision; callers should use a consistent
// rule_name label to avoid unbounded cardinality.
func (m *Metrics) SetRateLimitTokensCurrent(ruleName string, tokens float64) {
	m.RateLimitTokensCurrent.WithLabelValues(ruleName).Set(tokens)
}

// GatherForUI collects current metric values from the Prometheus registry and
// returns a flat map of metric name (with label key=value suffix for labelled
// metrics) to its current float64 value.  Counter families are summed across
// labels; Gauge families take the last observed value.
// The result is consumed by the DRL dashboard SPA via GET /v1/ui/api/metrics.
func (m *Metrics) GatherForUI() map[string]float64 {
	mfs, err := m.registry.Gather()
	if err != nil {
		return nil
	}
	result := make(map[string]float64, len(mfs)*2)
	for _, mf := range mfs {
		name := mf.GetName()
		for _, metric := range mf.GetMetric() {
			// Build a label suffix like {key=val,key2=val2} when labels exist.
			labelStr := ""
			for _, lp := range metric.GetLabel() {
				if labelStr != "" {
					labelStr += ","
				}
				labelStr += lp.GetName() + "=" + lp.GetValue()
			}
			key := name
			if labelStr != "" {
				key = name + "{" + labelStr + "}"
			}

			switch {
			case metric.GetCounter() != nil:
				result[key] += metric.GetCounter().GetValue()
			case metric.GetGauge() != nil:
				result[key] = metric.GetGauge().GetValue()
			}
		}
	}
	return result
}

// IncProxyRequest increments the proxy request counter for the given host,
// route prefix, and HTTP status code string (e.g. "200", "429").
func (m *Metrics) IncProxyRequest(host, route, status string) {
	m.ProxyRequestsTotal.WithLabelValues(host, route, status).Inc()
}

// IncProxyError increments the proxy error counter for the given host, route,
// and reason (e.g. "upstream").
func (m *Metrics) IncProxyError(host, route, reason string) {
	m.ProxyErrorsTotal.WithLabelValues(host, route, reason).Inc()
}

// ObserveProxyLatency records an upstream round-trip duration for the given
// host and route prefix.
func (m *Metrics) ObserveProxyLatency(host, route string, seconds float64) {
	m.ProxyLatencySeconds.WithLabelValues(host, route).Observe(seconds)
}

// IncOIDCRequest increments the OIDC authentication request counter for the given
// host, path, and outcome status (e.g. "success", "missing_token", "token_expired").
func (m *Metrics) IncOIDCRequest(host, path, status string) {
	m.OIDCRequestsTotal.WithLabelValues(host, path, status).Inc()
}

// ObserveOIDCLatency records the duration of a single OIDC JWT verification cycle
// (signature check + claim extraction) for the given host and path.
func (m *Metrics) ObserveOIDCLatency(host, path string, seconds float64) {
	m.OIDCVerificationDuration.WithLabelValues(host, path).Observe(seconds)
}

// CacheType constants for metrics labels
const (
	CacheTypeBlocklist  = "blocklist"
	CacheTypeAccounting = "accounting"
)
