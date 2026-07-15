package types

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/auth"
	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/audit"
	"vaultdb/internal/core/executor/eval"
	"vaultdb/internal/core/executor/optimizer"
	"vaultdb/internal/core/executor/parallel"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/core/wal"
	"vaultdb/internal/logging"
)

// ─── Result ─────────────────────────────────────────────────────────────────

// Result is a uniform executor output for all statements.
type Result struct {
	Type        string
	Columns     []string
	Rows        [][]string
	Schema      *storage.TableSchema
	Affected    int
	Message     string
	AsOfNote    string
	RowsScanned int
}

// ─── Core Interfaces ────────────────────────────────────────────────────────

// SubqueryRunner allows subpackages to execute subqueries without importing the command layer.
type SubqueryRunner interface {
	RunSubquery(ctx *ExecutionContext, stmt parser.Statement) (*Result, error)
}

// Command is the Command pattern abstraction.
type Command interface {
	Execute(ctx *ExecutionContext) (*Result, error)
}

// BroadcasterInterface is the minimal interface commands need from Broadcaster.
type BroadcasterInterface interface {
	NotifyTableChanged(dbName, tableName string, ctx *ExecutionContext)
}

// SessionInterface is the minimal interface commands need from Session.
type SessionInterface interface {
	IsInTx() bool
	GetActiveTx() *txmanager.Transaction
	SetActiveTx(tx *txmanager.Transaction)
	ClearActiveTx()
	CurrentDatabase() string
	SetCurrentDatabase(name string)
	Execute(stmt parser.Statement) (*Result, error)
	LogAudit(actor, action, target, detail string)
	GetPreparedStatement(name string) (*PreparedStatement, bool)
	SetPreparedStatement(name string, ps *PreparedStatement) error
	DeletePreparedStatement(name string)
	GetAuthManager() *auth.Manager
	GetAuditLog() *logging.AuditLogger
	GetAuditTable() *audit.TableLog
	GetArchivePath() string
	GetExecutorMaxRows() int
	GetTxManager() *txmanager.Manager
	InvalidateResultCache(tableName string)
	InvalidatePlanCache(tableName string)
	GetResultCache() interface{}
	GetPlanCache() interface{}
	GetSnapshotTxID() uint64
	GetMaxRows() int
}

// PreparedStatement holds a prepared statement.
type PreparedStatement struct {
	Name  string
	Query parser.Statement
}

// ─── ExecutionContext ───────────────────────────────────────────────────────

// ExecutionContext carries mutable session state and dependencies.
type ExecutionContext struct {
	Storage     storage.StorageEngine
	Session     SessionInterface
	Metrics     *metrics.Collector
	TxManager   *txmanager.Manager
	Broadcaster BroadcasterInterface
	Embedder    ai.Embedder
	WAL         *wal.WAL
	Stats       *optimizer.StatisticsCollector
	Ctx         context.Context

	// ColumnIndex caches lowercased column name → position for O(1) lookups.
	ColumnIndex map[string]int

	// WindowCols maps each window function expression to the synthetic result
	// column it was materialized into.
	WindowCols map[*parser.WindowFunctionExpr]string

	// SnapshotTxID enables snapshot isolation: when set, reads use this txID
	// to determine visibility of rows (0 = current).
	SnapshotTxID uint64

	// OldRow/NewRow hold pre/post mutation rows for RETURNING clause
	// with old.* / new.* syntax.
	OldRow storage.Row
	NewRow storage.Row

	// InCommitApply true while applyOps is running inside Commit.
	InCommitApply bool

	// Parallel holds the parallel execution configuration for this query.
	Parallel parallel.ParallelConfig

	// triggerDepth tracks recursive trigger invocation depth.
	triggerDepth int

	// FtsQuery holds the search query extracted from a WHERE ... FTS_MATCH/MATCH
	// predicate.
	FtsQuery string

	// RunSubquery allows eval/functions to execute subqueries without importing CommandFactory.
	RunSubquery SubqueryRunner

	// CreateCommand is a factory injected at context creation time.
	CreateCommand func(stmt parser.Statement) (Command, error)
}

// GetEmbedder implements eval.EmbedderProvider.
func (ctx *ExecutionContext) GetEmbedder() ai.Embedder { return ctx.Embedder }

