package accounting

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/metrics"
	drlproto "github.com/gchiesa/drl/internal/proto"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testAccountingCache(t *testing.T) *cache.AccountingCache {
	t.Helper()
	ac, err := cache.NewAccountingCache(cache.AccountingConfig{
		MaxSizeMB:  1,
		LocalNode:  "test-node",
		WindowSize: time.Minute,
		Logger:     testLogger(),
	})
	require.NoError(t, err)
	return ac
}

func testFlusherConfig(t *testing.T, ac *cache.AccountingCache, port int) FlusherConfig {
	t.Helper()
	return FlusherConfig{
		SenderID:      12345,
		Accounting:    ac,
		Logger:        testLogger(),
		Metrics:       metrics.NewMetrics(),
		FlushInterval: 100 * time.Millisecond,
		MaxBatchSize:  10,
		SyncPort:      port,
	}
}

func TestFlusher_Enqueue(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	f := NewFlusher(testFlusherConfig(t, ac, 0))

	f.Enqueue("10.0.0.1", 0xdeadbeef, 1)
	f.Enqueue("10.0.0.1", 0xcafebabe, 3)
	f.Enqueue("10.0.0.2", 0x12345678, 2)

	assert.Equal(t, int64(3), f.PendingCount())
}

func TestFlusher_Flush(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	// We need a flusher that can actually send, so start it
	cfg := testFlusherConfig(t, ac, 0)
	f := NewFlusher(cfg)
	require.NoError(t, f.Start())
	defer f.Stop()

	f.Enqueue("127.0.0.1", 0xdeadbeef, 1)
	f.Enqueue("127.0.0.1", 0xcafebabe, 2)
	assert.Equal(t, int64(2), f.PendingCount())

	// Trigger manual flush
	f.flush()

	assert.Equal(t, int64(0), f.PendingCount())
}

func TestFlusher_ReceiveAndApply(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	cfg := testFlusherConfig(t, ac, 0)
	f := NewFlusher(cfg)
	require.NoError(t, f.Start())
	defer f.Stop()

	// Get the actual listen address
	listenAddr := f.ListenerAddr().(*net.UDPAddr)

	// Send a batch directly to the flusher's listener
	batch := &drlproto.CounterBatch{
		SenderId:  99999,
		Timestamp: uint64(time.Now().UnixMilli()),
		Entries: []*drlproto.CounterEntry{
			{EntityHash: 0xaabbccdd, Hits: 5},
		},
	}

	data, err := proto.Marshal(batch)
	require.NoError(t, err)

	conn, err := net.DialUDP("udp", nil, listenAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.Write(data)
	require.NoError(t, err)

	// Wait for the receive loop to process it
	time.Sleep(100 * time.Millisecond)

	key := fmt.Sprintf("%016x", uint64(0xaabbccdd))
	count := ac.Get(key)
	assert.Equal(t, int64(5), count)
}

func TestFlusher_BatchSizeLimit(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	cfg := testFlusherConfig(t, ac, 0)
	cfg.MaxBatchSize = 3
	f := NewFlusher(cfg)
	require.NoError(t, f.Start())
	defer f.Stop()

	// Enqueue enough to trigger auto-flush (maxBatchSize=3)
	f.Enqueue("127.0.0.1", 0x1, 1)
	f.Enqueue("127.0.0.1", 0x2, 1)

	// This should trigger an auto-flush since 3 entries
	f.Enqueue("127.0.0.1", 0x3, 1)

	// After auto-flush, the buffer for 127.0.0.1 should be cleared
	assert.Equal(t, int64(0), f.PendingCount())
}

func TestFlusher_StartStop(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	cfg := testFlusherConfig(t, ac, 0)
	f := NewFlusher(cfg)

	err := f.Start()
	require.NoError(t, err)
	assert.NotNil(t, f.ListenerAddr())

	f.Stop()
	// Should not panic or deadlock
}

func TestFlusher_ProtobufRoundTrip(t *testing.T) {
	original := &drlproto.CounterBatch{
		SenderId:  42,
		Timestamp: 1234567890,
		Entries: []*drlproto.CounterEntry{
			{EntityHash: 0xdeadbeef, Hits: 10},
			{EntityHash: 0xcafebabe, Hits: 20},
		},
	}

	data, err := proto.Marshal(original)
	require.NoError(t, err)

	decoded := &drlproto.CounterBatch{}
	err = proto.Unmarshal(data, decoded)
	require.NoError(t, err)

	assert.Equal(t, original.SenderId, decoded.SenderId)
	assert.Equal(t, original.Timestamp, decoded.Timestamp)
	require.Len(t, decoded.Entries, 2)
	assert.Equal(t, original.Entries[0].EntityHash, decoded.Entries[0].EntityHash)
	assert.Equal(t, original.Entries[0].Hits, decoded.Entries[0].Hits)
	assert.Equal(t, original.Entries[1].EntityHash, decoded.Entries[1].EntityHash)
	assert.Equal(t, original.Entries[1].Hits, decoded.Entries[1].Hits)
}
