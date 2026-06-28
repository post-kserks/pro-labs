package executor

// DELETE command implementation.

import (
	"fmt"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

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

	if c.stmt.Returning != nil {
		return c.executeReturningDelete(ctx, dbName, schema, indices, rows)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *DeleteCommand) executeReturningDelete(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, preDeleteRows []storage.Row) (*Result, error) {
	var deletedRows []storage.Row
	for _, idx := range indices {
		if idx < len(preDeleteRows) {
			deletedRows = append(deletedRows, preDeleteRows[idx])
		}
	}
	return executeReturningGeneric(deletedRows, c.stmt.Returning, schema, ctx)
}
