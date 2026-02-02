package cache

import (
	"log/slog"
	"sync"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"github.com/samber/lo"
	"github.com/vmihailenco/msgpack/v5"
)

// BlocklistEntry represents a blocked IP with its TTL
type BlocklistEntry struct {
	IP        string    `msgpack:"ip"`
	ExpiresAt time.Time `msgpack:"expires_at"`
}

// BlocklistCache is a fully replicated in-memory cache for banned IPs
type BlocklistCache struct {
	cache   *ristretto.Cache[string, time.Time]
	entries sync.Map // map[string]time.Time for tracking all entries
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

	cache, err := ristretto.NewCache(&ristretto.Config[string, time.Time]{
		NumCounters: 10 * maxCost / 100, // ~10x expected max items
		MaxCost:     maxCost,
		BufferItems: 64,
		OnEvict: func(item *ristretto.Item[time.Time]) {
			// Remove from tracking map
			bc.entries.Delete(item.Key)
			if cfg.OnEvict != nil {
				cfg.OnEvict()
			}
		},
		Cost: func(value time.Time) int64 {
			// Each entry is approximately: IP string (~40 bytes) + time.Time (24 bytes) + overhead
			return 100
		},
	})
	if err != nil {
		return nil, err
	}

	bc.cache = cache
	return bc, nil
}

// IsBlocked checks if an IP is in the blocklist
func (b *BlocklistCache) IsBlocked(ip string) bool {
	expiresAt, found := b.cache.Get(ip)
	if !found {
		if b.onMiss != nil {
			b.onMiss()
		}
		return false
	}

	// Check if expired
	if time.Now().After(expiresAt) {
		b.cache.Del(ip)
		b.entries.Delete(ip)
		if b.onMiss != nil {
			b.onMiss()
		}
		return false
	}

	if b.onHit != nil {
		b.onHit()
	}
	return true
}

// Block adds an IP to the blocklist with a TTL
func (b *BlocklistCache) Block(ip string, ttl time.Duration) {
	expiresAt := time.Now().Add(ttl)
	b.cache.SetWithTTL(ip, expiresAt, 100, ttl)
	b.entries.Store(ip, expiresAt)
	b.cache.Wait() // Ensure the value is set before returning

	if b.logger != nil {
		b.logger.Debug("IP blocked",
			"ip", ip,
			"ttl", ttl,
			"expires_at", expiresAt,
		)
	}
}

// Unblock removes an IP from the blocklist
func (b *BlocklistCache) Unblock(ip string) {
	b.cache.Del(ip)
	b.entries.Delete(ip)
	if b.logger != nil {
		b.logger.Debug("IP unblocked", "ip", ip)
	}
}

// GetState serializes the current blocklist state for sync
func (b *BlocklistCache) GetState() ([]byte, error) {
	now := time.Now()
	entries := make([]BlocklistEntry, 0)

	// Iterate through tracked entries
	b.entries.Range(func(key, value any) bool {
		ip := key.(string)
		expiresAt := value.(time.Time)

		// Only include non-expired entries
		if expiresAt.After(now) {
			entries = append(entries, BlocklistEntry{
				IP:        ip,
				ExpiresAt: expiresAt,
			})
		}
		return true
	})

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
		// Only add if not expired
		if entry.ExpiresAt.After(now) {
			ttl := entry.ExpiresAt.Sub(now)
			b.cache.SetWithTTL(entry.IP, entry.ExpiresAt, 100, ttl)
			b.entries.Store(entry.IP, entry.ExpiresAt)
			merged++
		}
	}

	b.cache.Wait()

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
	count := 0
	b.entries.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Entries returns all current blocklist entries (for testing/debugging)
func (b *BlocklistCache) Entries() []BlocklistEntry {
	now := time.Now()
	entries := make([]BlocklistEntry, 0)

	b.entries.Range(func(key, value any) bool {
		ip := key.(string)
		expiresAt := value.(time.Time)

		if expiresAt.After(now) {
			entries = append(entries, BlocklistEntry{
				IP:        ip,
				ExpiresAt: expiresAt,
			})
		}
		return true
	})

	return entries
}

// Clear removes all entries from the blocklist
func (b *BlocklistCache) Clear() {
	b.entries.Range(func(key, _ any) bool {
		ip := key.(string)
		b.cache.Del(ip)
		b.entries.Delete(ip)
		return true
	})
	b.cache.Wait()
}

// Close closes the cache
func (b *BlocklistCache) Close() {
	b.cache.Close()
}

// Metrics returns current cache metrics
func (b *BlocklistCache) Metrics() *ristretto.Metrics {
	return b.cache.Metrics
}

// CostEstimate returns the estimated memory usage in bytes
func (b *BlocklistCache) CostEstimate() int64 {
	return lo.Sum(lo.Map(b.Entries(), func(e BlocklistEntry, _ int) int64 {
		return 100 // Each entry costs ~100 bytes
	}))
}
