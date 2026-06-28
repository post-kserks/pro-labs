package executor

// Команды DML: INSERT, UPDATE, DELETE.

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func executeReturningGeneric(rows []storage.Row, returningCols []parser.SelectColumn, schema *storage.TableSchema, ctx *ExecutionContext) (*Result, error) {
	resultRows := make([][]string, 0, len(rows))

	starMode := len(returningCols) == 0

	for _, row := range rows {
		var projected []string
		if starMode {
			projected = make([]string, len(row))
			for i := range row {
				projected[i] = valueToString(row[i])
			}
		} else {
			projected = make([]string, len(returningCols))
			for i, col := range returningCols {
				val, err := evalOperand(col.Expr, row, schema, ctx)
				if err != nil {
					projected[i] = "ERR"
				} else {
					projected[i] = valueToString(val)
				}
			}
		}
		resultRows = append(resultRows, projected)
	}

	var projectColumns []string
	if starMode {
		if schema != nil && len(schema.Columns) > 0 {
			projectColumns = make([]string, len(schema.Columns))
			for i, col := range schema.Columns {
				projectColumns[i] = col.Name
			}
		} else if len(resultRows) > 0 {
			projectColumns = make([]string, len(resultRows[0]))
			for i := range resultRows[0] {
				projectColumns[i] = fmt.Sprintf("col%d", i)
			}
		}
	} else {
		projectColumns = make([]string, len(returningCols))
		for i, col := range returningCols {
			if col.Alias != "" {
				projectColumns[i] = col.Alias
			} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
				projectColumns[i] = colRef.Name
			} else {
				projectColumns[i] = fmt.Sprintf("col%d", i)
			}
		}
	}
	return &Result{
		Type:    "rows",
		Columns: projectColumns,
		Rows:    resultRows,
	}, nil
}

func notifyMutation(ctx *ExecutionContext, dbName, tableName string) {
	if ctx.Stats != nil {
		ctx.Stats.InvalidateStats(dbName, tableName)
	}
	if ctx.Session != nil && ctx.Session.resultCache != nil {
		ctx.Session.resultCache.Invalidate(tableName)
	}
	if ctx.TxManager != nil {
		ctx.TxManager.BumpTableVersion(dbName, tableName)
	}
	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, tableName, ctx)
	}
}

// enforceRLSPolicies checks if RLS is enabled and policies exist for the table.
// Returns an error if the operation should be denied.
func enforceRLSPolicies(ctx *ExecutionContext, dbName, tableName string) error {
	schema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return err
	}
	if !schema.RLSEnabled {
		return nil
	}
	if len(schema.Policies) == 0 {
		return fmt.Errorf("RLS is enabled on table '%s' but no policies are defined", tableName)
	}
	return nil
}

func enforceCheckConstraints(schema *storage.TableSchema, row storage.Row) error {
	for _, c := range schema.Constraints {
		if c.Type == "CHECK" && c.Expr != "" {
			ok, err := evaluateCheckExpr(c.Expr, row, schema)
			if err != nil {
				return fmt.Errorf("CHECK constraint '%s': %w", c.Name, err)
			}
			if !ok {
				return fmt.Errorf("CHECK constraint '%s' violated", c.Name)
			}
		}
	}
	return nil
}

type InsertCommand struct {
	stmt *parser.InsertStatement
}

