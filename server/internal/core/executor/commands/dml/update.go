package dml

// UPDATE command implementation.

import (
	"fmt"
	"strings"

	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

type UpdateCommand struct {
	stmt *parser.UpdateStatement
}

// SetStmt sets the statement for this command.
func (c *UpdateCommand) SetStmt(stmt *parser.UpdateStatement) { c.stmt = stmt }

func init() {
	types.RegisterCommand("UPDATE", func(stmt parser.Statement) types.Command {
		return &UpdateCommand{stmt: stmt.(*parser.UpdateStatement)}
	})
}

func (c *UpdateCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, _ := types.RequireCurrentDB(ctx)
	if dbName != "" {
		if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
	}
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		frozen, err := types.FreezeUpdate(c.stmt, ctx)
		if err != nil {
			return nil, err
		}
		dbName, _ := types.RequireCurrentDB(ctx)
		var oldRows []storage.Row
		var oldIndices []int
		if dbName != "" && ctx.Storage.TableExists(dbName, c.stmt.TableName) {
			schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
			if err == nil {
				types.EnsureColumnIndex(ctx, schema)
				rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
				if err == nil {
					for idx, row := range rows {
						match, err := types.EvalExpr(frozen.Where, row, schema, ctx)
						if err == nil && match {
							oldRows = append(oldRows, row)
							oldIndices = append(oldIndices, idx)
						}
					}
				}
			}
		}

		ctx.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "update",
			DB:      ctx.Session.CurrentDatabase(),
			Table:   c.stmt.TableName,
			Payload: frozen,
			OldRow:  oldRows,
			Row:     oldIndices,
		})
		return &types.Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered UPDATE (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
		}, nil
	}
	return c.ExecuteImmediate(ctx)
}

// ExecuteImmediate applies the UPDATE immediately (skips tx buffering).
func (c *UpdateCommand) ExecuteImmediate(ctx *types.ExecutionContext) (*types.Result, error) {
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

func (c *UpdateCommand) executeImmediateInner(ctx *types.ExecutionContext) (*types.Result, error) {
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
	types.EnsureColumnIndex(ctx, schema)

	if c.stmt.FromSubquery != nil || c.stmt.FromTable != "" {
		return nil, fmt.Errorf("UPDATE FROM is not supported with VM engine yet")
	}

		var positions []int
	if c.stmt.Where != nil {
		pos, ok := tryIndexLookup(ctx, dbName, c.stmt.TableName, c.stmt.Where)
		if ok && pos != nil {
			positions = pos
		}
	}
	var matchedRows []storage.Row
	var newValues []storage.Row

	affected, err := ctx.Storage.UpdateRowsVM(dbName, c.stmt.TableName, positions, func(rawTuple []byte) (bool, error) {
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
	}, func(oldRow storage.Row) (storage.Row, error) {
		newRow := make(storage.Row, len(oldRow))
		copy(newRow, oldRow)

		for _, assign := range c.stmt.Assignments {
			val, errEval := types.EvalOperand(assign.Value, oldRow, schema, ctx)
			if errEval != nil {
				return nil, fmt.Errorf("column '%s': %w", assign.Column, errEval)
			}
			for ci, col := range schema.Columns {
				if strings.EqualFold(col.Name, assign.Column) && ci < len(newRow) {
					newRow[ci] = val
					break
				}
			}
		}

		for ci, col := range schema.Columns {
			if col.NotNull && ci < len(newRow) && newRow[ci] == nil {
				return nil, fmt.Errorf("NOT NULL constraint failed for column '%s'", col.Name)
			}
		}

		if errCheck := enforceCheckConstraints(schema, newRow); errCheck != nil {
			return nil, errCheck
		}

		matchedRows = append(matchedRows, oldRow)

		return newRow, nil
	}, func(indices []int, newRows []storage.Row) error {
		if errFK := types.EnforceForeignKeysOnUpdate(ctx, dbName, c.stmt.TableName, indices, newRows); errFK != nil {
			return errFK
		}
		if errUQ := enforceUniqueConstraintsOnUpdate(dbName, c.stmt.TableName, schema, indices, newRows, ctx); errUQ != nil {
			return errUQ
		}
		newValues = append(newValues, newRows...)
		return nil
	})

	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	invalidatePlanCache(ctx, c.stmt.TableName)

	types.FireTriggers(ctx, dbName, c.stmt.TableName, "UPDATE")

	if c.stmt.Returning != nil {
		return executeReturningGeneric(newValues, c.stmt.Returning, schema, ctx, matchedRows...)
	}

	return &types.Result{Type: "affected", Affected: affected}, nil
}

func (c *UpdateCommand) executeReturningUpdate(ctx *types.ExecutionContext, dbName string, schema *storage.TableSchema, indices []int, newValues []storage.Row, oldValues []storage.Row) (*types.Result, error) {
	return executeReturningGeneric(newValues, c.stmt.Returning, schema, ctx, oldValues...)
}
