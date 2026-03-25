package accounting

import (
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

const (
	DefaultFlushInterval = 10 * time.Second
	DefaultMaxBatchSize  = 1000
	MaxUDPPacketSize     = 1400
	DefaultSyncPort      = 7947
)

// nodeBuffer accumulates counter increments destined for a single owner node.
type nodeBuffer struct {
	entries map[uint64]uint32 // entityHash -> accumulated hits
}

// Flusher batches accounting increments and sends them to owner nodes via UDP.
// It also listens for incoming batches from other nodes.
type Flusher struct {
	buffers    map[string]*nodeBuffer // ownerAddr -> buffer
	conn       net.PacketConn         // UDP socket for sending
	listener   net.PacketConn         // UDP listener for receiving
	senderID   uint64
	accounting *cache.AccountingCache
	logger     *slog.Logger
	metrics    *metrics.Metrics

	stopCh        chan struct{}
	wg            sync.WaitGroup
	mu            sync.Mutex
	flushInterval time.Duration
	maxBatchSize  int
	syncPort      int
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
	SyncPort      int
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
	syncPort := cfg.SyncPort
	if syncPort == 0 {
		syncPort = DefaultSyncPort
	}

	return &Flusher{
		buffers:       make(map[string]*nodeBuffer),
		senderID:      cfg.SenderID,
		accounting:    cfg.Accounting,
		logger:        cfg.Logger,
		metrics:       cfg.Metrics,
		stopCh:        make(chan struct{}),
		flushInterval: flushInterval,
		maxBatchSize:  maxBatchSize,
		syncPort:      syncPort,
		batchPool: sync.Pool{
			New: func() any {
				return &drlproto.CounterBatch{}
			},
		},
	}
}

// Start opens the UDP listener and launches background goroutines for
// periodic flushing and receiving.
func (f *Flusher) Start() error {
	var err error

	// Open UDP listener
	f.listener, err = net.ListenPacket("udp", fmt.Sprintf(":%d", f.syncPort))
	if err != nil {
		return fmt.Errorf("failed to listen on UDP port %d: %w", f.syncPort, err)
	}

	// Open UDP send socket
	f.conn, err = net.ListenPacket("udp", ":0")
	if err != nil {
		_ = f.listener.Close()
		return fmt.Errorf("failed to open UDP send socket: %w", err)
	}

	f.logger.Info("accounting flusher started",
		"sync_port", f.syncPort,
		"flush_interval", f.flushInterval,
		"max_batch_size", f.maxBatchSize,
	)

	// Launch flush ticker
	f.wg.Add(1)
	go f.flushLoop()

	// Launch receiver
	f.wg.Add(1)
	go f.receiveLoop()

	return nil
}

// Stop signals background goroutines to stop, closes sockets, and waits.
func (f *Flusher) Stop() {
	close(f.stopCh)

	if f.listener != nil {
		_ = f.listener.Close()
	}
	if f.conn != nil {
		_ = f.conn.Close()
	}

	f.wg.Wait()
	f.logger.Info("accounting flusher stopped")
}

// Enqueue adds a counter increment for the given entity to the buffer for the
// specified owner node. If the buffer exceeds maxBatchSize, an immediate flush
// is triggered for that node.
func (f *Flusher) Enqueue(ownerAddr string, entityHash uint64, hits uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// TODO: gchiesa -> implement recalculation of ownership
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

	buf, ok := f.buffers[ownerAddr]
	if !ok {
		buf = &nodeBuffer{entries: make(map[uint64]uint32)}
		f.buffers[ownerAddr] = buf
	}

	buf.entries[entityHash] += hits

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

// ListenerAddr returns the address of the UDP listener, useful in tests.
func (f *Flusher) ListenerAddr() net.Addr {
	if f.listener != nil {
		return f.listener.LocalAddr()
	}
	return nil
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

// flushNode serializes and sends a CounterBatch to the given owner address.
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

	data, err := proto.Marshal(batch)
	if err != nil {
		f.logger.Error("failed to marshal counter batch", "error", err, "addr", addr)
		f.batchPool.Put(batch)
		return
	}

	// Reset and return to pool
	batch.Entries = batch.Entries[:0]
	f.batchPool.Put(batch)

	if len(data) > MaxUDPPacketSize {
		f.logger.Warn("counter batch exceeds max UDP packet size, dropping",
			"size", len(data),
			"max", MaxUDPPacketSize,
			"addr", addr,
		)
		buf.entries = make(map[uint64]uint32)
		return
	}

	target := fmt.Sprintf("%s:%d", addr, f.syncPort)
	udpAddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		f.logger.Error("failed to resolve UDP address", "error", err, "target", target)
		buf.entries = make(map[uint64]uint32)
		return
	}

	if _, err := f.conn.WriteTo(data, udpAddr); err != nil {
		f.logger.Error("failed to send counter batch", "error", err, "addr", addr)
	} else {
		if f.metrics != nil {
			f.metrics.IncAccountingFlush()
		}
		f.logger.Debug("flushed counter batch",
			"addr", addr,
			"entries", len(buf.entries),
		)
	}

	buf.entries = make(map[uint64]uint32)
}

func (f *Flusher) receiveLoop() {
	defer f.wg.Done()

	readBuf := make([]byte, MaxUDPPacketSize)

	for {
		select {
		case <-f.stopCh:
			return
		default:
		}

		n, _, err := f.listener.ReadFrom(readBuf)
		if err != nil {
			select {
			case <-f.stopCh:
				return
			default:
				f.logger.Debug("UDP read error", "error", err)
				continue
			}
		}

		batch := &drlproto.CounterBatch{}
		if err := proto.Unmarshal(readBuf[:n], batch); err != nil {
			f.logger.Warn("failed to unmarshal counter batch", "error", err)
			continue
		}

		if f.metrics != nil {
			f.metrics.IncAccountingUDPRecv()
		}

		for _, entry := range batch.Entries {
			key := fmt.Sprintf("%016x", entry.EntityHash)
			for range entry.Hits {
				f.accounting.Increment(key) // TODO: (gchiesa) improve for cumulative increment when the batch.Entries has entity with Hits > 1
			}
		}

		f.logger.Debug("received counter batch",
			"sender_id", batch.SenderId,
			"entries", len(batch.Entries),
		)
	}
}
