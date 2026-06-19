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

func (c *MergeCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	// Read target table
	if !ctx.Storage.TableExists(dbName, c.stmt.TargetTable) {
		return nil, fmt.Errorf("target table '%s' does not exist", c.stmt.TargetTable)
	}
	targetRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TargetTable)
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

	// Process each source row
	for _, sourceRow := range sourceRows {
		matched := false

		// Check if any target row matches
		for _, targetRow := range targetRows {
			combinedRow := append(append(storage.Row{}, targetRow...), sourceRow...)

			// Evaluate ON condition
			ok, err := evalExpr(c.stmt.OnCondition, combinedRow, combinedSchema, ctx)
			if err == nil && ok {
				matched = true

				// WHEN MATCHED THEN UPDATE
				if c.stmt.WhenMatched != nil && c.stmt.WhenMatched.Action == "UPDATE" {
					updates := make(map[string]storage.Value)
					for _, assign := range c.stmt.WhenMatched.Assignments {
						val, err := evalOperand(assign.Value, combinedRow, combinedSchema, ctx)
						if err != nil {
							return nil, err
						}
						updates[assign.Column] = val
					}

					// Find target row index
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
							return nil, err
						}
						affected++
					}
				}
				break
			}
		}

		// WHEN NOT MATCHED THEN INSERT
		if !matched && c.stmt.WhenNotMatched != nil && c.stmt.WhenNotMatched.Action == "INSERT" {
			if len(c.stmt.WhenNotMatched.Values) > 0 && len(c.stmt.WhenNotMatched.Columns) > 0 {
				if len(c.stmt.WhenNotMatched.Columns) != len(c.stmt.WhenNotMatched.Values[0]) {
					return nil, fmt.Errorf("MERGE: WHEN NOT MATCHED columns count (%d) doesn't match values count (%d)",
						len(c.stmt.WhenNotMatched.Columns), len(c.stmt.WhenNotMatched.Values[0]))
				}
			}

			// Build combined row with NULLs for target columns and source row values
			combinedRowForInsert := make(storage.Row, len(combinedSchema.Columns))
			targetColCount := len(targetSchema.Columns)
			for i, val := range sourceRow {
				if targetColCount+i < len(combinedRowForInsert) {
					combinedRowForInsert[targetColCount+i] = val
				}
			}

			// Build insert values using the combined schema/row
			insertValues := make(storage.Row, len(c.stmt.WhenNotMatched.Values[0]))
			for i, val := range c.stmt.WhenNotMatched.Values[0] {
				v, err := evalOperand(val, combinedRowForInsert, combinedSchema, ctx)
				if err != nil {
					return nil, err
				}
				insertValues[i] = v
			}

			// Map insert values to target table columns
			fullInsertRow := make(storage.Row, len(targetSchema.Columns))
			for i := range fullInsertRow {
				fullInsertRow[i] = nil
			}

			if len(c.stmt.WhenNotMatched.Columns) == 0 {
				if len(insertValues) != len(targetSchema.Columns) {
					return nil, fmt.Errorf("MERGE INSERT: expected %d values, got %d", len(targetSchema.Columns), len(insertValues))
				}
				for i, v := range insertValues {
					converted, err := normalizeForColumn(v, targetSchema.Columns[i])
					if err != nil {
						return nil, fmt.Errorf("MERGE INSERT column '%s': %w", targetSchema.Columns[i].Name, err)
					}
					fullInsertRow[i] = converted
				}
			} else {
				// Column list provided
				targetColIndex := make(map[string]int)
				for idx, col := range targetSchema.Columns {
					targetColIndex[strings.ToLower(col.Name)] = idx
				}
				for i, colName := range c.stmt.WhenNotMatched.Columns {
					idx, ok := targetColIndex[strings.ToLower(colName)]
					if !ok {
						return nil, fmt.Errorf("MERGE INSERT: unknown target column '%s'", colName)
					}
					if i < len(insertValues) {
						converted, err := normalizeForColumn(insertValues[i], targetSchema.Columns[idx])
						if err != nil {
							return nil, fmt.Errorf("MERGE INSERT column '%s': %w", colName, err)
						}
						fullInsertRow[idx] = converted
					}
				}
			}

			_, err = ctx.Storage.InsertRows(dbName, c.stmt.TargetTable, []storage.Row{fullInsertRow})
			if err != nil {
				return nil, err
			}
			affected++
		}
	}

	notifyMutation(ctx, dbName, c.stmt.TargetTable)

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("MERGE: %d rows affected.", affected),
	}, nil
}

// SavepointCommand выполняет SAVEPOINT.
type SavepointCommand struct {
	stmt *parser.SavepointStatement
}

func (c *SavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return nil, fmt.Errorf("SAVEPOINT not yet implemented; use BEGIN/COMMIT/ROLLBACK for transaction control")
}

// RollbackToSavepointCommand выполняет ROLLBACK TO SAVEPOINT.
type RollbackToSavepointCommand struct {
	stmt *parser.RollbackToSavepointStatement
}

func (c *RollbackToSavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return nil, fmt.Errorf("ROLLBACK TO SAVEPOINT not yet implemented; use BEGIN/COMMIT/ROLLBACK for transaction control")
}

// ReleaseSavepointCommand выполняет RELEASE SAVEPOINT.
type ReleaseSavepointCommand struct {
	stmt *parser.ReleaseSavepointStatement
}

func (c *ReleaseSavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return nil, fmt.Errorf("RELEASE SAVEPOINT not yet implemented; use BEGIN/COMMIT/ROLLBACK for transaction control")
}
