package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus metrics for DRL
type Metrics struct {
	ClusterSize prometheus.Gauge
	EventsTotal *prometheus.CounterVec
	registry    *prometheus.Registry
	server      *http.Server
}

// NewMetrics creates a new Metrics instance
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		ClusterSize: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "drl_membership_cluster_size",
			Help: "Current number of active members in the cluster",
		}),
		EventsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "drl_membership_events_total",
			Help: "Total number of membership events",
		}, []string{"event_type"}),
		registry: registry,
	}

	registry.MustRegister(m.ClusterSize)
	registry.MustRegister(m.EventsTotal)

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
