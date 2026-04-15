// Package ratelimit defines the RateLimiter interface and provides
// algorithm implementations for DRL's distributed rate limiting engine.
package ratelimit

import (
	"time"

	"github.com/gchiesa/drl/internal/config"
)

// Decision represents the outcome of a rate limit check.
type Decision struct {
	// Blocked is true when the entity has exceeded its limit.
	Blocked bool
	// RetryAfter is the duration until the entity should retry.
	RetryAfter time.Duration
	// CurrentCount is the current counter value after increment.
	CurrentCount int64
	// Limit is the configured threshold.
	Limit int64
	// RuleName is the name of the matching rule.
	RuleName string
	// TokensRemaining is the number of tokens left after this request.
	// Set to -1 when the algorithm is not token-bucket.
	TokensRemaining float64
}

// RateLimiter evaluates whether a request count has exceeded a rule's limit.
type RateLimiter interface {
	// Evaluate checks the current count against the rule and returns a decision.
	// key uniquely identifies the entity (used by stateful algorithms like token bucket).
	Evaluate(key string, currentCount int64, rule *config.AccountingRule, ruleName string) Decision
}
