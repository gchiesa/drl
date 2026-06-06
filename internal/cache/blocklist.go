package cache

import (
	"io"
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
	Key        string            `msgpack:"key"`                   // cache key (entity hash)
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

	bc := &BlocklistCache{
		logger:    logger,
		maxCost:   maxCost,
		onHit:     onHit,
		onMiss:    onMiss,
		onEvict:   onEvict,
		onSetCost: onSetCost,
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
			if e.WasEvicted() {
				onEvict()
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
		b.onMiss()
		return expiresAt, false
	}
	b.onHit()
	return data.expiresAt, true
}

// IsBlocked checks if a specified key exists in the blocklist without returning its expiration time.
func (b *BlocklistCache) IsBlocked(key string) bool {
	_, found := b.IsBlockedWithExpiration(key)
	return found
}

// BlockWithExpiresAt adds a key with a specific expiration time and an optional entity to the blocklist.
// Updates the entry if the existing expiration time is older than the provided one.
func (b *BlocklistCache) BlockWithExpiresAt(key string, expiresAt time.Time, entity *model.Entity) {
	blCacheEntry := &blocklistEntryData{expiresAt: expiresAt, entity: entity}
	localEntity, isNew := b.cache.SetIfAbsent(key, blCacheEntry)
	if !isNew && localEntity != nil {
		if localEntity.expiresAt.Before(expiresAt) {
			b.cache.Set(key, blCacheEntry)
			b.logger.Debug("entity blocked",
				"key", key,
				"expires_at", expiresAt,
			)
		} else {
			b.logger.Debug("entity already blocked, with fresher expiration time, skipping update",
				"key", key,
				"expires_at", expiresAt,
				"local_expires_at", localEntity.expiresAt,
			)
		}
	}
}

// Block adds a key to the blocklist with a TTL and optional entity metadata.
// The metadata is preserved so that ListEntries can reconstruct the original
// entity for the admin GET endpoint.
func (b *BlocklistCache) Block(key string, ttl time.Duration, entity *model.Entity) {
	expiresAt := time.Now().Add(ttl)
	b.cache.Set(key, &blocklistEntryData{expiresAt: expiresAt, entity: entity})

	b.logger.Debug("entity blocked",
		"key", key,
		"ttl", ttl,
		"expires_at", expiresAt,
	)
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
				Key:       key,
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

	b.logger.Debug("serializing blocklist state",
		"entry_count", len(entries),
	)

	data, err := msgpack.Marshal(entries)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// MergeState merges received state into the local blocklist.
//
// # Merging Strategy
//
// when merging the state, the strategy is the following:
//
//   - when a new entity has to be merged and the expiration associated with the incoming entity is newer than the cached
//     one, then the new expiration time is used to update the cached entity.
//
// In some cases indeed, the block event can be triggered locally on a node and after it receives the reconciliation event. In that case
// the longer expiration time wins.
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
			// `receivedEntity` is the received entity
			receivedEntity := &blocklistEntryData{expiresAt: entry.ExpiresAt}
			if entry.EntityIP != "" || entry.EntityPath != "" {
				receivedEntity.entity = &model.Entity{
					IP:      entry.EntityIP,
					Path:    entry.EntityPath,
					Headers: entry.EntityHdrs,
				}
			}
			if receivedEntity.entity == nil {
				b.logger.Warn("received entity without IP/Path/Headers, skipping merging", "entry", entry)
				continue
			}

			cacheKey := receivedEntity.entity.Key()

			// if entity does not exist then cache it
			var existing *blocklistEntryData
			var ok bool
			if existing, ok = b.cache.GetIfPresent(cacheKey); !ok {
				b.cache.Set(cacheKey, receivedEntity)
				merged++
				continue
			}
			// if existed, then validate if it's fresher than the cached one
			if existing.expiresAt.Before(receivedEntity.expiresAt) {
				b.cache.Set(cacheKey, receivedEntity)
				merged++
			}
		}
	}

	b.logger.Info("state sync complete",
		"received_entries", len(entries),
		"merged_entries", merged,
	)

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
				Key:       key,
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
