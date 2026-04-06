package accounting

import (
	"log/slog"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

const (
	DefaultFlushInterval = 200 * time.Millisecond
	DefaultMaxBatchSize  = 1000
)

// NodeSender sends accounting messages to peer nodes via memberlist.
type NodeSender interface {
	SendAccountingMsg(addr string, data []byte) error
}

// nodeBuffer accumulates counter increments destined for a single owner node.
type nodeBuffer struct {
	entries map[uint64]uint64 // entityHash -> accumulated hits
}

// Flusher batches accounting increments and sends them to owner nodes via
// memberlist's SendBestEffort. It relies on the membership delegate's
// NotifyMsg to handle incoming batches from other nodes.
type Flusher struct {
	buffers    map[string]*nodeBuffer // ownerAddr -> buffer
	sender     NodeSender
	senderID   uint64
	accounting *cache.AccountingCache
	logger     *slog.Logger
	metrics    *metrics.Metrics

	stopCh        chan struct{}
	wg            sync.WaitGroup
	mu            sync.Mutex
	flushInterval time.Duration
	maxBatchSize  int
	batchPool     sync.Pool
}

// FlusherConfig holds configuration for creating a Flusher.
type FlusherConfig struct {
	SenderID      uint64
	Accounting    *cache.AccountingCache
	Logger        *slog.Logger
	Metrics       *metrics.Metrics
	FlushInterval time.Duration
	MaxBatchSize  int
	Sender        NodeSender
}

// NewFlusher creates a new Flusher. Call Start() to begin background operations.
func NewFlusher(cfg FlusherConfig) *Flusher {
	flushInterval := cfg.FlushInterval
	if flushInterval == 0 {
		flushInterval = DefaultFlushInterval
	}
	maxBatchSize := cfg.MaxBatchSize
	if maxBatchSize == 0 {
		maxBatchSize = DefaultMaxBatchSize
	}

	return &Flusher{
		buffers:       make(map[string]*nodeBuffer),
		senderID:      cfg.SenderID,
		accounting:    cfg.Accounting,
		logger:        cfg.Logger,
		metrics:       cfg.Metrics,
		sender:        cfg.Sender,
		stopCh:        make(chan struct{}),
		flushInterval: flushInterval,
		maxBatchSize:  maxBatchSize,
		batchPool: sync.Pool{
			New: func() any {
				return &drlproto.CounterBatch{}
			},
		},
	}
}

// Start launches the background flush goroutine.
func (f *Flusher) Start() error {
	f.logger.Info("accounting flusher started",
		"flush_interval", f.flushInterval,
		"max_batch_size", f.maxBatchSize,
	)

	// Launch flush ticker
	f.wg.Add(1)
	go f.flushLoop()

	return nil
}

// Stop signals the background goroutine to stop and waits for completion.
func (f *Flusher) Stop() {
	close(f.stopCh)
	f.wg.Wait()
	f.logger.Info("accounting flusher stopped")
}

// Enqueue adds a counter increment for the given entity to the buffer for the
// specified owner node. If the buffer exceeds maxBatchSize, an immediate flush
// is triggered for that node.
func (f *Flusher) Enqueue(ownerAddr string, entityHash uint64, hits uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	buf, ok := f.buffers[ownerAddr]
	if !ok {
		buf = &nodeBuffer{entries: make(map[uint64]uint64)}
		f.buffers[ownerAddr] = buf
	}

	buf.entries[entityHash] += hits

	// when a Enqueue for a ownerAddr is requested there is the possibility the
	// ownerAddr has been added later to the cluster when this instance already had
	// ownership of entities, which after the new node joined will be owner by the
	// new node because ring hash changed. in this case, there will be the state
	// where:
	//
	// * nodeA owned entityA before nodeB joined
	// * nodeA started keeping accounting for entityA
	// * nodeB joined, now owner of entityA
	//
	// entityA has now some accounting in nodeA and some in nodeB to solve this
	// scenario, we can implement that upon Enqueue trigger, we use buf below to host
	// the new entry, but at the same time we recalculate what is now owned by
	// ownerAddr and we enqueue also those entries in buf.entries and we assume they
	// will be successfully transmitted, so that they can be remove from local accounting.
	if f.accounting != nil {
		if entities, exist := f.accounting.ConsumeTransferable(ownerAddr); exist {
			for k, v := range entities {
				buf.entries[k] += v
			}
		}
	}

	if len(buf.entries) >= f.maxBatchSize {
		f.flushNode(ownerAddr, buf)
	}
}

// PendingCount returns the total number of buffered entries across all nodes.
func (f *Flusher) PendingCount() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	for _, buf := range f.buffers {
		count += int64(len(buf.entries))
	}
	return count
}

func (f *Flusher) flushLoop() {
	defer f.wg.Done()

	ticker := time.NewTicker(f.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopCh:
			// Final flush before shutdown
			f.flush()
			return
		case <-ticker.C:
			f.flush()
		}
	}
}

func (f *Flusher) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for addr, buf := range f.buffers {
		if len(buf.entries) == 0 {
			continue
		}
		f.flushNode(addr, buf)
	}
}

// flushNode serializes a CounterBatch inside a DrlMessage and sends it to the
// given owner address via memberlist SendBestEffort.
// Must be called with f.mu held.
func (f *Flusher) flushNode(addr string, buf *nodeBuffer) {
	batch := f.batchPool.Get().(*drlproto.CounterBatch)
	batch.SenderId = f.senderID
	batch.Timestamp = uint64(time.Now().UnixMilli())
	batch.Entries = batch.Entries[:0]

	for hash, hits := range buf.entries {
		batch.Entries = append(batch.Entries, &drlproto.CounterEntry{
			EntityHash: hash,
			Hits:       hits,
		})
	}

	// Wrap in DrlMessage envelope
	msg := &drlproto.DrlMessage{
		Content: &drlproto.DrlMessage_Counters{Counters: batch},
	}

	data, err := proto.Marshal(msg)
	if err != nil {
		f.logger.Error("failed to marshal DrlMessage", "error", err, "addr", addr)
		batch.Entries = batch.Entries[:0]
		f.batchPool.Put(batch)
		buf.entries = make(map[uint64]uint64)
		return
	}

	// Reset and return batch to pool
	batch.Entries = batch.Entries[:0]
	f.batchPool.Put(batch)

	if f.sender != nil {
		if err := f.sender.SendAccountingMsg(addr, data); err != nil {
			f.logger.Error("failed to send counter batch", "error", err, "addr", addr)
		} else {
			if f.metrics != nil {
				f.metrics.IncAccountingFlush()
				f.metrics.IncMembershipBestEffort()
			}
			f.logger.Debug("flushed counter batch",
				"addr", addr,
				"entries", len(buf.entries),
			)
		}
	}

	buf.entries = make(map[uint64]uint64)
}
