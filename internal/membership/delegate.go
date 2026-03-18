package membership

import (
	"bytes"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/memberlist"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
)

// StateDelegate implements memberlist.Delegate interface for state synchronization
// It handles TCP Push/Pull state sync for the blocklist cache and user-level
// broadcasts for manual block/unblock operations.
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

	// Broadcast queue for user-level cluster events
	broadcastQueue *memberlist.TransmitLimitedQueue
	// bufPool reuses bytes.Buffer allocations for broadcast serialisation
	bufPool sync.Pool
}

// DelegateConfig holds configuration for the state delegate
type DelegateConfig struct {
	Blocklist   *cache.BlocklistCache
	Metrics     *metrics.Metrics
	Logger      *slog.Logger
	SyncTimeout time.Duration
	// NumNodesFunc returns the current number of live cluster nodes.
	// Used by the TransmitLimitedQueue to calculate retransmit counts.
	// Defaults to a function that returns 1 when not provided.
	NumNodesFunc func() int
}

// NewStateDelegate creates a new state delegate
func NewStateDelegate(cfg DelegateConfig) *StateDelegate {
	timeout := cfg.SyncTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	numNodes := cfg.NumNodesFunc
	if numNodes == nil {
		numNodes = func() int { return 1 }
	}

	d := &StateDelegate{
		blocklist:    cfg.Blocklist,
		metrics:      cfg.Metrics,
		logger:       cfg.Logger,
		syncComplete: make(chan struct{}),
		syncTimeout:  timeout,
		broadcastQueue: &memberlist.TransmitLimitedQueue{
			NumNodes:       numNodes,
			RetransmitMult: 3,
		},
	}

	d.bufPool = sync.Pool{
		New: func() any { return new(bytes.Buffer) },
	}

	return d
}

// NodeMeta returns metadata to be sent to other nodes.
// This is optional and we don't use it currently.
func (d *StateDelegate) NodeMeta(limit int) []byte {
	return nil
}

// NotifyMsg is called when a user-level broadcast is received from a peer.
// It decodes the BroadcastEvent and applies the block or unblock operation
// to the local Ristretto blocklist cache.
func (d *StateDelegate) NotifyMsg(buf []byte) {
	if len(buf) == 0 || d.blocklist == nil {
		return
	}

	event, err := decodeBroadcastEvent(buf)
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("failed to decode broadcast event", "error", err)
		}
		return
	}

	switch event.Type {
	case BroadcastEventBlock:
		var entity *model.Entity
		if event.EntityIP != "" || event.EntityPath != "" {
			entity = &model.Entity{
				IP:      event.EntityIP,
				Path:    event.EntityPath,
				Headers: event.EntityHdrs,
			}
		}
		if entity != nil {
			d.blocklist.BlockWithMeta(event.Key, event.TTL, entity)
		} else {
			d.blocklist.Block(event.Key, nil, event.TTL)
		}
		if d.logger != nil {
			d.logger.Debug("applied remote block event",
				"key", event.Key,
				"ttl", event.TTL,
				"has_entity", entity != nil,
			)
		}
	case BroadcastEventUnblock:
		d.blocklist.Unblock(event.Key)
		if d.logger != nil {
			d.logger.Debug("applied remote unblock event", "key", event.Key)
		}
	default:
		if d.logger != nil {
			d.logger.Warn("unknown broadcast event type", "type", event.Type)
		}
	}
}

// GetBroadcasts is called by memberlist when it wants messages to broadcast.
// Returns pending block/unblock events from the transmit queue.
func (d *StateDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return d.broadcastQueue.GetBroadcasts(overhead, limit)
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

// QueueBlockEvent encodes a BroadcastEventBlock and pushes it onto the
// transmit queue so memberlist will gossip it to all cluster peers.
// When entity is non-nil, its metadata is included in the broadcast so
// that receiving nodes can store it alongside the block entry.
func (d *StateDelegate) QueueBlockEvent(key string, ttl time.Duration, entity *model.Entity) error {
	event := BroadcastEvent{Type: BroadcastEventBlock, Key: key, TTL: ttl}
	if entity != nil {
		event.EntityIP = entity.IP
		event.EntityPath = entity.Path
		event.EntityHdrs = entity.Headers
	}

	buf := d.bufPool.Get().(*bytes.Buffer)
	buf.Reset()

	if err := encodeBroadcastEventInto(event, buf); err != nil {
		d.bufPool.Put(buf)
		return err
	}

	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	d.bufPool.Put(buf)

	d.broadcastQueue.QueueBroadcast(&blocklistBroadcast{data: data})
	return nil
}

// QueueUnblockEvent encodes a BroadcastEventUnblock and pushes it onto the
// transmit queue so memberlist will gossip it to all cluster peers.
func (d *StateDelegate) QueueUnblockEvent(key string) error {
	event := BroadcastEvent{Type: BroadcastEventUnblock, Key: key}

	buf := d.bufPool.Get().(*bytes.Buffer)
	buf.Reset()

	if err := encodeBroadcastEventInto(event, buf); err != nil {
		d.bufPool.Put(buf)
		return err
	}

	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	d.bufPool.Put(buf)

	d.broadcastQueue.QueueBroadcast(&blocklistBroadcast{data: data})
	return nil
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

// encodeBroadcastEventInto encodes event into buf using msgpack.
// Reuses the pool-allocated buffer to avoid allocations on the hot path.
func encodeBroadcastEventInto(event BroadcastEvent, buf *bytes.Buffer) error {
	data, err := encodeBroadcastEvent(event)
	if err != nil {
		return err
	}
	buf.Write(data)
	return nil
}
