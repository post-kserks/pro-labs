package executor

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

const maxMergeRows = 1000000

// CTECommand выполняет CTE (WITH clause).
type CTECommand struct {
	stmt *parser.CTEStatement
}

func (c *CTECommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return ExecuteCTEStatement(c.stmt, ctx)
}

// TruncateCommand performs TRUNCATE TABLE.
type TruncateCommand struct {
	stmt *parser.TruncateStatement
}

func (c *TruncateCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	// If inside an explicit user transaction, buffer the truncate for deferred execution.
	if ctx.Session.IsInTx() {
		ctx.Session.TxManager.AddOp(ctx.Session.ActiveTx, txmanager.PendingOp{
			Type:    "truncate",
			DB:      dbName,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered TRUNCATE (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}

	// Begin implicit transaction for atomicity.
	tx := ctx.TxManager.Begin()
	defer tx.Rollback()

	// Register the table so Commit captures the version snapshot and
	// detects concurrent modifications.
	ctx.TxManager.AddOp(tx, txmanager.PendingOp{
		Type:    "truncate",
		DB:      dbName,
		Table:   c.stmt.TableName,
		Payload: c.stmt,
	})

	if err := ctx.TxManager.Commit(tx, func(pendingOps []txmanager.PendingOp) error {
		for _, op := range pendingOps {
			if op.Type == "truncate" {
				rows, err := ctx.Storage.ReadCurrentRows(op.DB, op.Table)
				if err != nil {
					return err
				}
				if len(rows) > 0 {
					indices := make([]int, len(rows))
					for i := range indices {
						indices[i] = i
					}
					if _, err := ctx.Storage.DeleteRows(op.DB, op.Table, indices); err != nil {
						return err
					}
				}
				notifyMutation(ctx, op.DB, op.Table)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Table '%s' truncated.", c.stmt.TableName),
	}, nil
}

// MergeCommand выполняет MERGE INTO.
type MergeCommand struct {
	stmt *parser.MergeStatement
}

// extractMergeEqualityColumns извлекает пары столбцов из простого ON-условия
// вида "target.col = source.col" для hash join.
// Возвращает: (target col name, source col name).
func extractMergeEqualityColumns(cond parser.Expression, targetTable, sourceTable string) [][2]string {
	be, ok := cond.(*parser.BinaryExpr)
	if !ok || be.Operator != "=" {
		return nil
	}
	leftCol, lok := be.Left.(*parser.ColumnRef)
	rightCol, rok := be.Right.(*parser.ColumnRef)
	if !lok || !rok {
		return nil
	}
	// Определяем какой столбец к какой таблице относится
	leftName := leftCol.Name
	rightName := rightCol.Name
	leftIsTarget := strings.HasPrefix(leftName, targetTable+".")
	rightIsTarget := strings.HasPrefix(rightName, targetTable+".")
	if leftIsTarget && !rightIsTarget {
		return [][2]string{{leftName, rightName}}
	}
	if rightIsTarget && !leftIsTarget {
		return [][2]string{{rightName, leftName}}
	}
	return nil
}

// buildMergeKey создаёт хеш-ключ из значений столбцов для hash join.
func buildMergeKey(row storage.Row, colName string, colIdxMap map[string]int) string {
	ci, ok := colIdxMap[strings.ToLower(colName)]
	if !ok || ci >= len(row) {
		return ""
	}
	return valueToString(row[ci])
}

func (c *MergeCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	// Запись MERGE сериализуем с коммитами под commit-локом целевой таблицы
	// (Bug #2b); при commit-apply lock уже взят — mutateUnderTableLock это учитывает.
	var result *Result
	err = mutateUnderTableLock(ctx, dbName, c.stmt.TargetTable, func() error {
		var e error
		result, e = c.executeMerge(ctx, dbName)
		return e
	})
	return result, err
}

func (c *MergeCommand) executeMerge(ctx *ExecutionContext, dbName string) (*Result, error) {
	// Read target table
	if !ctx.Storage.TableExists(dbName, c.stmt.TargetTable) {
		return nil, fmt.Errorf("target table '%s' does not exist", c.stmt.TargetTable)
	}
	targetRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TargetTable)
	if err != nil {
		return nil, err
	}
	targetRows, err = applyTxOverlay(ctx, dbName, c.stmt.TargetTable, targetRows)
	if err != nil {
		return nil, err
	}

	// Read source table
	if !ctx.Storage.TableExists(dbName, c.stmt.SourceTable) {
		return nil, fmt.Errorf("source table '%s' does not exist", c.stmt.SourceTable)
	}
	sourceRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.SourceTable)
	if err != nil {
		return nil, err
	}
	sourceRows, err = applyTxOverlay(ctx, dbName, c.stmt.SourceTable, sourceRows)
	if err != nil {
		return nil, err
	}

	if len(sourceRows) > maxMergeRows {
		return nil, fmt.Errorf("MERGE INTO: source table too large (%d rows, max %d)", len(sourceRows), maxMergeRows)
	}

	// Get schemas
	targetSchema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TargetTable)
	if err != nil {
		return nil, err
	}
	sourceSchema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.SourceTable)
	if err != nil {
		return nil, err
	}

	// Create combined schema with qualified names (target alias, source alias)
	combinedSchema := &storage.TableSchema{
		Name:    "MERGE",
		Columns: make([]storage.ColumnSchema, 0, len(targetSchema.Columns)+len(sourceSchema.Columns)),
	}

	// Add target columns with target table name prefix
	for _, col := range targetSchema.Columns {
		newCol := col
		newCol.Name = c.stmt.TargetTable + "." + col.Name
		combinedSchema.Columns = append(combinedSchema.Columns, newCol)
	}

	// Add source columns with source table name prefix (or alias)
	sourceName := c.stmt.SourceTable
	if c.stmt.Alias != "" {
		sourceName = c.stmt.Alias
	}
	for _, col := range sourceSchema.Columns {
		newCol := col
		newCol.Name = sourceName + "." + col.Name
		combinedSchema.Columns = append(combinedSchema.Columns, newCol)
	}

	affected := 0

	// Try hash join for simple equality ON conditions
	eqCols := extractMergeEqualityColumns(c.stmt.OnCondition, c.stmt.TargetTable, sourceName)
	if len(eqCols) > 0 && c.stmt.WhenMatched != nil {
		return c.executeMergeHashJoin(ctx, dbName, targetRows, sourceRows, targetSchema, sourceSchema, eqCols, combinedSchema)
	}

	// Fall back to nested loop for complex ON conditions
	affected, err = c.executeMergeNestedLoop(ctx, dbName, targetRows, sourceRows, targetSchema, sourceSchema, combinedSchema, sourceName)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TargetTable)

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("MERGE: %d rows affected.", affected),
	}, nil
}

