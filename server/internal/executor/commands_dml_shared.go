package executor

// Shared DML utilities: RETURNING projection, mutation notification,
// RLS enforcement, CHECK constraint enforcement.

import (
	"fmt"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
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
