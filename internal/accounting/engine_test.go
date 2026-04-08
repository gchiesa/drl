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

	// Single node in ring means all keys are local. Two requests under
	// different paths but the same matching rule must collapse into one
	// counter (per-rule bucketing, not per-literal-path).
	e.Process("10.0.0.1", "/api/v1/resource", nil)
	e.Process("10.0.0.1", "/api/v1/other", nil)

	// Build same entity key to check cache: the bucket is scoped to the
	// rule's PathPrefix, not the literal request path.
	entity := model.Entity{IP: "10.0.0.1", Path: "/api"}
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
		// Bucket key uses the rule's PathPrefix, matching what Engine.Process
		// computes internally.
		entity := model.Entity{IP: ip, Path: "/api"}
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

func TestEngine_MatchRule_PathSegmentSemantics(t *testing.T) {
	// "/anything" must NOT match "/anythingelse" (string-prefix is not enough),
	// but it MUST match "/anything", "/anything/", and "/anything/other/".
	// "/" remains a catch-all for everything else.
	rules := map[string]config.AccountingRule{
		"catch-all": {PathPrefix: "/", Limit: 100, Per: "minute"},
		"anything":  {PathPrefix: "/anything", Limit: 10, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	cases := []struct {
		path string
		want string
	}{
		{"/anything", "anything"},
		{"/anything/", "anything"},
		{"/anything/other/", "anything"},
		{"/anything/other/deep", "anything"},
		{"/anythingelse", "catch-all"},
		{"/anythingelse/foo", "catch-all"},
		{"/foo", "catch-all"},
		{"/", "catch-all"},
	}
	for _, tc := range cases {
		rule := e.matchRuleV2(tc.path)
		require.NotNil(t, rule, "expected a match for %q", tc.path)
		assert.Equal(t, tc.want, rule.Name, "wrong rule for path %q", tc.path)
	}
}

func TestEngine_Process_RuleBucketing(t *testing.T) {
	// Every request matching the same rule (and same IP / rule-headers)
	// must collapse into one counter, regardless of the literal request path.
	rules := map[string]config.AccountingRule{
		"anything": {PathPrefix: "/anything", Limit: 10, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	e.Process("10.0.0.1", "/anything", nil)
	e.Process("10.0.0.1", "/anything/foo", nil)
	e.Process("10.0.0.1", "/anything/bar/baz", nil)

	bucketKey := model.Entity{IP: "10.0.0.1", Path: "/anything"}.Key()
	assert.Equal(t, int64(3), ac.Get(bucketKey),
		"all matching paths must aggregate into the rule's bucket")
	assert.Equal(t, int64(3), e.TrackedEntities())

	// And the gRPC-facing key builder must return the same bucket key for
	// any path under the prefix, so the blocklist lookup is consistent.
	for _, p := range []string{"/anything", "/anything/foo", "/anything/bar/baz"} {
		assert.Equal(t, bucketKey, e.BuildEntityKey("10.0.0.1", p, nil),
			"BuildEntityKey must return the rule bucket for %q", p)
	}
}

func TestEngine_Process_BucketingBlocksAggregate(t *testing.T) {
	// With per-rule bucketing the limit must trip after N total requests
	// across any mix of paths under the same rule.
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	bl := newMockBlocklist()
	bc := &mockBroadcaster{}

	rules := map[string]config.AccountingRule{
		"anything": {PathPrefix: "/anything", Limit: 3, Per: "minute"},
	}
	e := NewEngine(EngineConfig{
		Rules:       rules,
		Accounting:  ac,
		Logger:      testLogger(),
		Metrics:     metrics.NewMetrics(),
		Blocklist:   bl,
		Broadcaster: bc,
	})

	// 3 distinct paths under the prefix == 3 hits on the same bucket.
	e.Process("10.0.0.1", "/anything/a", nil)
	e.Process("10.0.0.1", "/anything/b", nil)
	e.Process("10.0.0.1", "/anything/c", nil)
	assert.Equal(t, 0, bl.blockedCount(), "should not block at the limit")

	// 4th hit on a yet-different path tips the bucket over.
	e.Process("10.0.0.1", "/anything/d", nil)
	assert.Equal(t, 1, bl.blockedCount(), "must block once aggregate exceeds limit")
	assert.Equal(t, 1, bc.eventCount(), "must broadcast a block event")
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

	// The entity should only include X-API-Key (from rule headers) and the
	// bucket key uses the rule's PathPrefix, not the literal request path.
	entity := model.Entity{
		IP:      "10.0.0.1",
		Path:    "/api",
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

func TestEngine_BulkLoad_LocalOwner(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	result := e.BulkLoad("10.0.0.1", "/api/v1/resource", nil, false)
	assert.Equal(t, BulkLoadAcceptedLocal, result)

	bucketKey := model.Entity{IP: "10.0.0.1", Path: "/api"}.Key()
	assert.Equal(t, int64(1), ac.Get(bucketKey))
	assert.Equal(t, int64(1), e.TrackedEntities())
}

func TestEngine_BulkLoad_RemoteEnqueue_DistributionEnabled(t *testing.T) {
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	f := NewFlusher(testFlusherConfig(t, ac, nil))

	// Two-node ring so some keys are non-local
	ac.AddNode("remote-node-1")

	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e := NewEngine(EngineConfig{
		Rules:      rules,
		Accounting: ac,
		Flusher:    f,
		Logger:     testLogger(),
		Metrics:    metrics.NewMetrics(),
	})

	// Find an IP that hashes to the remote owner.
	var remoteIP string
	for i := range 100 {
		candidate := "10.0.0." + string(rune('0'+i%10)) + "-" + string(rune('a'+i/10))
		key := model.Entity{IP: candidate, Path: "/api"}.Key()
		if !ac.IsOwner(key) {
			remoteIP = candidate
			break
		}
	}
	require.NotEmpty(t, remoteIP, "expected to find a non-local owner with two-node ring")

	result := e.BulkLoad(remoteIP, "/api/test", nil, true)
	assert.Equal(t, BulkLoadAcceptedRemote, result)
	assert.Equal(t, int64(1), f.PendingCount(), "remote enqueue must hit the flusher")
}

func TestEngine_BulkLoad_RemoteDrop_DistributionDisabled(t *testing.T) {
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	f := NewFlusher(testFlusherConfig(t, ac, nil))

	ac.AddNode("remote-node-1")

	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e := NewEngine(EngineConfig{
		Rules:      rules,
		Accounting: ac,
		Flusher:    f,
		Logger:     testLogger(),
		Metrics:    metrics.NewMetrics(),
	})

	var remoteIP string
	for i := range 100 {
		candidate := "10.0.0." + string(rune('0'+i%10)) + "-" + string(rune('a'+i/10))
		key := model.Entity{IP: candidate, Path: "/api"}.Key()
		if !ac.IsOwner(key) {
			remoteIP = candidate
			break
		}
	}
	require.NotEmpty(t, remoteIP)

	result := e.BulkLoad(remoteIP, "/api/test", nil, false)
	assert.Equal(t, BulkLoadDropped, result)
	assert.Equal(t, int64(0), f.PendingCount(), "must not enqueue when distribution disabled")
}

func TestEngine_BulkLoad_NoMatch(t *testing.T) {
	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e, ac := testEngine(t, rules)
	defer ac.Close()

	result := e.BulkLoad("10.0.0.1", "/other/path", nil, true)
	assert.Equal(t, BulkLoadNoMatch, result)
	assert.Equal(t, int64(0), e.TrackedEntities(), "tracked must not bump on no_match")
}

func TestEngine_BulkLoad_FlusherNil(t *testing.T) {
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	ac.AddNode("remote-node-1")

	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 100, Per: "minute"},
	}
	e := NewEngine(EngineConfig{
		Rules:      rules,
		Accounting: ac,
		Flusher:    nil, // explicitly no flusher
		Logger:     testLogger(),
		Metrics:    metrics.NewMetrics(),
	})

	var remoteIP string
	for i := range 100 {
		candidate := "10.0.0." + string(rune('0'+i%10)) + "-" + string(rune('a'+i/10))
		key := model.Entity{IP: candidate, Path: "/api"}.Key()
		if !ac.IsOwner(key) {
			remoteIP = candidate
			break
		}
	}
	require.NotEmpty(t, remoteIP)

	// Must not panic, must return Dropped.
	result := e.BulkLoad(remoteIP, "/api/test", nil, true)
	assert.Equal(t, BulkLoadDropped, result)
}

func TestEngine_BulkLoad_DoesNotEvaluateBlocking(t *testing.T) {
	// Load-bearing test for the milestone's "no blocking" rule: even if a
	// bulk load drives the count well past the rule's limit, the blocklist
	// and broadcaster must never be touched.
	ac := testEngineAccountingCache(t)
	defer ac.Close()
	bl := newMockBlocklist()
	bc := &mockBroadcaster{}

	rules := map[string]config.AccountingRule{
		"api-limit": {PathPrefix: "/api", Limit: 1, Per: "minute"},
	}
	e := NewEngine(EngineConfig{
		Rules:       rules,
		Accounting:  ac,
		Logger:      testLogger(),
		Metrics:     metrics.NewMetrics(),
		Blocklist:   bl,
		Broadcaster: bc,
	})

	for range 5 {
		result := e.BulkLoad("10.0.0.1", "/api/v1", nil, false)
		assert.Equal(t, BulkLoadAcceptedLocal, result)
	}

	assert.Equal(t, 0, bl.blockedCount(), "BulkLoad must not block entities")
	assert.Equal(t, 0, bc.eventCount(), "BulkLoad must not broadcast block events")

	bucketKey := model.Entity{IP: "10.0.0.1", Path: "/api"}.Key()
	assert.Equal(t, int64(5), ac.Get(bucketKey), "all hits must accumulate")
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