// GetGoContext implements eval.EmbedderProvider.
func (ctx *ExecutionContext) GetGoContext() context.Context { return ctx.Ctx }

// SetColumnIndex implements eval.ColumnIndexProvider.
func (ctx *ExecutionContext) SetColumnIndex(idx map[string]int) { ctx.ColumnIndex = idx }

// TriggerDepth returns the current trigger invocation depth.
func (ctx *ExecutionContext) TriggerDepth() int { return ctx.triggerDepth }

// SetTriggerDepth sets the trigger invocation depth.
func (ctx *ExecutionContext) SetTriggerDepth(d int) { ctx.triggerDepth = d }

// ─── Command Registry ───────────────────────────────────────────────────────

// CommandFactory creates a Command from a parser.Statement.
type CommandFactory func(stmt parser.Statement) Command

var commandRegistry = map[string]CommandFactory{}

// RegisterCommand registers a command factory by statement name.
func RegisterCommand(name string, factory CommandFactory) {
	commandRegistry[name] = factory
}

// System view function hooks to avoid circular imports between executor and subpackages.
var (
	GetPGStatActivityRowsFunc func() []storage.Row
	GetPGLocksRowsFunc        func(rowLocks *storage.RowLockManager) []storage.Row
	KillSessionFunc           func(id uint64) bool
)

// GetCommandFactory returns a factory that looks up commands by statement name.
// The returned function takes a parser.Statement and a name; it delegates to the
// registered factory for that name. If no factory is registered, returns nil.
func GetCommandFactory() func(name string, stmt parser.Statement) Command {
	return func(name string, stmt parser.Statement) Command {
		if factory, ok := commandRegistry[name]; ok {
			return factory(stmt)
		}
		return nil
	}
}

// ─── Database / Projection Helpers ──────────────────────────────────────────

// RequireCurrentDB returns the current database name or an error if none is selected.
func RequireCurrentDB(ctx *ExecutionContext) (string, error) {
	db := ctx.Session.CurrentDatabase()
	if strings.TrimSpace(db) == "" {
		return "", fmt.Errorf("no active database selected; use USE <database>; first")
	}
	return db, nil
}

// ResolveDatabase returns the requested database if it exists, or falls back to the current one.
func ResolveDatabase(ctx *ExecutionContext, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return RequireCurrentDB(ctx)
	}
	if !ctx.Storage.DatabaseExists(requested) {
		return "", fmt.Errorf("database '%s' does not exist", requested)
	}
	return requested, nil
}

// ResolveProjection builds column index and name slices for a SELECT projection.
func ResolveProjection(schema *storage.TableSchema, requested []string) ([]int, []string, error) {
	if len(requested) == 0 {
		indices := make([]int, len(schema.Columns))
		columns := make([]string, len(schema.Columns))
		for i, col := range schema.Columns {
			indices[i] = i
			columns[i] = col.Name
		}
		return indices, columns, nil
	}

	columnIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = i
	}

	indices := make([]int, 0, len(requested))
	columns := make([]string, 0, len(requested))
	for _, name := range requested {
		idx, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, nil, fmt.Errorf("unknown column '%s'", name)
		}
		indices = append(indices, idx)
		columns = append(columns, schema.Columns[idx].Name)
	}

	return indices, columns, nil
}

// EnsureColumnIndex lazily builds or refreshes ctx.ColumnIndex when the schema changes.
func EnsureColumnIndex(ctx *ExecutionContext, schema *storage.TableSchema) {
	if ctx == nil || schema == nil {
		return
	}
	ctx.ColumnIndex = eval.BuildColumnIndex(schema)
}

// ─── Result Cache Key ───────────────────────────────────────────────────────

