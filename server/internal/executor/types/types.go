package types

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/audit"
	"vaultdb/internal/auth"
	"vaultdb/internal/executor/eval"
	"vaultdb/internal/executor/parallel"
	"vaultdb/internal/executor/optimizer"
	"vaultdb/internal/logging"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
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

// ─── Type Formatting / Inference ────────────────────────────────────────────

// FormatColumnType returns the display type for a column (e.g. "VARCHAR(255)").
func FormatColumnType(column storage.ColumnSchema) string {
	if column.Type == "VARCHAR" && column.VarcharLen > 0 {
		return fmt.Sprintf("VARCHAR(%d)", column.VarcharLen)
	}
	return column.Type
}

// ValueToString converts any value to its string representation.
func ValueToString(value interface{}) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// InferType determines the SQL type of a Go value.
func InferType(val interface{}) string {
	if val == nil {
		return "TEXT"
	}
	switch v := val.(type) {
	case int64, int:
		return "INT"
	case float64:
		return "FLOAT"
	case bool:
		return "BOOL"
	case []float64:
		return "VECTOR"
	case map[string]interface{}:
		return "FLEXIBLE"
	case string:
		raw, err := storage.DecodeJSON([]byte(v))
		if err == nil {
			if _, ok := raw.(map[string]interface{}); ok {
				return "FLEXIBLE"
			}
		}
		return "TEXT"
	default:
		return "TEXT"
	}
}

// InferTypeFromExpr determines expression type from schema.
func InferTypeFromExpr(expr interface{}, schema *storage.TableSchema) string {
	return "TEXT"
}

// ─── Value Comparison / Conversion ──────────────────────────────────────────

// ParserValueToRaw converts parser.Value to a raw Go value.
func ParserValueToRaw(value interface{}) interface{} {
	if pv, ok := value.(parser.Value); ok {
		return eval.ParserValueToRaw(pv)
	}
	return value
}

// EvalOperandRaw extracts raw value from parser expression.
func EvalOperandRaw(expr interface{}) interface{} {
	return expr
}

// RowsEqual compares two table rows element by element.
func RowsEqual(a, b storage.Row) bool {
	return eval.RowsEqual(a, b)
}

// ValuesEqual compares two storage.Value values.
func ValuesEqual(a, b storage.Value) bool {
	return eval.ValuesEqual(a, b)
}

// ValuesEqualCaseInsensitive compares two storage.Value values case-insensitively.
func ValuesEqualCaseInsensitive(a, b storage.Value) bool {
	return eval.ValuesEqualCaseInsensitive(a, b)
}

// CompareOrdered compares two ordered values.
func CompareOrdered[T ~float64 | ~string](left, right T, op string) (bool, error) {
	switch op {
	case "=":
		return left == right, nil
	case "!=":
		return left != right, nil
	case "<":
		return left < right, nil
	case ">":
		return left > right, nil
	case "<=":
		return left <= right, nil
	case ">=":
		return left >= right, nil
	default:
		return false, fmt.Errorf("unknown operator '%s'", op)
	}
}

// ─── Type Coercion ──────────────────────────────────────────────────────────

// NormalizeForColumn coerces a single value to match a column's type.
func NormalizeForColumn(value storage.Value, col storage.ColumnSchema) (storage.Value, error) {
	tmpSchema := storage.TableSchema{Columns: []storage.ColumnSchema{col}}
	row := storage.Row{value}
	coerced, err := CoerceRowViaEval(row, &tmpSchema)
	if err != nil {
		return nil, err
	}
	return coerced[0], nil
}

// CoerceRowViaEval coerces an entire row to match a schema.
func CoerceRowViaEval(row storage.Row, schema *storage.TableSchema) (storage.Row, error) {
	coerced := make(storage.Row, len(row))
	for i, raw := range row {
		value, err := CoerceToColumn(raw, schema.Columns[i])
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
		}
		coerced[i] = value
	}
	return coerced, nil
}

