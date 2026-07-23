package executor

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/auth"
	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/audit"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/core/wal"
	"vaultdb/internal/logging"
)

const defaultMaxPreparedStatements = 1000

var sessionIDCounter uint64

func nextSessionID() uint64 {
	return atomic.AddUint64(&sessionIDCounter, 1)
}

type Session struct {
	executor  *Executor
	currentDB string
	mu        sync.RWMutex

	id   uint64
	user string

	ActiveTx    *txmanager.Transaction
	TxManager   *txmanager.Manager
	Broadcaster *Broadcaster
	AuditLog    *logging.AuditLogger
	AuditTable  *audit.TableLog
	ArchivePath string

	PreparedStatements map[string]*PreparedStatement
	planCache          *PlanCache
	resultCache        *ResultCache
	snapshotTxID       uint64
	maxPreparedStmts   int
	serverCtx          context.Context

	// RBAC: token and role for permission checks.
	token string
	role  string

	// Session variables (e.g. synchronous_commit)
	Variables map[string]string
}

// SessionConfig contains all parameters for creating a session.
type SessionConfig struct {
	Store            storage.StorageEngine
	Metrics          *metrics.Collector
	TxManager        *txmanager.Manager
	Broadcaster      *Broadcaster
	Embedder         ai.Embedder
	WAL              *wal.WAL
	AuthManager      *auth.Manager
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

// NewSessionWithConfig creates a session with full configuration.
func NewSessionWithConfig(cfg SessionConfig) *Session {
	id := nextSessionID()
	s := &Session{
		executor:           New(cfg.Store, cfg.Metrics, cfg.TxManager, cfg.Broadcaster),
		TxManager:          cfg.TxManager,
		Broadcaster:        cfg.Broadcaster,
		PreparedStatements: make(map[string]*PreparedStatement),
		planCache:          NewPlanCache(defaultPlanCacheSize),
		resultCache:        NewResultCache(defaultResultCacheSize, defaultResultCacheTTL),
		maxPreparedStmts:   defaultMaxPreparedStatements,
		id:                 id,
		Variables:          map[string]string{"synchronous_commit": "on"},
	}
	GlobalRegistry.RegisterSession(id, "", "", "", 0, nil)
	GlobalRegistry.UpdateQuery(id, "", StateIdle, 0)
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
	if cfg.AuthManager != nil {
		s.SetAuthManager(cfg.AuthManager)
	}
	return s
}

// SetMaxPreparedStatements sets the maximum number of prepared statements per session.
func (s *Session) SetMaxPreparedStatements(n int) {
	if n > 0 {
		s.maxPreparedStmts = n
	}
}

// ID returns the session's unique ID.
func (s *Session) ID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// SetID sets the session's unique ID and updates the registry.
func (s *Session) SetID(id uint64) {
	s.mu.Lock()
	oldID := s.id
	s.id = id
	user := s.user
	db := s.currentDB
	s.mu.Unlock()
	if oldID != id {
		GlobalRegistry.UnregisterSession(oldID)
		GlobalRegistry.RegisterSession(id, user, db, "", 0, nil)
		GlobalRegistry.UpdateQuery(id, "", StateIdle, 0)
	}
}

// User returns the session's username.
func (s *Session) User() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.user
}

// SetUser sets the session's username and updates the registry.
func (s *Session) SetUser(user string) {
	s.mu.Lock()
	s.user = user
	id := s.id
	db := s.currentDB
	s.mu.Unlock()
	GlobalRegistry.RegisterSession(id, user, db, "", 0, nil)
	GlobalRegistry.UpdateQuery(id, "", StateIdle, 0)
}

// SetResultCacheConfig configures result cache size and TTL.
func (s *Session) SetResultCacheConfig(size int, ttlSec int) {
	if size > 0 {
		s.resultCache = NewResultCache(size, time.Duration(ttlSec)*time.Second)
	}
}

// SetEmbedder connects an embedding provider for SEMANTIC_MATCH/AI_EMBED.
func (s *Session) SetEmbedder(emb ai.Embedder) {
	s.executor.SetEmbedder(emb)
}

// SetAuthManager connects the authentication manager for RBAC checks.
func (s *Session) SetAuthManager(m *auth.Manager) {
	s.executor.SetAuthManager(m)
}

// GetAuthManager returns the session's auth manager (may be nil).
func (s *Session) GetAuthManager() *auth.Manager {
	if s.executor == nil {
		return nil
	}
	s.executor.mu.RLock()
	defer s.executor.mu.RUnlock()
	return s.executor.authMgr
}

// SetWAL connects WAL for writing transaction operations.
func (s *Session) SetWAL(w *wal.WAL) {
	s.executor.SetWAL(w)
}

// SetQueryTimeout sets the query execution timeout.
func (s *Session) SetQueryTimeout(d time.Duration) {
	s.executor.SetQueryTimeout(d)
}

// Storage returns the underlying storage engine.
func (s *Session) Storage() storage.StorageEngine {
	return s.executor.storage
}

