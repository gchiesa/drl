package cache

import (
	"time"

	"github.com/gchiesa/drl/internal/model"
)

// BlocklistRepository defines the storage contract for the blocklist cache.
// Both the in-memory (Otter) and Redis backends implement this interface,
// allowing callers to switch providers without any code changes.
type BlocklistRepository interface {
	// IsBlocked reports whether key is currently in the blocklist.
	IsBlocked(key string) bool

	// IsBlockedWithExpiration returns the absolute expiration time and whether
	// the key exists in the blocklist.
	IsBlockedWithExpiration(key string) (expiresAt time.Time, found bool)

	// Block adds key to the blocklist for the specified TTL.
	Block(key string, ttl time.Duration, entity *model.Entity)

	// BlockWithExpiresAt adds key with a specific absolute expiration time.
	// If the key already exists with a fresher expiration, the update is skipped.
	BlockWithExpiresAt(key string, expiresAt time.Time, entity *model.Entity)

	// Unblock removes key from the blocklist.
	Unblock(key string)

	// ListEntries returns all non-expired blocklist entries with their metadata.
	ListEntries() []model.BlockedEntityInfo

	// GetState serializes the current blocklist for Push/Pull state sync.
	GetState() ([]byte, error)

	// MergeState merges received Push/Pull state into the local blocklist.
	MergeState(data []byte) error

	// Count returns the approximate number of entries in the blocklist.
	Count() int

	// Entries returns all non-expired entries (for testing and debugging).
	Entries() []BlocklistEntry

	// Clear removes all entries from the blocklist.
	Clear()

	// Close releases backend resources.
	Close()

	// CostEstimate returns the estimated memory usage in bytes.
	CostEstimate() int64
}

// AccountingRepository defines the storage contract for the accounting cache.
// Both the in-memory (Otter) and Redis backends implement this interface.
type AccountingRepository interface {
	// IsOwner reports whether the local node owns the given key according to
	// the consistent hash ring.
	IsOwner(key string) bool

	// GetOwner returns the node address that owns the given key.
	GetOwner(key string) string

	// Increment atomically increments the counter for key by delta and returns
	// the new value. Creates the entry if it does not exist.
	Increment(key string, delta int64) int64

	// Get returns the current counter value for key (0 if not found).
	Get(key string) int64

	// Reset removes the counter for key.
	Reset(key string)

	// AddNode registers a new node in the consistent hash ring.
	AddNode(node string)

	// RemoveNode removes a node from the consistent hash ring.
	RemoveNode(node string)

	// UpdateNodes reconciles the hash ring with the provided node list.
	UpdateNodes(nodes []string)

	// GetNodes returns the current set of nodes in the hash ring.
	GetNodes() []string

	// SnapshotAll returns a point-in-time snapshot of all key→count entries.
	SnapshotAll() map[string]int64

	// ConsumeTransferable returns and removes the set of entries that should
	// be transferred to ownerAddr after a ring topology change.
	// Returns (nil, false) when there is nothing to transfer (e.g. Redis backend).
	ConsumeTransferable(ownerAddr string) (t Transferable, ok bool)

	// LocalNode returns the local node identifier.
	LocalNode() string

	// SetLocalNode updates the local node identifier and adjusts the hash ring.
	SetLocalNode(node string)

	// GetEstimatedEntities returns the approximate number of tracked entries.
	GetEstimatedEntities() int64

	// Close releases backend resources.
	Close()
}
