package executor

import (
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
