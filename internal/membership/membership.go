package membership

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// Cluster manages the memberlist cluster membership
type Cluster struct {
	config     *config.Config
	memberlist *memberlist.Memberlist
	metrics    *metrics.Metrics
	logger     *slog.Logger
	mu         sync.RWMutex
	ready      bool
}

// NewCluster creates a new Cluster instance
func NewCluster(cfg *config.Config, m *metrics.Metrics, logger *slog.Logger) *Cluster {
	return &Cluster{
		config:  cfg,
		metrics: m,
		logger:  logger,
	}
}

// Start initializes and starts the memberlist cluster
func (c *Cluster) Start() error {
	// Get the actual instance IP
	var advertiseIP string
	var err error
	if advertiseIP, err = getInstanceIP(); err != nil {
		return err
	}

	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = c.config.NodeName
	mlConfig.BindAddr = c.config.Membership.BindAddr
	mlConfig.BindPort = c.config.Membership.Port
	mlConfig.AdvertiseAddr = advertiseIP
	mlConfig.AdvertisePort = c.config.Membership.Port

	// Set up event delegate for membership events
	mlConfig.Events = &eventDelegate{
		cluster: c,
		logger:  c.logger,
	}

	// Reduce logging noise from the memberlist
	mlConfig.LogOutput = &slogWriter{logger: c.logger.With("component", "memberlist")}

	ml, err := memberlist.Create(mlConfig)
	if err != nil {
		return fmt.Errorf("failed to create memberlist: %w", err)
	}

	c.mu.Lock()
	c.memberlist = ml
	c.mu.Unlock()

	c.logger.Info("memberlist started",
		"local_node_name", c.config.NodeName,
		"bind_addr", c.config.Membership.BindAddr,
		"bind_port", c.config.Membership.Port,
	)

	// Update initial cluster size
	c.updateClusterSize()

	return nil
}

// JoinCluster attempts to join the cluster by discovering peers via DNS
func (c *Cluster) JoinCluster() error {
	// Wait for networking to stabilize
	c.logger.Info("waiting for network to stabilize",
		"delay", c.config.Membership.StartupDelay,
	)
	time.Sleep(c.config.Membership.StartupDelay)

	// Resolve peers via DNS
	peers, err := c.discoverPeers()
	if err != nil {
		c.logger.Warn("failed to discover peers via DNS",
			"error", err,
			"service_name", c.config.Membership.ServiceName,
		)
		// Not fatal - we might be the first node
		c.setReady(true)
		return nil
	}

	// Filter out our own IP
	localAddr := c.memberlist.LocalNode().Addr.String()
	var peersToJoin []string
	for _, peer := range peers {
		if peer != localAddr {
			peersToJoin = append(peersToJoin, peer)
		}
	}

	c.logger.Debug("discovered peers via DNS",
		"discovered_peers", peers,
		"peers_to_join", peersToJoin,
		"local_addr", localAddr,
	)

	if len(peersToJoin) == 0 {
		c.logger.Info("no peers to join, starting as first node")
		c.setReady(true)
		return nil
	}

	// Attempt to join
	numJoined, err := c.memberlist.Join(peersToJoin)
	if err != nil {
		c.logger.Warn("failed to join some peers",
			"error", err,
			"attempted", len(peersToJoin),
			"joined", numJoined,
		)
	}

	c.logger.Info("joined cluster",
		"peers_joined", numJoined,
		"cluster_size", c.memberlist.NumMembers(),
	)

	c.updateClusterSize()
	c.setReady(true)

	return nil
}

// discoverPeers resolves the discovery service name to get peer IPs
func (c *Cluster) discoverPeers() ([]string, error) {
	ips, err := net.LookupIP(c.config.Membership.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed: %w", err)
	}

	var peers []string
	for _, ip := range ips {
		// Only use IPv4 addresses
		if ip.To4() != nil {
			peers = append(peers, ip.String())
		}
	}

	return peers, nil
}

// updateClusterSize updates the metrics with current cluster size
func (c *Cluster) updateClusterSize() {
	c.mu.RLock()
	ml := c.memberlist
	c.mu.RUnlock()

	if ml == nil {
		// Memberlist not yet initialized (can happen during startup)
		return
	}

	size := ml.NumMembers()
	c.metrics.SetClusterSize(size)
	c.logger.Info("cluster size updated",
		"cluster_size", size,
	)
}

// setReady marks the cluster as ready
func (c *Cluster) setReady(ready bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = ready
}

// IsReady returns whether the cluster is ready to serve requests
func (c *Cluster) IsReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ready
}

// Members returns the list of current cluster members
func (c *Cluster) Members() []*memberlist.Node {
	return c.memberlist.Members()
}

// NumMembers returns the number of members in the cluster
func (c *Cluster) NumMembers() int {
	return c.memberlist.NumMembers()
}

// MemberNames returns the names of all cluster members
func (c *Cluster) MemberNames() []string {
	members := c.memberlist.Members()
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.Name
	}
	return names
}

// Leave gracefully leaves the cluster
func (c *Cluster) Leave(timeout time.Duration) error {
	if c.memberlist == nil {
		return nil
	}

	if c.memberlist.NumMembers() == 1 {
		if c.memberlist.Members()[0].Name == c.config.NodeName {
			c.logger.Info("only member, not leaving anything")
			return c.memberlist.Shutdown()
		}
	}
	c.logger.Info("leaving cluster")
	if err := c.memberlist.Leave(timeout); err != nil {
		return fmt.Errorf("failed to leave cluster: %w", err)
	}
	return c.memberlist.Shutdown()
}

func getInstanceIP() (string, error) {
	var instanceIP string
	// Get the actual instance IP
	hostname, _ := os.Hostname()
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return instanceIP, err
	}
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			instanceIP = ipv4.String()
			break
		}
	}
	return instanceIP, nil
}
