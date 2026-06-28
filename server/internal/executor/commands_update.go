package executor

// UPDATE command implementation.

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

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

	var evalRows []storage.Row
	var evalSchema *storage.TableSchema
	if fromRows != nil {
		evalRows = make([]storage.Row, 0)
		for _, targetRow := range rows {
			for _, sourceRow := range fromRows {
				combinedRow := append(append(storage.Row{}, targetRow...), sourceRow...)
				evalRows = append(evalRows, combinedRow)
			}
		}
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

	if c.stmt.Returning != nil {
		return c.executeReturningUpdate(ctx, dbName, schema, indices, rows)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *UpdateCommand) executeReturningUpdate(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, preUpdateRows []storage.Row) (*Result, error) {
	var updatedRows []storage.Row
	for _, idx := range indices {
		if idx < len(preUpdateRows) {
			updatedRows = append(updatedRows, preUpdateRows[idx])
		}
	}
	return executeReturningGeneric(updatedRows, c.stmt.Returning, schema, ctx)
}