// executeMergeHashJoin использует hash join для простых ON-условий (col = col).
func (c *MergeCommand) executeMergeHashJoin(ctx *ExecutionContext, dbName string, targetRows, sourceRows []storage.Row, targetSchema, sourceSchema *storage.TableSchema, eqCols [][2]string, combinedSchema *storage.TableSchema) (*Result, error) {
	affected := 0

	// combinedColIdx maps combined-schema column names to their index.
	// eqCols[0][0] = "heroes.id" (target side), eqCols[0][1] = "u.id" (source side).
	// Both are qualified names from the combined schema.
	combinedColIdx := make(map[string]int, len(combinedSchema.Columns))
	for i, col := range combinedSchema.Columns {
		combinedColIdx[strings.ToLower(col.Name)] = i
	}

	targetColOffset := len(targetSchema.Columns)

	// Build index on target rows using the target-side qualified name.
	targetIndex := make(map[string][]int)
	for idx, row := range targetRows {
		// Build a temporary combined row (just the target portion) for key extraction.
		key := buildMergeKeyFromCombined(row, eqCols[0][0], combinedColIdx, 0)
		targetIndex[key] = append(targetIndex[key], idx)
	}

	for _, sourceRow := range sourceRows {
		// Build combined row for this source to look up the key.
		tmpCombined := make(storage.Row, len(combinedSchema.Columns))
		for i := range tmpCombined {
			tmpCombined[i] = nil
		}
		copy(tmpCombined[targetColOffset:], sourceRow)

		key := buildMergeKeyFromCombined(tmpCombined, eqCols[0][1], combinedColIdx, 0)
		targetIndices := targetIndex[key]

		if len(targetIndices) == 0 {
			if c.stmt.WhenNotMatched != nil && c.stmt.WhenNotMatched.Action == "INSERT" {
				if err := c.executeMergeInsert(ctx, dbName, sourceRow, targetSchema, sourceSchema, combinedSchema); err != nil {
					return nil, err
				}
				affected++
			}
			continue
		}

		for _, targetIdx := range targetIndices {
			targetRow := targetRows[targetIdx]
			combinedRow := append(append(storage.Row{}, targetRow...), sourceRow...)

			ok, err := evalExpr(c.stmt.OnCondition, combinedRow, combinedSchema, ctx)
			if err != nil || !ok {
				continue
			}

			if c.stmt.WhenMatched != nil && c.stmt.WhenMatched.Action == "UPDATE" {
				updates := make(map[string]storage.Value)
				for _, assign := range c.stmt.WhenMatched.Assignments {
					val, err := evalOperand(assign.Value, combinedRow, combinedSchema, ctx)
					if err != nil {
						return nil, err
					}
					updates[assign.Column] = val
				}

				_, err = ctx.Storage.UpdateRows(dbName, c.stmt.TargetTable, []int{targetIdx}, updates)
				if err != nil {
					return nil, err
				}
				affected++
			}
		}
	}

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("MERGE: %d rows affected.", affected),
	}, nil
}