func (c *InsertCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, _ := requireCurrentDB(ctx)
	if dbName != "" {
		if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
	}
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		// Замораживаем волатильные функции (NOW/UUID/...) в литералы, чтобы
		// overlay-чтение и применение при COMMIT использовали одинаковые
		// значения (Bug #3).
		frozen, err := freezeInsert(c.stmt, ctx)
		if err != nil {
			return nil, err
		}
		ctx.Session.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "insert",
			DB:      ctx.Session.CurrentDatabase(),
			Table:   c.stmt.TableName,
			Payload: frozen,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered INSERT (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

// executeImmediate сериализует autocommit-запись (мутация + bump версии) с
// коммитами под per-table commit-локом (Bug #2b). При commit-apply
// (ctx.InCommitApply) lock уже взят — mutateUnderTableLock его не берёт повторно.
func (c *InsertCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return c.executeImmediateInner(ctx)
	}
	var result *Result
	err = mutateUnderTableLock(ctx, dbName, c.stmt.TableName, func() error {
		var e error
		result, e = c.executeImmediateInner(ctx)
		return e
	})
	return result, err
}

func (c *InsertCommand) executeImmediateInner(ctx *ExecutionContext) (*Result, error) {
	if ctx.Ctx != nil {
		select {
		case <-ctx.Ctx.Done():
			return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
		default:
		}
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// Handle INSERT ... SELECT
	if c.stmt.SelectQuery != nil {
		return c.executeInsertSelect(ctx, dbName, schema)
	}

	// Handle INFER SCHEMA
	if len(schema.Columns) == 0 && len(c.stmt.Rows) > 0 {
		// Infer schema from first row
		inferredCols := make([]storage.ColumnSchema, 0, len(c.stmt.Rows[0]))
		for i, expr := range c.stmt.Rows[0] {
			val, _ := evalOperand(expr, nil, nil, ctx)
			colType := inferType(val)
			name := fmt.Sprintf("col%d", i)
			if len(c.stmt.Columns) > i {
				name = c.stmt.Columns[i]
			}
			inferredCols = append(inferredCols, storage.ColumnSchema{Name: name, Type: colType})
		}

		// Update table schema on disk
		for _, col := range inferredCols {
			if err := ctx.Storage.AlterTableAddColumn(dbName, c.stmt.TableName, col, nil); err != nil {
				return nil, fmt.Errorf("infer schema failed: %w", err)
			}
		}
		// Reload schema
		schema, err = ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
	}

	rowsToInsert, err := c.buildRows(schema, ctx)
	if err != nil {
		return nil, err
	}

	for i, row := range rowsToInsert {
		for j, col := range schema.Columns {
			if col.Type == "ENUM" && len(col.EnumValues) > 0 && j < len(row) && row[j] != nil {
				val := valueToString(row[j])
				valid := false
				for _, ev := range col.EnumValues {
					if val == ev {
						valid = true
						break
					}
				}
				if !valid {
					return nil, fmt.Errorf("invalid ENUM value '%s' for column '%s' (valid: %v)", val, col.Name, col.EnumValues)
				}
			}
		}
		_ = i
	}

	// Enforce CHECK constraints
	for i, row := range rowsToInsert {
		if err := enforceCheckConstraints(schema, row); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
	}

	// Handle ON CONFLICT (UPSERT)
	if c.stmt.OnConflict != nil {
		return c.executeUpsert(ctx, dbName, schema, rowsToInsert)
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}
	fireTriggers(ctx, dbName, c.stmt.TableName, "INSERT")

	// Handle RETURNING
	if c.stmt.Returning != nil {
		return c.executeReturning(ctx, dbName, schema, rowsToInsert)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *InsertCommand) buildRows(schema *storage.TableSchema, ctx *ExecutionContext) ([]storage.Row, error) {
	result := make([]storage.Row, 0, len(c.stmt.Rows))

	if len(c.stmt.Columns) == 0 {
		for rowIndex, row := range c.stmt.Rows {
			if len(row) != len(schema.Columns) {
				return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(schema.Columns))
			}
			normalized := make(storage.Row, len(row))
			for i, expr := range row {
				val, err := evalOperand(expr, nil, nil, ctx)
				if err != nil {
					return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
				}
				converted, err := normalizeForColumn(val, schema.Columns[i])
				if err != nil {
					return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
				}
				normalized[i] = converted
			}
			result = append(result, normalized)
		}
		return result, nil
	}

	columnIndex := make(map[string]int, len(schema.Columns))
	for idx, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = idx
	}

	mappedColumns := make([]int, len(c.stmt.Columns))
	for i, name := range c.stmt.Columns {
		idx, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("unknown column '%s'", name)
		}
		mappedColumns[i] = idx
	}

	for rowIndex, row := range c.stmt.Rows {
		if len(row) != len(mappedColumns) {
			return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(mappedColumns))
		}

		normalized := make(storage.Row, len(schema.Columns))
		for i := range normalized {
			normalized[i] = nil
		}

		for i, expr := range row {
			colIdx := mappedColumns[i]
			val, err := evalOperand(expr, nil, nil, ctx)
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			converted, err := normalizeForColumn(val, schema.Columns[colIdx])
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			normalized[colIdx] = converted
		}
		result = append(result, normalized)
	}

	return result, nil
}

