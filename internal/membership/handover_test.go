package membership

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

func testHandoverLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testAccountingCache(t *testing.T, localNode string) *cache.AccountingCache {
	t.Helper()
	ac, err := cache.NewAccountingCache(cache.AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  localNode,
		WindowSize: time.Minute,
	})
	require.NoError(t, err)
	t.Cleanup(ac.Close)
	return ac
}

// mockFlusher records enqueue calls.
type mockFlusher struct {
	mu       sync.Mutex
	enqueued []enqueueCall
}

type enqueueCall struct {
	ownerAddr  string
	entityHash uint64
	hits       uint64
}

func (f *mockFlusher) Enqueue(ownerAddr string, entityHash uint64, hits uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, enqueueCall{ownerAddr, entityHash, hits})
}

func TestHandover_ZstdRoundTrip(t *testing.T) {
	h, err := NewHandover(HandoverConfig{
		Cluster:    &Cluster{localIP: "local-node"},
		Accounting: testAccountingCache(t, "local-node"),
		Metrics:    metrics.NewMetrics(),
		Logger:     testHandoverLogger(),
	})
	require.NoError(t, err)

	original := []byte("test data for compression round-trip with zstd")
	compressed := h.compressZstd(original)
	decompressed, err := h.decompressZstd(compressed)
	require.NoError(t, err)
	assert.Equal(t, original, decompressed)
}

func TestHandover_HandleIncoming_MergesState(t *testing.T) {
	ac := testAccountingCache(t, "local-node")
	flusher := &mockFlusher{}

	h, err := NewHandover(HandoverConfig{
		Cluster:    &Cluster{localIP: "local-node"},
		Accounting: ac,
		Flusher:    flusher,
		Metrics:    metrics.NewMetrics(),
		Logger:     testHandoverLogger(),
		Settling:   10 * time.Millisecond, // short settling for tests
	})
	require.NoError(t, err)

	// Build a HandoverPayload with entries
	batch := &drlproto.CounterBatch{
		SenderId:  12345,
		Timestamp: uint64(time.Now().UnixMilli()),
		Entries: []*drlproto.CounterEntry{
			{EntityHash: 0xaabbccdd, Hits: 10},
			{EntityHash: 0x11223344, Hits: 5},
		},
	}
	batchData, err := proto.Marshal(batch)
	require.NoError(t, err)

	compressed := h.compressZstd(batchData)

	payload := &drlproto.HandoverPayload{
		SenderId:          12345,
		Timestamp:         uint64(time.Now().UnixMilli()),
		CompressedEntries: compressed,
		EntityCount:       2,
	}

	h.HandleIncoming(payload)

	// Wait for settling + redistribution
	time.Sleep(100 * time.Millisecond)

	// With single node (local-node), all entries should be merged locally
	key1 := fmt.Sprintf("%016x", uint64(0xaabbccdd))
	key2 := fmt.Sprintf("%016x", uint64(0x11223344))
	assert.Equal(t, int64(10), ac.Get(key1))
	assert.Equal(t, int64(5), ac.Get(key2))
}

func TestHandover_HandleIncoming_RejectsWhenShuttingDown(t *testing.T) {
	ac := testAccountingCache(t, "local-node")

	h, err := NewHandover(HandoverConfig{
		Cluster:    &Cluster{localIP: "local-node"},
		Accounting: ac,
		Metrics:    metrics.NewMetrics(),
		Logger:     testHandoverLogger(),
		Settling:   10 * time.Millisecond,
	})
	require.NoError(t, err)

	// Mark as shutting down
	close(h.shutdownCh)

	payload := &drlproto.HandoverPayload{
		SenderId:          12345,
		CompressedEntries: []byte("does not matter"),
		EntityCount:       1,
	}

	// Should not panic and should not process
	h.HandleIncoming(payload)

	time.Sleep(50 * time.Millisecond)
	// No entries should be in the cache
	snapshot := ac.SnapshotAll()
	assert.Empty(t, snapshot)
}

func TestHandover_HandleIncoming_RedistributesToCorrectOwners(t *testing.T) {
	ac := testAccountingCache(t, "local-node")

	// Add a remote node so some keys will be remote
	ac.AddNode("remote-node")

	flusher := &mockFlusher{}

	h, err := NewHandover(HandoverConfig{
		Cluster:    &Cluster{localIP: "local-node"},
		Accounting: ac,
		Flusher:    flusher,
		Metrics:    metrics.NewMetrics(),
		Logger:     testHandoverLogger(),
		Settling:   10 * time.Millisecond,
	})
	require.NoError(t, err)

	// Build entries — some will hash to local, some to remote
	entries := make([]*drlproto.CounterEntry, 20)
	for i := range 20 {
		entries[i] = &drlproto.CounterEntry{
			EntityHash: uint64(0x1000 + i),
			Hits:       uint64(i + 1),
		}
	}

	batch := &drlproto.CounterBatch{
		SenderId:  99999,
		Timestamp: uint64(time.Now().UnixMilli()),
		Entries:   entries,
	}
	batchData, err := proto.Marshal(batch)
	require.NoError(t, err)

	compressed := h.compressZstd(batchData)
	payload := &drlproto.HandoverPayload{
		SenderId:          99999,
		CompressedEntries: compressed,
		EntityCount:       uint64(len(entries)),
	}

	h.HandleIncoming(payload)
	time.Sleep(200 * time.Millisecond)

	// Verify some ended up local and some were enqueued to flusher
	snapshot := ac.SnapshotAll()
	flusher.mu.Lock()
	enqueued := len(flusher.enqueued)
	flusher.mu.Unlock()

	totalProcessed := len(snapshot) + enqueued
	assert.Equal(t, 20, totalProcessed,
		"all 20 entries should be processed (local=%d, remote=%d)", len(snapshot), enqueued)
}

func TestHandover_IsShuttingDown(t *testing.T) {
	h, err := NewHandover(HandoverConfig{
		Cluster:    &Cluster{localIP: "local-node"},
		Accounting: testAccountingCache(t, "local-node"),
		Metrics:    metrics.NewMetrics(),
		Logger:     testHandoverLogger(),
	})
	require.NoError(t, err)

	assert.False(t, h.IsShuttingDown())
	close(h.shutdownCh)
	assert.True(t, h.IsShuttingDown())
}

func TestHandover_Evacuate_NoEntries(t *testing.T) {
	ac := testAccountingCache(t, "local-node")

	h, err := NewHandover(HandoverConfig{
		Cluster:    &Cluster{localIP: "local-node"},
		Accounting: ac,
		Metrics:    metrics.NewMetrics(),
		Logger:     testHandoverLogger(),
	})
	require.NoError(t, err)

	// Empty cache — should return nil without error
	err = h.Evacuate()
	assert.NoError(t, err)
}
