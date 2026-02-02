package api

import (
	"time"

	"github.com/gofiber/fiber/v2"
)

// handleStatus returns the cluster status
func (s *Server) handleStatus(c *fiber.Ctx) error {
	uptime := time.Since(s.startTime)

	var activePeers []string
	if s.cluster != nil {
		activePeers = s.cluster.MemberNames()
	}

	return c.JSON(fiber.Map{
		"cluster_name":   s.clusterName,
		"node_id":        s.nodeID,
		"active_peers":   activePeers,
		"uptime":         uptime.String(),
		"uptime_seconds": uptime.Seconds(),
	})
}