// Run executes a parsed statement directly, bypassing the normal Execute path.
// This is used for concurrent testing scenarios.
func (s *Session) Run(stmt parser.Statement) (*Result, error) {
	return s.executor.Run(stmt, s)
}

// SetMaxRows sets the maximum number of rows in SELECT results.
func (s *Session) SetMaxRows(n int) {
	s.executor.SetMaxRows(n)
}

func (s *Session) IsInTx() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive
}

// GetActiveTx returns the current transaction under lock.
// If there is no transaction — returns nil.
func (s *Session) GetActiveTx() *txmanager.Transaction {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveTx
}

func (s *Session) Execute(stmt parser.Statement) (*Result, error) {
	return s.executor.Run(stmt, s)
}

var globalASTCache = NewASTCache(10000)

// Parse parses the given SQL string, utilizing the global AST cache for faster processing.
// NOTE: We only cache DML/DQL queries, DDL might have side effects or be less recurrent.
func (s *Session) Parse(sql string) (parser.Statement, error) {
	if stmt := globalASTCache.Get(sql); stmt != nil {
		return stmt, nil
	}

	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}

	if stmt != nil {
		// Only cache SELECT, INSERT, UPDATE, DELETE
		switch stmt.(type) {
		case *parser.SelectStatement, *parser.InsertStatement, *parser.UpdateStatement, *parser.DeleteStatement:
			globalASTCache.Put(sql, stmt)
		}
	}

	return stmt, nil
}

func (s *Session) CurrentDatabase() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentDB
}

func (s *Session) SetVariable(name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Variables[name] = value
}

func (s *Session) GetVariable(name string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Variables[name]
}

func (s *Session) SetCurrentDatabase(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentDB = name
}

// SetSnapshotTxID sets the transaction ID for snapshot isolation in live queries.
func (s *Session) SetSnapshotTxID(txID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotTxID = txID
}

// SnapshotTxID returns the transaction snapshot ID (0 = no snapshot).
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

// SetServerContext sets the server context for query cancellation at shutdown.
func (s *Session) SetServerContext(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverCtx = ctx
}

// ServerContext returns the server context (or context.Background if not set).
func (s *Session) ServerContext() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.serverCtx != nil {
		return s.serverCtx
	}
	return context.Background()
}

// SetToken stores the bearer token for RBAC permission checks.
func (s *Session) SetToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

// GetToken returns the stored bearer token.
func (s *Session) GetToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// SetRole stores the user role (e.g. "admin", "writer", "reader").
func (s *Session) SetRole(role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.role = role
}

// GetRole returns the stored role.
func (s *Session) GetRole() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.role
}

// Close cleans up session resources.
// If there is an active transaction — rolls it back to avoid data loss
// and resource leaks (spill files, etc.).
func (s *Session) Close() {
	s.mu.Lock()
	id := s.id
	if s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive {
		s.ActiveTx.Rollback(s.Storage)
	}
	s.PreparedStatements = make(map[string]*PreparedStatement)
	s.ActiveTx = nil
	s.mu.Unlock()
	GlobalRegistry.UnregisterSession(id)
}

// Reset resets session state for reuse in the pool.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive {
		s.ActiveTx.Rollback(s.Storage)
	}
	s.ActiveTx = nil
	s.currentDB = ""
	s.snapshotTxID = 0
	s.token = ""
	s.role = ""
	s.PreparedStatements = make(map[string]*PreparedStatement)
	// Reset executor settings for pooled sessions
	if s.executor != nil {
		s.executor.SetMaxRows(0)
		s.executor.SetQueryTimeout(0)
	}
}

// LogAudit appends an entry to the table-based audit log.
func (s *Session) LogAudit(actor, action, target, detail string) {
	if s.AuditTable != nil {
		s.AuditTable.Append(audit.Entry{
			Actor:  actor,
			Action: action,
			Target: target,
			Detail: detail,
		})
	}
}

// ─── SessionInterface accessors (for types.SessionInterface) ────────────────

func (s *Session) GetAuditTable() *audit.TableLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AuditTable
}

func (s *Session) GetAuditLog() *logging.AuditLogger {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.AuditLog
}

func (s *Session) GetArchivePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ArchivePath
}

func (s *Session) GetTxManager() *txmanager.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TxManager
}

func (s *Session) GetSnapshotTxID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotTxID
}

func (s *Session) GetMaxRows() int {
	if s.executor == nil {
		return 0
	}
	s.executor.mu.RLock()
	defer s.executor.mu.RUnlock()
	return s.executor.maxRows
}

func (s *Session) GetExecutorMaxRows() int {
	return s.GetMaxRows()
}

func (s *Session) InvalidateResultCache(tableName string) {
	if s.resultCache != nil {
		s.resultCache.Invalidate(tableName)
	}
}

func (s *Session) InvalidatePlanCache(tableName string) {
	if s.planCache != nil {
		s.planCache.Invalidate(tableName)
	}
}

func (s *Session) GetResultCache() interface{} {
	return s.resultCache
}

func (s *Session) GetPlanCache() interface{} {
	return s.planCache
}
