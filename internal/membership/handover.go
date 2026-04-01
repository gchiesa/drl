package membership

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	"github.com/cespare/xxhash/v2"
	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

const (
	DefaultHandoverTimeout = 10 * time.Second
	DefaultSettlingPeriod  = 2 * time.Second
)

// FlusherEnqueuer is the interface for enqueuing accounting updates to the flusher.
// This avoids importing the accounting package directly.
type FlusherEnqueuer interface {
	Enqueue(ownerAddr string, entityHash uint64, hits uint64)
}

// Handover manages the graceful state evacuation when a node is shutting down.
type Handover struct {
	cluster    *Cluster
	accounting *cache.AccountingCache
	flusher    FlusherEnqueuer
	metrics    *metrics.Metrics
	logger     *slog.Logger
	shutdownCh chan struct{}
	settling   time.Duration
	timeout    time.Duration

	encoderPool sync.Pool
	decoderPool sync.Pool
}

// HandoverConfig holds configuration for creating a Handover.
type HandoverConfig struct {
	Cluster    *Cluster
	Accounting *cache.AccountingCache
	Flusher    FlusherEnqueuer
	Metrics    *metrics.Metrics
	Logger     *slog.Logger
	Settling   time.Duration
	Timeout    time.Duration
}

// NewHandover creates a new Handover instance.
func NewHandover(cfg HandoverConfig) *Handover {
	settling := cfg.Settling
	if settling == 0 {
		settling = DefaultSettlingPeriod
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultHandoverTimeout
	}
	return &Handover{
		cluster:    cfg.Cluster,
		accounting: cfg.Accounting,
		flusher:    cfg.Flusher,
		metrics:    cfg.Metrics,
		logger:     cfg.Logger,
		shutdownCh: make(chan struct{}),
		settling:   settling,
		timeout:    timeout,
		encoderPool: sync.Pool{
			New: func() any {
				enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
				return enc
			},
		},
		decoderPool: sync.Pool{
			New: func() any {
				dec, _ := zstd.NewReader(nil)
				return dec
			},
		},
	}
}

// IsShuttingDown returns true if this node is in the process of shutting down.
func (h *Handover) IsShuttingDown() bool {
	select {
	case <-h.shutdownCh:
		return true
	default:
		return false
	}
}

// Evacuate snapshots the local accounting cache, compresses it, and sends it
// to an adopter node. Returns an error if the handover fails.
func (h *Handover) Evacuate() error {
	close(h.shutdownCh) // mark as shutting down

	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	start := time.Now()

	// Snapshot all local accounting entries
	snapshot := h.accounting.SnapshotAll()
	if len(snapshot) == 0 {
		h.logger.Info("handover: no accounting entries to evacuate")
		return nil
	}

	h.logger.Info("handover: starting state evacuation",
		"entities", len(snapshot),
	)

	// Build CounterBatch from snapshot
	batch := &drlproto.CounterBatch{
		SenderId:  xxhash.Sum64String(h.cluster.LocalAddr()),
		Timestamp: uint64(time.Now().UnixMilli()),
		Entries:   make([]*drlproto.CounterEntry, 0, len(snapshot)),
	}
	for key, count := range snapshot {
		entityHash, err := strconv.ParseUint(key, 16, 64)
		if err != nil {
			h.logger.Warn("handover: skipping entry with invalid key", "key", key, "error", err)
			continue
		}
		batch.Entries = append(batch.Entries, &drlproto.CounterEntry{
			EntityHash: entityHash,
			Hits:       uint64(count),
		})
	}

	// Marshal and compress
	batchData, err := proto.Marshal(batch)
	if err != nil {
		return fmt.Errorf("handover: failed to marshal CounterBatch: %w", err)
	}

	compressed := h.compressZstd(batchData)

	// Build HandoverPayload
	payload := &drlproto.HandoverPayload{
		SenderId:          batch.SenderId,
		Timestamp:         batch.Timestamp,
		CompressedEntries: compressed,
		EntityCount:       uint64(len(batch.Entries)),
	}

	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Handover{Handover: payload},
	}

	msgData, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("handover: failed to marshal DrlMessage: %w", err)
	}

	h.logger.Info("handover: payload prepared",
		"entities", len(batch.Entries),
		"uncompressed_bytes", len(batchData),
		"compressed_bytes", len(compressed),
	)

	// Select adopter(s) and try to send
	localAddr := h.cluster.LocalAddr()
	addrs := h.cluster.MemberAddrs()

	for _, addr := range addrs {
		if addr == localAddr {
			continue
		}

		select {
		case <-ctx.Done():
			if h.metrics != nil {
				h.metrics.IncHandoverFailed()
			}
			return fmt.Errorf("handover: timeout exceeded (%v)", h.timeout)
		default:
		}

		h.logger.Info("handover: sending to adopter", "addr", addr)
		if err := h.cluster.SendReliableMsg(addr, msgData); err != nil {
			h.logger.Warn("handover: adopter rejected, trying next",
				"addr", addr,
				"error", err,
			)
			continue
		}

		// Success
		duration := time.Since(start)
		if h.metrics != nil {
			h.metrics.AddHandoverOut(float64(len(batch.Entries)))
			h.metrics.ObserveHandoverDuration(float64(duration.Milliseconds()))
		}
		h.logger.Info("handover: state evacuation complete",
			"adopter", addr,
			"entities", len(batch.Entries),
			"duration", duration,
		)
		return nil
	}

	if h.metrics != nil {
		h.metrics.IncHandoverFailed()
	}
	return fmt.Errorf("handover: no healthy adopters available")
}

