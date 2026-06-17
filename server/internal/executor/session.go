package executor

import (
	"fmt"
	"sync"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

const maxPreparedStatements = 1000

type Session struct {
	executor  *Executor
	currentDB string
	mu        sync.RWMutex

	ActiveTx    *txmanager.Transaction
	TxManager   *txmanager.Manager
	Broadcaster *Broadcaster

	PreparedStatements map[string]*PreparedStatement
	planCache          *PlanCache
	resultCache        *ResultCache
}

type PreparedStatement struct {
	Name  string
	Query parser.Statement
}

func NewSession(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, b *Broadcaster) *Session {
	return &Session{
		executor:           New(store, m, txm, b),
		TxManager:          txm,
		Broadcaster:        b,
		PreparedStatements: make(map[string]*PreparedStatement),
		planCache:          NewPlanCache(defaultPlanCacheSize),
		resultCache:        NewResultCache(defaultResultCacheSize, defaultResultCacheTTL),
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

func (s *Session) GetPreparedStatement(name string) (*PreparedStatement, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps, ok := s.PreparedStatements[name]
	return ps, ok
}

func (s *Session) SetPreparedStatement(name string, ps *PreparedStatement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.PreparedStatements[name]; !exists && len(s.PreparedStatements) >= maxPreparedStatements {
		return fmt.Errorf("too many prepared statements (max %d)", maxPreparedStatements)
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

// Close очищает ресурсы сессии.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PreparedStatements = make(map[string]*PreparedStatement)
	s.ActiveTx = nil
}
