package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"
)

// Bulk-load outcome strings, mirrored from internal/accounting. Duplicated
// here as untyped string constants to avoid an api → accounting import.
const (
	bulkLoadResultNoMatch        = "no_match"
	bulkLoadResultAcceptedLocal  = "accepted_local"
	bulkLoadResultAcceptedRemote = "accepted_remote"
	bulkLoadResultDropped        = "dropped"
	bulkLoadResultInvalid        = "invalid"

	// maxBulkLoadParseErrors caps how many parse-error messages are echoed
	// back in the response body, to keep responses bounded for big payloads.
	maxBulkLoadParseErrors = 20
	// maxBulkLoadDroppedLogged caps the number of dropped entity keys in the
	// summary warn log.
	maxBulkLoadDroppedLogged = 10
	// bulkLoadScannerMaxLine is the per-line cap for the NDJSON scanner.
	bulkLoadScannerMaxLine = 1024 * 1024 // 1 MiB
)

// bulkLoadLine is one record in the NDJSON request body.
type bulkLoadLine struct {
	SourceIP string            `json:"sourceIP"`
	Path     string            `json:"path"`
	Headers  map[string]string `json:"headers"`
}

// bulkLoadResponse is the JSON body returned by POST /accounting/load.
type bulkLoadResponse struct {
	ID             string   `json:"id"`
	Total          int      `json:"total"`
	AcceptedLocal  int      `json:"accepted_local"`
	AcceptedRemote int      `json:"accepted_remote"`
	Dropped        int      `json:"dropped"`
	NoMatch        int      `json:"no_match"`
	Invalid        int      `json:"invalid"`
	Errors         []string `json:"errors"`
}

// accountingStatsResponse is the JSON body returned by GET /accounting/stats.
type accountingStatsResponse struct {
	LocalNodeID            string `json:"local_node_id"`
	MonitoredEntitiesCount int64  `json:"monitored_entities_count"`
	BatchedUpdatesPending  int64  `json:"batched_updates_pending"`
	EstimatedEntitiesCount int64  `json:"estimated_entities_count"`
}

// handleAccountingLoad handles POST /accounting/load
//
// Body: NDJSON, one bulkLoadLine per line. The handler streams the body
// line-by-line, invoking the engine's BulkLoad path for each record.
//
// Query parameters:
//
//	distributionEnabled=true|false (default false)
//	  When false, records whose owner is a remote node are dropped.
//	  When true, they are forwarded to the owning node via the flusher.
//
// Bulk-loaded entities are ingested directly into the accounting cache and
// do NOT pass through rate-limit/blocklist evaluation. This endpoint exists
// to support load-testing the cache and warming nodes; it must never trigger
// blocking behaviour.
func (s *Server) handleAccountingLoad(c *fiber.Ctx) error {
	distributionEnabled := false
	switch c.Query("distributionEnabled") {
	case "true", "1":
		distributionEnabled = true
	case "", "false", "0":
		distributionEnabled = false
	default:
		return c.Status(fiber.StatusBadRequest).JSON(bulkLoadResponse{
			ID:     generateOperationID(),
			Errors: []string{"invalid distributionEnabled (expected true|false)"},
		})
	}

	resp := bulkLoadResponse{ID: generateOperationID(), Errors: []string{}}
	droppedKeys := make([]string, 0, maxBulkLoadDroppedLogged)

	scanner := bufio.NewScanner(bytes.NewReader(c.Body()))
	scanner.Buffer(make([]byte, 0, 64*1024), bulkLoadScannerMaxLine)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}

		var rec bulkLoadLine
		if err := json.Unmarshal(raw, &rec); err != nil {
			resp.Total++
			resp.Invalid++
			s.metrics.IncAccountingBulkLoad(bulkLoadResultInvalid)
			if len(resp.Errors) < maxBulkLoadParseErrors {
				resp.Errors = append(resp.Errors,
					fmt.Sprintf("line %d: %v", lineNo, err))
			}
			continue
		}

		if rec.SourceIP == "" || rec.Path == "" {
			resp.Total++
			resp.Invalid++
			s.metrics.IncAccountingBulkLoad(bulkLoadResultInvalid)
			if len(resp.Errors) < maxBulkLoadParseErrors {
				resp.Errors = append(resp.Errors,
					fmt.Sprintf("line %d: sourceIP and path are required", lineNo))
			}
			continue
		}

		resp.Total++
		outcome := s.bulkLoader.BulkLoad(rec.SourceIP, rec.Path, rec.Headers, distributionEnabled)
		switch outcome {
		case bulkLoadResultAcceptedLocal:
			resp.AcceptedLocal++
		case bulkLoadResultAcceptedRemote:
			resp.AcceptedRemote++
		case bulkLoadResultDropped:
			resp.Dropped++
			if len(droppedKeys) < maxBulkLoadDroppedLogged {
				droppedKeys = append(droppedKeys,
					fmt.Sprintf("%s%s", rec.SourceIP, rec.Path))
			}
		case bulkLoadResultNoMatch:
			resp.NoMatch++
		}
	}

	if err := scanner.Err(); err != nil {
		resp.Errors = append(resp.Errors, fmt.Sprintf("read error: %v", err))
		s.logger.Warn("bulk load scanner error",
			"error", err,
			"total", resp.Total,
		)
	}

	if resp.Dropped > 0 {
		s.logger.Warn("bulk load dropped non-owned entities",
			"dropped", resp.Dropped,
			"distribution_enabled", distributionEnabled,
			"sample", droppedKeys,
		)
	}

	s.logger.Info("bulk load completed",
		"total", resp.Total,
		"accepted_local", resp.AcceptedLocal,
		"accepted_remote", resp.AcceptedRemote,
		"dropped", resp.Dropped,
		"no_match", resp.NoMatch,
		"invalid", resp.Invalid,
		"distribution_enabled", distributionEnabled,
	)

	return c.Status(fiber.StatusOK).JSON(resp)
}

// handleAccountingStats handles GET /accounting/stats
func (s *Server) handleAccountingStats(c *fiber.Ctx) error {
	resp := accountingStatsResponse{
		LocalNodeID: s.nodeID,
	}
	resp.MonitoredEntitiesCount = s.accountingStats.TrackedEntities()
	resp.BatchedUpdatesPending = s.accountingStats.PendingUpdates()
	resp.EstimatedEntitiesCount = s.accountingStats.EstimatedEntities()

	return c.JSON(resp)
}
