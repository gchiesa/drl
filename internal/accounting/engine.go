package accounting

import (
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
	"github.com/gchiesa/drl/internal/ratelimit"
	"github.com/hashicorp/go-immutable-radix/v2"
)

// BlocklistEnforcer allows the engine to block entities without importing
// the cache package's concrete type (avoiding circular dependencies in tests).
type BlocklistEnforcer interface {
	IsBlocked(key string) bool
	Block(key string, entity *model.Entity, ttl time.Duration)
}

// BlockBroadcaster queues block events for cluster-wide propagation.
type BlockBroadcaster interface {
	QueueBlockEvent(key string, ttl time.Duration, entity *model.Entity) error
}

// Engine is the accounting engine that matches incoming requests against
// configurable rules, hashes the entity to determine the owner node, and
// either increments locally or enqueues a remote update via the Flusher.
// When a threshold is exceeded, it blocks the entity locally and broadcasts.
type Engine struct {
	rules       *iradix.Tree[*accountingRuleWithName]
	accounting  *cache.AccountingCache
	flusher     *Flusher
	logger      *slog.Logger
	metrics     *metrics.Metrics
	tracked     atomic.Int64
	limiter     ratelimit.RateLimiter
	blocklist   BlocklistEnforcer
	broadcaster BlockBroadcaster
}

// accountingRuleWithName pairs a rule with its config map key (for metrics labels).
type accountingRuleWithName struct {
	config.AccountingRule
	Name string
}

// EngineConfig holds the configuration for creating an Engine.
type EngineConfig struct {
	Rules       map[string]config.AccountingRule
	Accounting  *cache.AccountingCache
	Flusher     *Flusher
	Logger      *slog.Logger
	Metrics     *metrics.Metrics
	Limiter     ratelimit.RateLimiter
	Blocklist   BlocklistEnforcer
	Broadcaster BlockBroadcaster
}

// NewEngine creates a new accounting Engine.
func NewEngine(cfg EngineConfig) *Engine {
	limiter := cfg.Limiter
	if limiter == nil {
		limiter = ratelimit.NewSlidingWindow()
	}

	e := &Engine{
		rules:       createPathRadixTree(cfg.Rules),
		accounting:  cfg.Accounting,
		flusher:     cfg.Flusher,
		logger:      cfg.Logger,
		metrics:     cfg.Metrics,
		limiter:     limiter,
		blocklist:   cfg.Blocklist,
		broadcaster: cfg.Broadcaster,
	}
	for name, rule := range cfg.Rules {
		e.logger.Info("accounting rule loaded",
			"name", name,
			"path_prefix", rule.PathPrefix,
			"limit", rule.Limit,
			"per", rule.Per,
			"headers", rule.Headers,
		)
	}
	return e
}

// GetFlusher returns the Flusher instance associated with the Engine, allowing interaction with batching and flushing.
func (e *Engine) GetFlusher() *Flusher {
	return e.flusher
}

// Process evaluates the incoming request against accounting rules. If a rule
// matches, the entity is hashed and either counted locally (if this node is
// the owner) or enqueued for remote flushing. When a local increment causes
// the counter to exceed the rule's limit, the entity is blocked and a block
// event is broadcast cluster-wide.
func (e *Engine) Process(sourceIP, path string, headers map[string]string) {
	rule := e.matchRuleV2(path)
	if rule == nil {
		return
	}

	// Build entity using only the headers specified in the rule
	entity := model.Entity{
		IP:      sourceIP,
		Path:    path,
		Headers: filterHeaders(headers, rule.Headers),
	}

	key := entity.Key()

	// Parse entity hash from the hex key for the protobuf batch
	entityHash, _ := strconv.ParseUint(key, 16, 64)

	e.tracked.Add(1)

	ownerAddr := e.accounting.GetOwner(key)
	if e.accounting.IsOwner(key) {
		// Local increment and threshold check
		newCount := e.accounting.Increment(key)
		if e.metrics != nil {
			e.metrics.IncAccountingLocal()
		}
		e.logger.Debug("local accounting increment",
			"key", key,
			"owner", ownerAddr,
			"source_ip", sourceIP,
			"path", path,
			"count", newCount,
		)

		// Evaluate rate limit
		decision := e.limiter.Evaluate(newCount, &rule.AccountingRule, rule.Name)
		if decision.Blocked {
			e.logger.Warn("rate limit exceeded, blocking entity",
				"key", key,
				"rule", rule.Name,
				"count", newCount,
				"limit", rule.Limit,
			)
			if e.blocklist != nil {
				e.blocklist.Block(key, &entity, decision.RetryAfter)
			}
			if e.broadcaster != nil {
				_ = e.broadcaster.QueueBlockEvent(key, decision.RetryAfter, &entity)
			}
			if e.metrics != nil {
				e.metrics.IncRateLimitBlock(rule.Name, "threshold_exceeded")
			}
		}
	} else {
		// Remote enqueue
		if e.flusher != nil {
			e.flusher.Enqueue(ownerAddr, entityHash, 1)
		}
		if e.metrics != nil {
			e.metrics.IncAccountingRemote()
		}
		e.logger.Debug("remote accounting enqueue",
			"key", key,
			"owner", ownerAddr,
			"source_ip", sourceIP,
			"path", path,
		)
	}
}

// PendingUpdates returns the number of batched updates waiting to be flushed.
func (e *Engine) PendingUpdates() int64 {
	if e.flusher == nil {
		return 0
	}
	return e.flusher.PendingCount()
}

// TrackedEntities returns the total number of entity increments processed.
func (e *Engine) TrackedEntities() int64 {
	return e.tracked.Load()
}

// matchRuleV2 attempts to find the longest prefix match for the given path in the rules and returns the corresponding rule.
// Returns nil if no matching rule is found.
func (e *Engine) matchRuleV2(path string) *accountingRuleWithName {
	_, rule, found := e.rules.Root().LongestPrefix([]byte(path))
	if found {
		return rule
	}
	return nil
}

// filterHeaders returns a new map containing only the specified header keys.
func filterHeaders(headers map[string]string, keys []string) map[string]string {
	if len(keys) == 0 || headers == nil {
		return nil
	}
	result := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := headers[k]; ok {
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// createPathRadixTree constructs a radix tree to efficiently match paths to accounting rules based on their PathPrefix.
func createPathRadixTree(rules map[string]config.AccountingRule) *iradix.Tree[*accountingRuleWithName] {
	r := iradix.New[*accountingRuleWithName]()
	for name, rule := range rules {
		entry := &accountingRuleWithName{AccountingRule: rule, Name: name}
		r, _, _ = r.Insert([]byte(rule.PathPrefix), entry)
	}
	return r
}
