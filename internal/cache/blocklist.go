package cache

import (
	"log/slog"
	"time"

	"github.com/maypok86/otter/v2"
	"github.com/samber/lo"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/gchiesa/drl/internal/model"
)

// BlocklistEntry is the wire format used for state sync (Push/Pull).
// New fields use `omitempty` so that nodes running older code can still
// deserialise the payload without errors.
type BlocklistEntry struct {
	IP         string            `msgpack:"ip"`                    // cache key (entity hash)
	ExpiresAt  time.Time         `msgpack:"expires_at"`            // absolute expiration
	EntityIP   string            `msgpack:"entity_ip,omitempty"`   // original IP
	EntityPath string            `msgpack:"entity_path,omitempty"` // original URI path
	EntityHdrs map[string]string `msgpack:"entity_hdrs,omitempty"` // original headers
}

// blocklistEntryData is the value stored in the otter cache.
type blocklistEntryData struct {
	expiresAt time.Time
	entity    *model.Entity // nil for automatic (rate-limiter) blocks
}

// BlocklistCache is a fully replicated in-memory cache for banned IPs.
// Otter's native iteration (cache.All()) replaces the previous sync.Map
// secondary index that was needed with Ristretto.
type BlocklistCache struct {
	cache   *otter.Cache[string, *blocklistEntryData]
	logger  *slog.Logger
	maxCost int64

	// Callbacks for metrics
	onHit     func()
	onMiss    func()
	onEvict   func()
	onSetCost func(int64)
}

// BlocklistConfig holds configuration for the blocklist cache
type BlocklistConfig struct {
	MaxSizeMB int64
	Logger    *slog.Logger
	OnHit     func()
	OnMiss    func()
	OnEvict   func()
	OnSetCost func(int64)
}

// NewBlocklistCache creates a new blocklist cache
func NewBlocklistCache(cfg BlocklistConfig) (*BlocklistCache, error) {
	maxCost := cfg.MaxSizeMB * 1024 * 1024 // Convert MB to bytes

	bc := &BlocklistCache{
		logger:    cfg.Logger,
		maxCost:   maxCost,
		onHit:     cfg.OnHit,
		onMiss:    cfg.OnMiss,
		onEvict:   cfg.OnEvict,
		onSetCost: cfg.OnSetCost,
	}

	cache, err := otter.New[string, *blocklistEntryData](&otter.Options[string, *blocklistEntryData]{
		MaximumWeight: uint64(maxCost),
		Weigher: func(_ string, _ *blocklistEntryData) uint32 {
			// Each entry is approximately: key string (~40 bytes) + time.Time (24 bytes) + overhead
			return 100
		},
		ExpiryCalculator: otter.ExpiryCreatingFunc[string, *blocklistEntryData](func(e otter.Entry[string, *blocklistEntryData]) time.Duration {
			ttl := time.Until(e.Value.expiresAt)
			if ttl <= 0 {
				return time.Millisecond
			}
			return ttl
		}),
		OnDeletion: func(e otter.DeletionEvent[string, *blocklistEntryData]) {
			if e.WasEvicted() && cfg.OnEvict != nil {
				cfg.OnEvict()
			}
		},
	})
	if err != nil {
		return nil, err
	}

	bc.cache = cache
	return bc, nil
}

// IsBlockedWithExpiration return the expiration time when a key is in the blocklist
func (b *BlocklistCache) IsBlockedWithExpiration(key string) (expiresAt time.Time, found bool) {
	data, found := b.cache.GetIfPresent(key)
	if !found {
		if b.onMiss != nil {
			b.onMiss()
		}
		return expiresAt, false
	}
	if b.onHit != nil {
		b.onHit()
	}
	return data.expiresAt, true
}

// IsBlocked checks if a specified key exists in the blocklist without returning its expiration time.
func (b *BlocklistCache) IsBlocked(key string) bool {
	_, found := b.IsBlockedWithExpiration(key)
	return found
}

// Block adds a key to the blocklist with a TTL and optional entity metadata.
// The metadata is preserved so that ListEntries can reconstruct the original
// entity for the admin GET endpoint.
func (b *BlocklistCache) Block(key string, ttl time.Duration, entity *model.Entity) {
	expiresAt := time.Now().Add(ttl)
	b.cache.Set(key, &blocklistEntryData{expiresAt: expiresAt, entity: entity})

	if b.logger != nil {
		b.logger.Debug("entity blocked",
			"key", key,
			"ttl", ttl,
			"expires_at", expiresAt,
		)
	}
}

