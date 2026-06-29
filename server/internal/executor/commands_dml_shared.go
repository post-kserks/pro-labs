package executor

// Shared DML utilities: RETURNING projection, mutation notification,
// RLS enforcement, CHECK constraint enforcement.

import (
	"fmt"
	"strconv"
	"strings"

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

// filterRowsWithRLS applies RLS USING policies to filter rows.
// Returns only rows that match at least one policy's USING expression.
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

// exprToSQL converts a parser expression back to SQL text for storage.
func exprToSQL(expr parser.Expression) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		return e.Name
	case parser.Value:
		return formatValue(e)
	case *parser.Value:
		return formatValue(*e)
	case *parser.BinaryExpr:
		return "(" + exprToSQL(e.Left) + " " + e.Operator + " " + exprToSQL(e.Right) + ")"
	case *parser.AndExpr:
		return "(" + exprToSQL(e.Left) + " AND " + exprToSQL(e.Right) + ")"
	case *parser.OrExpr:
		return "(" + exprToSQL(e.Left) + " OR " + exprToSQL(e.Right) + ")"
	case *parser.NotExpr:
		return "(NOT " + exprToSQL(e.Expr) + ")"
	case *parser.InExpr:
		args := make([]string, len(e.Right))
		for i, a := range e.Right {
			args[i] = exprToSQL(a)
		}
		op := " IN "
		if e.Not {
			op = " NOT IN "
		}
		return "(" + exprToSQL(e.Left) + op + "(" + strings.Join(args, ", ") + "))"
	case *parser.BetweenExpr:
		op := " BETWEEN "
		if e.Not {
			op = " NOT BETWEEN "
		}
		return "(" + exprToSQL(e.Expr) + op + exprToSQL(e.Lower) + " AND " + exprToSQL(e.Upper) + ")"
	case *parser.CastExpr:
		return "CAST(" + exprToSQL(e.Expr) + " AS " + e.TargetType + ")"
	case *parser.JsonPathExpr:
		return exprToSQL(e.Left) + "->>'" + e.Path + "'"
	default:
		return fmt.Sprintf("%v", expr)
	}
}

func formatValue(v parser.Value) string {
	switch v.Type {
	case "int":
		return strconv.FormatInt(v.IntVal, 10)
	case "float":
		return strconv.FormatFloat(v.FltVal, 'f', -1, 64)
	case "string":
		return "'" + strings.ReplaceAll(v.StrVal, "'", "''") + "'"
	case "bool":
		if v.BoolVal {
			return "TRUE"
		}
		return "FALSE"
	case "null":
		return "NULL"
	default:
		return v.StrVal
	}
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