// ResultCacheKey builds a cache key for a SELECT statement.
func ResultCacheKey(stmt *parser.SelectStatement, dbName string) string {
	key := dbName + ":"
	if stmt.TableName != "" {
		key += stmt.TableName
	}
	for _, col := range stmt.Columns {
		key += ":" + FormatSelectColumnForCache(col)
	}
	if stmt.Where != nil {
		key += ":W:" + FormatExpressionForCache(stmt.Where)
	}
	if len(stmt.GroupBy) > 0 {
		key += ":GB"
	}
	if stmt.Having != nil {
		key += ":H"
	}
	if len(stmt.OrderBy) > 0 {
		key += ":O"
		for _, ob := range stmt.OrderBy {
			key += FormatExpressionForCache(ob.Expr) + ob.Direction
		}
	}
	if stmt.HasLimit {
		key += fmt.Sprintf(":L%d", stmt.Limit)
	}
	if stmt.LimitExpr != nil {
		key += ":LE:" + FormatExpressionForCache(stmt.LimitExpr)
	}
	if stmt.HasOffset {
		key += fmt.Sprintf(":OF%d", stmt.Offset)
	}
	if stmt.OffsetExpr != nil {
		key += ":OE:" + FormatExpressionForCache(stmt.OffsetExpr)
	}
	if stmt.Distinct {
		key += ":D"
	}
	if stmt.AsOf != nil {
		if stmt.AsOf.UseVersion {
			key += fmt.Sprintf(":ASOFv%d", stmt.AsOf.Version)
		} else {
			key += ":ASOF:" + stmt.AsOf.Timestamp
		}
	}
	return key
}

// FormatSelectColumnForCache formats a SELECT column for cache key generation.
func FormatSelectColumnForCache(col parser.SelectColumn) string {
	if col.Alias != "" {
		return "A" + col.Alias
	}
	return FormatExpressionForCache(col.Expr)
}

// FormatExpressionForCache formats an expression for cache key generation.
func FormatExpressionForCache(expr parser.Expression) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		return FormatExpressionForCache(e.Left) + e.Operator + FormatExpressionForCache(e.Right)
	case *parser.AndExpr:
		return FormatExpressionForCache(e.Left) + "AND" + FormatExpressionForCache(e.Right)
	case *parser.OrExpr:
		return FormatExpressionForCache(e.Left) + "OR" + FormatExpressionForCache(e.Right)
	case *parser.NotExpr:
		return "NOT" + FormatExpressionForCache(e.Expr)
	case *parser.ColumnRef:
		return e.Name
	case *parser.FunctionCall:
		args := ""
		for i, arg := range e.Args {
			if i > 0 {
				args += ","
			}
			args += FormatExpressionForCache(arg)
		}
		return e.Name + "(" + args + ")"
	case *parser.AggregateExpr:
		args := ""
		for i, arg := range e.Args {
			if i > 0 {
				args += ","
			}
			args += FormatExpressionForCache(arg)
		}
		prefix := ""
		if e.Distinct {
			prefix = "DISTINCT"
		}
		return e.Name + "(" + prefix + args + ")"
	case *parser.WindowFunctionExpr:
		return "WIN:" + e.FuncName
	case *parser.WindowExpr:
		return "WIN:" + e.Function
	case parser.Value:
		return FormatValueForCache(e)
	case *parser.Value:
		return FormatValueForCache(*e)
	default:
		return fmt.Sprintf("E%T", expr)
	}
}

// FormatValueForCache formats a parser.Value for cache key generation.
func FormatValueForCache(v parser.Value) string {
	switch v.Type {
	case "int":
		return fmt.Sprintf("%d", v.IntVal)
	case "float":
		return fmt.Sprintf("%g", v.FltVal)
	case "string":
		return v.StrVal
	case "bool":
		if v.BoolVal {
			return "T"
		}
		return "F"
	default:
		return "?"
	}
}

// ─── Expression to SQL ──────────────────────────────────────────────────────