// buildMergeKeyFromCombined extracts a key from a combined row using the combined schema column name.
func buildMergeKeyFromCombined(row storage.Row, colName string, combinedColIdx map[string]int, _ int) string {
	ci, ok := combinedColIdx[strings.ToLower(colName)]
	if !ok || ci >= len(row) {
		return ""
	}
	return valueToString(row[ci])
}

// executeMergeNestedLoop используется для сложных ON-условий.
func (c *MergeCommand) executeMergeNestedLoop(ctx *ExecutionContext, dbName string, targetRows, sourceRows []storage.Row, targetSchema, sourceSchema *storage.TableSchema, combinedSchema *storage.TableSchema, sourceName string) (int, error) {
	affected := 0

	for _, sourceRow := range sourceRows {
		matched := false

		for _, targetRow := range targetRows {
			combinedRow := append(append(storage.Row{}, targetRow...), sourceRow...)

			ok, err := evalExpr(c.stmt.OnCondition, combinedRow, combinedSchema, ctx)
			if err == nil && ok {
				matched = true

				if c.stmt.WhenMatched != nil && c.stmt.WhenMatched.Action == "UPDATE" {
					updates := make(map[string]storage.Value)
					for _, assign := range c.stmt.WhenMatched.Assignments {
						val, err := evalOperand(assign.Value, combinedRow, combinedSchema, ctx)
						if err != nil {
							return 0, err
						}
						updates[assign.Column] = val
					}

					targetIdx := -1
					for i, tr := range targetRows {
						if rowsEqual(tr, targetRow) {
							targetIdx = i
							break
						}
					}

					if targetIdx >= 0 {
						_, err = ctx.Storage.UpdateRows(dbName, c.stmt.TargetTable, []int{targetIdx}, updates)
						if err != nil {
							return 0, err
						}
						affected++
					}
				}
				break
			}
		}

		if !matched && c.stmt.WhenNotMatched != nil && c.stmt.WhenNotMatched.Action == "INSERT" {
			if err := c.executeMergeInsert(ctx, dbName, sourceRow, targetSchema, sourceSchema, combinedSchema); err != nil {
				return 0, err
			}
			affected++
		}
	}

	return affected, nil
}

