package executor

import (
	"fmt"
	"strings"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

const maxMergeRows = 1000000

// CTECommand executes CTE (WITH clause).
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
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		ctx.Session.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "truncate",
			DB:      dbName,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered TRUNCATE (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
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
				if err := ctx.Storage.TruncateTable(op.DB, op.Table); err != nil {
					return err
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

// MergeCommand executes MERGE INTO.
type MergeCommand struct {
	stmt *parser.MergeStatement
}

// extractMergeEqualityColumns extracts column pairs from a simple ON condition
// of the form "target.col = source.col" for hash join.
// Returns: (target col name, source col name).
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
	// Determine which column belongs to which table
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

// buildMergeKeyFromCombined builds a hash key from column values for hash join.
func buildMergeKeyFromCombined(row storage.Row, colName string, combinedColIdx map[string]int, _ int) string {
	ci, ok := combinedColIdx[strings.ToLower(colName)]
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

	// MERGE writes are serialized with commits under the target table's commit lock
	// (Bug #2b); at commit-apply time, the lock is already held — mutateUnderTableLock accounts for this.
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

	// Read source: either a named table, a subquery, or VALUES
	var sourceRows []storage.Row
	var sourceSchema *storage.TableSchema
	if c.stmt.SourceValues != nil {
		// VALUES source: evaluate expressions and build in-memory rows
		sourceSchema, sourceRows, err = c.buildSourceFromValues(ctx)
		if err != nil {
			return nil, fmt.Errorf("MERGE USING VALUES: %w", err)
		}
	} else if c.stmt.SourceQuery != nil {
		// Execute subquery to obtain source rows and schema
		srcResult, err := c.executeSourceSubquery(ctx)
		if err != nil {
			return nil, fmt.Errorf("MERGE USING subquery: %w", err)
		}
		sourceRows = make([]storage.Row, len(srcResult.Rows))
		for i, r := range srcResult.Rows {
			row := make(storage.Row, len(r))
			for j, v := range r {
				row[j] = v
			}
			sourceRows[i] = row
		}
		// Build a synthetic schema from column names
		if len(srcResult.Columns) > 0 {
			cols := make([]storage.ColumnSchema, len(srcResult.Columns))
			for i, name := range srcResult.Columns {
				cols[i] = storage.ColumnSchema{Name: name}
			}
			sourceSchema = &storage.TableSchema{Name: "MERGE_SOURCE", Columns: cols}
		}
	} else {
		if !ctx.Storage.TableExists(dbName, c.stmt.SourceTable) {
			return nil, fmt.Errorf("source table '%s' does not exist", c.stmt.SourceTable)
		}
		sourceRows, err = ctx.Storage.ReadCurrentRows(dbName, c.stmt.SourceTable)
		if err != nil {
			return nil, err
		}
		sourceRows, err = applyTxOverlay(ctx, dbName, c.stmt.SourceTable, sourceRows)
		if err != nil {
			return nil, err
		}
		sourceSchema, err = ctx.Storage.GetTableSchema(dbName, c.stmt.SourceTable)
		if err != nil {
			return nil, err
		}
	}

	if len(sourceRows) > maxMergeRows {
		return nil, fmt.Errorf("MERGE INTO: source table too large (%d rows, max %d)", len(sourceRows), maxMergeRows)
	}

	// Get schemas
	targetSchema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TargetTable)
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
	} else if sourceName == "" && c.stmt.SourceQuery != nil {
		sourceName = "MERGE_SOURCE"
	}
	for _, col := range sourceSchema.Columns {
		newCol := col
		newCol.Name = sourceName + "." + col.Name
		combinedSchema.Columns = append(combinedSchema.Columns, newCol)
	}

	affected := 0
	var returningRows []storage.Row
	var oldRows []storage.Row

	// Try hash join for simple equality ON conditions
	eqCols := extractMergeEqualityColumns(c.stmt.OnCondition, c.stmt.TargetTable, sourceName)
	if len(eqCols) > 0 && c.stmt.WhenMatched != nil {
		result, retRows, oldR, err := c.executeMergeHashJoin(ctx, dbName, targetRows, sourceRows, targetSchema, sourceSchema, eqCols, combinedSchema)
		if err != nil {
			return nil, err
		}
		affected = result
		returningRows = retRows
		oldRows = oldR
	} else {
		// Fall back to nested loop for complex ON conditions
		affected, returningRows, oldRows, err = c.executeMergeNestedLoop(ctx, dbName, targetRows, sourceRows, targetSchema, sourceSchema, combinedSchema, sourceName)
		if err != nil {
			return nil, err
		}
	}

	notifyMutation(ctx, dbName, c.stmt.TargetTable)

	if len(c.stmt.Returning) > 0 && len(returningRows) > 0 {
		return executeReturningGeneric(returningRows, c.stmt.Returning, targetSchema, ctx, oldRows...)
	}

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("MERGE: %d rows affected.", affected),
	}, nil
}

// executeSourceSubquery runs the USING subquery and returns its result.
func (c *MergeCommand) executeSourceSubquery(ctx *ExecutionContext) (*Result, error) {
	cmd := &SelectCommand{stmt: c.stmt.SourceQuery.(*parser.SelectStatement)}
	return cmd.Execute(ctx)
}

