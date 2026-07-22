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

	var positions []int
	if c.stmt.Where != nil {
		pos, ok := tryIndexLookup(ctx, dbName, c.stmt.TableName, c.stmt.Where)
		if ok && pos != nil {
			positions = pos
		}
	}

	var deletedRows []storage.Row

	affected, err := ctx.Storage.DeleteRowsVM(dbName, c.stmt.TableName, positions, func(rawTuple []byte) (bool, error) {
		if c.stmt.Where == nil {
			return true, nil
		}
		_, _, row, errRow := storage.DecodeRow(rawTuple, schema)
		if errRow != nil {
			return false, errRow
		}
		if schema.RLSEnabled && len(schema.Policies) > 0 {
			visible := false
			for _, policy := range schema.Policies {
				if policy.UsingExpr == "" {
					visible = true
					break
				}
				expr, errExpr := parser.ParseExpression(policy.UsingExpr)
				if errExpr != nil {
					return false, fmt.Errorf("RLS policy '%s': invalid expression: %w", policy.Name, errExpr)
				}
				ok, errEval := types.EvalOperand(expr, row, schema, ctx)
				if errEval != nil {
					continue
				}
				if b, ok := ok.(bool); ok && b {
					visible = true
					break
				}
			}
			if !visible {
				return false, nil
			}
		}

		match, errEval := types.EvalExpr(c.stmt.Where, row, schema, ctx)
		if errEval != nil {
			return false, errEval
		}
		return match, nil
	}, func(indices []int, rows []storage.Row) error {
		if errFK := types.EnforceForeignKeysOnDelete(ctx, dbName, c.stmt.TableName, indices); errFK != nil {
			return errFK
		}
		if errCas := types.EnforceCascadeDeletes(ctx, dbName, c.stmt.TableName, indices); errCas != nil {
			return errCas
		}
		deletedRows = append(deletedRows, rows...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	invalidatePlanCache(ctx, c.stmt.TableName)

	types.FireTriggers(ctx, dbName, c.stmt.TableName, "DELETE")

	if c.stmt.Returning != nil {
		return c.executeReturningDelete(ctx, dbName, schema, nil, deletedRows)
	}

	return &types.Result{Type: "affected", Affected: affected}, nil
}

func (c *DeleteCommand) executeReturningDelete(ctx *types.ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, deletedRows []storage.Row) (*types.Result, error) {
	return executeReturningGeneric(deletedRows, c.stmt.Returning, schema, ctx)
}
