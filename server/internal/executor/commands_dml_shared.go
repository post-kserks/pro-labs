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

func executeReturningGeneric(rows []storage.Row, returningCols []parser.SelectColumn, schema *storage.TableSchema, ctx *ExecutionContext, oldRows ...storage.Row) (*Result, error) {
	resultRows := make([][]string, 0, len(rows))

	starMode := len(returningCols) == 0

	for i, row := range rows {
		// Set old/new row context for old.* / new.* syntax
		if ctx != nil {
			ctx.NewRow = row
			if i < len(oldRows) {
				ctx.OldRow = oldRows[i]
			} else {
				ctx.OldRow = row
			}
		}

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
	case *parser.JSONAccess:
		return exprToSQL(e.Expr) + " " + e.Operator + " " + exprToSQL(e.Argument)
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

// enforceUniqueConstraints checks UNIQUE column constraints against existing rows.
func enforceUniqueConstraints(dbName, tableName string, schema *storage.TableSchema, rows []storage.Row, ctx *ExecutionContext) error {
	existingRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
	if err != nil {
		return err
	}

	for i, col := range schema.Columns {
		if !col.Unique {
			continue
		}
		existing := make(map[interface{}]bool, len(existingRows))
		for _, row := range existingRows {
			if i < len(row) && row[i] != nil {
				existing[row[i]] = true
			}
		}
		for _, row := range rows {
			if i < len(row) && row[i] != nil {
				if existing[row[i]] {
					return fmt.Errorf("duplicate value %v for unique column '%s'", row[i], col.Name)
				}
				existing[row[i]] = true
			}
		}
	}

	// Also check unique index constraints
	if err := enforceUniqueIndexConstraints(dbName, tableName, rows, ctx); err != nil {
		return err
	}

	return nil
}

// enforceUniqueConstraintsOnUpdate checks UNIQUE constraints for UPDATE operations.
func enforceUniqueConstraintsOnUpdate(dbName, tableName string, schema *storage.TableSchema, indices []int, newValues []storage.Row, ctx *ExecutionContext) error {
	existingRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
	if err != nil {
		return err
	}

	updatedPositions := make(map[int]bool, len(indices))
	for _, idx := range indices {
		updatedPositions[idx] = true
	}

	for i, col := range schema.Columns {
		if !col.Unique {
			continue
		}
		existing := make(map[interface{}]bool, len(existingRows))
		for pos, row := range existingRows {
			if updatedPositions[pos] {
				continue
			}
			if i < len(row) && row[i] != nil {
				existing[row[i]] = true
			}
		}
		for _, row := range newValues {
			if i < len(row) && row[i] != nil {
				if existing[row[i]] {
					return fmt.Errorf("duplicate value %v for unique column '%s'", row[i], col.Name)
				}
				existing[row[i]] = true
			}
		}
	}

	// Also check unique index constraints (skip for updates - unique indexes are enforced at insert time)
	return nil
}

// enforceUniqueIndexConstraints checks UNIQUE index constraints against existing rows.
func enforceUniqueIndexConstraints(dbName, tableName string, rows []storage.Row, ctx *ExecutionContext) error {
	indexNames, err := ctx.Storage.ListIndexes(dbName, tableName)
	if err != nil {
		return err
	}

	for _, idxName := range indexNames {
		idx, ok := ctx.Storage.GetIndex(dbName, tableName, idxName)
		if !ok {
			continue
		}
		if !idx.IsUnique() {
			continue
		}

		// For single-column indexes, check for duplicate values
		colName := idx.Column()
		colIdx := idx.ColIndex()

		// Build set of existing values from the index
		existingRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
		if err != nil {
			return err
		}

		existingKeys := make(map[string]bool, len(existingRows))
		for _, row := range existingRows {
			if colIdx < len(row) && row[colIdx] != nil {
				key := fmt.Sprintf("%v", row[colIdx])
				existingKeys[key] = true
			}
		}

		// Check new rows against existing keys
		for _, row := range rows {
			if colIdx < len(row) && row[colIdx] != nil {
				key := fmt.Sprintf("%v", row[colIdx])
				if existingKeys[key] {
					return fmt.Errorf("duplicate value %v for unique index '%s' on column '%s'", row[colIdx], idxName, colName)
				}
				existingKeys[key] = true
			}
		}
	}

	return nil
}
