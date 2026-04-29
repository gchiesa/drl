package cache

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash/v2"
	"github.com/gchiesa/drl/internal/model"
	"github.com/maypok86/otter/v2"
	"github.com/samber/lo"
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

// Transferable is the type for entities which can be moved to new owners
type Transferable map[uint64]uint64

// AccountingCache is a partitioned in-memory cache for request counters
// using consistent hashing to determine ownership
type AccountingCache struct {
	cache        *otter.Cache[string, *atomic.Int64]
	ring         *consistent.Consistent
	localNode    string
	logger       *slog.Logger
	maxCost      int64
	mu           sync.RWMutex
	windowSize   time.Duration
	transferable map[string]Transferable
	tmu          sync.RWMutex

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

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	noop := func() {}
	noopCost := func(int64) {}
	onHit := cfg.OnHit
	if onHit == nil {
		onHit = noop
	}
	onMiss := cfg.OnMiss
	if onMiss == nil {
		onMiss = noop
	}
	onEvict := cfg.OnEvict
	if onEvict == nil {
		onEvict = noop
	}
	onSetCost := cfg.OnSetCost
	if onSetCost == nil {
		onSetCost = noopCost
	}

	ac := &AccountingCache{
		localNode:    cfg.LocalNode,
		logger:       logger,
		maxCost:      maxCost,
		windowSize:   windowSize,
		transferable: make(map[string]Transferable),
		onHit:        onHit,
		onMiss:       onMiss,
		onEvict:      onEvict,
		onSetCost:    onSetCost,
	}

	cache, err := otter.New[string, *atomic.Int64](&otter.Options[string, *atomic.Int64]{
		MaximumWeight: uint64(maxCost),
		Weigher: func(_ string, _ *atomic.Int64) uint32 {
			// Each entry: IP string (~40 bytes) + atomic.Int64 (8 bytes) + overhead
			return 100
		},
		ExpiryCalculator: otter.ExpiryCreating[string, *atomic.Int64](windowSize),
		OnDeletion: func(e otter.DeletionEvent[string, *atomic.Int64]) {
			if e.WasEvicted() {
				onEvict()
			}
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

// getOwnerLockedForKey determines and returns the owning node for the given key
// using consistent hashing. It requires to be protected by a lock
func (a *AccountingCache) getOwnerLockedForKey(key string) string {
	if a.ring.GetMembers() == nil || len(a.ring.GetMembers()) == 0 {
		return a.localNode
	}

	owner := a.ring.LocateKey([]byte(key))
	if owner == nil {
		return a.localNode
	}

	return owner.String()
}

// IsOwner checks if the local node owns the given key according to consistent hashing
func (a *AccountingCache) IsOwner(key string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.getOwnerLockedForKey(key) == a.localNode
}

// GetOwner returns the node that owns the given key
func (a *AccountingCache) GetOwner(key string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.getOwnerLockedForKey(key)
}

// Increment increments the counter for a key and returns the new count
// This should only be called for keys that this node owns
func (a *AccountingCache) Increment(key string, delta int64) int64 {
	counter, found := a.cache.GetIfPresent(key)
	if found {
		a.onHit()
		return counter.Add(delta)
	}

	a.onMiss()

	// Create new counter
	newCounter := &atomic.Int64{}
	newCounter.Store(delta)
	a.cache.Set(key, newCounter)

	return delta
}

// Get returns the current count for a key
func (a *AccountingCache) Get(key string) int64 {
	counter, found := a.cache.GetIfPresent(key)
	if !found {
		a.onMiss()
		return 0
	}
	a.onHit()
	return counter.Load()
}

// Reset resets the counter for an key
func (a *AccountingCache) Reset(key string) {
	a.cache.Invalidate(key)
}

// AddNode adds a node to the consistent hash ring
func (a *AccountingCache) AddNode(node string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ring.Add(Member(node))

	// get all the other members
	var otherNodes []string
	for _, n := range a.ring.GetMembers() {
		if n.String() == a.localNode {
			continue
		}
		otherNodes = append(otherNodes, n.String())
	}
	tMetrics := a.updateTransferableLocked(otherNodes)
	a.logger.Debug("updated transferable entities", "total_entities_to_transfer", fmt.Sprintf("%v", lo.MapValues(tMetrics, func(m int64, k string) string {
		return fmt.Sprintf("[%d]", m)
	})))
	a.logger.Debug("node added to hash ring",
		"node", node,
		"total_members", len(a.ring.GetMembers()),
	)
}

// RemoveNode removes a node from the consistent hash ring
func (a *AccountingCache) RemoveNode(node string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.ring.Remove(node)

	// get all the other members
	var otherNodes []string
	for _, n := range a.ring.GetMembers() {
		if n.String() == a.localNode {
			continue
		}
		otherNodes = append(otherNodes, n.String())
	}
	tMetrics := a.updateTransferableLocked(otherNodes)
	a.logger.Debug("updated transferable entities", "total_entities_to_transfer", fmt.Sprintf("%v", lo.MapValues(tMetrics, func(m int64, k string) string {
		return fmt.Sprintf("[%d]", m)
	})))
	a.logger.Debug("node removed from hash ring",
		"node", node,
		"total_members", len(a.ring.GetMembers()),
	)
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

	// get all the other members
	var otherNodes []string
	for _, n := range a.ring.GetMembers() {
		if n.String() == a.localNode {
			continue
		}
		otherNodes = append(otherNodes, n.String())
	}
	tMetrics := a.updateTransferableLocked(otherNodes)
	a.logger.Debug("updated transferable entities", "total_entities_to_transfer", fmt.Sprintf("%v", lo.MapValues(tMetrics, func(m int64, k string) string {
		return fmt.Sprintf("[%d]", m)
	})))
	a.logger.Debug("hash ring updated",
		"total_members", len(a.ring.GetMembers()),
	)
}

// updateTransferableLocked updates the transferable entities for the given
// members and returns their metrics. It requires to be called from a function
// with lock
func (a *AccountingCache) updateTransferableLocked(members []string) (tMetrics map[string]int64) {
	tMetrics = make(map[string]int64)
	for _, m := range members {
		a.transferable[m] = a.getTransferableLocked(m)
		tMetrics[m] = int64(len(a.transferable[m]))
	}
	return tMetrics
}

func (a *AccountingCache) ConsumeTransferable(ownerAddr string) (t Transferable, ok bool) {
	a.tmu.Lock()
	defer a.tmu.Unlock()
	t, ok = a.transferable[ownerAddr]
	delete(a.transferable, ownerAddr)
	return t, ok
}

// getTransferableLocked collects and invalidates cache entries owned by the
// given node and returns them as a map of hashes and counts. It requires to be
// called from a function with lock
func (a *AccountingCache) getTransferableLocked(ownerAddr string) map[uint64]uint64 {
	var err error

	toTransfer := make([]string, 0, a.cache.EstimatedSize())
	// collect the keys first
	for k := range a.cache.Keys() {
		if a.getOwnerLockedForKey(k) == ownerAddr {
			toTransfer = append(toTransfer, k)
		}
	}

	transfer := make(map[uint64]uint64, a.cache.EstimatedSize())
	for _, k := range toTransfer {
		var hash uint64
		if hash, err = model.EntityKeyToHash(k); err != nil {
			continue
		}
		if v, ok := a.cache.Invalidate(k); ok {
			transfer[hash] = uint64(v.Load())
		}
	}
	return transfer
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

// SnapshotAll returns a read-only snapshot of all current key→count entries.
// Keys are hex-encoded entity hashes, values are the current counter values.
func (a *AccountingCache) SnapshotAll() map[string]int64 {
	snapshot := make(map[string]int64, a.cache.EstimatedSize())
	for k := range a.cache.Keys() {
		if counter, found := a.cache.GetIfPresent(k); found {
			snapshot[k] = counter.Load()
		}
	}
	return snapshot
}

// Close closes the cache
func (a *AccountingCache) Close() {
	a.cache.StopAllGoroutines()
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

func (a *AccountingCache) GetEstimatedEntities() int64 {
	return int64(a.cache.EstimatedSize())
}
