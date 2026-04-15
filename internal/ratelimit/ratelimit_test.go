package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gchiesa/drl/internal/config"
)

// ── SlidingWindow ────────────────────────────────────────────────────────────

func TestSlidingWindow_Evaluate_UnderLimit(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 100, Per: "minute"}

	d := sw.Evaluate("key1", 50, rule, "api-limit")
	assert.False(t, d.Blocked)
	assert.Equal(t, int64(50), d.CurrentCount)
	assert.Equal(t, int64(100), d.Limit)
	assert.Equal(t, "api-limit", d.RuleName)
	assert.Equal(t, time.Duration(0), d.RetryAfter)
	assert.Equal(t, float64(-1), d.TokensRemaining, "sliding window should report -1 for TokensRemaining")
}

func TestSlidingWindow_Evaluate_AtLimit(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 100, Per: "minute"}

	d := sw.Evaluate("key1", 100, rule, "api-limit")
	assert.False(t, d.Blocked, "at exactly the limit should not be blocked")
}

func TestSlidingWindow_Evaluate_OverLimit(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 100, Per: "minute"}

	d := sw.Evaluate("key1", 101, rule, "api-limit")
	assert.True(t, d.Blocked)
	assert.Equal(t, time.Minute, d.RetryAfter)
	assert.Equal(t, int64(101), d.CurrentCount)
	assert.Equal(t, "api-limit", d.RuleName)
}

func TestSlidingWindow_Evaluate_PerSecond(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/health", Limit: 10, Per: "second"}

	d := sw.Evaluate("key1", 11, rule, "health-limit")
	assert.True(t, d.Blocked)
	assert.Equal(t, time.Second, d.RetryAfter)
}

func TestSlidingWindow_Evaluate_ZeroCount(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/", Limit: 1, Per: "minute"}

	d := sw.Evaluate("key1", 0, rule, "root")
	assert.False(t, d.Blocked)
}

// ── TokenBucket ──────────────────────────────────────────────────────────────

func TestTokenBucket_NewBucketStartsFull(t *testing.T) {
	tb := NewTokenBucket(10, 1)
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 10, Per: "minute"}

	// First request should be allowed — bucket starts full.
	d := tb.Evaluate("entity-1", 1, rule, "api-limit")
	assert.False(t, d.Blocked)
	assert.Equal(t, float64(9), d.TokensRemaining, "9 tokens left after consuming 1 from full bucket of 10")
	assert.Equal(t, "api-limit", d.RuleName)
}

func TestTokenBucket_AllowsUpToCapacity(t *testing.T) {
	capacity := float64(5)
	tb := NewTokenBucket(capacity, 0) // refillRate=0 so no refills between calls
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 5, Per: "minute"}

	for i := 0; i < int(capacity); i++ {
		d := tb.Evaluate("entity-drain", int64(i+1), rule, "api-limit")
		assert.False(t, d.Blocked, "request %d should be allowed", i+1)
	}
}

func TestTokenBucket_BlocksWhenExhausted(t *testing.T) {
	tb := NewTokenBucket(3, 0) // 3 tokens, no refill
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 3, Per: "minute"}

	// Drain the bucket
	for i := 0; i < 3; i++ {
		d := tb.Evaluate("entity-exhaust", int64(i+1), rule, "api-limit")
		require.False(t, d.Blocked, "request %d should be allowed while draining", i+1)
	}

	// Next request must be blocked
	d := tb.Evaluate("entity-exhaust", 4, rule, "api-limit")
	assert.True(t, d.Blocked)
	assert.True(t, d.TokensRemaining < 1.0, "tokens should be < 1 when blocked")
}

func TestTokenBucket_RetryAfterWithRefillRate(t *testing.T) {
	refillRate := float64(2) // 2 tokens/second → 0.5 s per token
	tb := NewTokenBucket(1, refillRate)
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 1, Per: "minute"}

	// Consume the only token
	d := tb.Evaluate("entity-retry", 1, rule, "rule")
	require.False(t, d.Blocked)

	// Now blocked: need 1 token at 2/s → ~500 ms
	d = tb.Evaluate("entity-retry", 2, rule, "rule")
	assert.True(t, d.Blocked)
	assert.Greater(t, d.RetryAfter, time.Duration(0))
	assert.LessOrEqual(t, d.RetryAfter, 600*time.Millisecond,
		"RetryAfter should be ~500 ms for 1 token at 2 tok/s")
}

func TestTokenBucket_TokensRefillOverTime(t *testing.T) {
	refillRate := float64(100) // 100 tokens/second — fast refill for testing
	tb := NewTokenBucket(10, refillRate)
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 10, Per: "minute"}

	// Drain all 10 tokens
	for i := 0; i < 10; i++ {
		d := tb.Evaluate("entity-refill", int64(i+1), rule, "rule")
		require.False(t, d.Blocked)
	}

	// Bucket is empty — next call should be blocked
	d := tb.Evaluate("entity-refill", 11, rule, "rule")
	require.True(t, d.Blocked)

	// Wait long enough for at least 1 token to refill (100 tok/s → 10 ms per token)
	time.Sleep(20 * time.Millisecond)

	// Now should be allowed again
	d = tb.Evaluate("entity-refill", 12, rule, "rule")
	assert.False(t, d.Blocked, "bucket should have refilled at least 1 token after 20 ms at 100 tok/s")
}

func TestTokenBucket_CapacityIsRespected(t *testing.T) {
	capacity := float64(5)
	tb := NewTokenBucket(capacity, 1000) // very fast refill
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 5, Per: "minute"}

	// Drain one token
	d := tb.Evaluate("entity-cap", 1, rule, "rule")
	require.False(t, d.Blocked)
	assert.Equal(t, float64(4), d.TokensRemaining)

	// Wait a long time — bucket should never exceed capacity
	time.Sleep(50 * time.Millisecond)

	// After refill, tokens should be capped at capacity. Consume one and verify.
	d = tb.Evaluate("entity-cap", 2, rule, "rule")
	assert.False(t, d.Blocked)
	// tokens after refill = min(5, 4 + elapsed*1000) = 5, then consume 1 → 4
	assert.Equal(t, float64(4), d.TokensRemaining,
		"tokens should be capped at capacity-1 after consuming one token from a full bucket")
}

func TestTokenBucket_IndependentEntities(t *testing.T) {
	tb := NewTokenBucket(2, 0) // 2 tokens, no refill
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 2, Per: "minute"}

	// Drain entity-A fully
	tb.Evaluate("entity-a", 1, rule, "rule")
	tb.Evaluate("entity-a", 2, rule, "rule")
	dA := tb.Evaluate("entity-a", 3, rule, "rule")
	assert.True(t, dA.Blocked, "entity-A should be blocked after exhausting its bucket")

	// entity-B should still have a full bucket (independent state)
	dB := tb.Evaluate("entity-b", 1, rule, "rule")
	assert.False(t, dB.Blocked, "entity-B should not be affected by entity-A's exhausted bucket")
}

func TestTokenBucket_TokensRemainingInDecision(t *testing.T) {
	tb := NewTokenBucket(5, 0)
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 5, Per: "minute"}

	d := tb.Evaluate("entity-tokens", 1, rule, "rule")
	assert.False(t, d.Blocked)
	assert.Equal(t, float64(4), d.TokensRemaining)

	d = tb.Evaluate("entity-tokens", 2, rule, "rule")
	assert.False(t, d.Blocked)
	assert.Equal(t, float64(3), d.TokensRemaining)
}