// buildSourceFromValues converts VALUES expressions into in-memory rows and a synthetic schema.
func (c *MergeCommand) buildSourceFromValues(ctx *ExecutionContext) (*storage.TableSchema, []storage.Row, error) {
	numCols := len(c.stmt.SourceValues[0])

	// Build column names from aliases or defaults
	colNames := c.stmt.SourceColumns
	if len(colNames) == 0 {
		colNames = make([]string, numCols)
		for i := range colNames {
			colNames[i] = fmt.Sprintf("col%d", i+1)
		}
	}
	if len(colNames) != numCols {
		return nil, nil, fmt.Errorf("expected %d column aliases, got %d", numCols, len(colNames))
	}

	cols := make([]storage.ColumnSchema, numCols)
	for i, name := range colNames {
		cols[i] = storage.ColumnSchema{Name: name}
	}
	schema := &storage.TableSchema{Name: "MERGE_VALUES_SOURCE", Columns: cols}

	// Empty combined row for expression evaluation (no target columns available)
	emptyCombined := make(storage.Row, 0)

	rows := make([]storage.Row, 0, len(c.stmt.SourceValues))
	for _, valueRow := range c.stmt.SourceValues {
		row := make(storage.Row, numCols)
		for i, expr := range valueRow {
			val, err := evalOperand(expr, emptyCombined, nil, ctx)
			if err != nil {
				return nil, nil, fmt.Errorf("VALUES expression %d: %w", i+1, err)
			}
			row[i] = val
		}
		rows = append(rows, row)
	}

	return schema, rows, nil
}

// executeMergeHashJoin uses hash join for simple ON conditions (col = col).
func (c *MergeCommand) executeMergeHashJoin(ctx *ExecutionContext, dbName string, targetRows, sourceRows []storage.Row, targetSchema, sourceSchema *storage.TableSchema, eqCols [][2]string, combinedSchema *storage.TableSchema) (int, []storage.Row, []storage.Row, error) {
	affected := 0
	var returningRows []storage.Row
	var oldRows []storage.Row

	combinedColIdx := make(map[string]int, len(combinedSchema.Columns))
	for i, col := range combinedSchema.Columns {
		combinedColIdx[strings.ToLower(col.Name)] = i
	}

	targetColOffset := len(targetSchema.Columns)

	targetIndex := make(map[string][]int)
	for idx, row := range targetRows {
		key := buildMergeKeyFromCombined(row, eqCols[0][0], combinedColIdx, 0)
		targetIndex[key] = append(targetIndex[key], idx)
	}

	type pendingUpdate struct {
		targetIdx int
		newRow    storage.Row
		oldRow    storage.Row
	}
	var pendingUpdates []pendingUpdate

	for _, sourceRow := range sourceRows {
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
					return 0, nil, nil, err
				}
				affected++
				newRow := make(storage.Row, len(targetSchema.Columns))
				for i := range newRow {
					if i < len(sourceRow) {
						newRow[i] = sourceRow[i]
					}
				}
				returningRows = append(returningRows, newRow)
				oldRows = append(oldRows, newRow)
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
				newRow := make(storage.Row, len(targetRow))
				copy(newRow, targetRow)
				for _, assign := range c.stmt.WhenMatched.Assignments {
					val, err := evalOperand(assign.Value, combinedRow, combinedSchema, ctx)
					if err != nil {
						return 0, nil, nil, err
					}
					for ci, sc := range targetSchema.Columns {
						if strings.EqualFold(sc.Name, assign.Column) && ci < len(newRow) {
							newRow[ci] = val
							break
						}
					}
				}
				oldRow := make(storage.Row, len(targetRow))
				copy(oldRow, targetRow)
				pendingUpdates = append(pendingUpdates, pendingUpdate{targetIdx: targetIdx, newRow: newRow, oldRow: oldRow})
			}
		}
	}

	if len(pendingUpdates) > 0 {
		indices := make([]int, len(pendingUpdates))
		newValues := make([]storage.Row, len(pendingUpdates))
		for i, pu := range pendingUpdates {
			indices[i] = pu.targetIdx
			newValues[i] = pu.newRow
		}
		if _, err := ctx.Storage.UpdateRowsDirect(dbName, c.stmt.TargetTable, indices, newValues); err != nil {
			return 0, nil, nil, err
		}
		affected += len(pendingUpdates)
		for _, pu := range pendingUpdates {
			returningRows = append(returningRows, pu.newRow)
			oldRows = append(oldRows, pu.oldRow)
		}
	}

	return affected, returningRows, oldRows, nil
}

