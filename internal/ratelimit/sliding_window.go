package ratelimit

import (
	"github.com/gchiesa/drl/internal/config"
)

// SlidingWindow implements a fixed/sliding window rate limiter.
// Because DRL's accounting cache already uses TTL-based windows (entries expire
// after the rule's `per` duration), this implementation simply compares the
// current counter value against the configured limit.
type SlidingWindow struct{}

// NewSlidingWindow creates a new SlidingWindow rate limiter.
func NewSlidingWindow() *SlidingWindow {
	return &SlidingWindow{}
}

// Evaluate checks whether the current count exceeds the rule's limit.
func (sw *SlidingWindow) Evaluate(currentCount int64, rule *config.AccountingRule, ruleName string) Decision {
	d := Decision{
		CurrentCount: currentCount,
		Limit:        rule.Limit,
		RuleName:     ruleName,
	}

	if currentCount > rule.Limit {
		d.Blocked = true
		d.RetryAfter = rule.WindowDuration()
	}

	return d
}