// CoerceToColumn converts a value to match a column's declared type.
func CoerceToColumn(value storage.Value, column storage.ColumnSchema) (storage.Value, error) {
	if value == nil {
		return nil, nil
	}

	switch strings.ToUpper(column.Type) {
	case "INT":
		switch v := value.(type) {
		case int64:
			return v, nil
		case int:
			return int64(v), nil
		case float64:
			if float64(int64(v)) != v {
				return nil, fmt.Errorf("cannot cast FLOAT to INT without precision loss")
			}
			return int64(v), nil
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse string as INT: %q", v)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("expected INT-compatible value, got %T", value)
		}
	case "FLOAT":
		switch v := value.(type) {
		case float64:
			return v, nil
		case int64:
			return float64(v), nil
		case int:
			return float64(v), nil
		case string:
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse string as FLOAT: %q", v)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("expected FLOAT-compatible value, got %T", value)
		}
	case "BOOL":
		boolValue, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected BOOL value, got %T", value)
		}
		return boolValue, nil
	case "TEXT", "VARCHAR", "BLOB":
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected string value, got %T", value)
		}
		if column.Type == "VARCHAR" && column.VarcharLen > 0 && len([]rune(stringValue)) > column.VarcharLen {
			return nil, fmt.Errorf("VARCHAR(%d) overflow", column.VarcharLen)
		}
		return stringValue, nil
	case "VECTOR":
		vec, err := eval.ToVector(value)
		if err != nil {
			return nil, err
		}
		if column.VarcharLen > 0 && len(vec) != column.VarcharLen {
			return nil, fmt.Errorf("VECTOR(%d) dimension mismatch: got %d", column.VarcharLen, len(vec))
		}
		return vec, nil
	case "FLEXIBLE":
		switch v := value.(type) {
		case map[string]interface{}:
			return v, nil
		case string:
			raw, err := storage.DecodeJSON([]byte(v))
			if err == nil {
				if m, ok := raw.(map[string]interface{}); ok {
					return m, nil
				}
			}
			return v, nil
		default:
			return ValueToString(value), nil
		}
	case "DATE", "TIME", "TIMESTAMP", "DECIMAL":
		return ValueToString(value), nil
	case "JSONB", "JSON":
		return ValueToString(value), nil
	default:
		return nil, fmt.Errorf("unsupported column type '%s'", column.Type)
	}
}

// ─── Foreign Key Enforcement ────────────────────────────────────────────────

// BuildRefIndex creates a set of composite keys from referenced rows for FK validation.
func BuildRefIndex(rows []storage.Row, schema *storage.TableSchema, refCols []string) map[string]bool {
	set := make(map[string]bool)
	for _, row := range rows {
		key := BuildFKKey(row, schema, refCols)
		set[key] = true
	}
	return set
}

// BuildFKKey builds a composite key string from row values for FK columns.
func BuildFKKey(row storage.Row, schema *storage.TableSchema, columns []string) string {
	colIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		colIndex[strings.ToLower(col.Name)] = i
	}
	var b strings.Builder
	for i, colName := range columns {
		if i > 0 {
			b.WriteByte(0)
		}
		idx, ok := colIndex[strings.ToLower(colName)]
		if !ok || idx >= len(row) {
			continue
		}
		b.WriteString(ValueToString(row[idx]))
	}
	return b.String()
}

// EnforceForeignKeysOnInsert checks that all FK references in new rows are valid.
func EnforceForeignKeysOnInsert(ctx *ExecutionContext, dbName string, tableName string, rows []storage.Row) error {
	if dbName == "" {
		return nil
	}
	schema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return err
	}
	if len(schema.Constraints) == 0 {
		return nil
	}
	for _, fk := range schema.Constraints {
		if fk.Type != "FOREIGN_KEY" || fk.RefTable == "" {
			continue
		}
		if !ctx.Storage.TableExists(dbName, fk.RefTable) {
			return fmt.Errorf("foreign key constraint '%s': referenced table '%s' does not exist", fk.Name, fk.RefTable)
		}
		refSchema, err := ctx.Storage.GetTableSchema(dbName, fk.RefTable)
		if err != nil {
			return err
		}
		refRows, err := ctx.Storage.ReadCurrentRows(dbName, fk.RefTable)
		if err != nil {
			return err
		}
		refSet := BuildRefIndex(refRows, refSchema, fk.RefCols)
		for i, row := range rows {
			key := BuildFKKey(row, schema, fk.Columns)
			if len(key) == 0 {
				continue
			}
			if !refSet[key] {
				return fmt.Errorf("foreign key constraint '%s' violated: row %d references non-existent value in '%s'", fk.Name, i, fk.RefTable)
			}
		}
	}
	return nil
}

