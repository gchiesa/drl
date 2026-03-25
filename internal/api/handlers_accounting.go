package api

import (
	"github.com/gofiber/fiber/v2"
)

// accountingStatsResponse is the JSON body returned by GET /accounting/stats.
type accountingStatsResponse struct {
	LocalNodeID            string `json:"local_node_id"`
	MonitoredEntitiesCount int64  `json:"monitored_entities_count"`
	BatchedUpdatesPending  int64  `json:"batched_updates_pending"`
	EstimatedEntitiesCount int64  `json:"estimated_entities_count"`
}

// handleAccountingStats handles GET /accounting/stats
func (s *Server) handleAccountingStats(c *fiber.Ctx) error {
	resp := accountingStatsResponse{
		LocalNodeID: s.nodeID,
	}

	if s.accountingStats != nil {
		resp.MonitoredEntitiesCount = s.accountingStats.TrackedEntities()
		resp.BatchedUpdatesPending = s.accountingStats.PendingUpdates()
		resp.EstimatedEntitiesCount = s.accountingStats.EstimatedEntities()
	}

	return c.JSON(resp)
}
