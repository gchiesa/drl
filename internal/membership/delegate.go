package membership

import (
	"bytes"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

// StateDelegate implements memberlist.Delegate interface for state synchronization.
// It handles TCP Push/Pull state sync for the blocklist cache and processes
// incoming DrlMessage envelopes (accounting batches, block/unblock events).
type StateDelegate struct {
	blocklist  *cache.BlocklistCache
	accounting *cache.AccountingCache
	metrics    *metrics.Metrics
	logger     *slog.Logger
	cluster    *Cluster

	// Readiness tracking
	ready        atomic.Bool
	syncComplete chan struct{}
	syncOnce     sync.Once
	syncTimeout  time.Duration

	// Sync start time for metrics
	syncStartTime time.Time

	// bufPool reuses bytes.Buffer allocations for broadcast serialisation
	bufPool sync.Pool

	// handover handles graceful state evacuation on shutdown
	handover *Handover
}

// DelegateConfig holds configuration for the state delegate
type DelegateConfig struct {
	Blocklist   *cache.BlocklistCache
	Accounting  *cache.AccountingCache
	Metrics     *metrics.Metrics
	Logger      *slog.Logger
	SyncTimeout time.Duration
}

// NewStateDelegate creates a new state delegate
func NewStateDelegate(cfg DelegateConfig) *StateDelegate {
	timeout := cfg.SyncTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	d := &StateDelegate{
		blocklist:    cfg.Blocklist,
		accounting:   cfg.Accounting,
		metrics:      cfg.Metrics,
		logger:       cfg.Logger,
		syncComplete: make(chan struct{}),
		syncTimeout:  timeout,
	}

	d.bufPool = sync.Pool{
		New: func() any { return new(bytes.Buffer) },
	}

	return d
}

// SetCluster sets the cluster reference for SendReliable operations.
// Must be called after the cluster is created but before any block events.
func (d *StateDelegate) SetCluster(cluster *Cluster) {
	d.cluster = cluster
}

// NodeMeta returns metadata to be sent to other nodes.
// This is optional and we don't use it currently.
func (d *StateDelegate) NodeMeta(limit int) []byte {
	return nil
}

// NotifyMsg is called when a user-level message is received from a peer
// (via SendBestEffort or SendReliable). It unmarshals the DrlMessage
// envelope and dispatches to the appropriate handler.
func (d *StateDelegate) NotifyMsg(buf []byte) {
	if len(buf) == 0 {
		return
	}

	msg := &drlproto.DrlMessage{}
	if err := proto.Unmarshal(buf, msg); err != nil {
		if d.logger != nil {
			d.logger.Warn("failed to unmarshal DrlMessage", "error", err)
		}
		return
	}

	switch content := msg.Content.(type) {
	case *drlproto.DrlMessage_Counters:
		d.handleAccountingMsg(content.Counters)
	case *drlproto.DrlMessage_Block:
		d.handleBlockEvent(content.Block)
	case *drlproto.DrlMessage_Unblock:
		d.handleUnblockEvent(content.Unblock)
	case *drlproto.DrlMessage_Handover:
		d.handleHandoverPayload(content.Handover)
	default:
		if d.logger != nil {
			d.logger.Warn("received DrlMessage with unknown content type")
		}
	}
}

// handleAccountingMsg processes an incoming CounterBatch from a peer,
// applying increments to the local accounting cache.
func (d *StateDelegate) handleAccountingMsg(batch *drlproto.CounterBatch) {
	if d.accounting == nil || batch == nil {
		return
	}

	if d.metrics != nil {
		d.metrics.IncAccountingMsgRecv()
		d.metrics.IncMembershipBestEffort()
	}

	for _, entry := range batch.Entries {
		key := fmt.Sprintf("%016x", entry.EntityHash)
		d.accounting.Increment(key, int64(entry.Hits))
	}

	if d.logger != nil {
		d.logger.Debug("received counter batch",
			"sender_id", batch.SenderId,
			"entries", len(batch.Entries),
		)
	}
}

// handleBlockEvent processes an incoming block event from a peer.
func (d *StateDelegate) handleBlockEvent(evt *drlproto.BlockEvent) {
	if d.blocklist == nil || evt == nil {
		return
	}

	if d.metrics != nil {
		d.metrics.IncMembershipReliable()
	}

	ttl := time.Duration(evt.TtlNanos)
	var entity *model.Entity
	if evt.EntityIp != "" || evt.EntityPath != "" {
		entity = &model.Entity{
			IP:      evt.EntityIp,
			Path:    evt.EntityPath,
			Headers: evt.EntityHdrs,
		}
	}

	if entity != nil {
		d.blocklist.BlockWithMeta(evt.Key, ttl, entity)
	} else {
		d.blocklist.Block(evt.Key, nil, ttl)
	}

	if d.logger != nil {
		d.logger.Debug("applied remote block event",
			"key", evt.Key,
			"ttl", ttl,
			"has_entity", entity != nil,
		)
	}
}

// handleUnblockEvent processes an incoming unblock event from a peer.
func (d *StateDelegate) handleUnblockEvent(evt *drlproto.UnblockEvent) {
	if d.blocklist == nil || evt == nil {
		return
	}

	if d.metrics != nil {
		d.metrics.IncMembershipReliable()
	}

	d.blocklist.Unblock(evt.Key)
	if d.logger != nil {
		d.logger.Debug("applied remote unblock event", "key", evt.Key)
	}
}

// GetBroadcasts is called by memberlist when it wants messages to broadcast.
// We no longer use the gossip broadcast queue; all messaging is via
// SendBestEffort / SendReliable.
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

// QueueBlockEvent builds a DrlMessage with a BlockEvent and sends it
// reliably to all cluster peers via memberlist.SendReliable.
func (d *StateDelegate) QueueBlockEvent(key string, ttl time.Duration, entity *model.Entity) error {
	evt := &drlproto.BlockEvent{
		Key:      key,
		TtlNanos: int64(ttl),
	}
	if entity != nil {
		evt.EntityIp = entity.IP
		evt.EntityPath = entity.Path
		evt.EntityHdrs = entity.Headers
	}

	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Block{Block: evt},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal block DrlMessage: %w", err)
	}

	return d.sendToAllPeers(data)
}