// EnforceForeignKeysOnDelete checks that deleting rows won't break FK constraints.
func EnforceForeignKeysOnDelete(ctx *ExecutionContext, dbName string, tableName string, indices []int) error {
	if dbName == "" {
		return nil
	}
	tables, err := ctx.Storage.ListTables(dbName)
	if err != nil {
		return err
	}
	for _, info := range tables {
		if info.Name == tableName {
			continue
		}
		childSchema, err := ctx.Storage.GetTableSchema(dbName, info.Name)
		if err != nil {
			continue
		}
		for _, fk := range childSchema.Constraints {
			if fk.Type != "FOREIGN_KEY" || fk.RefTable != tableName {
				continue
			}
			parentRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
			if err != nil {
				continue
			}
			childRows, err := ctx.Storage.ReadCurrentRows(dbName, info.Name)
			if err != nil {
				continue
			}
			parentSchema, err := ctx.Storage.GetTableSchema(dbName, tableName)
			if err != nil {
				continue
			}
			for _, idx := range indices {
				if idx >= len(parentRows) {
					continue
				}
				parentKey := BuildFKKey(parentRows[idx], parentSchema, fk.RefCols)
				for ci, childRow := range childRows {
					childKey := BuildFKKey(childRow, childSchema, fk.Columns)
					if childKey == parentKey {
						if fk.OnDeleteCascade {
							continue
						}
						return fmt.Errorf("foreign key constraint '%s' violated: row in '%s' references deleted parent row (index %d)", fk.Name, info.Name, ci)
					}
				}
			}
		}
	}
	return nil
}

// EnforceForeignKeysOnUpdate checks that updated FK values are valid.
func EnforceForeignKeysOnUpdate(ctx *ExecutionContext, dbName string, tableName string, indices []int, newValues []storage.Row) error {
	if dbName == "" {
		return nil
	}
	tableSchema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return err
	}

	for _, fk := range tableSchema.Constraints {
		if fk.Type != "FOREIGN_KEY" || fk.RefTable == "" {
			continue
		}
		if !ctx.Storage.TableExists(dbName, fk.RefTable) {
			continue
		}
		refSchema, err := ctx.Storage.GetTableSchema(dbName, fk.RefTable)
		if err != nil {
			continue
		}
		refRows, err := ctx.Storage.ReadCurrentRows(dbName, fk.RefTable)
		if err != nil {
			continue
		}
		refSet := BuildRefIndex(refRows, refSchema, fk.RefCols)
		for i := range indices {
			if i >= len(newValues) {
				continue
			}
			newRow := newValues[i]
			newKey := BuildFKKey(newRow, tableSchema, fk.Columns)
			if len(newKey) == 0 {
				continue
			}
			if !refSet[newKey] {
				return fmt.Errorf("foreign key constraint '%s' violated: row references non-existent value in '%s'", fk.Name, fk.RefTable)
			}
		}
	}

	return nil
}

