package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

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
	CurrentDB   *string
	Session     *Session
	Metrics     *metrics.Collector
	TxManager   *txmanager.Manager
	Broadcaster *Broadcaster
	Embedder    ai.Embedder
	WAL         *wal.WAL
	Stats       *StatisticsCollector
	Ctx         context.Context

	// WindowCols maps each window function expression to the synthetic result
	// column it was materialized into, so several window functions in one
	// query project their own values.
	WindowCols map[*parser.WindowFunctionExpr]string
}

type Executor struct {
	storage      storage.StorageEngine
	metrics      *metrics.Collector
	txm          *txmanager.Manager
	broadcaster  *Broadcaster
	embedder     ai.Embedder
	wal          *wal.WAL
	queryTimeout time.Duration
}

func New(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, b *Broadcaster) *Executor {
	// По умолчанию AI не настроен: SEMANTIC_MATCH/AI_EMBED возвращают
	// понятную ошибку, а не тихий mock-результат.
	return &Executor{storage: store, metrics: m, txm: txm, broadcaster: b, embedder: ai.NoopEmbedder{}}
}

// SetWAL подключает WAL для записи операций транзакций.
func (e *Executor) SetWAL(w *wal.WAL) {
	e.wal = w
}

// SetEmbedder подключает реальный embedding-провайдер.
func (e *Executor) SetEmbedder(emb ai.Embedder) {
	if emb != nil {
		e.embedder = emb
	}
}

// SetQueryTimeout задаёт таймаут на выполнение запроса.
func (e *Executor) SetQueryTimeout(d time.Duration) {
	e.queryTimeout = d
}

func (e *Executor) Run(stmt parser.Statement, sess *Session) (*Result, error) {
	start := time.Now()
	cmd, err := CommandFactory(stmt)
	if err != nil {
		return nil, err
	}

	var queryCtx context.Context
	if e.queryTimeout > 0 {
		var cancel context.CancelFunc
		queryCtx, cancel = context.WithTimeout(context.Background(), e.queryTimeout)
		defer cancel()
	}

	ctx := &ExecutionContext{
		Storage:     e.storage,
		CurrentDB:   &sess.currentDB,
		Session:     sess,
		Metrics:     e.metrics,
		TxManager:   e.txm,
		Broadcaster: e.broadcaster,
		Embedder:    e.embedder,
		WAL:         e.wal,
		Ctx:         queryCtx,
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

// TODO: Replace type switch with a registry map for cleaner extensibility.
// Pattern: var commandRegistry = map[reflect.Type]func(parser.Statement) Command{}
func CommandFactory(stmt parser.Statement) (Command, error) {
	switch s := stmt.(type) {
	case *parser.CreateDatabaseStatement:
		return &CreateDatabaseCommand{stmt: s}, nil
	case *parser.DropDatabaseStatement:
		return &DropDatabaseCommand{stmt: s}, nil
	case *parser.AlterTableStatement:
		return &AlterTableCommand{stmt: s}, nil
	case *parser.UseDatabaseStatement:
		return &UseDatabaseCommand{stmt: s}, nil
	case *parser.ShowDatabasesStatement:
		return &ShowDatabasesCommand{stmt: s}, nil
	case *parser.ShowTablesStatement:
		return &ShowTablesCommand{stmt: s}, nil
	case *parser.DescribeTableStatement:
		return &DescribeTableCommand{stmt: s}, nil
	case *parser.CreateTableStatement:
		return &CreateTableCommand{stmt: s}, nil
	case *parser.DropTableStatement:
		return &DropTableCommand{stmt: s}, nil
	case *parser.SelectStatement:
		return &SelectCommand{stmt: s}, nil
	case *parser.ExplainStatement:
		return &ExplainCommand{stmt: s}, nil
	case *parser.HistoryStatement:
		return &HistoryCommand{stmt: s}, nil
	case *parser.InsertStatement:
		return &InsertCommand{stmt: s}, nil
	case *parser.UpdateStatement:
		return &UpdateCommand{stmt: s}, nil
	case *parser.DeleteStatement:
		return &DeleteCommand{stmt: s}, nil
	case *parser.VacuumStatement:
		return &VacuumCommand{stmt: s}, nil
	case *parser.CreateIndexStatement:
		return &CreateIndexCommand{stmt: s}, nil
	case *parser.DropIndexStatement:
		return &DropIndexCommand{stmt: s}, nil
	case *parser.ShowIndexesStatement:
		return &ShowIndexesCommand{stmt: s}, nil
	case *parser.BeginStatement:
		return &BeginCommand{stmt: s}, nil
	case *parser.CommitStatement:
		return &CommitCommand{stmt: s}, nil
	case *parser.RollbackStatement:
		return &RollbackCommand{stmt: s}, nil
	case *parser.PrepareStatement:
		return &PrepareCommand{stmt: s}, nil
	case *parser.ExecuteStatement:
		return &ExecutePreparedCommand{stmt: s}, nil
	case *parser.DeallocateStatement:
		return &DeallocateCommand{stmt: s}, nil
	case *parser.SetOperationStatement:
		return &SetOperationCommand{stmt: s}, nil
	case *parser.MigrationStatement:
		return &MigrationCommand{stmt: s}, nil
	case *parser.CreatePolicyStatement:
		return &CreatePolicyCommand{stmt: s}, nil
	case *parser.EnableRlsStatement:
		return &EnableRlsCommand{stmt: s}, nil
	case *parser.CTEStatement:
		return &CTECommand{stmt: s}, nil
	case *parser.TruncateStatement:
		return &TruncateCommand{stmt: s}, nil
	case *parser.MergeStatement:
		return &MergeCommand{stmt: s}, nil
	case *parser.SavepointStatement:
		return &SavepointCommand{stmt: s}, nil
	case *parser.RollbackToSavepointStatement:
		return &RollbackToSavepointCommand{stmt: s}, nil
	case *parser.ReleaseSavepointStatement:
		return &ReleaseSavepointCommand{stmt: s}, nil
	case *parser.CreateViewStatement:
		return &CreateViewCommand{stmt: s}, nil
	case *parser.DropViewStatement:
		return &DropViewCommand{stmt: s}, nil
	case *parser.CreateTriggerStatement:
		return &CreateTriggerCommand{stmt: s}, nil
	case *parser.DropTriggerStatement:
		return &DropTriggerCommand{stmt: s}, nil
	case *parser.CreateFunctionStatement:
		return &CreateFunctionCommand{stmt: s}, nil
	case *parser.DropFunctionStatement:
		return &DropFunctionCommand{stmt: s}, nil
	case *parser.CreateProcedureStatement:
		return &CreateProcedureCommand{stmt: s}, nil
	case *parser.DropProcedureStatement:
		return &DropProcedureCommand{stmt: s}, nil
	case *parser.CallProcedureStatement:
		return &CallProcedureCommand{stmt: s}, nil
	default:
		return nil, fmt.Errorf("unknown statement type: %T", stmt)
	}
}
