package executor

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
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
	// Register all command factories
	registerCommand(reflect.TypeOf((*parser.CreateDatabaseStatement)(nil)), func(s parser.Statement) Command {
		return &CreateDatabaseCommand{stmt: s.(*parser.CreateDatabaseStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.DropDatabaseStatement)(nil)), func(s parser.Statement) Command { return &DropDatabaseCommand{stmt: s.(*parser.DropDatabaseStatement)} })
	registerCommand(reflect.TypeOf((*parser.AlterTableStatement)(nil)), func(s parser.Statement) Command { return &AlterTableCommand{stmt: s.(*parser.AlterTableStatement)} })
	registerCommand(reflect.TypeOf((*parser.UseDatabaseStatement)(nil)), func(s parser.Statement) Command { return &UseDatabaseCommand{stmt: s.(*parser.UseDatabaseStatement)} })
	registerCommand(reflect.TypeOf((*parser.ShowDatabasesStatement)(nil)), func(s parser.Statement) Command {
		return &ShowDatabasesCommand{stmt: s.(*parser.ShowDatabasesStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.ShowTablesStatement)(nil)), func(s parser.Statement) Command { return &ShowTablesCommand{stmt: s.(*parser.ShowTablesStatement)} })
	registerCommand(reflect.TypeOf((*parser.DescribeTableStatement)(nil)), func(s parser.Statement) Command {
		return &DescribeTableCommand{stmt: s.(*parser.DescribeTableStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.CreateTableStatement)(nil)), func(s parser.Statement) Command { return &CreateTableCommand{stmt: s.(*parser.CreateTableStatement)} })
	registerCommand(reflect.TypeOf((*parser.DropTableStatement)(nil)), func(s parser.Statement) Command { return &DropTableCommand{stmt: s.(*parser.DropTableStatement)} })
	registerCommand(reflect.TypeOf((*parser.SelectStatement)(nil)), func(s parser.Statement) Command { return &SelectCommand{stmt: s.(*parser.SelectStatement)} })
	registerCommand(reflect.TypeOf((*parser.ExplainStatement)(nil)), func(s parser.Statement) Command { return &ExplainCommand{stmt: s.(*parser.ExplainStatement)} })
	registerCommand(reflect.TypeOf((*parser.HistoryStatement)(nil)), func(s parser.Statement) Command { return &HistoryCommand{stmt: s.(*parser.HistoryStatement)} })
	registerCommand(reflect.TypeOf((*parser.InsertStatement)(nil)), func(s parser.Statement) Command { return &InsertCommand{stmt: s.(*parser.InsertStatement)} })
	registerCommand(reflect.TypeOf((*parser.UpdateStatement)(nil)), func(s parser.Statement) Command { return &UpdateCommand{stmt: s.(*parser.UpdateStatement)} })
	registerCommand(reflect.TypeOf((*parser.DeleteStatement)(nil)), func(s parser.Statement) Command { return &DeleteCommand{stmt: s.(*parser.DeleteStatement)} })
	registerCommand(reflect.TypeOf((*parser.VacuumStatement)(nil)), func(s parser.Statement) Command { return &VacuumCommand{stmt: s.(*parser.VacuumStatement)} })
	registerCommand(reflect.TypeOf((*parser.CreateIndexStatement)(nil)), func(s parser.Statement) Command { return &CreateIndexCommand{stmt: s.(*parser.CreateIndexStatement)} })
	registerCommand(reflect.TypeOf((*parser.DropIndexStatement)(nil)), func(s parser.Statement) Command { return &DropIndexCommand{stmt: s.(*parser.DropIndexStatement)} })
	registerCommand(reflect.TypeOf((*parser.ShowIndexesStatement)(nil)), func(s parser.Statement) Command { return &ShowIndexesCommand{stmt: s.(*parser.ShowIndexesStatement)} })
	registerCommand(reflect.TypeOf((*parser.BeginStatement)(nil)), func(s parser.Statement) Command { return &BeginCommand{stmt: s.(*parser.BeginStatement)} })
	registerCommand(reflect.TypeOf((*parser.CommitStatement)(nil)), func(s parser.Statement) Command { return &CommitCommand{stmt: s.(*parser.CommitStatement)} })
	registerCommand(reflect.TypeOf((*parser.RollbackStatement)(nil)), func(s parser.Statement) Command { return &RollbackCommand{stmt: s.(*parser.RollbackStatement)} })
	registerCommand(reflect.TypeOf((*parser.PrepareStatement)(nil)), func(s parser.Statement) Command { return &PrepareCommand{stmt: s.(*parser.PrepareStatement)} })
	registerCommand(reflect.TypeOf((*parser.ExecuteStatement)(nil)), func(s parser.Statement) Command { return &ExecutePreparedCommand{stmt: s.(*parser.ExecuteStatement)} })
	registerCommand(reflect.TypeOf((*parser.DeallocateStatement)(nil)), func(s parser.Statement) Command { return &DeallocateCommand{stmt: s.(*parser.DeallocateStatement)} })
	registerCommand(reflect.TypeOf((*parser.SetOperationStatement)(nil)), func(s parser.Statement) Command { return &SetOperationCommand{stmt: s.(*parser.SetOperationStatement)} })
	registerCommand(reflect.TypeOf((*parser.MigrationStatement)(nil)), func(s parser.Statement) Command { return &MigrationCommand{stmt: s.(*parser.MigrationStatement)} })
	registerCommand(reflect.TypeOf((*parser.CreatePolicyStatement)(nil)), func(s parser.Statement) Command { return &CreatePolicyCommand{stmt: s.(*parser.CreatePolicyStatement)} })
	registerCommand(reflect.TypeOf((*parser.EnableRlsStatement)(nil)), func(s parser.Statement) Command { return &EnableRlsCommand{stmt: s.(*parser.EnableRlsStatement)} })
	registerCommand(reflect.TypeOf((*parser.CTEStatement)(nil)), func(s parser.Statement) Command { return &CTECommand{stmt: s.(*parser.CTEStatement)} })
	registerCommand(reflect.TypeOf((*parser.TruncateStatement)(nil)), func(s parser.Statement) Command { return &TruncateCommand{stmt: s.(*parser.TruncateStatement)} })
	registerCommand(reflect.TypeOf((*parser.MergeStatement)(nil)), func(s parser.Statement) Command { return &MergeCommand{stmt: s.(*parser.MergeStatement)} })
	registerCommand(reflect.TypeOf((*parser.SavepointStatement)(nil)), func(s parser.Statement) Command { return &SavepointCommand{stmt: s.(*parser.SavepointStatement)} })
	registerCommand(reflect.TypeOf((*parser.RollbackToSavepointStatement)(nil)), func(s parser.Statement) Command {
		return &RollbackToSavepointCommand{stmt: s.(*parser.RollbackToSavepointStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.ReleaseSavepointStatement)(nil)), func(s parser.Statement) Command {
		return &ReleaseSavepointCommand{stmt: s.(*parser.ReleaseSavepointStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.CreateViewStatement)(nil)), func(s parser.Statement) Command { return &CreateViewCommand{stmt: s.(*parser.CreateViewStatement)} })
	registerCommand(reflect.TypeOf((*parser.DropViewStatement)(nil)), func(s parser.Statement) Command { return &DropViewCommand{stmt: s.(*parser.DropViewStatement)} })
	registerCommand(reflect.TypeOf((*parser.CreateTriggerStatement)(nil)), func(s parser.Statement) Command {
		return &CreateTriggerCommand{stmt: s.(*parser.CreateTriggerStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.DropTriggerStatement)(nil)), func(s parser.Statement) Command { return &DropTriggerCommand{stmt: s.(*parser.DropTriggerStatement)} })
	registerCommand(reflect.TypeOf((*parser.CreateFunctionStatement)(nil)), func(s parser.Statement) Command {
		return &CreateFunctionCommand{stmt: s.(*parser.CreateFunctionStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.DropFunctionStatement)(nil)), func(s parser.Statement) Command { return &DropFunctionCommand{stmt: s.(*parser.DropFunctionStatement)} })
	registerCommand(reflect.TypeOf((*parser.CreateProcedureStatement)(nil)), func(s parser.Statement) Command {
		return &CreateProcedureCommand{stmt: s.(*parser.CreateProcedureStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.DropProcedureStatement)(nil)), func(s parser.Statement) Command {
		return &DropProcedureCommand{stmt: s.(*parser.DropProcedureStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.CallProcedureStatement)(nil)), func(s parser.Statement) Command {
		return &CallProcedureCommand{stmt: s.(*parser.CallProcedureStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.ShowEncryptionStatusStatement)(nil)), func(s parser.Statement) Command {
		return &ShowEncryptionStatusCommand{stmt: s.(*parser.ShowEncryptionStatusStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.VerifyAuditLogStatement)(nil)), func(s parser.Statement) Command {
		return &VerifyAuditLogCommand{stmt: s.(*parser.VerifyAuditLogStatement)}
	})
	registerCommand(reflect.TypeOf((*parser.CopyStatement)(nil)), func(s parser.Statement) Command {
		stmt := s.(*parser.CopyStatement)
		if stmt.IsFrom {
			return &CopyFromCommand{stmt: stmt}
		}
		return &CopyToCommand{stmt: stmt}
	})
}

// Command is the Command pattern abstraction.
type Command interface {
	Execute(ctx *ExecutionContext) (*Result, error)
}

// Result is a uniform executor output for all statements.
type Result struct {
	Type     string
	Columns  []string
	Rows     [][]string
	Schema   *storage.TableSchema
	Affected int
	Message  string
	AsOfNote string
}

// ExecutionContext carries mutable session state and dependencies.
type ExecutionContext struct {
	Storage     storage.StorageEngine
	Session     *Session
	Metrics     *metrics.Collector
	TxManager   *txmanager.Manager
	Broadcaster *Broadcaster
	Embedder    ai.Embedder
	WAL         *wal.WAL
	Stats       *StatisticsCollector
	Ctx         context.Context

	// ColumnIndex caches lowercased column name → position for O(1) lookups.
	// Built once per query from the schema; used by resolveColumn.
	ColumnIndex map[string]int

	// WindowCols maps each window function expression to the synthetic result
	// column it was materialized into, so several window functions in one
	// query project their own values.
	WindowCols map[*parser.WindowFunctionExpr]string

	// SnapshotTxID enables snapshot isolation: when set, reads use this txID
	// to determine visibility of rows (0 = current).
	SnapshotTxID uint64

	// OldRow/NewRow hold pre/post mutation rows for RETURNING clause
	// with old.* / new.* syntax.
	OldRow storage.Row
	NewRow storage.Row

	// InCommitApply true, пока выполняется applyOps внутри Commit. В этот момент
	// commit-локи нужных таблиц уже захвачены (sync.Mutex не реентрантный),
	// поэтому autocommit-обёртка mutateUnderTableLock НЕ должна брать их повторно
	// — иначе self-deadlock (Bug #2, deadlock guard).
	InCommitApply bool

	// Parallel holds the parallel execution configuration for this query.
	Parallel ParallelConfig

	// triggerDepth tracks recursive trigger invocation depth.
	// Incremented before executeTriggerBody, decremented after.
	triggerDepth int
}

type Executor struct {
	storage       storage.StorageEngine
	metrics       *metrics.Collector
	txm           *txmanager.Manager
	broadcaster   *Broadcaster
	embedder      ai.Embedder
	wal           *wal.WAL
	queryTimeout  time.Duration
	maxRows       int
	parallel      ParallelConfig
	mu            sync.RWMutex
}

func New(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, b *Broadcaster) *Executor {
	// По умолчанию AI не настроен: SEMANTIC_MATCH/AI_EMBED возвращают
	// понятную ошибку, а не тихий mock-результат.
	return &Executor{storage: store, metrics: m, txm: txm, broadcaster: b, embedder: ai.NoopEmbedder{}}
}

// SetWAL подключает WAL для записи операций транзакций.
func (e *Executor) SetWAL(w *wal.WAL) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.wal = w
}

// SetEmbedder подключает реальный embedding-провайдер.
func (e *Executor) SetEmbedder(emb ai.Embedder) {
	if emb != nil {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.embedder = emb
	}
}

// SetQueryTimeout задаёт таймаут на выполнение запроса.
func (e *Executor) SetQueryTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.queryTimeout = d
}

// SetMaxRows задаёт максимальное количество строк в результате SELECT.
func (e *Executor) SetMaxRows(n int) {
	if n > 0 {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.maxRows = n
	}
}

// SetParallelConfig настраивает параллельное выполнение запросов.
func (e *Executor) SetParallelConfig(cfg ParallelConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.parallel = cfg
}

// ParallelConfig возвращает текущую конфигурацию параллельного выполнения.
func (e *Executor) ParallelConfig() ParallelConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.parallel
}

func (e *Executor) Run(stmt parser.Statement, sess *Session) (*Result, error) {
	start := time.Now()
	cmd, err := CommandFactory(stmt)
	if err != nil {
		return nil, err
	}

	e.mu.RLock()
	queryTimeout := e.queryTimeout
	embedder := e.embedder
	wal := e.wal
	parallelCfg := e.parallel
	e.mu.RUnlock()

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
func CommandFactory(stmt parser.Statement) (Command, error) {
	stmtType := reflect.TypeOf(stmt)
	if factory, ok := commandRegistry[stmtType]; ok {
		return factory(stmt), nil
	}
	return nil, fmt.Errorf("unknown statement type: %T", stmt)
}
