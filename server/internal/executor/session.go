package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/logging"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

const defaultMaxPreparedStatements = 1000

type Session struct {
	executor  *Executor
	currentDB string
	mu        sync.RWMutex

	ActiveTx    *txmanager.Transaction
	TxManager   *txmanager.Manager
	Broadcaster *Broadcaster
	AuditLog    *logging.AuditLogger

	PreparedStatements map[string]*PreparedStatement
	planCache          *PlanCache
	resultCache        *ResultCache
	snapshotTxID       uint64
	maxPreparedStmts   int
	serverCtx          context.Context
}

type PreparedStatement struct {
	Name  string
	Query parser.Statement
}

// SessionConfig содержит все параметры для создания сессии.
type SessionConfig struct {
	Store            storage.StorageEngine
	Metrics          *metrics.Collector
	TxManager        *txmanager.Manager
	Broadcaster      *Broadcaster
	Embedder         ai.Embedder
	WAL              *wal.WAL
	QueryTimeout     time.Duration
	MaxRows          int
	MaxPreparedStmts int
	ResultCacheSize  int
	ResultCacheTTL   time.Duration
}

func NewSession(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, b *Broadcaster) *Session {
	return NewSessionWithConfig(SessionConfig{
		Store:       store,
		Metrics:     m,
		TxManager:   txm,
		Broadcaster: b,
	})
}

// NewSessionWithConfig создаёт сессию с полной конфигурацией.
func NewSessionWithConfig(cfg SessionConfig) *Session {
	s := &Session{
		executor:           New(cfg.Store, cfg.Metrics, cfg.TxManager, cfg.Broadcaster),
		TxManager:          cfg.TxManager,
		Broadcaster:        cfg.Broadcaster,
		PreparedStatements: make(map[string]*PreparedStatement),
		planCache:          NewPlanCache(defaultPlanCacheSize),
		resultCache:        NewResultCache(defaultResultCacheSize, defaultResultCacheTTL),
		maxPreparedStmts:   defaultMaxPreparedStatements,
	}
	if cfg.Embedder != nil {
		s.SetEmbedder(cfg.Embedder)
	}
	if cfg.WAL != nil {
		s.SetWAL(cfg.WAL)
	}
	if cfg.QueryTimeout > 0 {
		s.SetQueryTimeout(cfg.QueryTimeout)
	}
	if cfg.MaxRows > 0 {
		s.SetMaxRows(cfg.MaxRows)
	}
	if cfg.MaxPreparedStmts > 0 {
		s.SetMaxPreparedStatements(cfg.MaxPreparedStmts)
	}
	if cfg.ResultCacheSize > 0 {
		s.SetResultCacheConfig(cfg.ResultCacheSize, int(cfg.ResultCacheTTL.Seconds()))
	}
	return s
}

// SetMaxPreparedStatements sets the maximum number of prepared statements per session.
func (s *Session) SetMaxPreparedStatements(n int) {
	if n > 0 {
		s.maxPreparedStmts = n
	}
}

// SetResultCacheConfig configures result cache size and TTL.
func (s *Session) SetResultCacheConfig(size int, ttlSec int) {
	if size > 0 {
		s.resultCache = NewResultCache(size, time.Duration(ttlSec)*time.Second)
	}
}

// SetEmbedder подключает embedding-провайдер для SEMANTIC_MATCH/AI_EMBED.
func (s *Session) SetEmbedder(emb ai.Embedder) {
	s.executor.SetEmbedder(emb)
}

// SetWAL подключает WAL для записи операций транзакций.
func (s *Session) SetWAL(w *wal.WAL) {
	s.executor.SetWAL(w)
}

// SetQueryTimeout задаёт таймаут на выполнение запроса.
func (s *Session) SetQueryTimeout(d time.Duration) {
	s.executor.SetQueryTimeout(d)
}

// SetMaxRows задаёт максимальное количество строк в результате SELECT.
func (s *Session) SetMaxRows(n int) {
	s.executor.SetMaxRows(n)
}

func (s *Session) IsInTx() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive
}

// GetActiveTx возвращает текущую транзакцию под блокировкой.
// Если транзакции нет — возвращает nil.
func (s *Session) GetActiveTx() *txmanager.Transaction {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveTx
}

func (s *Session) Execute(stmt parser.Statement) (*Result, error) {
	return s.executor.Run(stmt, s)
}

func (s *Session) CurrentDatabase() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentDB
}

func (s *Session) SetCurrentDatabase(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentDB = name
}

// SetSnapshotTxID задаёт ID транзакции для snapshot isolation при live queries.
func (s *Session) SetSnapshotTxID(txID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotTxID = txID
}

// SnapshotTxID возвращает ID снимка транзакции (0 = нет снимка).
func (s *Session) SnapshotTxID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotTxID
}

func (s *Session) GetPreparedStatement(name string) (*PreparedStatement, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps, ok := s.PreparedStatements[name]
	return ps, ok
}

func (s *Session) SetPreparedStatement(name string, ps *PreparedStatement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.PreparedStatements[name]; !exists && len(s.PreparedStatements) >= s.maxPreparedStmts {
		return fmt.Errorf("too many prepared statements (max %d)", s.maxPreparedStmts)
	}
	s.PreparedStatements[name] = ps
	return nil
}

func (s *Session) DeletePreparedStatement(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.PreparedStatements, name)
}

func (s *Session) SetActiveTx(tx *txmanager.Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ActiveTx = tx
}

func (s *Session) ClearActiveTx() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ActiveTx = nil
}

// SetServerContext задаёт контекст сервера для отмены запросов при shutdown.
func (s *Session) SetServerContext(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverCtx = ctx
}

// ServerContext возвращает контекст сервера (или context.Background если не задан).
func (s *Session) ServerContext() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.serverCtx != nil {
		return s.serverCtx
	}
	return context.Background()
}

// Close очищает ресурсы сессии.
// Если есть активная транзакция — откатывает её, чтобы не терять данные
// и не утекать ресурсы (spill-файлы и т.д.).
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive {
		s.ActiveTx.Rollback()
	}
	s.PreparedStatements = make(map[string]*PreparedStatement)
	s.ActiveTx = nil
}

// Reset сбрасывает состояние сессии для повторного использования в пуле.
// Откатывает активную транзакцию, очищает prepared statements,
// сбрасывает текущую БД и snapshot.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive {
		s.ActiveTx.Rollback()
	}
	s.ActiveTx = nil
	s.currentDB = ""
	s.snapshotTxID = 0
	s.PreparedStatements = make(map[string]*PreparedStatement)
	// Reset executor settings for pooled sessions
	if s.executor != nil {
		s.executor.SetMaxRows(0)
		s.executor.SetQueryTimeout(0)
	}
}
