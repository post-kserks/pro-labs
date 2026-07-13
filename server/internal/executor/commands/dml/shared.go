package dml

// Shared DML utilities: RETURNING projection, mutation notification,
// RLS enforcement, CHECK constraint enforcement.

import (
	"fmt"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/executor/types"
)

func executeReturningGeneric(rows []storage.Row, returningCols []parser.SelectColumn, schema *storage.TableSchema, ctx *types.ExecutionContext, oldRows ...storage.Row) (*types.Result, error) {
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
				projected[i] = types.ValueToString(row[i])
			}
		} else {
			projected = make([]string, len(returningCols))
			for i, col := range returningCols {
				val, err := types.EvalOperand(col.Expr, row, schema, ctx)
				if err != nil {
					projected[i] = "ERR"
				} else {
					projected[i] = types.ValueToString(val)
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
	return &types.Result{
		Type:    "rows",
		Columns: projectColumns,
		Rows:    resultRows,
	}, nil
}

func notifyMutation(ctx *types.ExecutionContext, dbName, tableName string) {
	if ctx.Stats != nil {
		ctx.Stats.InvalidateStats(dbName, tableName)
	}
	if ctx.Session != nil {
		ctx.Session.InvalidateResultCache(tableName)
	}
	if ctx.TxManager != nil {
		ctx.TxManager.BumpTableVersion(dbName, tableName)
	}
	types.NotifyBroadcaster(ctx, dbName, tableName)
}

// enforceRLSPolicies checks if RLS is enabled and policies exist for the table.
func enforceRLSPolicies(ctx *types.ExecutionContext, dbName, tableName string) error {
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
func filterRowsWithRLS(rows []storage.Row, schema *storage.TableSchema, ctx *types.ExecutionContext, dbName, tableName string) ([]storage.Row, error) {
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
			ok, err := types.EvalOperand(expr, row, schema, ctx)
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

func enforceCheckConstraints(schema *storage.TableSchema, row storage.Row) error {
	for _, c := range schema.Constraints {
		if c.Type == "CHECK" && c.Expr != "" {
			ok, err := types.EvaluateCheckExpr(c.Expr, row, schema)
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
func enforceUniqueConstraints(dbName, tableName string, schema *storage.TableSchema, rows []storage.Row, ctx *types.ExecutionContext) error {
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
func enforceUniqueConstraintsOnUpdate(dbName, tableName string, schema *storage.TableSchema, indices []int, newValues []storage.Row, ctx *types.ExecutionContext) error {
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

	return nil
}

// enforceUniqueIndexConstraints checks UNIQUE index constraints against existing rows.
func enforceUniqueIndexConstraints(dbName, tableName string, rows []storage.Row, ctx *types.ExecutionContext) error {
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

		colName := idx.Column()
		colIdx := idx.ColIndex()

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

// invalidatePlanCache invalidates the plan cache for a table if available.
func invalidatePlanCache(ctx *types.ExecutionContext, tableName string) {
	if ctx.Session != nil {
		ctx.Session.InvalidatePlanCache(tableName)
	}
}

// rowsEqual compares two rows element by element.
func rowsEqual(a, b storage.Row) bool {
	return types.RowsEqual(a, b)
}