// executeMergeNestedLoop executes MERGE via nested loop.
// All UPDATEs are collected and applied in a single UpdateRowsDirect call
// to avoid position shifting from sequential UpdateRows.
func (c *MergeCommand) executeMergeNestedLoop(ctx *ExecutionContext, dbName string, targetRows, sourceRows []storage.Row, targetSchema, sourceSchema *storage.TableSchema, combinedSchema *storage.TableSchema, sourceName string) (int, []storage.Row, []storage.Row, error) {
	affected := 0
	var returningRows []storage.Row
	var oldRows []storage.Row

	type pendingUpdate struct {
		targetIdx int
		newRow    storage.Row
		oldRow    storage.Row
	}
	var pendingUpdates []pendingUpdate

	for _, sourceRow := range sourceRows {
		matched := false

		for _, targetRow := range targetRows {
			combinedRow := append(append(storage.Row{}, targetRow...), sourceRow...)

			ok, err := evalExpr(c.stmt.OnCondition, combinedRow, combinedSchema, ctx)
			if err == nil && ok {
				matched = true

				if c.stmt.WhenMatched != nil && c.stmt.WhenMatched.Action == "UPDATE" {
					targetIdx := -1
					for i, tr := range targetRows {
						if rowsEqual(tr, targetRow) {
							targetIdx = i
							break
						}
					}
					if targetIdx < 0 {
						break
					}

					newRow := make(storage.Row, len(targetRow))
					copy(newRow, targetRow)
					for _, assign := range c.stmt.WhenMatched.Assignments {
						val, err := evalOperand(assign.Value, combinedRow, combinedSchema, ctx)
						if err != nil {
							return 0, nil, nil, err
						}
						for ci, sc := range targetSchema.Columns {
							if strings.EqualFold(sc.Name, assign.Column) && ci < len(newRow) {
								newRow[ci] = val
								break
							}
						}
					}
					oldRow := make(storage.Row, len(targetRow))
					copy(oldRow, targetRow)
					pendingUpdates = append(pendingUpdates, pendingUpdate{targetIdx: targetIdx, newRow: newRow, oldRow: oldRow})
				}
				break
			}
		}

		if !matched && c.stmt.WhenNotMatched != nil && c.stmt.WhenNotMatched.Action == "INSERT" {
			if err := c.executeMergeInsert(ctx, dbName, sourceRow, targetSchema, sourceSchema, combinedSchema); err != nil {
				return 0, nil, nil, err
			}
			affected++
			newRow := make(storage.Row, len(targetSchema.Columns))
			for i := range newRow {
				if i < len(sourceRow) {
					newRow[i] = sourceRow[i]
				}
			}
			returningRows = append(returningRows, newRow)
			oldRows = append(oldRows, newRow)
		}
	}

	if len(pendingUpdates) > 0 {
		indices := make([]int, len(pendingUpdates))
		newValues := make([]storage.Row, len(pendingUpdates))
		for i, pu := range pendingUpdates {
			indices[i] = pu.targetIdx
			newValues[i] = pu.newRow
		}
		if _, err := ctx.Storage.UpdateRowsDirect(dbName, c.stmt.TargetTable, indices, newValues); err != nil {
			return 0, nil, nil, err
		}
		affected += len(pendingUpdates)
		for _, pu := range pendingUpdates {
			returningRows = append(returningRows, pu.newRow)
			oldRows = append(oldRows, pu.oldRow)
		}
	}

	return affected, returningRows, oldRows, nil
}

// executeMergeInsert performs INSERT for WHEN NOT MATCHED.
func (c *MergeCommand) executeMergeInsert(ctx *ExecutionContext, dbName string, sourceRow storage.Row, targetSchema, sourceSchema *storage.TableSchema, combinedSchema *storage.TableSchema) error {
	if c.stmt.WhenNotMatched.SelectQuery != nil {
		return c.executeMergeInsertSelect(ctx, dbName, sourceRow, targetSchema, sourceSchema, combinedSchema)
	}

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

// executeMergeInsertSelect handles INSERT ... SELECT in WHEN NOT MATCHED.
// Each expression in the SELECT list is evaluated against the combined
// (target + source) row, just like VALUES expressions are.
func (c *MergeCommand) executeMergeInsertSelect(ctx *ExecutionContext, dbName string, sourceRow storage.Row, targetSchema, sourceSchema *storage.TableSchema, combinedSchema *storage.TableSchema) error {
	selStmt, ok := c.stmt.WhenNotMatched.SelectQuery.(*parser.SelectStatement)
	if !ok {
		return fmt.Errorf("MERGE INSERT: unsupported SELECT query type")
	}

	combinedRowForInsert := make(storage.Row, len(combinedSchema.Columns))
	targetColCount := len(targetSchema.Columns)
	for i, val := range sourceRow {
		if targetColCount+i < len(combinedRowForInsert) {
			combinedRowForInsert[targetColCount+i] = val
		}
	}

	// Evaluate each SELECT column expression against the combined row
	insertValues := make(storage.Row, len(selStmt.Columns))
	for i, col := range selStmt.Columns {
		v, err := evalOperand(col.Expr, combinedRowForInsert, combinedSchema, ctx)
		if err != nil {
			return fmt.Errorf("MERGE INSERT ... SELECT column %d: %w", i, err)
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

// SavepointCommand executes SAVEPOINT.
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

// RollbackToSavepointCommand executes ROLLBACK TO SAVEPOINT.
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

// ReleaseSavepointCommand executes RELEASE SAVEPOINT.
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
