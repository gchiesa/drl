package accounting

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
)

func testEngineAccountingCache(t *testing.T) *cache.AccountingCache {
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

func testEngine(t *testing.T, rules map[string]config.AccountingRule) (*Engine, *cache.AccountingCache) {
	t.Helper()
	ac := testEngineAccountingCache(t)
	m := metrics.NewMetrics()

	e := NewEngine(EngineConfig{
		Rules:      rules,
		Accounting: ac,
		Flusher:    nil, // no flusher for most tests
		Logger:     testLogger(),
		Metrics:    m,
	})
	return e, ac
}

func TestEngine_MatchRule(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"api-limit":    {PathPrefix: "/api/v1", Headers: []string{"X-API-Key"}, Limit: 100, Per: "minute"},
		"health-limit": {PathPrefix: "/health", Limit: 500, Per: "second"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	// Match first rule
	rule := e.matchRuleV2("/api/v1/users")
	require.NotNil(t, rule)
	assert.Equal(t, "/api/v1", rule.PathPrefix)
	assert.Equal(t, "api-limit", rule.Name)

	// Match second rule
	rule = e.matchRuleV2("/health")
	require.NotNil(t, rule)
	assert.Equal(t, "/health", rule.PathPrefix)
	assert.Equal(t, "health-limit", rule.Name)

	// No match
	rule = e.matchRuleV2("/unknown/path")
	assert.Nil(t, rule)
}

func TestEngine_Process_LocalOwner(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	// Single node in ring means all keys are local
	e.Process("10.0.0.1", "/api/v1/resource", nil)
	e.Process("10.0.0.1", "/api/v1/resource", nil)

	// Build same entity key to check cache
	entity := model.Entity{IP: "10.0.0.1", Path: "/api/v1/resource"}
	key := entity.Key()

	count := ac.Get(key)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, int64(2), e.TrackedEntities())
}

func TestEngine_Process_RemoteOwner(t *testing.T) {
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	m := metrics.NewMetrics()

	// Create a flusher for enqueuing
	fCfg := testFlusherConfig(t, ac, nil)
	f := NewFlusher(fCfg)

	// Add a remote node so that some keys are not local
	ac.AddNode("remote-node-1")

	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}

	e := NewEngine(EngineConfig{
		Rules:      rules,
		Accounting: ac,
		Flusher:    f,
		Logger:     testLogger(),
		Metrics:    m,
	})

	// Process many requests - some will be local, some remote
	// With two nodes, ownership is split by consistent hashing
	localCount := int64(0)
	remoteCount := int64(0)
	for i := range 20 {
		ip := "10.0.0." + string(rune('0'+i%10))
		entity := model.Entity{IP: ip, Path: "/api/test"}
		key := entity.Key()

		if ac.IsOwner(key) {
			localCount++
		} else {
			remoteCount++
		}
		e.Process(ip, "/api/test", nil)
	}

	assert.Equal(t, int64(20), e.TrackedEntities())
	// There should be some pending if any were remote
	if remoteCount > 0 {
		assert.Greater(t, f.PendingCount(), int64(0))
	}
}

func TestEngine_Process_NoMatch(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	e.Process("10.0.0.1", "/other/path", nil)
	assert.Equal(t, int64(0), e.TrackedEntities())
}

func TestEngine_Process_WithHeaders(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Headers: []string{"X-API-Key"}, Limit: 100, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	headers := map[string]string{
		"X-API-Key":    "abc123",
		"Content-Type": "application/json",
		"User-Agent":   "test",
	}

	e.Process("10.0.0.1", "/api/v1", headers)

	// The entity should only include X-API-Key (from rule headers)
	entity := model.Entity{
		IP:      "10.0.0.1",
		Path:    "/api/v1",
		Headers: map[string]string{"X-API-Key": "abc123"},
	}
	key := entity.Key()

	count := ac.Get(key)
	assert.Equal(t, int64(1), count)
}

func TestEngine_PendingUpdates(t *testing.T) {
	ac := testEngineAccountingCache(t)
	defer ac.Close()

	// Engine without flusher
	e := NewEngine(EngineConfig{
		Accounting: ac,
		Logger:     testLogger(),
	})
	assert.Equal(t, int64(0), e.PendingUpdates())

	// Engine with flusher
	f := NewFlusher(testFlusherConfig(t, ac, nil))
	f.Enqueue("10.0.0.1", 0xdead, 1)

	e2 := NewEngine(EngineConfig{
		Accounting: ac,
		Flusher:    f,
		Logger:     testLogger(),
	})
	assert.Equal(t, int64(1), e2.PendingUpdates())
}

func TestEngine_TrackedEntities(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"root": {PathPrefix: "/", Limit: 1000, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	assert.Equal(t, int64(0), e.TrackedEntities())

	e.Process("10.0.0.1", "/path", nil)
	e.Process("10.0.0.2", "/path", nil)
	e.Process("10.0.0.3", "/path", nil)

	assert.Equal(t, int64(3), e.TrackedEntities())
}

// mockBlocklist records Block calls for testing.
type mockBlocklist struct {
	mu      sync.Mutex
	blocked map[string]time.Duration
}

func newMockBlocklist() *mockBlocklist {
	return &mockBlocklist{blocked: make(map[string]time.Duration)}
}

func (m *mockBlocklist) IsBlocked(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.blocked[key]
	return ok
}

func (m *mockBlocklist) Block(key string, entity *model.Entity, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocked[key] = ttl
}

func (m *mockBlocklist) blockedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.blocked)
}

// mockBroadcaster records QueueBlockEvent calls.
type mockBroadcaster struct {
	mu     sync.Mutex
	events []string
}

func (m *mockBroadcaster) QueueBlockEvent(key string, _ time.Duration, _ *model.Entity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, key)
	return nil
}

func (m *mockBroadcaster) eventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.events)
}

func TestEngine_Process_ThresholdBlocking(t *testing.T) {
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	m := metrics.NewMetrics()
	bl := newMockBlocklist()
	bc := &mockBroadcaster{}

	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 3, Per: "minute"},
	}

	e := NewEngine(EngineConfig{
		Rules:       rules,
		Accounting:  ac,
		Logger:      testLogger(),
		Metrics:     m,
		Blocklist:   bl,
		Broadcaster: bc,
	})

	// Process 3 requests (at limit, not blocked)
	for range 3 {
		e.Process("10.0.0.1", "/api/resource", nil)
	}
	assert.Equal(t, 0, bl.blockedCount(), "should not be blocked at limit")

	// 4th request exceeds the limit
	e.Process("10.0.0.1", "/api/resource", nil)
	assert.Equal(t, 1, bl.blockedCount(), "should be blocked after exceeding limit")
	assert.Equal(t, 1, bc.eventCount(), "should broadcast block event")
}

func TestFilterHeaders(t *testing.T) {
	headers := map[string]string{
		"X-API-Key":    "key1",
		"Content-Type": "json",
		"User-Agent":   "bot",
	}

	// Filter to specific keys
	result := filterHeaders(headers, []string{"X-API-Key", "Content-Type"})
	assert.Len(t, result, 2)
	assert.Equal(t, "key1", result["X-API-Key"])
	assert.Equal(t, "json", result["Content-Type"])

	// No keys specified
	result = filterHeaders(headers, nil)
	assert.Nil(t, result)

	// No matching keys
	result = filterHeaders(headers, []string{"X-Custom"})
	assert.Nil(t, result)

	// Nil headers
	result = filterHeaders(nil, []string{"X-API-Key"})
	assert.Nil(t, result)
}
