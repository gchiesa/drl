package cache

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash/v2"
	"github.com/dgraph-io/ristretto/v2"
)

// AccountingEntry represents a request counter for an IP
type AccountingEntry struct {
	Count     int64
	UpdatedAt time.Time
}

// Member implements consistent.Member interface
type Member string

func (m Member) String() string {
	return string(m)
}

// hasher implements consistent.Hasher interface using xxhash
type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// AccountingCache is a partitioned in-memory cache for request counters
// using consistent hashing to determine ownership
type AccountingCache struct {
	cache      *ristretto.Cache[string, *atomic.Int64]
	ring       *consistent.Consistent
	localNode  string
	logger     *slog.Logger
	maxCost    int64
	mu         sync.RWMutex
	windowSize time.Duration

	// Callbacks for metrics
	onHit     func()
	onMiss    func()
	onEvict   func()
	onSetCost func(int64)
}

// AccountingConfig holds configuration for the accounting cache
type AccountingConfig struct {
	MaxSizeMB  int64
	LocalNode  string
	WindowSize time.Duration // Time window for rate limiting
	Logger     *slog.Logger
	OnHit      func()
	OnMiss     func()
	OnEvict    func()
	OnSetCost  func(int64)
}

// NewAccountingCache creates a new accounting cache
func NewAccountingCache(cfg AccountingConfig) (*AccountingCache, error) {
	maxCost := cfg.MaxSizeMB * 1024 * 1024 // Convert MB to bytes

	// Set default window size if not specified
	windowSize := cfg.WindowSize
	if windowSize == 0 {
		windowSize = time.Minute
	}

	ac := &AccountingCache{
		localNode:  cfg.LocalNode,
		logger:     cfg.Logger,
		maxCost:    maxCost,
		windowSize: windowSize,
		onHit:      cfg.OnHit,
		onMiss:     cfg.OnMiss,
		onEvict:    cfg.OnEvict,
		onSetCost:  cfg.OnSetCost,
	}

	cache, err := ristretto.NewCache(&ristretto.Config[string, *atomic.Int64]{
		NumCounters: 10 * maxCost / 100,
		MaxCost:     maxCost,
		BufferItems: 64,
		OnEvict: func(item *ristretto.Item[*atomic.Int64]) {
			if cfg.OnEvict != nil {
				cfg.OnEvict()
			}
		},
		Cost: func(value *atomic.Int64) int64 {
			// Each entry: IP string (~40 bytes) + atomic.Int64 (8 bytes) + overhead
			return 100
		},
	})
	if err != nil {
		return nil, err
	}

	ac.cache = cache

	// Initialize consistent hash ring with default config
	ringCfg := consistent.Config{
		PartitionCount:    271,
		ReplicationFactor: 20,
		Load:              1.25,
		Hasher:            hasher{},
	}
	ac.ring = consistent.New(nil, ringCfg)

	// Add local node to the ring
	if cfg.LocalNode != "" {
		ac.ring.Add(Member(cfg.LocalNode))
	}

	return ac, nil
}

// IsOwner checks if the local node owns the given IP according to consistent hashing
func (a *AccountingCache) IsOwner(ip string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.ring.GetMembers() == nil || len(a.ring.GetMembers()) == 0 {
		return true // If no members, assume ownership
	}

	owner := a.ring.LocateKey([]byte(ip))
	if owner == nil {
		return true // If no owner found, assume ownership
	}

	return owner.String() == a.localNode
}

// GetOwner returns the node that owns the given IP
func (a *AccountingCache) GetOwner(ip string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.ring.GetMembers() == nil || len(a.ring.GetMembers()) == 0 {
		return a.localNode
	}

	owner := a.ring.LocateKey([]byte(ip))
	if owner == nil {
		return a.localNode
	}

	return owner.String()
}

// Increment increments the counter for an IP and returns the new count
// This should only be called for IPs that this node owns
func (a *AccountingCache) Increment(ip string) int64 {
	counter, found := a.cache.Get(ip)
	if found {
		if a.onHit != nil {
			a.onHit()
		}
		return counter.Add(1)
	}

	if a.onMiss != nil {
		a.onMiss()
	}

	// Create new counter
	newCounter := &atomic.Int64{}
	newCounter.Store(1)
	a.cache.SetWithTTL(ip, newCounter, 100, a.windowSize)
	a.cache.Wait()

	return 1
}

// Get returns the current count for an IP
func (a *AccountingCache) Get(ip string) int64 {
	counter, found := a.cache.Get(ip)
	if !found {
		if a.onMiss != nil {
			a.onMiss()
		}
		return 0
	}

	if a.onHit != nil {
		a.onHit()
	}
	return counter.Load()
}

// Reset resets the counter for an IP
func (a *AccountingCache) Reset(ip string) {
	a.cache.Del(ip)
}

// AddNode adds a node to the consistent hash ring
func (a *AccountingCache) AddNode(node string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ring.Add(Member(node))

	if a.logger != nil {
		a.logger.Debug("node added to hash ring",
			"node", node,
			"total_members", len(a.ring.GetMembers()),
		)
	}
}

// RemoveNode removes a node from the consistent hash ring
func (a *AccountingCache) RemoveNode(node string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ring.Remove(node)

	if a.logger != nil {
		a.logger.Debug("node removed from hash ring",
			"node", node,
			"total_members", len(a.ring.GetMembers()),
		)
	}
}

// UpdateNodes updates the hash ring with the given list of nodes
func (a *AccountingCache) UpdateNodes(nodes []string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Get current members
	currentMembers := make(map[string]bool)
	for _, m := range a.ring.GetMembers() {
		currentMembers[m.String()] = true
	}

	// Build new member set
	newMembers := make(map[string]bool)
	for _, n := range nodes {
		newMembers[n] = true
	}

	// Remove nodes that are no longer present
	for m := range currentMembers {
		if !newMembers[m] {
			a.ring.Remove(m)
		}
	}

	// Add new nodes
	for n := range newMembers {
		if !currentMembers[n] {
			a.ring.Add(Member(n))
		}
	}

	if a.logger != nil {
		a.logger.Debug("hash ring updated",
			"total_members", len(a.ring.GetMembers()),
		)
	}
}

// GetNodes returns the current list of nodes in the hash ring
func (a *AccountingCache) GetNodes() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	members := a.ring.GetMembers()
	nodes := make([]string, len(members))
	for i, m := range members {
		nodes[i] = m.String()
	}
	return nodes
}

// Close closes the cache
func (a *AccountingCache) Close() {
	a.cache.Close()
}

// Metrics returns current cache metrics
func (a *AccountingCache) Metrics() *ristretto.Metrics {
	return a.cache.Metrics
}

// LocalNode returns the local node identifier
func (a *AccountingCache) LocalNode() string {
	return a.localNode
}

// SetLocalNode sets the local node identifier
func (a *AccountingCache) SetLocalNode(node string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Remove old local node if different
	if a.localNode != "" && a.localNode != node {
		a.ring.Remove(a.localNode)
	}

	a.localNode = node

	// Add new local node
	if node != "" {
		a.ring.Add(Member(node))
	}
}
