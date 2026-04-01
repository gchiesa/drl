package accounting

import (
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

// mockSender records messages sent via SendAccountingMsg.
type mockSender struct {
	mu   sync.Mutex
	sent map[string][][]byte // addr -> list of payloads
}

func newMockSender() *mockSender {
	return &mockSender{sent: make(map[string][][]byte)}
}

func (m *mockSender) SendAccountingMsg(addr string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.sent[addr] = append(m.sent[addr], cp)
	return nil
}

func (m *mockSender) totalSent() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int
	for _, msgs := range m.sent {
		n += len(msgs)
	}
	return n
}

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

func testFlusherConfig(t *testing.T, ac *cache.AccountingCache, sender NodeSender) FlusherConfig {
	t.Helper()
	return FlusherConfig{
		SenderID:      12345,
		Accounting:    ac,
		Logger:        testLogger(),
		Metrics:       metrics.NewMetrics(),
		FlushInterval: 100 * time.Millisecond,
		MaxBatchSize:  10,
		Sender:        sender,
	}
}

func TestFlusher_Enqueue(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	f := NewFlusher(testFlusherConfig(t, ac, nil))

	f.Enqueue("10.0.0.1", 0xdeadbeef, 1)
	f.Enqueue("10.0.0.1", 0xcafebabe, 3)
	f.Enqueue("10.0.0.2", 0x12345678, 2)

	assert.Equal(t, int64(3), f.PendingCount())
}

func TestFlusher_Flush(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	sender := newMockSender()
	cfg := testFlusherConfig(t, ac, sender)
	f := NewFlusher(cfg)
	require.NoError(t, f.Start())
	defer f.Stop()

	f.Enqueue("127.0.0.1", 0xdeadbeef, 1)
	f.Enqueue("127.0.0.1", 0xcafebabe, 2)
	assert.Equal(t, int64(2), f.PendingCount())

	// Trigger manual flush
	f.flush()

	assert.Equal(t, int64(0), f.PendingCount())
	assert.Equal(t, 1, sender.totalSent(), "one batch should have been sent")
}

func TestFlusher_DrlMessageFormat(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	sender := newMockSender()
	cfg := testFlusherConfig(t, ac, sender)
	f := NewFlusher(cfg)
	require.NoError(t, f.Start())
	defer f.Stop()

	f.Enqueue("127.0.0.1", 0xaabbccdd, 5)
	f.flush()

	require.Equal(t, 1, sender.totalSent())
	data := sender.sent["127.0.0.1"][0]

	// Verify it's a valid DrlMessage with CounterBatch
	msg := &drlproto.DrlMessage{}
	require.NoError(t, proto.Unmarshal(data, msg))

	counters := msg.GetCounters()
	require.NotNil(t, counters, "message must contain CounterBatch")
	assert.Equal(t, uint64(12345), counters.SenderId)
	require.Len(t, counters.Entries, 1)
	assert.Equal(t, uint64(0xaabbccdd), counters.Entries[0].EntityHash)
	assert.Equal(t, uint64(5), counters.Entries[0].Hits)
}

func TestFlusher_BatchSizeLimit(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	sender := newMockSender()
	cfg := testFlusherConfig(t, ac, sender)
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
	assert.Equal(t, 1, sender.totalSent(), "auto-flush should have sent one batch")
}

func TestFlusher_StartStop(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	cfg := testFlusherConfig(t, ac, newMockSender())
	f := NewFlusher(cfg)

	err := f.Start()
	require.NoError(t, err)

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

func TestFlusher_NilSender(t *testing.T) {
	ac := testAccountingCache(t)
	defer ac.Close()

	cfg := testFlusherConfig(t, ac, nil)
	f := NewFlusher(cfg)
	require.NoError(t, f.Start())
	defer f.Stop()

	// Enqueue and flush with nil sender should not panic
	f.Enqueue("127.0.0.1", 0xdeadbeef, 1)
	f.flush()
	assert.Equal(t, int64(0), f.PendingCount())
}
