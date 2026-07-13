package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/executor/types"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// ──────────────────────────────────────────────────────────────────────────────
// Delegating wrappers — canonical implementations live in types/.
// These wrappers keep the existing unexported API stable so that the 20+
// call-sites throughout the executor package do not need to change yet.
// ──────────────────────────────────────────────────────────────────────────────

// asSession type-asserts ctx.Session to the concrete *Session.
// Returns nil if the assertion fails (should never happen in production).
func asSession(ctx *ExecutionContext) *Session {
	s, _ := ctx.Session.(*Session)
	return s
}

func requireCurrentDB(ctx *ExecutionContext) (string, error) {
	return types.RequireCurrentDB(ctx)
}

func resolveDatabase(ctx *ExecutionContext, requested string) (string, error) {
	return types.ResolveDatabase(ctx, requested)
}

func resolveProjection(schema *storage.TableSchema, requested []string) ([]int, []string, error) {
	return types.ResolveProjection(schema, requested)
}

func ensureColumnIndex(ctx *ExecutionContext, schema *storage.TableSchema) {
	types.EnsureColumnIndex(ctx, schema)
}

func formatColumnType(column storage.ColumnSchema) string {
	return types.FormatColumnType(column)
}

func valueToString(value interface{}) string {
	return types.ValueToString(value)
}

func parserValueToRaw(value interface{}) interface{} {
	return types.ParserValueToRaw(value)
}

func evalOperandRaw(expr interface{}) interface{} {
	return types.EvalOperandRaw(expr)
}

func normalizeForColumn(value storage.Value, col storage.ColumnSchema) (storage.Value, error) {
	return types.NormalizeForColumn(value, col)
}

func coerceToColumn(value storage.Value, column storage.ColumnSchema) (storage.Value, error) {
	return types.CoerceToColumn(value, column)
}

func coerceRowViaEval(row storage.Row, schema *storage.TableSchema) (storage.Row, error) {
	return types.CoerceRowViaEval(row, schema)
}

func inferType(val interface{}) string {
	return types.InferType(val)
}

func rowsEqual(a, b storage.Row) bool {
	return types.RowsEqual(a, b)
}

func valuesEqual(a, b storage.Value) bool {
	return types.ValuesEqual(a, b)
}

func valuesEqualCaseInsensitive(a, b storage.Value) bool {
	return types.ValuesEqualCaseInsensitive(a, b)
}

func inferTypeFromExpr(expr interface{}, schema *storage.TableSchema) string {
	return types.InferTypeFromExpr(expr, schema)
}

func compareOrdered[T ~float64 | ~string](left, right T, op string) (bool, error) {
	return types.CompareOrdered(left, right, op)
}

// Foreign key / DDL helpers (kept as local wrappers for backward compat).

func enforceForeignKeysOnInsert(ctx *ExecutionContext, dbName string, tableName string, rows []storage.Row) error {
	return types.EnforceForeignKeysOnInsert(ctx, dbName, tableName, rows)
}

func enforceForeignKeysOnDelete(ctx *ExecutionContext, dbName string, tableName string, indices []int) error {
	return types.EnforceForeignKeysOnDelete(ctx, dbName, tableName, indices)
}

func enforceForeignKeysOnUpdate(ctx *ExecutionContext, dbName string, tableName string, indices []int, newValues []storage.Row) error {
	return types.EnforceForeignKeysOnUpdate(ctx, dbName, tableName, indices, newValues)
}

func enforceCascadeDeletes(ctx *ExecutionContext, dbName string, tableName string, indices []int) error {
	return types.EnforceCascadeDeletes(ctx, dbName, tableName, indices)
}

func fillAutoIncrementColumns(ctx *ExecutionContext, dbName, tableName string, schema *storage.TableSchema, rows []storage.Row) error {
	return types.FillAutoIncrementColumns(ctx, dbName, tableName, schema, rows)
}

func ensureSystemTable(ctx *ExecutionContext, dbName string) error {
	return types.EnsureSystemTable(ctx, dbName)
}

func storeObject(ctx *ExecutionContext, dbName, objType, name string, definition interface{}) error {
	return types.StoreObject(ctx, dbName, objType, name, definition)
}

func loadObject(ctx *ExecutionContext, dbName, objType, name string) (map[string]interface{}, error) {
	return types.LoadObject(ctx, dbName, objType, name)
}

func deleteObject(ctx *ExecutionContext, dbName, objType, name string) error {
	return types.DeleteObject(ctx, dbName, objType, name)
}

