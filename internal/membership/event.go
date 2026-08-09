package membership

import (
	"log/slog"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/hashicorp/memberlist"
)

// eventDelegate handles memberlist events
type eventDelegate struct {
	cluster      *Cluster
	cacheManager *cache.Manager
	logger       *slog.Logger
}

func (e *eventDelegate) NotifyJoin(node *memberlist.Node) {
	e.logger.Info("node joined",
		"node_name", node.Name,
		"node_addr", node.Addr.String(),
	)
	e.cluster.metrics.IncEvent("join")
	// Run updateClusterSize asynchronously to avoid blocking memberlist's internal operations.
	// Calling memberlist methods from within event callbacks can cause deadlock/contention.
	go func() {
		e.cluster.updateClusterSize()
		e.cluster.cacheManager.UpdateNodes(e.cluster.MemberAddrs())
	}()

	// Establish the persistent gRPC channel to the newly-known peer, if
	// enabled. EstablishForPeer decides deterministically whether the local
	// node dials or waits to be dialed, so this is safe to call from both
	// the joiner's and the existing members' perspective.
	if cm := e.cluster.GetChannelManager(); cm != nil {
		cm.EstablishForPeer(node.Addr.String())
	}
}

func (e *eventDelegate) NotifyLeave(node *memberlist.Node) {
	e.logger.Info("node left",
		"node_name", node.Name,
		"node_addr", node.Addr.String(),
	)
	e.cluster.metrics.IncEvent("leave")
	// Run updateClusterSize asynchronously to avoid blocking memberlist's internal operations.
	go func() {
		e.cluster.updateClusterSize()
		e.cluster.cacheManager.UpdateNodes(e.cluster.MemberAddrs())
	}()

	// Close the persistent gRPC channel to the departed peer, if any.
	if cm := e.cluster.GetChannelManager(); cm != nil {
		cm.Close(node.Addr.String())
	}
}

func (e *eventDelegate) NotifyUpdate(node *memberlist.Node) {
	e.logger.Debug("node updated",
		"node_name", node.Name,
		"node_addr", node.Addr.String(),
	)
}