// QueueUnblockEvent builds a DrlMessage with an UnblockEvent and sends it
// reliably to all cluster peers via memberlist.SendReliable.
func (d *StateDelegate) QueueUnblockEvent(key string) error {
	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Unblock{Unblock: &drlproto.UnblockEvent{Key: key}},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal unblock DrlMessage: %w", err)
	}

	return d.sendToAllPeers(data)
}

// sendToAllPeers sends data to all cluster peers via SendReliable, skipping self.
func (d *StateDelegate) sendToAllPeers(data []byte) error {
	if d.cluster == nil {
		return fmt.Errorf("cluster not set on delegate")
	}
	var lastErr error
	localAddr := d.cluster.LocalAddr()
	for _, addr := range d.cluster.MemberAddrs() {
		if addr == localAddr {
			continue
		}
		if err := d.cluster.SendReliableMsg(addr, data); err != nil {
			if d.logger != nil {
				d.logger.Warn("failed to send reliable msg to peer",
					"addr", addr,
					"error", err,
				)
			}
			lastErr = err
		}
	}
	return lastErr
}

// SetHandover sets the handover handler for graceful state evacuation.
func (d *StateDelegate) SetHandover(h *Handover) {
	d.handover = h
}

// handleHandoverPayload delegates incoming handover payloads to the Handover handler.
func (d *StateDelegate) handleHandoverPayload(payload *drlproto.HandoverPayload) {
	if d.handover == nil {
		if d.logger != nil {
			d.logger.Warn("received handover payload but no handover handler configured")
		}
		return
	}
	d.handover.HandleIncoming(payload)
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
