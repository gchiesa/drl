package membership

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/hashicorp/memberlist"

	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
)

// Cluster manages the memberlist cluster membership
type Cluster struct {
	config        *config.Config
	localIP       string
	cacheManager  *cache.Manager
	memberlist    *memberlist.Memberlist
	metrics       *metrics.Metrics
	logger        *slog.Logger
	stateDelegate *StateDelegate
	mu            sync.RWMutex
	ready         bool
}

// NewCluster creates a new Cluster instance
func NewCluster(cfg *config.Config, localIP string, cacheManager *cache.Manager, m *metrics.Metrics, logger *slog.Logger) *Cluster {
	return &Cluster{
		config:       cfg,
		localIP:      localIP,
		cacheManager: cacheManager,
		metrics:      m,
		logger:       logger,
	}
}

// SetStateDelegate sets the state delegate for state synchronization
func (c *Cluster) SetStateDelegate(delegate *StateDelegate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stateDelegate = delegate
}

// GetStateDelegate returns the state delegate
func (c *Cluster) GetStateDelegate() *StateDelegate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stateDelegate
}

// Start initializes and starts the memberlist cluster
func (c *Cluster) Start() error {
	mlConfig := memberlist.DefaultLANConfig()
	mlConfig.Name = c.localIP
	mlConfig.BindAddr = c.config.Membership.BindAddr
	mlConfig.BindPort = c.config.Membership.Port
	mlConfig.AdvertiseAddr = c.localIP
	mlConfig.AdvertisePort = c.config.Membership.Port

	// Apply gossip tuning
	if c.config.Membership.GossipInterval > 0 {
		mlConfig.GossipInterval = c.config.Membership.GossipInterval
	}
	if c.config.Membership.GossipNodes > 0 {
		mlConfig.GossipNodes = c.config.Membership.GossipNodes
	}

	// Configure encryption keyring
	keyring, err := BuildKeyring(c.config.Membership.SecretKeys)
	if err != nil {
		return fmt.Errorf("failed to build encryption keyring: %w", err)
	}
	if keyring != nil {
		mlConfig.Keyring = keyring
		c.logger.Info("memberlist encryption enabled",
			"num_keys", len(c.config.Membership.SecretKeys),
		)
	} else {
		c.logger.Warn("memberlist encryption disabled: no secret keys configured. " +
			"If encryption was previously enabled this represents degraded security")
	}

	// Set up event delegate for membership events
	mlConfig.Events = &eventDelegate{
		cluster:      c,
		cacheManager: c.cacheManager,
		logger:       c.logger,
	}

	// Set up state delegate for Push/Pull state sync if available
	c.mu.RLock()
	if c.stateDelegate != nil {
		mlConfig.Delegate = c.stateDelegate
	}
	c.mu.RUnlock()

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
		"local_node_ip", c.localIP,
		"bind_addr", c.config.Membership.BindAddr,
		"bind_port", c.config.Membership.Port,
		"gossip_interval", c.config.Membership.GossipInterval,
		"gossip_nodes", c.config.Membership.GossipNodes,
		"encryption_enabled", keyring != nil,
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
		c.markReady()
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
		c.markReady()
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
	c.cacheManager.UpdateNodes(c.MemberAddrs())

	// Wait for state sync if delegate is configured
	c.mu.RLock()
	delegate := c.stateDelegate
	c.mu.RUnlock()

	if delegate != nil && numJoined > 0 {
		// Wait for state sync to complete
		delegate.WaitForSync()
	} else {
		// No delegate or no peers joined, mark as ready immediately
		c.markReady()
	}

	return nil
}

// markReady marks the cluster as ready, handling both delegate and non-delegate cases
func (c *Cluster) markReady() {
	c.mu.Lock()
	c.ready = true
	delegate := c.stateDelegate
	c.mu.Unlock()

	if delegate != nil {
		delegate.MarkReady()
	}
}

// privateIPv4Nets are the RFC 1918 ranges plus link-local. Pod IPs in any
// Kubernetes CNI will always fall inside one of these; public addresses
// (e.g. AWS Global Accelerator) never will.
var privateIPv4Nets = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
	} {
		_, n, _ := net.ParseCIDR(cidr)
		nets = append(nets, n)
	}
	return nets
}()

