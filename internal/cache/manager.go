package cache

import (
	"log/slog"
	"time"
)

// Manager provides a unified interface for blocklist and accounting caches
type Manager struct {
	Blocklist  *BlocklistCache
	Accounting *AccountingCache
	logger     *slog.Logger
}

// ManagerConfig holds configuration for the cache manager
type ManagerConfig struct {
	BlocklistSizeMB  int64
	AccountingSizeMB int64
	LocalNode        string
	WindowSize       time.Duration
	Logger           *slog.Logger

	// Metrics callbacks
	OnBlocklistHit    func()
	OnBlocklistMiss   func()
	OnBlocklistEvict  func()
	OnAccountingHit   func()
	OnAccountingMiss  func()
	OnAccountingEvict func()
}

// NewManager creates a new cache manager with both blocklist and accounting caches
func NewManager(cfg ManagerConfig) (*Manager, error) {
	blocklist, err := NewBlocklistCache(BlocklistConfig{
		MaxSizeMB: cfg.BlocklistSizeMB,
		Logger:    cfg.Logger,
		OnHit:     cfg.OnBlocklistHit,
		OnMiss:    cfg.OnBlocklistMiss,
		OnEvict:   cfg.OnBlocklistEvict,
	})
	if err != nil {
		return nil, err
	}

	accounting, err := NewAccountingCache(AccountingConfig{
		MaxSizeMB:  cfg.AccountingSizeMB,
		LocalNode:  cfg.LocalNode,
		WindowSize: cfg.WindowSize,
		Logger:     cfg.Logger,
		OnHit:      cfg.OnAccountingHit,
		OnMiss:     cfg.OnAccountingMiss,
		OnEvict:    cfg.OnAccountingEvict,
	})
	if err != nil {
		blocklist.Close()
		return nil, err
	}

	return &Manager{
		Blocklist:  blocklist,
		Accounting: accounting,
		logger:     cfg.Logger,
	}, nil
}

// Close closes both caches
func (m *Manager) Close() {
	if m.Blocklist != nil {
		m.Blocklist.Close()
	}
	if m.Accounting != nil {
		m.Accounting.Close()
	}
}

// UpdateNodes updates the accounting cache's hash ring with the given nodes
func (m *Manager) UpdateNodes(nodes []string) {
	m.Accounting.UpdateNodes(nodes)
}

// SetLocalNode sets the local node identifier for both caches
func (m *Manager) SetLocalNode(node string) {
	m.Accounting.SetLocalNode(node)
}
