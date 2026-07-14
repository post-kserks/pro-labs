package executor

// Shared DML utilities that remain in root for use by non-DML files.
// The canonical implementations live in commands/dml/shared.go.

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/executor/types"
)

func exprToSQL(expr parser.Expression) string {
	return types.ExprToSQL(expr)
}

func formatValue(v parser.Value) string {
	return types.FormatValueForExpr(v)
}

func notifyMutation(ctx *ExecutionContext, dbName, tableName string) {
	if ctx.Stats != nil {
		ctx.Stats.InvalidateStats(dbName, tableName)
	}
	if ctx.Session != nil {
		ctx.Session.InvalidateResultCache(tableName)
	}
	if ctx.TxManager != nil {
		ctx.TxManager.BumpTableVersion(dbName, tableName)
	}
	if b, ok := ctx.Broadcaster.(*Broadcaster); ok && b != nil {
		b.NotifyTableChanged(dbName, tableName, ctx)
	}
}

// filterRowsWithRLS applies RLS USING policies to filter rows.
func filterRowsWithRLS(rows []storage.Row, schema *storage.TableSchema, ctx *ExecutionContext, dbName, tableName string) ([]storage.Row, error) {
	if !schema.RLSEnabled {
		return rows, nil
	}
	if len(schema.Policies) == 0 {
		return nil, fmt.Errorf("RLS is enabled on table '%s' but no policies are defined", tableName)
	}

	filtered := make([]storage.Row, 0, len(rows))
	for _, row := range rows {
		visible := false
		for _, policy := range schema.Policies {
			if policy.UsingExpr == "" {
				visible = true
				break
			}
			expr, err := parser.ParseExpression(policy.UsingExpr)
			if err != nil {
				return nil, fmt.Errorf("RLS policy '%s': invalid expression: %w", policy.Name, err)
			}
			ok, err := evalOperand(expr, row, schema, ctx)
			if err != nil {
				continue
			}
			if b, ok := ok.(bool); ok && b {
				visible = true
				break
			}
		}
		if visible {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

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