// EnforceCascadeDeletes performs cascade deletes for FK relationships.
func EnforceCascadeDeletes(ctx *ExecutionContext, dbName string, tableName string, indices []int) error {
	if dbName == "" {
		return nil
	}
	tables, err := ctx.Storage.ListTables(dbName)
	if err != nil {
		return err
	}
	for _, info := range tables {
		if info.Name == tableName {
			continue
		}
		childSchema, err := ctx.Storage.GetTableSchema(dbName, info.Name)
		if err != nil {
			continue
		}
		for _, fk := range childSchema.Constraints {
			if fk.Type != "FOREIGN_KEY" || fk.RefTable != tableName {
				continue
			}
			if !fk.OnDeleteCascade {
				continue
			}
			parentRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
			if err != nil {
				continue
			}
			childRows, err := ctx.Storage.ReadCurrentRows(dbName, info.Name)
			if err != nil {
				continue
			}
			var toDelete []int
			for _, idx := range indices {
				if idx >= len(parentRows) {
					continue
				}
				parentKey := BuildFKKey(parentRows[idx], childSchema, fk.RefCols)
				for ci, childRow := range childRows {
					childKey := BuildFKKey(childRow, childSchema, fk.Columns)
					if childKey == parentKey {
						toDelete = append(toDelete, ci)
					}
				}
			}
			if len(toDelete) > 0 {
				if _, err := ctx.Storage.DeleteRows(dbName, info.Name, toDelete); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ─── Sequence / Auto-Increment ──────────────────────────────────────────────

var (
	sequenceMu       sync.Mutex
	sequenceCounters = make(map[string]int64)
)

func sequenceKey(dbName, tableName, colName string) string {
	return strings.ToLower(dbName + "." + tableName + "." + colName)
}

// GetNextAutoIncrement returns the next auto-increment value for a column.
func GetNextAutoIncrement(ctx *ExecutionContext, dbName, tableName, colName string) (int64, error) {
	sequenceMu.Lock()
	defer sequenceMu.Unlock()

	key := sequenceKey(dbName, tableName, colName)
	if next, ok := sequenceCounters[key]; ok {
		sequenceCounters[key] = next + 1
		return next, nil
	}

	maxVal, err := initSequenceFromTable(ctx, dbName, tableName, colName)
	if err != nil {
		return 0, err
	}

	next := maxVal + 1
	sequenceCounters[key] = next + 1
	return next, nil
}

func initSequenceFromTable(ctx *ExecutionContext, dbName, tableName, colName string) (int64, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
	if err != nil {
		return 0, err
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return 0, err
	}

	colIdx := -1
	for i, col := range schema.Columns {
		if strings.EqualFold(col.Name, colName) {
			colIdx = i
			break
		}
	}
	if colIdx == -1 {
		return 0, fmt.Errorf("column '%s' not found in table '%s'", colName, tableName)
	}

	var maxVal int64
	for _, row := range rows {
		if colIdx >= len(row) || row[colIdx] == nil {
			continue
		}
		var val int64
		switch v := row[colIdx].(type) {
		case int64:
			val = v
		case float64:
			val = int64(v)
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				continue
			}
			val = parsed
		default:
			continue
		}
		if val > maxVal {
			maxVal = val
		}
	}
	return maxVal, nil
}

// FillAutoIncrementColumns fills nil auto-increment columns with the next sequence value.
func FillAutoIncrementColumns(ctx *ExecutionContext, dbName, tableName string, schema *storage.TableSchema, rows []storage.Row) error {
	for i, col := range schema.Columns {
		if !col.AutoIncrement {
			continue
		}
		for j := range rows {
			if rows[j][i] != nil {
				continue
			}
			nextVal, err := GetNextAutoIncrement(ctx, dbName, tableName, col.Name)
			if err != nil {
				return fmt.Errorf("auto-increment for column '%s': %w", col.Name, err)
			}
			rows[j][i] = nextVal
		}
	}
	return nil
}

// ─── DDL Object Storage ─────────────────────────────────────────────────────

const (
	ObjTypeView      = "view"
	ObjTypeTrigger   = "trigger"
	ObjTypeFunction  = "function"
	ObjTypeProcedure = "procedure"
)

const SystemTableName = "_objects"

// EnsureSystemTable creates the _objects table if it does not exist yet.
func EnsureSystemTable(ctx *ExecutionContext, dbName string) error {
	if ctx.Storage.TableExists(dbName, SystemTableName) {
		return nil
	}
	schema := storage.TableSchema{
		Name: SystemTableName,
		Columns: []storage.ColumnSchema{
			{Name: "name", Type: "TEXT"},
			{Name: "type", Type: "TEXT"},
			{Name: "definition", Type: "TEXT"},
			{Name: "created_at", Type: "INT"},
		},
	}
	return ctx.Storage.CreateTable(dbName, schema)
}

// StoreObject stores a DDL object (view/trigger/function/procedure).
func StoreObject(ctx *ExecutionContext, dbName, objType, name string, definition interface{}) error {
	if err := EnsureSystemTable(ctx, dbName); err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	defJSON, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("store object: marshal definition: %w", err)
	}

	existing, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	var existingIdx []int
	var createdAt int64
	for i, row := range existing {
		if len(row) >= 3 && ValuesEqual(row[0], name) && ValuesEqual(row[1], objType) {
			existingIdx = append(existingIdx, i)
			if len(row) >= 4 {
				if ts, ok := row[3].(int64); ok {
					createdAt = ts
				}
			}
		}
	}

	if len(existingIdx) > 0 {
		if _, err := ctx.Storage.DeleteRows(dbName, SystemTableName, existingIdx); err != nil {
			return fmt.Errorf("store object: delete old: %w", err)
		}
	}

	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	newRow := storage.Row{name, objType, string(defJSON), createdAt}
	if _, err := ctx.Storage.InsertRows(dbName, SystemTableName, []storage.Row{newRow}); err != nil {
		return fmt.Errorf("store object: insert: %w", err)
	}

	return nil
}

// LoadObject loads a DDL object by name and type.
func LoadObject(ctx *ExecutionContext, dbName, objType, name string) (map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return nil, fmt.Errorf("load object: %w", err)
	}

	for _, row := range rows {
		if len(row) >= 3 && ValuesEqualCaseInsensitive(row[0], name) && ValuesEqual(row[1], objType) {
			defJSON, _ := row[2].(string)
			if defJSON == "" {
				return nil, nil
			}
			var def map[string]interface{}
			if err := json.Unmarshal([]byte(defJSON), &def); err != nil {
				return nil, fmt.Errorf("load object: unmarshal: %w", err)
			}
			return def, nil
		}
	}
	return nil, nil
}

// DeleteObject deletes a DDL object by name and type.
func DeleteObject(ctx *ExecutionContext, dbName, objType, name string) error {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}

	var indices []int
	for i, row := range rows {
		if len(row) >= 3 && ValuesEqual(row[0], name) && ValuesEqual(row[1], objType) {
			indices = append(indices, i)
		}
	}

	if len(indices) == 0 {
		return fmt.Errorf("object '%s' not found", name)
	}

	if _, err := ctx.Storage.DeleteRows(dbName, SystemTableName, indices); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// LoadAllObjectsByType loads all objects of the given type.
func LoadAllObjectsByType(ctx *ExecutionContext, dbName, objType string) ([]map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return nil, fmt.Errorf("load all objects: %w", err)
	}

	var results []map[string]interface{}
	for _, row := range rows {
		if len(row) >= 3 && ValuesEqual(row[1], objType) {
			defJSON, _ := row[2].(string)
			if defJSON == "" {
				continue
			}
			var def map[string]interface{}
			if err := json.Unmarshal([]byte(defJSON), &def); err != nil {
				continue
			}
			results = append(results, def)
		}
	}
	return results, nil
}

