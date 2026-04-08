package accounting

import (
	"log/slog"
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

// BuildEntityKey matches the path against accounting rules, filters headers
// per the matched rule, and returns the entity key. Returns "" if no rule
// matches. This is used by the gRPC server for fast blocklist lookups with
// the same key that Process/blocking uses.
//
// The returned key is scoped to the matched rule (not the literal request
// path), so all requests under a rule's PathPrefix collapse into one bucket
// per (IP, rule, rule-headers).
func (e *Engine) BuildEntityKey(sourceIP, path string, headers map[string]string) string {
	rule := e.matchRuleV2(path)
	if rule == nil {
		return ""
	}
	return e.buildEntity(sourceIP, rule, headers).Key()
}

// buildEntity constructs the canonical accounting Entity for a request that
// matched a rule. The Path field is set to rule.PathPrefix so that every
// request under that prefix shares one counter, and the Headers field is
// filtered to only the keys named by the rule.
func (e *Engine) buildEntity(sourceIP string, rule *accountingRuleWithName, headers map[string]string) model.Entity {
	return model.Entity{
		IP:      sourceIP,
		Path:    rule.PathPrefix,
		Headers: filterHeaders(headers, rule.Headers),
	}
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

	// Bucket the request by rule, not by literal path, so that every request
	// matching the same rule (and same IP / rule-scoped headers) shares one
	// counter and is enforced against the rule's limit in aggregate.
	entity := e.buildEntity(sourceIP, rule, headers)
	// Hash the entity once; key is just its hex form, used by the local
	// caches, while the raw uint64 goes on the wire to remote owners.
	entityHash := entity.Hash()
	key := model.HashToEntityKey(entityHash)

	e.tracked.Add(1)

	ownerAddr := e.accounting.GetOwner(key)
	if e.accounting.IsOwner(key) {
		// Local increment and threshold check
		newCount := e.accounting.Increment(key, 1)
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

// EstimatedEntities returns the entities in the cache
func (e *Engine) EstimatedEntities() int64 {
	return e.accounting.GetEstimatedEntities()
}

// matchRuleV2 attempts to find the longest path-prefix match for the given
// path and returns the corresponding rule. Returns nil if no matching rule
// is found.
//
// The radix tree's LongestPrefix is a string-prefix match, which would
// incorrectly match e.g. "/anythingelse" against a rule with PathPrefix
// "/anything". To enforce path-segment semantics we walk shorter candidates
// in the tree until we find one that ends at a path boundary in the input
// (i.e. the prefix itself ends with '/', equals the input path, or the next
// character in the input is '/'). The "/" rule always matches.
func (e *Engine) matchRuleV2(path string) *accountingRuleWithName {
	root := e.rules.Root()
	candidate := []byte(path)
	for {
		k, rule, found := root.LongestPrefix(candidate)
		if !found {
			return nil
		}
		if isPathSegmentMatch(path, string(k)) {
			return rule
		}
		// The matched key is a string-prefix but not a path-segment prefix
		// (e.g. tree has "/anything" and input is "/anythingelse"). Trim
		// the candidate by one byte and retry to find a shorter prefix
		// that does sit on a segment boundary.
		if len(k) == 0 {
			return nil
		}
		candidate = []byte(string(k)[:len(k)-1])
	}
}

// isPathSegmentMatch reports whether prefix is a path-segment prefix of
// path (not merely a string prefix). The root prefix "/" always matches.
func isPathSegmentMatch(path, prefix string) bool {
	if prefix == "/" || prefix == "" {
		return true
	}
	if len(prefix) > len(path) || path[:len(prefix)] != prefix {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	// prefix already ends in '/' (e.g. "/api/") -- any continuation is fine
	if prefix[len(prefix)-1] == '/' {
		return true
	}
	// next byte after the prefix in the path must be a segment separator
	return path[len(prefix)] == '/'
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
