package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/gchiesa/drl/internal/config"
)

func TestSlidingWindow_Evaluate_UnderLimit(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 100, Per: "minute"}

	d := sw.Evaluate(50, rule, "api-limit")
	assert.False(t, d.Blocked)
	assert.Equal(t, int64(50), d.CurrentCount)
	assert.Equal(t, int64(100), d.Limit)
	assert.Equal(t, "api-limit", d.RuleName)
	assert.Equal(t, time.Duration(0), d.RetryAfter)
}

func TestSlidingWindow_Evaluate_AtLimit(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 100, Per: "minute"}

	d := sw.Evaluate(100, rule, "api-limit")
	assert.False(t, d.Blocked, "at exactly the limit should not be blocked")
}

func TestSlidingWindow_Evaluate_OverLimit(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/api", Limit: 100, Per: "minute"}

	d := sw.Evaluate(101, rule, "api-limit")
	assert.True(t, d.Blocked)
	assert.Equal(t, time.Minute, d.RetryAfter)
	assert.Equal(t, int64(101), d.CurrentCount)
	assert.Equal(t, "api-limit", d.RuleName)
}

func TestSlidingWindow_Evaluate_PerSecond(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/health", Limit: 10, Per: "second"}

	d := sw.Evaluate(11, rule, "health-limit")
	assert.True(t, d.Blocked)
	assert.Equal(t, time.Second, d.RetryAfter)
}

func TestSlidingWindow_Evaluate_ZeroCount(t *testing.T) {
	sw := NewSlidingWindow()
	rule := &config.AccountingRule{PathPrefix: "/", Limit: 1, Per: "minute"}

	d := sw.Evaluate(0, rule, "root")
	assert.False(t, d.Blocked)
}