// executeUpsert выполняет INSERT ... ON CONFLICT DO NOTHING/UPDATE.
// Использует хеш-таблицу для O(1) поиска конфликтов вместо O(n) скана.
func (c *InsertCommand) executeUpsert(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, rowsToInsert []storage.Row) (*Result, error) {
	affected := 0

	conflictCols := c.stmt.OnConflict.Columns
	if len(conflictCols) == 0 {
		conflictCols = nil
		for _, col := range schema.Columns {
			conflictCols = append(conflictCols, col.Name)
		}
	}
	colIdxMap := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		colIdxMap[strings.ToLower(col.Name)] = i
	}

	existingRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// Build hash index: conflict key → index in existingRows
	conflictMap := make(map[string]int, len(existingRows))
	for idx, row := range existingRows {
		key := buildUpsertConflictKey(row, conflictCols, colIdxMap)
		conflictMap[key] = idx
	}

	for _, row := range rowsToInsert {
		key := buildUpsertConflictKey(row, conflictCols, colIdxMap)
		conflictIdx, conflict := conflictMap[key]

		if conflict {
			if c.stmt.OnConflict.Action == "NOTHING" {
				continue
			}
			if c.stmt.OnConflict.Action == "UPDATE" {
				updates := make(map[string]storage.Value)
				for _, assign := range c.stmt.OnConflict.Assignments {
					val, err := evalOperand(assign.Value, row, schema, ctx)
					if err != nil {
						return nil, err
					}
					updates[assign.Column] = val
				}
				_, err := ctx.Storage.UpdateRows(dbName, c.stmt.TableName, []int{conflictIdx}, updates)
				if err != nil {
					return nil, err
				}
				affected++
				// Update cached row for subsequent conflict checks
				existingRows[conflictIdx] = row
				conflictMap[key] = conflictIdx
			}
		} else {
			_, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, []storage.Row{row})
			if err != nil {
				return nil, err
			}
			affected++
			// Add new row to conflict map
			newIdx := len(existingRows)
			existingRows = append(existingRows, row)
			conflictMap[key] = newIdx
		}
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

// buildUpsertConflictKey создаёт хеш-ключ из значений столбцов конфликта.
func buildUpsertConflictKey(row storage.Row, conflictCols []string, colIdxMap map[string]int) string {
	var b strings.Builder
	for i, colName := range conflictCols {
		if i > 0 {
			b.WriteByte(0)
		}
		ci, ok := colIdxMap[strings.ToLower(colName)]
		if !ok {
			continue
		}
		if ci < len(row) {
			b.WriteString(valueToString(row[ci]))
		}
	}
	return b.String()
}

// executeInsertSelect выполняет INSERT ... SELECT.
func (c *InsertCommand) executeInsertSelect(ctx *ExecutionContext, dbName string, schema *storage.TableSchema) (*Result, error) {
	// Выполняем SELECT запрос
	cmd, err := CommandFactory(c.stmt.SelectQuery)
	if err != nil {
		return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
	}

	res, err := cmd.Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
	}

	// Конвертируем результат в строки для вставки
	rowsToInsert := make([]storage.Row, 0, len(res.Rows))
	for _, row := range res.Rows {
		storageRow := make(storage.Row, len(schema.Columns))
		for i := range schema.Columns {
			if i < len(row) {
				val, err := convertStringToValue(row[i], schema.Columns[i])
				if err != nil {
					return nil, fmt.Errorf("INSERT ... SELECT: column '%s': %w", schema.Columns[i].Name, err)
				}
				storageRow[i] = val
			}
		}
		rowsToInsert = append(rowsToInsert, storageRow)
	}

	// Вставляем строки
	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}

	// Handle RETURNING
	if c.stmt.Returning != nil {
		return c.executeReturning(ctx, dbName, schema, rowsToInsert)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

// executeReturning выполняет RETURNING clause для INSERT.
func (c *InsertCommand) executeReturning(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, insertedRows []storage.Row) (*Result, error) {
	return executeReturningGeneric(insertedRows, c.stmt.Returning, schema, ctx)
}

// convertStringToValue конвертирует строку в значение нужного типа.
func convertStringToValue(s string, col storage.ColumnSchema) (storage.Value, error) {
	switch strings.ToUpper(col.Type) {
	case "INT":
		val, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to INT", s)
		}
		return val, nil
	case "FLOAT":
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to FLOAT", s)
		}
		return val, nil
	case "BOOL":
		val, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to BOOL", s)
		}
		return val, nil
	case "TEXT", "VARCHAR":
		return s, nil
	default:
		return s, nil
	}
}