// Unblock removes a key from the blocklist
func (b *BlocklistCache) Unblock(key string) {
	b.cache.Invalidate(key)
	if b.logger != nil {
		b.logger.Debug("entity unblocked", "key", key)
	}
}

// ListEntries returns all current blocklist entries with their metadata.
// Entries whose TTL has expired are filtered out.
func (b *BlocklistCache) ListEntries() []model.BlockedEntityInfo {
	now := time.Now()
	var result []model.BlockedEntityInfo

	for key, data := range b.cache.All() {
		if data.expiresAt.After(now) {
			result = append(result, model.BlockedEntityInfo{
				Key:       key,
				ExpiresAt: data.expiresAt,
				Entity:    data.entity,
			})
		}
	}

	return result
}

// GetState serializes the current blocklist state for sync
func (b *BlocklistCache) GetState() ([]byte, error) {
	now := time.Now()
	entries := make([]BlocklistEntry, 0)

	for key, data := range b.cache.All() {
		if data.expiresAt.After(now) {
			entry := BlocklistEntry{
				IP:        key,
				ExpiresAt: data.expiresAt,
			}
			if data.entity != nil {
				entry.EntityIP = data.entity.IP
				entry.EntityPath = data.entity.Path
				entry.EntityHdrs = data.entity.Headers
			}
			entries = append(entries, entry)
		}
	}

	if b.logger != nil {
		b.logger.Debug("serializing blocklist state",
			"entry_count", len(entries),
		)
	}

	data, err := msgpack.Marshal(entries)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// MergeState merges received state into the local blocklist
func (b *BlocklistCache) MergeState(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	var entries []BlocklistEntry
	if err := msgpack.Unmarshal(data, &entries); err != nil {
		return err
	}

	now := time.Now()
	merged := 0

	for _, entry := range entries {
		if entry.ExpiresAt.After(now) {
			ed := &blocklistEntryData{expiresAt: entry.ExpiresAt}
			if entry.EntityIP != "" || entry.EntityPath != "" {
				ed.entity = &model.Entity{
					IP:      entry.EntityIP,
					Path:    entry.EntityPath,
					Headers: entry.EntityHdrs,
				}
			}

			// Preserve local entity metadata when the remote entry lacks it.
			// This prevents Push/Pull sync from erasing metadata that was
			// set by the admin API on this node.
			if ed.entity == nil {
				if existing, ok := b.cache.GetIfPresent(entry.IP); ok {
					if existing.entity != nil {
						ed.entity = existing.entity
					}
				}
			}

			b.cache.Set(entry.IP, ed)
			merged++
		}
	}

	if b.logger != nil {
		b.logger.Info("state sync complete",
			"received_entries", len(entries),
			"merged_entries", merged,
		)
	}

	return nil
}

// Count returns the approximate number of entries in the blocklist
func (b *BlocklistCache) Count() int {
	return b.cache.EstimatedSize()
}

// Entries returns all current blocklist entries (for testing/debugging)
func (b *BlocklistCache) Entries() []BlocklistEntry {
	now := time.Now()
	entries := make([]BlocklistEntry, 0)

	for key, data := range b.cache.All() {
		if data.expiresAt.After(now) {
			entry := BlocklistEntry{
				IP:        key,
				ExpiresAt: data.expiresAt,
			}
			if data.entity != nil {
				entry.EntityIP = data.entity.IP
				entry.EntityPath = data.entity.Path
				entry.EntityHdrs = data.entity.Headers
			}
			entries = append(entries, entry)
		}
	}

	return entries
}

// Clear removes all entries from the blocklist
func (b *BlocklistCache) Clear() {
	b.cache.InvalidateAll()
}

// Close closes the cache
func (b *BlocklistCache) Close() {
	b.cache.StopAllGoroutines()
}

// CostEstimate returns the estimated memory usage in bytes
func (b *BlocklistCache) CostEstimate() int64 {
	return lo.Sum(lo.Map(b.Entries(), func(e BlocklistEntry, _ int) int64 {
		return 100 // Each entry costs ~100 bytes
	}))
}
