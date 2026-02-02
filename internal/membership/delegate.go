package membership

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
)

// StateDelegate implements memberlist.Delegate interface for state synchronization
// It handles TCP Push/Pull state sync for the blocklist cache
type StateDelegate struct {
	blocklist *cache.BlocklistCache
	metrics   *metrics.Metrics
	logger    *slog.Logger

	// Readiness tracking
	ready        atomic.Bool
	syncComplete chan struct{}
	syncOnce     sync.Once
	syncTimeout  time.Duration

	// Sync start time for metrics
	syncStartTime time.Time
}

// DelegateConfig holds configuration for the state delegate
type DelegateConfig struct {
	Blocklist   *cache.BlocklistCache
	Metrics     *metrics.Metrics
	Logger      *slog.Logger
	SyncTimeout time.Duration
}

// NewStateDelegate creates a new state delegate
func NewStateDelegate(cfg DelegateConfig) *StateDelegate {
	timeout := cfg.SyncTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &StateDelegate{
		blocklist:    cfg.Blocklist,
		metrics:      cfg.Metrics,
		logger:       cfg.Logger,
		syncComplete: make(chan struct{}),
		syncTimeout:  timeout,
	}
}

// NodeMeta returns metadata to be sent to other nodes.
// This is optional and we don't use it currently.
func (d *StateDelegate) NodeMeta(limit int) []byte {
	return nil
}

// NotifyMsg is called when a user-level message is received.
// This is for user-level broadcasts, not for state sync.
func (d *StateDelegate) NotifyMsg(buf []byte) {
	// We don't use user-level broadcasts for now
}

// GetBroadcasts is called when memberlist wants broadcasts to send.
// We don't use broadcasts for state sync.
func (d *StateDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return nil
}

// LocalState returns the local state to be sent to peers during Push/Pull sync.
// This is the main state sync method - it serializes the blocklist for transfer.
func (d *StateDelegate) LocalState(join bool) []byte {
	if d.blocklist == nil {
		return nil
	}

	state, err := d.blocklist.GetState()
	if err != nil {
		if d.logger != nil {
			d.logger.Error("failed to get local state for sync",
				"error", err,
			)
		}
		return nil
	}

	if d.logger != nil {
		d.logger.Debug("providing local state for sync",
			"is_join", join,
			"state_size_bytes", len(state),
		)
	}

	return state
}

// MergeRemoteState is called when state is received from a peer during Push/Pull sync.
// This is called on the joining node to receive the current cluster state.
func (d *StateDelegate) MergeRemoteState(buf []byte, join bool) {
	if d.blocklist == nil {
		return
	}

	// Record sync start time if this is the first sync
	d.syncOnce.Do(func() {
		d.syncStartTime = time.Now()
	})

	if d.logger != nil {
		d.logger.Info("state sync started",
			"is_join", join,
			"state_size_bytes", len(buf),
		)
	}

	if err := d.blocklist.MergeState(buf); err != nil {
		if d.logger != nil {
			d.logger.Error("failed to merge remote state",
				"error", err,
			)
		}
		return
	}

	// Mark sync as complete
	d.markSyncComplete()

	// Record sync duration
	if d.metrics != nil {
		duration := time.Since(d.syncStartTime).Seconds()
		d.metrics.ObserveSyncDuration(duration)
	}

	if d.logger != nil {
		entries := d.blocklist.Count()
		d.logger.Info("state sync complete",
			"received_entries", entries,
		)
	}
}

// markSyncComplete marks the state sync as complete and signals readiness
func (d *StateDelegate) markSyncComplete() {
	d.ready.Store(true)
	select {
	case d.syncComplete <- struct{}{}:
	default:
		// Channel already has a value or is closed
	}
}

// WaitForSync blocks until the initial state sync is complete or timeout is reached.
// Returns true if sync completed, false if timeout occurred.
func (d *StateDelegate) WaitForSync() bool {
	// If we're already ready, return immediately
	if d.ready.Load() {
		return true
	}

	if d.logger != nil {
		d.logger.Info("waiting for state sync",
			"timeout", d.syncTimeout,
		)
	}

	select {
	case <-d.syncComplete:
		return true
	case <-time.After(d.syncTimeout):
		if d.logger != nil {
			d.logger.Warn("state sync timeout reached, proceeding without full sync",
				"timeout", d.syncTimeout,
			)
		}
		// Mark as ready anyway after timeout
		d.ready.Store(true)
		return false
	}
}

// IsReady returns true if the initial state sync is complete
func (d *StateDelegate) IsReady() bool {
	return d.ready.Load()
}

// MarkReady marks the delegate as ready (for cases where we don't need sync)
func (d *StateDelegate) MarkReady() {
	d.markSyncComplete()
}

// SetBlocklist sets the blocklist cache reference
func (d *StateDelegate) SetBlocklist(blocklist *cache.BlocklistCache) {
	d.blocklist = blocklist
}

// GetBlocklist returns the blocklist cache reference
func (d *StateDelegate) GetBlocklist() *cache.BlocklistCache {
	return d.blocklist
}
