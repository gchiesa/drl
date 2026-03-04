package accounting

import (
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/gchiesa/drl/internal/cache"
	"github.com/gchiesa/drl/internal/config"
	"github.com/gchiesa/drl/internal/metrics"
	"github.com/gchiesa/drl/internal/model"
)

// Engine is the accounting engine that matches incoming requests against
// configurable rules, hashes the entity to determine the owner node, and
// either increments locally or enqueues a remote update via the Flusher.
type Engine struct {
	rules      []config.AccountingRule
	accounting *cache.AccountingCache
	flusher    *Flusher
	logger     *slog.Logger
	metrics    *metrics.Metrics
	tracked    atomic.Int64
}

// EngineConfig holds the configuration for creating an Engine.
type EngineConfig struct {
	Rules      []config.AccountingRule
	Accounting *cache.AccountingCache
	Flusher    *Flusher
	Logger     *slog.Logger
	Metrics    *metrics.Metrics
}

// NewEngine creates a new accounting Engine.
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		rules:      cfg.Rules,
		accounting: cfg.Accounting,
		flusher:    cfg.Flusher,
		logger:     cfg.Logger,
		metrics:    cfg.Metrics,
	}
}

// Process evaluates the incoming request against accounting rules. If a rule
// matches, the entity is hashed and either counted locally (if this node is
// the owner) or enqueued for remote flushing.
func (e *Engine) Process(sourceIP, path string, headers map[string]string) {
	rule := e.matchRule(path)
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
		// Local increment
		e.accounting.Increment(key)
		if e.metrics != nil {
			e.metrics.IncAccountingLocal()
		}
		e.logger.Debug("local accounting increment",
			"key", key,
			"owner", ownerAddr,
			"source_ip", sourceIP,
			"path", path,
		)
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

// matchRule returns the first rule whose PathPrefix matches the given path.
func (e *Engine) matchRule(path string) *config.AccountingRule {
	for i := range e.rules {
		if strings.HasPrefix(path, e.rules[i].PathPrefix) {
			return &e.rules[i]
		}
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
