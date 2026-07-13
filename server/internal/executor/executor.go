package executor

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/auth"
	_ "vaultdb/internal/executor/commands/audit"
	authcmd "vaultdb/internal/executor/commands/auth"
	_ "vaultdb/internal/executor/commands/ddl"
	_ "vaultdb/internal/executor/commands/dml"
	_ "vaultdb/internal/executor/commands/sel"
	txcmd "vaultdb/internal/executor/commands/tx"
	"vaultdb/internal/executor/parallel"
	"vaultdb/internal/executor/types"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

// Type aliases — concrete types now live in types/.
type (
	ExecutionContext    = types.ExecutionContext
	Command            = types.Command
	Result             = types.Result
	SubqueryRunner     = types.SubqueryRunner
	PreparedStatement  = types.PreparedStatement
)

// commandFactory is a function that creates a Command from a parser.Statement.
type commandFactory func(stmt parser.Statement) Command

// commandRegistry maps statement types to their factory functions.
var commandRegistry = map[reflect.Type]commandFactory{}

// registerCommand registers a command factory for a statement type.
func registerCommand(stmtType reflect.Type, factory commandFactory) {
	commandRegistry[stmtType] = factory
}

func init() {
	// Commands registered via subpackage blank imports (SELECT, BEGIN, COMMIT,
	// ROLLBACK, SAVEPOINT, ROLLBACK_TO_SAVEPOINT, RELEASE_SAVEPOINT, PREPARE,
	// EXECUTE_PREPARED, DEALLOCATE, EXPLAIN, HISTORY, SET_OPERATION,
	// CREATE_ROLE, DROP_ROLE, GRANT, REVOKE, REVOKE_TOKEN,
	// VERIFY_AUDIT_LOG, ARCHIVE_AUDIT_LOG).

	// Commands remaining in root (not yet extracted):
	registerCommand(reflect.TypeOf((*parser.CTEStatement)(nil)), func(s parser.Statement) Command { return &CTECommand{stmt: s.(*parser.CTEStatement)} })
}

type Executor struct {
	storage      storage.StorageEngine
	metrics      *metrics.Collector
	txm          *txmanager.Manager
	broadcaster  *Broadcaster
	embedder     ai.Embedder
	wal          *wal.WAL
	authMgr      *auth.Manager // RBAC: nil means permission checks disabled
	queryTimeout time.Duration
	maxRows      int
	parallel     parallel.ParallelConfig
	mu           sync.RWMutex
}

func New(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, b *Broadcaster) *Executor {
	// By default AI is not configured: SEMANTIC_MATCH/AI_EMBED return
	// a clear error instead of a silent mock result.
	return &Executor{storage: store, metrics: m, txm: txm, broadcaster: b, embedder: ai.NoopEmbedder{}}
}

// SetWAL connects WAL for write operations of transactions.
func (e *Executor) SetWAL(w *wal.WAL) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.wal = w
}

// SetEmbedder connects a real embedding provider.
func (e *Executor) SetEmbedder(emb ai.Embedder) {
	if emb != nil {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.embedder = emb
	}
}

// SetQueryTimeout sets the query execution timeout.
func (e *Executor) SetQueryTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queryTimeout = d
}

// SetMaxRows sets the maximum number of rows in SELECT results.
func (e *Executor) SetMaxRows(n int) {
	if n > 0 {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.maxRows = n
	}
}

// SetParallelConfig configures parallel query execution.
func (e *Executor) SetParallelConfig(cfg parallel.ParallelConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.parallel = cfg
}

// ParallelConfig returns the current parallel execution configuration.
func (e *Executor) ParallelConfig() parallel.ParallelConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.parallel
}

// SetAuthManager connects the authentication manager for RBAC checks.
func (e *Executor) SetAuthManager(m *auth.Manager) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.authMgr = m
}

// subqueryRunner implements SubqueryRunner by delegating to CommandFactory.
type subqueryRunner struct {
	executor *Executor
}

func (r *subqueryRunner) RunSubquery(ctx *ExecutionContext, stmt parser.Statement) (*Result, error) {
	cmd, err := ctx.CreateCommand(stmt)
	if err != nil {
		return nil, err
	}
	return cmd.Execute(ctx)
}

func (e *Executor) Run(stmt parser.Statement, sess *Session) (*Result, error) {
	start := time.Now()

	e.mu.RLock()
	queryTimeout := e.queryTimeout
	embedder := e.embedder
	wal := e.wal
	parallelCfg := e.parallel
	authMgr := e.authMgr
	e.mu.RUnlock()

	// RBAC permission check: if auth is enabled and the session carries a token,
	// verify the role is allowed to perform this operation.
	if authMgr != nil && authMgr.Enabled() {
		token := sess.GetToken()
		if token != "" {
			op := stmt.StatementType()
			if !authMgr.CheckPermission(token, op) {
				return nil, fmt.Errorf("permission denied: role %q cannot execute %s", sess.GetRole(), op)
			}
		}
	}

	queryCtx := sess.ServerContext()
	if queryTimeout > 0 {
		var cancel context.CancelFunc
		queryCtx, cancel = context.WithTimeout(queryCtx, queryTimeout)
		defer cancel()
	}

	ctx := &ExecutionContext{
		Storage:      e.storage,
		Session:      sess,
		Metrics:      e.metrics,
		TxManager:    e.txm,
		Broadcaster:  e.broadcaster,
		Embedder:     embedder,
		WAL:          wal,
		Ctx:          queryCtx,
		SnapshotTxID: sess.SnapshotTxID(),
		Parallel:     parallelCfg,
		RunSubquery:  &subqueryRunner{executor: e},
		CreateCommand: CommandFactory,
	}

	cmd, err := ctx.CreateCommand(stmt)
	if err != nil {
		return nil, err
	}
	result, err := cmd.Execute(ctx)

	if e.metrics != nil {
		duration := time.Since(start)
		queryType := strings.ToLower(stmt.StatementType())
		status := "ok"
		if err != nil {
			status = "error"
		}
		e.metrics.RecordQuery(queryType, status, duration)
	}

	return result, err
}

// CommandFactory creates a Command from a parser.Statement using the registry.
// First checks the local reflect.Type-based registry, then falls back to the
// types package's string-name registry (used by subpackages like commands/dml).
func CommandFactory(stmt parser.Statement) (Command, error) {
	if stmt == nil {
		return nil, fmt.Errorf("nil statement")
	}
	stmtType := reflect.TypeOf(stmt)
	if factory, ok := commandRegistry[stmtType]; ok {
		return factory(stmt), nil
	}
	// Fallback: check types registry by statement name
	typesFactory := types.GetCommandFactory()(stmt.StatementType(), stmt)
	if typesFactory != nil {
		return typesFactory, nil
	}
	return nil, fmt.Errorf("unknown statement type: %T", stmt)
}

// BindParams re-exports types.BindParams for backward compatibility.
var BindParams = types.BindParams

// GetRoleGrants re-exports commands/auth.GetRoleGrants for backward compatibility.
var GetRoleGrants = authcmd.GetRoleGrants

// Undo functions re-exported for test backward compatibility.
var undoInsert = txcmd.UndoInsert
var undoUpdate = txcmd.UndoUpdate
var undoDelete = txcmd.UndoDelete