func loadAllObjectsByType(ctx *ExecutionContext, dbName, objType string) ([]map[string]interface{}, error) {
	return types.LoadAllObjectsByType(ctx, dbName, objType)
}

func loadViewRLS(ctx *ExecutionContext, dbName, viewName string) (bool, []storage.RLSPolicy, error) {
	return types.LoadViewRLS(ctx, dbName, viewName)
}

func setViewRLS(ctx *ExecutionContext, dbName, viewName string, enabled bool) error {
	return types.SetViewRLS(ctx, dbName, viewName, enabled)
}

func addViewPolicy(ctx *ExecutionContext, dbName, viewName string, policy storage.RLSPolicy) error {
	return types.AddViewPolicy(ctx, dbName, viewName, policy)
}

func ResultCacheKey(stmt *parser.SelectStatement, dbName string) string {
	return types.ResultCacheKey(stmt, dbName)
}

// ──────────────────────────────────────────────────────────────────────────────
// DDL helpers that remain in root for use by non-DDL files (RBAC, hooks).
// Canonical DDL command implementations live in commands/ddl/.
// ──────────────────────────────────────────────────────────────────────────────

func sanitizeObjectName(name string) (string, error) {
	if err := storage.ValidateObjectName(name); err != nil {
		return "", err
	}
	return name, nil
}

func fireTriggers(ctx *ExecutionContext, dbName, tableName, event string) {
	const maxTriggerDepth = 3
	if ctx.TriggerDepth() >= maxTriggerDepth {
		return
	}

	triggers, err := loadAllObjectsByType(ctx, dbName, types.ObjTypeTrigger)
	if err != nil {
		return
	}
	for _, td := range triggers {
		triggerTable, _ := td["table"].(string)
		triggerEvent, _ := td["event"].(string)
		timing, _ := td["timing"].(string)
		body, _ := td["body"].(string)
		name, _ := td["name"].(string)

		if triggerTable != tableName || !strings.EqualFold(triggerEvent, event) {
			continue
		}
		if timing != "AFTER" {
			continue
		}
		if body == "" {
			continue
		}
		ctx.SetTriggerDepth(ctx.TriggerDepth() + 1)
		err := executeTriggerBody(ctx, body)
		ctx.SetTriggerDepth(ctx.TriggerDepth() - 1)
		if err != nil {
			_ = name // logged via slog in production
		}
	}
}

func executeTriggerBody(ctx *ExecutionContext, body string) error {
	stmt, err := parser.Parse(body)
	if err != nil {
		return fmt.Errorf("trigger body parse: %w", err)
	}
	_, err = ctx.RunSubquery.RunSubquery(ctx, stmt)
	return err
}

// ─── Validation helpers (used by tests in root package) ─────────────────────

func isMigrationSafe(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.SelectStatement, *parser.InsertStatement, *parser.UpdateStatement, *parser.DeleteStatement:
		return true
	case *parser.CreateTableStatement:
		name := strings.ToLower(s.TableName)
		return !strings.HasPrefix(name, "_") && name != "vaultdb_audit_log"
	case *parser.CreateIndexStatement:
		return true
	case *parser.CreateViewStatement:
		return true
	case *parser.AlterTableStatement:
		return isAlterTableSafe(s)
	default:
		return false
	}
}

func isAlterTableSafe(stmt *parser.AlterTableStatement) bool {
	switch stmt.Action.(type) {
	case *parser.AlterAddColumn, *parser.AlterAddConstraint:
		return true
	default:
		return false
	}
}

func splitSQLStatements(sql string) []string {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for _, ch := range sql {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' && (inSingleQuote || inDoubleQuote) {
			current.WriteRune(ch)
			escaped = true
			continue
		}
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteRune(ch)
			continue
		}
		if ch == ';' && !inSingleQuote && !inDoubleQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func containsSubqueryDML(sel *parser.SelectStatement) bool {
	return types.ContainsSubqueryDML(sel)
}

func validateWASMPath(rawBody string, dataDir string) (string, error) {
	raw := strings.TrimPrefix(rawBody, "file://")

	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("WASM path must not be absolute: %s", raw)
	}

	absPath := filepath.Clean(filepath.Join(dataDir, raw))
	absDataDir := filepath.Clean(dataDir)
	if !strings.HasPrefix(absPath, absDataDir+string(os.PathSeparator)) && absPath != absDataDir {
		return "", fmt.Errorf("WASM path escapes data directory: %s", raw)
	}

	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("WASM module not found: %s", absPath)
	}
	return absPath, nil
}
