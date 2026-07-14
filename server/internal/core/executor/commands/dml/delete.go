package dml

// DELETE command implementation.

import (
	"fmt"

	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

type DeleteCommand struct {
	stmt *parser.DeleteStatement
}

// SetStmt sets the statement for this command.
func (c *DeleteCommand) SetStmt(stmt *parser.DeleteStatement) { c.stmt = stmt }

func init() {
	types.RegisterCommand("DELETE", func(stmt parser.Statement) types.Command {
		return &DeleteCommand{stmt: stmt.(*parser.DeleteStatement)}
	})
}

func (c *DeleteCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, _ := types.RequireCurrentDB(ctx)
	if dbName != "" {
		if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
	}
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		frozen, err := types.FreezeDelete(c.stmt, ctx)
		if err != nil {
			return nil, err
		}
		dbName, _ := types.RequireCurrentDB(ctx)
		var deletedRows []storage.Row
		if dbName != "" && ctx.Storage.TableExists(dbName, c.stmt.TableName) {
			schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
			if err == nil {
				rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
				if err == nil {
					for _, row := range rows {
						match, err := types.EvalExpr(frozen.Where, row, schema, ctx)
						if err == nil && match {
							deletedRows = append(deletedRows, row)
						}
					}
				}
			}
		}

		ctx.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "delete",
			DB:      ctx.Session.CurrentDatabase(),
			Table:   c.stmt.TableName,
			Payload: frozen,
			Row:     deletedRows,
		})
		return &types.Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered DELETE (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
		}, nil
	}
	return c.ExecuteImmediate(ctx)
}

// ExecuteImmediate applies the DELETE immediately (skips tx buffering).
func (c *DeleteCommand) ExecuteImmediate(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return c.executeImmediateInner(ctx)
	}
	var result *types.Result
	err = types.MutateUnderTableLock(ctx, dbName, c.stmt.TableName, func() error {
		var e error
		result, e = c.executeImmediateInner(ctx)
		return e
	})
	return result, err
}

func (c *DeleteCommand) executeImmediateInner(ctx *types.ExecutionContext) (*types.Result, error) {
	if ctx.Ctx != nil {
		select {
		case <-ctx.Ctx.Done():
			return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
		default:
		}
	}

	dbName, err := types.RequireCurrentDB(ctx)
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

	// Build column index for O(1) lookups during WHERE evaluation.
	types.EnsureColumnIndex(ctx, schema)

	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	rows, err = filterRowsWithRLS(rows, schema, ctx, dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(rows))
	for idx, row := range rows {
		match, err := types.EvalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if match {
			indices = append(indices, idx)
		}
	}

	if err := types.EnforceForeignKeysOnDelete(ctx, dbName, c.stmt.TableName, indices); err != nil {
		return nil, err
	}
	if err := types.EnforceCascadeDeletes(ctx, dbName, c.stmt.TableName, indices); err != nil {
		return nil, err
	}

	affected, err := ctx.Storage.DeleteRows(dbName, c.stmt.TableName, indices)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	invalidatePlanCache(ctx, c.stmt.TableName)

	types.FireTriggers(ctx, dbName, c.stmt.TableName, "DELETE")

	if c.stmt.Returning != nil {
		return c.executeReturningDelete(ctx, dbName, schema, indices, rows)
	}

	return &types.Result{Type: "affected", Affected: affected}, nil
}

func (c *DeleteCommand) executeReturningDelete(ctx *types.ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, preDeleteRows []storage.Row) (*types.Result, error) {
	var deletedRows []storage.Row
	for _, idx := range indices {
		if idx < len(preDeleteRows) {
			deletedRows = append(deletedRows, preDeleteRows[idx])
		}
	}
	return executeReturningGeneric(deletedRows, c.stmt.Returning, schema, ctx)
}