// LoadViewRLS loads the RLS metadata from a view definition.
func LoadViewRLS(ctx *ExecutionContext, dbName, viewName string) (bool, []storage.RLSPolicy, error) {
	def, err := LoadObject(ctx, dbName, ObjTypeView, viewName)
	if err != nil || def == nil {
		return false, nil, err
	}
	rlsEnabled, _ := def["rls_enabled"].(bool)
	var policies []storage.RLSPolicy
	if pRaw, ok := def["policies"]; ok {
		pBytes, err := json.Marshal(pRaw)
		if err != nil {
			return false, nil, fmt.Errorf("marshal view policies: %w", err)
		}
		if err := json.Unmarshal(pBytes, &policies); err != nil {
			return false, nil, fmt.Errorf("unmarshal view policies: %w", err)
		}
	}
	return rlsEnabled, policies, nil
}

// SetViewRLS updates the RLS enabled flag on a view definition.
func SetViewRLS(ctx *ExecutionContext, dbName, viewName string, enabled bool) error {
	def, err := LoadObject(ctx, dbName, ObjTypeView, viewName)
	if err != nil {
		return err
	}
	if def == nil {
		return fmt.Errorf("view '%s' not found", viewName)
	}
	def["rls_enabled"] = enabled
	return StoreObject(ctx, dbName, ObjTypeView, viewName, def)
}

// AddViewPolicy appends an RLS policy to a view definition.
func AddViewPolicy(ctx *ExecutionContext, dbName, viewName string, policy storage.RLSPolicy) error {
	def, err := LoadObject(ctx, dbName, ObjTypeView, viewName)
	if err != nil {
		return err
	}
	if def == nil {
		return fmt.Errorf("view '%s' not found", viewName)
	}
	var policies []storage.RLSPolicy
	if pRaw, ok := def["policies"]; ok {
		pBytes, _ := json.Marshal(pRaw)
		_ = json.Unmarshal(pBytes, &policies)
	}
	policies = append(policies, policy)
	def["policies"] = policies
	return StoreObject(ctx, dbName, ObjTypeView, viewName, def)
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

// EvalOperandFn evaluates a parser expression against a row.
var EvalOperandFn func(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error)

// EvalExprFn evaluates a parser expression and returns a boolean result.
var EvalExprFn func(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error)

// EvaluateCheckExprFn evaluates a CHECK constraint expression string against a row.
var EvaluateCheckExprFn func(exprStr string, row storage.Row, schema *storage.TableSchema) (bool, error)

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

// EvalOperand evaluates a parser expression against a row.
func EvalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	return EvalOperandFn(expr, row, schema, ctx)
}

