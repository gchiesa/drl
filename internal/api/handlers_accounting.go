package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"

	"github.com/gchiesa/drl/internal/api/models"
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

// handleAccountingLoad handles POST /v1/accounting/load.
//
// Body: NDJSON, one record per line. The handler streams the body
// line-by-line, invoking the engine's BulkLoad path for each record.
//
// Bulk-loaded entities are ingested directly into the accounting cache and
// do NOT pass through rate-limit/blocklist evaluation. This endpoint exists
// to support load-testing the cache and warming nodes; it must never trigger
// blocking behaviour.
//
// @Summary      Bulk-load accounting entries
// @Description  Ingests a stream of NDJSON records into the accounting cache without rate-limit evaluation.
// @Description  Useful for warming cache state or load-testing. Does not trigger blocking.
// @Tags         accounting
// @Accept       application/x-ndjson
// @Produce      json
// @Param        distributionEnabled  query    boolean                 false  "Forward records owned by remote nodes via the flusher (default: false)"
// @Param        body                 body     models.BulkLoadLine     true   "NDJSON stream — one record per line"
// @Success      200                  {object} models.BulkLoadResponse
// @Failure      400                  {object} models.BulkLoadResponse
// @Failure      401                  {object} models.ErrorResponse
// @Security     DigestAuth
// @Security     BearerToken
// @Router       /accounting/load [post]
func (s *Server) handleAccountingLoad(c *fiber.Ctx) error {
	distributionEnabled := false
	switch c.Query("distributionEnabled") {
	case "true", "1":
		distributionEnabled = true
	case "", "false", "0":
		distributionEnabled = false
	default:
		return c.Status(fiber.StatusBadRequest).JSON(models.BulkLoadResponse{
			ID:     generateOperationID(),
			Errors: []string{"invalid distributionEnabled (expected true|false)"},
		})
	}

	resp := models.BulkLoadResponse{ID: generateOperationID(), Errors: []string{}}
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

		var rec models.BulkLoadLine
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

// handleAccountingStats handles GET /v1/accounting/stats.
//
// @Summary      Get accounting statistics
// @Description  Returns local accounting statistics for this node: tracked entities, pending updates, and cache estimates.
// @Tags         accounting
// @Produce      json
// @Success      200  {object}  models.AccountingStatsResponse
// @Failure      401  {object}  models.ErrorResponse
// @Security     DigestAuth
// @Security     BearerToken
// @Router       /accounting/stats [get]
func (s *Server) handleAccountingStats(c *fiber.Ctx) error {
	resp := models.AccountingStatsResponse{
		LocalNodeID:            s.nodeID,
		MonitoredEntitiesCount: s.accountingStats.TrackedEntities(),
		BatchedUpdatesPending:  s.accountingStats.PendingUpdates(),
		EstimatedEntitiesCount: s.accountingStats.EstimatedEntities(),
	}

	return c.JSON(resp)
}