// ExprToSQL converts a parser expression back to SQL text for storage.
func ExprToSQL(expr parser.Expression) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		return e.Name
	case parser.Value:
		return FormatValueForExpr(e)
	case *parser.Value:
		return FormatValueForExpr(*e)
	case *parser.BinaryExpr:
		return "(" + ExprToSQL(e.Left) + " " + e.Operator + " " + ExprToSQL(e.Right) + ")"
	case *parser.AndExpr:
		return "(" + ExprToSQL(e.Left) + " AND " + ExprToSQL(e.Right) + ")"
	case *parser.OrExpr:
		return "(" + ExprToSQL(e.Left) + " OR " + ExprToSQL(e.Right) + ")"
	case *parser.NotExpr:
		return "(NOT " + ExprToSQL(e.Expr) + ")"
	case *parser.InExpr:
		args := make([]string, len(e.Right))
		for i, a := range e.Right {
			args[i] = ExprToSQL(a)
		}
		op := " IN "
		if e.Not {
			op = " NOT IN "
		}
		return "(" + ExprToSQL(e.Left) + op + "(" + strings.Join(args, ", ") + "))"
	case *parser.BetweenExpr:
		op := " BETWEEN "
		if e.Not {
			op = " NOT BETWEEN "
		}
		return "(" + ExprToSQL(e.Expr) + op + ExprToSQL(e.Lower) + " AND " + ExprToSQL(e.Upper) + ")"
	case *parser.CastExpr:
		return "CAST(" + ExprToSQL(e.Expr) + " AS " + e.TargetType + ")"
	case *parser.JsonPathExpr:
		return ExprToSQL(e.Left) + "->>'" + e.Path + "'"
	case *parser.JSONAccess:
		return ExprToSQL(e.Expr) + " " + e.Operator + " " + ExprToSQL(e.Argument)
	default:
		return fmt.Sprintf("%v", expr)
	}
}

// FormatValueForExpr formats a parser.Value as SQL text.
func FormatValueForExpr(v parser.Value) string {
	switch v.Type {
	case "int":
		return strconv.FormatInt(v.IntVal, 10)
	case "float":
		return strconv.FormatFloat(v.FltVal, 'f', -1, 64)
	case "string":
		return "'" + strings.ReplaceAll(v.StrVal, "'", "''") + "'"
	case "bool":
		if v.BoolVal {
			return "TRUE"
		}
		return "FALSE"
	case "null":
		return "NULL"
	default:
		return v.StrVal
	}
}

// ─── Function Injection Hooks ────────────────────────────────────────────────
// These variables are set by the root executor package at init time.
// Subpackages (e.g. commands/dml) call the exported wrappers below
// instead of importing the root package, which avoids circular dependencies.

// FreezeInsertFn returns a copy of INSERT with volatile functions frozen.
var FreezeInsertFn func(s *parser.InsertStatement, ctx *ExecutionContext) (*parser.InsertStatement, error)

// FreezeUpdateFn returns a copy of UPDATE with volatile functions frozen.
var FreezeUpdateFn func(s *parser.UpdateStatement, ctx *ExecutionContext) (*parser.UpdateStatement, error)

// FreezeDeleteFn returns a copy of DELETE with volatile functions frozen.
var FreezeDeleteFn func(s *parser.DeleteStatement, ctx *ExecutionContext) (*parser.DeleteStatement, error)

// ApplyTxOverlayFn applies buffered transaction operations on top of base rows.
var ApplyTxOverlayFn func(ctx *ExecutionContext, db, table string, base []storage.Row) ([]storage.Row, error)

// MutateUnderTableLockFn executes fn under per-table commit lock.
var MutateUnderTableLockFn func(ctx *ExecutionContext, db, table string, fn func() error) error

// FireTriggersFn fires AFTER triggers for a table mutation event.
var FireTriggersFn func(ctx *ExecutionContext, dbName, tableName, event string)

// NotifyBroadcasterFn notifies the broadcaster about a table mutation.
// This is separate from the other hooks because the Broadcaster type
// lives in the root executor package and can't be directly referenced.
var NotifyBroadcasterFn func(ctx *ExecutionContext, dbName, tableName string)

// ExecuteSelectWithCTEFn executes a SELECT with CTE clause.
var ExecuteSelectWithCTEFn func(stmt *parser.SelectStatement, ctx *ExecutionContext) (*Result, error)

// FreezeInsert returns a copy of INSERT with volatile functions frozen.
func FreezeInsert(s *parser.InsertStatement, ctx *ExecutionContext) (*parser.InsertStatement, error) {
	return FreezeInsertFn(s, ctx)
}

// FreezeUpdate returns a copy of UPDATE with volatile functions frozen.
func FreezeUpdate(s *parser.UpdateStatement, ctx *ExecutionContext) (*parser.UpdateStatement, error) {
	return FreezeUpdateFn(s, ctx)
}

// FreezeDelete returns a copy of DELETE with volatile functions frozen.
func FreezeDelete(s *parser.DeleteStatement, ctx *ExecutionContext) (*parser.DeleteStatement, error) {
	return FreezeDeleteFn(s, ctx)
}