// discoverPeers resolves the discovery service name to get peer IPs.
//
// net.LookupIP is intentionally avoided: with CGO_ENABLED=0 the pure-Go
// resolver does not always apply /etc/resolv.conf search domains and ndots
// in the same order as the system libc resolver, which can cause the name to
// be resolved by an upstream public DNS server instead of the cluster DNS,
// returning unexpected public IPs.
//
// Using net.DefaultResolver.LookupIPAddr with a context ensures the pure-Go
// resolver path is taken with full resolv.conf semantics. Results are also
// filtered to RFC 1918 private ranges so a misconfigured DNS can never inject
// a public address into the peer list.
func (c *Cluster) discoverPeers() ([]string, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(context.Background(), c.config.Membership.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed: %w", err)
	}

	var peers []string
	for _, addr := range addrs {
		ip := addr.IP.To4()
		if ip == nil {
			continue // skip IPv6
		}
		private := false
		for _, n := range privateIPv4Nets {
			if n.Contains(ip) {
				private = true
				break
			}
		}
		if !private {
			c.logger.Warn("discoverPeers: ignoring non-private IP returned by DNS",
				slog.String("service", c.config.Membership.ServiceName),
				slog.String("ip", ip.String()))
			continue
		}
		peers = append(peers, ip.String())
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

// IsReady returns whether the cluster is ready to serve requests
func (c *Cluster) IsReady() bool {
	c.mu.RLock()
	delegate := c.stateDelegate
	ready := c.ready
	c.mu.RUnlock()

	// If we have a delegate, check its readiness
	if delegate != nil {
		return delegate.IsReady()
	}
	return ready
}

// Members returns the list of current cluster members
func (c *Cluster) Members() []*memberlist.Node {
	c.mu.RLock()
	ml := c.memberlist
	c.mu.RUnlock()
	if ml == nil {
		return nil
	}
	return ml.Members()
}

// NumMembers returns the number of members in the cluster
func (c *Cluster) NumMembers() int {
	c.mu.RLock()
	ml := c.memberlist
	c.mu.RUnlock()
	if ml == nil {
		return 0
	}
	return ml.NumMembers()
}

// MemberNames returns the names of all cluster members
func (c *Cluster) MemberNames() []string {
	members := c.Members()
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.Name
	}
	return names
}

// MemberAddrs returns the addresses of all cluster members
func (c *Cluster) MemberAddrs() []string {
	members := c.Members()
	addrs := make([]string, len(members))
	for i, m := range members {
		addrs[i] = m.Addr.String()
	}
	return addrs
}

// LocalAddr returns the local node's IP address.
func (c *Cluster) LocalAddr() string {
	return c.localIP
}

// SendAccountingMsg sends an accounting message to the given peer via
// memberlist's SendBestEffort (UDP, unreliable). The data must be a
// pre-marshalled DrlMessage.
func (c *Cluster) SendAccountingMsg(addr string, data []byte) error {
	c.mu.RLock()
	ml := c.memberlist
	c.mu.RUnlock()
	if ml == nil {
		return fmt.Errorf("memberlist not initialized")
	}
	node := c.findNodeByAddr(addr)
	if node == nil {
		return fmt.Errorf("node not found for addr: %s", addr)
	}
	return ml.SendBestEffort(node, data)
}

// SendReliableMsg sends a message to the given peer via memberlist's
// SendReliable (TCP, guaranteed delivery).
func (c *Cluster) SendReliableMsg(addr string, data []byte) error {
	c.mu.RLock()
	ml := c.memberlist
	c.mu.RUnlock()
	if ml == nil {
		return fmt.Errorf("memberlist not initialized")
	}
	node := c.findNodeByAddr(addr)
	if node == nil {
		return fmt.Errorf("node not found for addr: %s", addr)
	}
	return ml.SendReliable(node, data)
}

// findNodeByAddr returns the memberlist Node for the given address, or nil.
func (c *Cluster) findNodeByAddr(addr string) *memberlist.Node {
	for _, m := range c.memberlist.Members() {
		if m.Addr.String() == addr {
			return m
		}
	}
	return nil
}

// Leave gracefully leaves the cluster
func (c *Cluster) Leave(timeout time.Duration) error {
	if c.memberlist == nil {
		return nil
	}

	if c.memberlist.NumMembers() == 1 {
		if c.memberlist.Members()[0].Addr.String() == c.localIP {
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
