// Package models contains shared request/response structs for the DRL Private API (v1).
package models

// ErrorResponse is the standardized error body returned by all endpoints on failure.
//
// Example:
//
//	{"error":"invalid ip","code":400,"details":"ip parameter must be a valid IPv4 address"}
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Details string `json:"details,omitempty"`
}

// StatusResponse is the JSON body returned by GET /v1/status.
type StatusResponse struct {
	ClusterName         string   `json:"cluster_name"`
	NodeID              string   `json:"node_id"`
	ActivePeers         []string `json:"active_peers"`
	ActivePeerAddresses []string `json:"active_peer_addresses"`
	Uptime              string   `json:"uptime"`
	UptimeSeconds       float64  `json:"uptime_seconds"`
}

// AccountingStatsResponse is the JSON body returned by GET /v1/accounting/stats.
type AccountingStatsResponse struct {
	LocalNodeID            string `json:"local_node_id"`
	MonitoredEntitiesCount int64  `json:"monitored_entities_count"`
	BatchedUpdatesPending  int64  `json:"batched_updates_pending"`
	EstimatedEntitiesCount int64  `json:"estimated_entities_count"`
}

// BulkLoadLine is one record in the NDJSON request body for POST /v1/accounting/load.
type BulkLoadLine struct {
	SourceIP string            `json:"sourceIP" validate:"required"`
	Path     string            `json:"path" validate:"required"`
	Headers  map[string]string `json:"headers"`
}

// BulkLoadResponse is the JSON body returned by POST /v1/accounting/load.
type BulkLoadResponse struct {
	ID             string   `json:"id"`
	Total          int      `json:"total"`
	AcceptedLocal  int      `json:"accepted_local"`
	AcceptedRemote int      `json:"accepted_remote"`
	Dropped        int      `json:"dropped"`
	NoMatch        int      `json:"no_match"`
	Invalid        int      `json:"invalid"`
	Errors         []string `json:"errors"`
}

// EntityResponse is the JSON body returned by block/unblock endpoints.
type EntityResponse struct {
	ID      string            `json:"id"`
	IP      string            `json:"ip"`
	URIPath string            `json:"uriPath"`
	Headers map[string]string `json:"headers"`
	Message string            `json:"message"`
	Errors  []string          `json:"errors"`
}

// BlockedEntityEntry is one element in the GET /v1/blocked-entity JSON array.
type BlockedEntityEntry struct {
	ID        string            `json:"id"`
	IP        string            `json:"ip"`
	URIPath   string            `json:"uriPath"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt string            `json:"expires_at"`
}

// GetTokenResponse is returned by GET /v1/ui/get-token.
type GetTokenResponse struct {
	BootstrapToken string `json:"bootstrap_token"`
}

// UIMetricsResponse is returned by GET /v1/ui/api/metrics.
type UIMetricsResponse struct {
	NodeID    string             `json:"nodeId"`
	Timestamp string             `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}