// ApplyTxOverlay applies buffered transaction operations on top of base rows.
func ApplyTxOverlay(ctx *ExecutionContext, db, table string, base []storage.Row) ([]storage.Row, error) {
	return ApplyTxOverlayFn(ctx, db, table, base)
}

// MutateUnderTableLock executes fn under per-table commit lock.
func MutateUnderTableLock(ctx *ExecutionContext, db, table string, fn func() error) error {
	return MutateUnderTableLockFn(ctx, db, table, fn)
}

// FireTriggers fires AFTER triggers for a table mutation event.
func FireTriggers(ctx *ExecutionContext, dbName, tableName, event string) {
	FireTriggersFn(ctx, dbName, tableName, event)
}

// NotifyBroadcaster notifies the broadcaster about a table mutation.
func NotifyBroadcaster(ctx *ExecutionContext, dbName, tableName string) {
	if NotifyBroadcasterFn != nil {
		NotifyBroadcasterFn(ctx, dbName, tableName)
	}
}

// ExecuteSelectWithCTE executes a SELECT with CTE clause.
func ExecuteSelectWithCTE(stmt *parser.SelectStatement, ctx *ExecutionContext) (*Result, error) {
	return ExecuteSelectWithCTEFn(stmt, ctx)
}

// ─── Prepared Statement Binding ──────────────────────────────────────────────

// BindParams binds parameter values into a prepared statement, replacing
// ParamRef nodes with concrete parser.Value literals.
func BindParams(stmt parser.Statement, params []parser.Value) (parser.Statement, error) {
	switch s := stmt.(type) {
	case *parser.SelectStatement:
		bound := *s
		bound.Where = BindExpr(s.Where, params)
		bound.Having = BindExpr(s.Having, params)
		if len(s.Joins) > 0 {
			bound.Joins = make([]parser.JoinClause, len(s.Joins))
			for i, join := range s.Joins {
				bound.Joins[i] = join
				bound.Joins[i].Condition = BindExpr(join.Condition, params)
			}
		}
		if s.LimitExpr != nil {
			bound.LimitExpr = BindExpr(s.LimitExpr, params)
		}
		if s.OffsetExpr != nil {
			bound.OffsetExpr = BindExpr(s.OffsetExpr, params)
		}
		return &bound, nil
	case *parser.UpdateStatement:
		bound := *s
		bound.Assignments = make([]parser.Assignment, len(s.Assignments))
		for i, a := range s.Assignments {
			bound.Assignments[i] = parser.Assignment{
				Column: a.Column,
				Value:  BindExpr(a.Value, params),
			}
		}
		bound.Where = BindExpr(s.Where, params)
		return &bound, nil
	case *parser.InsertStatement:
		bound := *s
		bound.Rows = make([][]parser.Expression, len(s.Rows))
		for i, row := range s.Rows {
			bound.Rows[i] = make([]parser.Expression, len(row))
			for j, expr := range row {
				bound.Rows[i][j] = BindExpr(expr, params)
			}
		}
		return &bound, nil
	case *parser.DeleteStatement:
		bound := *s
		bound.Where = BindExpr(s.Where, params)
		return &bound, nil
	}
	return nil, fmt.Errorf("EXECUTE not supported for %T", stmt)
}

// BindExpr replaces ParamRef nodes in an expression with concrete values.
func BindExpr(expr parser.Expression, params []parser.Value) parser.Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *parser.ParamRef:
		if e.Index < 1 || e.Index > len(params) {
			return &parser.Value{Type: "null"}
		}
		p := params[e.Index-1]
		return &p
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{
			Left:     BindExpr(e.Left, params),
			Operator: e.Operator,
			Right:    BindExpr(e.Right, params),
		}
	case *parser.AndExpr:
		return &parser.AndExpr{
			Left:  BindExpr(e.Left, params),
			Right: BindExpr(e.Right, params),
		}
	case *parser.OrExpr:
		return &parser.OrExpr{
			Left:  BindExpr(e.Left, params),
			Right: BindExpr(e.Right, params),
		}
	case *parser.NotExpr:
		return &parser.NotExpr{
			Expr: BindExpr(e.Expr, params),
		}
	case *parser.Value:
		return e
	case *parser.ColumnRef:
		return e
	}
	return expr
}
