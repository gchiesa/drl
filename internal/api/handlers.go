package api

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
)

// handleStatus returns the cluster status
func (s *Server) handleStatus(c *fiber.Ctx) error {
	uptime := time.Since(s.startTime)

	var activePeers []string
	var peerAPIAddresses []string
	activePeers = s.cluster.MemberNames()
	// Build peer API addresses: combine member IP addresses with this node's API port.
	// The current node's own address is excluded so the SPA does not proxy back to
	// itself and double-count metrics it already fetched directly.
	if s.apiPort != "" {
		ownIP := s.cluster.LocalAddr()
		for _, addr := range s.cluster.MemberAddrs() {
			if addr == ownIP {
				continue // skip self
			}
			peerAPIAddresses = append(peerAPIAddresses, fmt.Sprintf("%s:%s", addr, s.apiPort))
		}
	}

	return c.JSON(fiber.Map{
		"cluster_name":          s.clusterName,
		"node_id":               s.nodeID,
		"active_peers":          activePeers,
		"active_peer_addresses": peerAPIAddresses,
		"uptime":                uptime.String(),
		"uptime_seconds":        uptime.Seconds(),
	})
}