type UpdateCommand struct {
	stmt *parser.UpdateStatement
}

func (c *UpdateCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, _ := requireCurrentDB(ctx)
	if dbName != "" {
		if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
	}
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		// Замораживаем волатильные функции в присваиваниях и WHERE (Bug #3),
		// чтобы overlay и применение при COMMIT совпали.
		frozen, err := freezeUpdate(c.stmt, ctx)
		if err != nil {
			return nil, err
		}
		dbName, _ := requireCurrentDB(ctx)
		var oldRows []storage.Row
		var oldIndices []int
		if dbName != "" && ctx.Storage.TableExists(dbName, c.stmt.TableName) {
			schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
			if err == nil {
				rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
				if err == nil {
					for idx, row := range rows {
						match, err := evalExpr(frozen.Where, row, schema, ctx)
						if err == nil && match {
							oldRows = append(oldRows, row)
							oldIndices = append(oldIndices, idx)
						}
					}
				}
			}
		}

		ctx.Session.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "update",
			DB:      ctx.Session.CurrentDatabase(),
			Table:   c.stmt.TableName,
			Payload: frozen,
			OldRow:  oldRows,
			Row:     oldIndices,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered UPDATE (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

// executeImmediate — см. комментарий к InsertCommand.executeImmediate (Bug #2b).
func (c *UpdateCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return c.executeImmediateInner(ctx)
	}
	var result *Result
	err = mutateUnderTableLock(ctx, dbName, c.stmt.TableName, func() error {
		var e error
		result, e = c.executeImmediateInner(ctx)
		return e
	})
	return result, err
}

func (c *UpdateCommand) executeImmediateInner(ctx *ExecutionContext) (*Result, error) {
	if ctx.Ctx != nil {
		select {
		case <-ctx.Ctx.Done():
			return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
		default:
		}
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// Handle UPDATE ... FROM
	var fromRows []storage.Row
	var fromSchema *storage.TableSchema
	if c.stmt.FromTable != "" {
		if !ctx.Storage.TableExists(dbName, c.stmt.FromTable) {
			return nil, fmt.Errorf("FROM table '%s' does not exist", c.stmt.FromTable)
		}
		fromRows, err = ctx.Storage.ReadCurrentRows(dbName, c.stmt.FromTable)
		if err != nil {
			return nil, err
		}
		fromSchema, err = ctx.Storage.GetTableSchema(dbName, c.stmt.FromTable)
		if err != nil {
			return nil, err
		}
	}

	updates := make(map[string]storage.Value)
	for _, assign := range c.stmt.Assignments {
		val, err := evalOperand(assign.Value, nil, schema, ctx)
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", assign.Column, err)
		}
		updates[assign.Column] = val
	}

	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// If FROM clause, create combined rows for WHERE evaluation
	var evalRows []storage.Row
	var evalSchema *storage.TableSchema
	if fromRows != nil {
		// Combine target and source rows for WHERE evaluation
		evalRows = make([]storage.Row, 0)
		for _, targetRow := range rows {
			for _, sourceRow := range fromRows {
				combinedRow := append(append(storage.Row{}, targetRow...), sourceRow...)
				evalRows = append(evalRows, combinedRow)
			}
		}
		// Create combined schema
		evalSchema = &storage.TableSchema{
			Name:    "UPDATE_JOIN",
			Columns: make([]storage.ColumnSchema, 0, len(schema.Columns)+len(fromSchema.Columns)),
		}
		for _, col := range schema.Columns {
			evalSchema.Columns = append(evalSchema.Columns, col)
		}
		for _, col := range fromSchema.Columns {
			newCol := col
			if c.stmt.FromAlias != "" {
				newCol.Name = c.stmt.FromAlias + "." + col.Name
			}
			evalSchema.Columns = append(evalSchema.Columns, newCol)
		}
	} else {
		evalRows = rows
		evalSchema = schema
	}

	indices := make([]int, 0)
	if fromRows != nil {
		seenTarget := make(map[int]bool)
		for idx, row := range evalRows {
			match, err := evalExpr(c.stmt.Where, row, evalSchema, ctx)
			if err != nil {
				return nil, err
			}
			if match {
				targetIdx := idx / len(fromRows)
				if !seenTarget[targetIdx] {
					seenTarget[targetIdx] = true
					indices = append(indices, targetIdx)
				}
			}
		}
	} else {
		for idx, row := range evalRows {
			match, err := evalExpr(c.stmt.Where, row, evalSchema, ctx)
			if err != nil {
				return nil, err
			}
			if match {
				indices = append(indices, idx)
			}
		}
	}

	// Enforce CHECK constraints on updated rows
	for _, idx := range indices {
		if idx < len(rows) {
			newRow := make(storage.Row, len(rows[idx]))
			copy(newRow, rows[idx])
			for col, val := range updates {
				for ci, c := range schema.Columns {
					if strings.EqualFold(c.Name, col) && ci < len(newRow) {
						newRow[ci] = val
						break
					}
				}
			}
			if err := enforceCheckConstraints(schema, newRow); err != nil {
				return nil, fmt.Errorf("row %d: %w", idx, err)
			}
		}
	}

	affected, err := ctx.Storage.UpdateRows(dbName, c.stmt.TableName, indices, updates)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}

	fireTriggers(ctx, dbName, c.stmt.TableName, "UPDATE")

	// Handle RETURNING
	if c.stmt.Returning != nil {
		return c.executeReturningUpdate(ctx, dbName, schema, indices, rows)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

// executeReturningUpdate выполняет RETURNING clause для UPDATE.
func (c *UpdateCommand) executeReturningUpdate(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, preUpdateRows []storage.Row) (*Result, error) {
	var updatedRows []storage.Row
	for _, idx := range indices {
		if idx < len(preUpdateRows) {
			updatedRows = append(updatedRows, preUpdateRows[idx])
		}
	}
	return executeReturningGeneric(updatedRows, c.stmt.Returning, schema, ctx)
}

type DeleteCommand struct {
	stmt *parser.DeleteStatement
}

func (c *DeleteCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, _ := requireCurrentDB(ctx)
	if dbName != "" {
		if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
	}
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		// Замораживаем волатильные функции в WHERE (Bug #3).
		frozen, err := freezeDelete(c.stmt, ctx)
		if err != nil {
			return nil, err
		}
		dbName, _ := requireCurrentDB(ctx)
		var deletedRows []storage.Row
		if dbName != "" && ctx.Storage.TableExists(dbName, c.stmt.TableName) {
			schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
			if err == nil {
				rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
				if err == nil {
					for _, row := range rows {
						match, err := evalExpr(frozen.Where, row, schema, ctx)
						if err == nil && match {
							deletedRows = append(deletedRows, row)
						}
					}
				}
			}
		}

		ctx.Session.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "delete",
			DB:      ctx.Session.CurrentDatabase(),
			Table:   c.stmt.TableName,
			Payload: frozen,
			Row:     deletedRows,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered DELETE (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

// executeImmediate — см. комментарий к InsertCommand.executeImmediate (Bug #2b).
func (c *DeleteCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return c.executeImmediateInner(ctx)
	}
	var result *Result
	err = mutateUnderTableLock(ctx, dbName, c.stmt.TableName, func() error {
		var e error
		result, e = c.executeImmediateInner(ctx)
		return e
	})
	return result, err
}

func (c *DeleteCommand) executeImmediateInner(ctx *ExecutionContext) (*Result, error) {
	if ctx.Ctx != nil {
		select {
		case <-ctx.Ctx.Done():
			return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
		default:
		}
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}
	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(rows))
	for idx, row := range rows {
		match, err := evalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if match {
			indices = append(indices, idx)
		}
	}

	affected, err := ctx.Storage.DeleteRows(dbName, c.stmt.TableName, indices)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}

	fireTriggers(ctx, dbName, c.stmt.TableName, "DELETE")

	// Handle RETURNING
	if c.stmt.Returning != nil {
		return c.executeReturningDelete(ctx, dbName, schema, indices, rows)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

// executeReturningDelete выполняет RETURNING clause для DELETE.
func (c *DeleteCommand) executeReturningDelete(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, preDeleteRows []storage.Row) (*Result, error) {
	var deletedRows []storage.Row
	for _, idx := range indices {
		if idx < len(preDeleteRows) {
			deletedRows = append(deletedRows, preDeleteRows[idx])
		}
	}
	return executeReturningGeneric(deletedRows, c.stmt.Returning, schema, ctx)
}