// HandleIncoming processes a HandoverPayload received from a leaving node.
// If this node is also shutting down, it returns immediately (rejection).
func (h *Handover) HandleIncoming(payload *drlproto.HandoverPayload) {
	if h.IsShuttingDown() {
		h.logger.Warn("handover: rejecting incoming handover (this node is also shutting down)")
		return
	}

	if payload == nil || len(payload.CompressedEntries) == 0 {
		return
	}

	h.logger.Info("handover: received state from leaving node",
		"sender_id", payload.SenderId,
		"entity_count", payload.EntityCount,
	)

	// Decompress
	decompressed, err := h.decompressZstd(payload.CompressedEntries)
	if err != nil {
		h.logger.Error("handover: failed to decompress payload", "error", err)
		return
	}

	// Unmarshal CounterBatch
	batch := &drlproto.CounterBatch{}
	if err := proto.Unmarshal(decompressed, batch); err != nil {
		h.logger.Error("handover: failed to unmarshal CounterBatch", "error", err)
		return
	}

	// Process in background with settling period
	go h.redistribute(batch)
}

// redistribute waits for the settling period (for ring convergence after the
// sender's leave), then re-distributes each entry to its new owner.
func (h *Handover) redistribute(batch *drlproto.CounterBatch) {
	h.logger.Info("handover: waiting for settling period",
		"settling", h.settling,
		"entries", len(batch.Entries),
	)
	time.Sleep(h.settling)

	var localCount, remoteCount int64
	for _, entry := range batch.Entries {
		key := fmt.Sprintf("%016x", entry.EntityHash)
		owner := h.accounting.GetOwner(key)

		if h.accounting.IsOwner(key) {
			// Merge locally — increment only, no threshold check
			h.accounting.Increment(key, int64(entry.Hits))
			localCount++
		} else if h.flusher != nil {
			// Enqueue for the new owner
			h.flusher.Enqueue(owner, entry.EntityHash, entry.Hits)
			remoteCount++
		}
	}

	if h.metrics != nil {
		h.metrics.AddHandoverIn(float64(localCount + remoteCount))
	}

	h.logger.Info("handover: redistribution complete",
		"total_entries", len(batch.Entries),
		"local_merged", localCount,
		"remote_enqueued", remoteCount,
	)
}

// compressZstd compresses data using zstd with a pooled encoder.
func (h *Handover) compressZstd(data []byte) []byte {
	enc := h.encoderPool.Get().(*zstd.Encoder)
	compressed := enc.EncodeAll(data, make([]byte, 0, len(data)/2))
	h.encoderPool.Put(enc)
	return compressed
}

// decompressZstd decompresses zstd data with a pooled decoder.
func (h *Handover) decompressZstd(data []byte) ([]byte, error) {
	dec := h.decoderPool.Get().(*zstd.Decoder)
	decompressed, err := dec.DecodeAll(data, nil)
	h.decoderPool.Put(dec)
	return decompressed, err
}
