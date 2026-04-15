package ratelimit

import (
	"math"
	"sync"
	"time"

	"github.com/gchiesa/drl/internal/config"
)

// bucketState holds the per-entity token bucket state.
// Access must be protected by mu.
type bucketState struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

// TokenBucket implements the token bucket rate limiting algorithm.
//
// Each entity starts with a full bucket of tokens (capacity). Tokens refill
// at refillRate tokens per second up to capacity. A request is allowed when
// at least one token is available; it is denied when the bucket is empty.
//
// Capacity is taken from the AccountingRule.Limit field, enabling per-rule
// burst ceilings. RefillRate is the global rate supplied at construction.
//
// Bucket state is stored in a sync.Map keyed by entity key, providing O(1)
// amortised lookups with low lock contention.
type TokenBucket struct {
	buckets    sync.Map // string key → *bucketState
	capacity   float64  // global burst cap (tokens)
	refillRate float64  // tokens added per second
}

// NewTokenBucket creates a new TokenBucket rate limiter.
// capacity is the maximum burst size; refillRate is tokens added per second.
func NewTokenBucket(capacity float64, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		refillRate: refillRate,
	}
}

// Evaluate applies the token bucket algorithm for the given entity key.
// currentCount (the accounting cache counter) is recorded in the Decision for
// observability but does not influence the token bucket decision.
func (tb *TokenBucket) Evaluate(key string, currentCount int64, rule *config.AccountingRule, ruleName string) Decision {
	d := Decision{
		CurrentCount: currentCount,
		Limit:        rule.Limit,
		RuleName:     ruleName,
	}

	capacity := tb.capacity

	state := tb.getOrCreate(key, capacity)

	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(state.lastRefill).Seconds()
	// Refill tokens proportional to elapsed time, capped at capacity.
	state.tokens = math.Min(capacity, state.tokens+elapsed*tb.refillRate)
	state.lastRefill = now

	if state.tokens >= 1.0 {
		state.tokens -= 1.0
		d.TokensRemaining = state.tokens
		d.Blocked = false
	} else {
		d.TokensRemaining = state.tokens
		d.Blocked = true
		if tb.refillRate > 0 {
			tokensNeeded := 1.0 - state.tokens
			d.RetryAfter = time.Duration(tokensNeeded / tb.refillRate * float64(time.Second))
		} else {
			d.RetryAfter = rule.WindowDuration()
		}
	}

	return d
}

// getOrCreate returns the existing bucket state for key, or creates a full
// bucket if none exists yet.
func (tb *TokenBucket) getOrCreate(key string, capacity float64) *bucketState {
	candidate := &bucketState{
		tokens:     capacity,
		lastRefill: time.Now(),
	}
	actual, _ := tb.buckets.LoadOrStore(key, candidate)
	return actual.(*bucketState) //nolint:forcetypeassert
}