// executeMergeInsert выполняет INSERT для WHEN NOT MATCHED.
func (c *MergeCommand) executeMergeInsert(ctx *ExecutionContext, dbName string, sourceRow storage.Row, targetSchema, sourceSchema *storage.TableSchema, combinedSchema *storage.TableSchema) error {
	if len(c.stmt.WhenNotMatched.Values) > 0 && len(c.stmt.WhenNotMatched.Columns) > 0 {
		if len(c.stmt.WhenNotMatched.Columns) != len(c.stmt.WhenNotMatched.Values[0]) {
			return fmt.Errorf("MERGE: WHEN NOT MATCHED columns count (%d) doesn't match values count (%d)",
				len(c.stmt.WhenNotMatched.Columns), len(c.stmt.WhenNotMatched.Values[0]))
		}
	}

	combinedRowForInsert := make(storage.Row, len(combinedSchema.Columns))
	targetColCount := len(targetSchema.Columns)
	for i, val := range sourceRow {
		if targetColCount+i < len(combinedRowForInsert) {
			combinedRowForInsert[targetColCount+i] = val
		}
	}

	insertValues := make(storage.Row, len(c.stmt.WhenNotMatched.Values[0]))
	for i, val := range c.stmt.WhenNotMatched.Values[0] {
		v, err := evalOperand(val, combinedRowForInsert, combinedSchema, ctx)
		if err != nil {
			return err
		}
		insertValues[i] = v
	}

	fullInsertRow := make(storage.Row, len(targetSchema.Columns))
	for i := range fullInsertRow {
		fullInsertRow[i] = nil
	}

	if len(c.stmt.WhenNotMatched.Columns) == 0 {
		if len(insertValues) != len(targetSchema.Columns) {
			return fmt.Errorf("MERGE INSERT: expected %d values, got %d", len(targetSchema.Columns), len(insertValues))
		}
		for i, v := range insertValues {
			converted, err := normalizeForColumn(v, targetSchema.Columns[i])
			if err != nil {
				return fmt.Errorf("MERGE INSERT column '%s': %w", targetSchema.Columns[i].Name, err)
			}
			fullInsertRow[i] = converted
		}
	} else {
		targetColIndex := make(map[string]int)
		for idx, col := range targetSchema.Columns {
			targetColIndex[strings.ToLower(col.Name)] = idx
		}
		for i, colName := range c.stmt.WhenNotMatched.Columns {
			idx, ok := targetColIndex[strings.ToLower(colName)]
			if !ok {
				return fmt.Errorf("MERGE INSERT: unknown target column '%s'", colName)
			}
			if i < len(insertValues) {
				converted, err := normalizeForColumn(insertValues[i], targetSchema.Columns[idx])
				if err != nil {
					return fmt.Errorf("MERGE INSERT column '%s': %w", colName, err)
				}
				fullInsertRow[idx] = converted
			}
		}
	}

	_, err := ctx.Storage.InsertRows(dbName, c.stmt.TargetTable, []storage.Row{fullInsertRow})
	return err
}

// SavepointCommand выполняет SAVEPOINT.
type SavepointCommand struct {
	stmt *parser.SavepointStatement
}

func (c *SavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	tx.Savepoint(c.stmt.Name)
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Savepoint '%s' established.", c.stmt.Name),
	}, nil
}

// RollbackToSavepointCommand выполняет ROLLBACK TO SAVEPOINT.
type RollbackToSavepointCommand struct {
	stmt *parser.RollbackToSavepointStatement
}

func (c *RollbackToSavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("ROLLBACK TO SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	if err := tx.RollbackToSavepoint(c.stmt.Name); err != nil {
		return nil, err
	}
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Rolled back to savepoint '%s'.", c.stmt.Name),
	}, nil
}

// ReleaseSavepointCommand выполняет RELEASE SAVEPOINT.
type ReleaseSavepointCommand struct {
	stmt *parser.ReleaseSavepointStatement
}

func (c *ReleaseSavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("RELEASE SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	if !tx.ReleaseSavepoint(c.stmt.Name) {
		return nil, fmt.Errorf("savepoint %q does not exist", c.stmt.Name)
	}
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Savepoint '%s' released.", c.stmt.Name),
	}, nil
}