// EvalExpr evaluates a parser expression and returns a boolean result.
func EvalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	return EvalExprFn(expr, row, schema, ctx)
}

// EvaluateCheckExpr evaluates a CHECK constraint expression string against a row.
func EvaluateCheckExpr(exprStr string, row storage.Row, schema *storage.TableSchema) (bool, error) {
	return EvaluateCheckExprFn(exprStr, row, schema)
}

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

// ─── DML Detection in Expressions ──────────────────────────────────────────

// ContainsSubqueryDML recursively walks a SELECT statement's expressions and
// subqueries to detect any non-SELECT (INSERT/UPDATE/DELETE) DML.
func ContainsSubqueryDML(sel *parser.SelectStatement) bool {
	// Walk CTEs
	for _, cte := range sel.CTEs {
		if ContainsStatementDML(cte.Query) {
			return true
		}
	}
	// Walk FROM subquery
	if sel.FromSubquery != nil {
		if ContainsSubqueryDML(sel.FromSubquery) {
			return true
		}
	}
	// Walk JOINs
	for _, j := range sel.Joins {
		if ContainsExprDML(j.Condition) {
			return true
		}
	}
	// Walk column expressions
	for _, col := range sel.Columns {
		if ContainsExprDML(col.Expr) {
			return true
		}
	}
	// Walk WHERE, HAVING
	if ContainsExprDML(sel.Where) {
		return true
	}
	if ContainsExprDML(sel.Having) {
		return true
	}
	// Walk GROUP BY, ORDER BY expressions
	for _, e := range sel.GroupBy {
		if ContainsExprDML(e) {
			return true
		}
	}
	for _, o := range sel.OrderBy {
		if ContainsExprDML(o.Expr) {
			return true
		}
	}
	return false
}

// ContainsStatementDML checks if a Statement contains DML subqueries.
func ContainsStatementDML(stmt parser.Statement) bool {
	if sel, ok := stmt.(*parser.SelectStatement); ok {
		return ContainsSubqueryDML(sel)
	}
	return false // non-SELECT statements as CTE body are fine here
}

// ContainsExprDML checks if an Expression tree contains a subquery with DML.
func ContainsExprDML(expr parser.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.SubqueryExpr:
		if sel, ok := e.Query.(*parser.SelectStatement); ok {
			return ContainsSubqueryDML(sel)
		}
		return true // non-SELECT subquery is DML
	case *parser.ExistsExpr:
		if sel, ok := e.Select.(*parser.SelectStatement); ok {
			return ContainsSubqueryDML(sel)
		}
		return true
	case *parser.ComparisonSubqueryExpr:
		if sel, ok := e.Subquery.(*parser.SelectStatement); ok {
			return ContainsSubqueryDML(sel)
		}
		return true
	case *parser.BinaryExpr:
		return ContainsExprDML(e.Left) || ContainsExprDML(e.Right)
	case *parser.AndExpr:
		return ContainsExprDML(e.Left) || ContainsExprDML(e.Right)
	case *parser.OrExpr:
		return ContainsExprDML(e.Left) || ContainsExprDML(e.Right)
	case *parser.NotExpr:
		return ContainsExprDML(e.Expr)
	case *parser.InExpr:
		if ContainsExprDML(e.Left) {
			return true
		}
		for _, r := range e.Right {
			if ContainsExprDML(r) {
				return true
			}
		}
		return false
	case *parser.BetweenExpr:
		return ContainsExprDML(e.Expr) || ContainsExprDML(e.Lower) || ContainsExprDML(e.Upper)
	case *parser.CaseExpr:
		if e.Base != nil && ContainsExprDML(e.Base) {
			return true
		}
		for _, w := range e.Whens {
			if ContainsExprDML(w.Condition) || ContainsExprDML(w.Result) {
				return true
			}
		}
		return e.Else != nil && ContainsExprDML(e.Else)
	case *parser.CastExpr:
		return ContainsExprDML(e.Expr)
	case *parser.FunctionCall:
		for _, a := range e.Args {
			if ContainsExprDML(a) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		for _, a := range e.Args {
			if ContainsExprDML(a) {
				return true
			}
		}
		return false
	case *parser.WindowFunctionExpr:
		for _, a := range e.Args {
			if ContainsExprDML(a) {
				return true
			}
		}
		for _, p := range e.Over.PartitionBy {
			if ContainsExprDML(p) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
