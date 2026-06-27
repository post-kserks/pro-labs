package executor

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/storage"
)

func (c *SelectCommand) executeJoins(ctx *ExecutionContext, dbName string, leftSchema *storage.TableSchema, leftRows []storage.Row) (*storage.TableSchema, []storage.Row, error) {
	currentSchema := leftSchema
	currentRows := leftRows

	for _, join := range c.stmt.Joins {
		if !ctx.Storage.TableExists(dbName, join.TableName) {
			return nil, nil, fmt.Errorf("joined table '%s' does not exist", join.TableName)
		}
		rightSchema, err := ctx.Storage.GetTableSchema(dbName, join.TableName)
		if err != nil {
			return nil, nil, err
		}
		if join.Alias != "" {
			rightSchema.Name = join.Alias
		}

		rightRows, err := ctx.Storage.ReadCurrentRows(dbName, join.TableName)
		if err != nil {
			return nil, nil, err
		}
		// read-your-own-writes для правой таблицы джойна (Bug #1).
		rightRows, err = applyTxOverlay(ctx, dbName, join.TableName, rightRows)
		if err != nil {
			return nil, nil, err
		}

		// Create combined schema with qualified names
		combinedSchema := &storage.TableSchema{
			Name:    "JOIN_RESULT",
			Columns: make([]storage.ColumnSchema, 0, len(currentSchema.Columns)+len(rightSchema.Columns)),
		}
		for _, col := range currentSchema.Columns {
			newCol := col
			if !strings.Contains(newCol.Name, ".") && currentSchema.Name != "JOIN_RESULT" {
				newCol.Name = currentSchema.Name + "." + col.Name
			}
			combinedSchema.Columns = append(combinedSchema.Columns, newCol)
		}
		for _, col := range rightSchema.Columns {
			newCol := col
			newCol.Name = rightSchema.Name + "." + col.Name
			combinedSchema.Columns = append(combinedSchema.Columns, newCol)
		}

		newRows := make([]storage.Row, 0)

		switch join.Type {
		case "CROSS":
			// Cross join: all combinations
			for _, lrow := range currentRows {
				for _, rrow := range rightRows {
					combinedRow := append(append(storage.Row{}, lrow...), rrow...)
					newRows = append(newRows, combinedRow)
				}
			}

		case "INNER", "":
			// Inner join: only matching rows
			for _, lrow := range currentRows {
				matched := false
				for _, rrow := range rightRows {
					combinedRow := append(append(storage.Row{}, lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedRow, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, combinedRow)
						matched = true
					}
				}
				_ = matched
			}

		case "LEFT":
			// Left join: all left rows, NULL-fill unmatched right
			rightNulls := make(storage.Row, len(rightSchema.Columns))
			for i := range rightNulls {
				rightNulls[i] = nil
			}
			for _, lrow := range currentRows {
				matched := false
				for _, rrow := range rightRows {
					combinedRow := append(append(storage.Row{}, lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedRow, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, combinedRow)
						matched = true
					}
				}
				if !matched {
					// No match — add left row with NULLs for right columns
					newRows = append(newRows, append(append(storage.Row{}, lrow...), rightNulls...))
				}
			}

		case "RIGHT":
			// Right join: all right rows, NULL-fill unmatched left
			leftNulls := make(storage.Row, len(currentSchema.Columns))
			for i := range leftNulls {
				leftNulls[i] = nil
			}
			for _, rrow := range rightRows {
				matched := false
				for _, lrow := range currentRows {
					combinedRow := append(append(storage.Row{}, lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedRow, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, combinedRow)
						matched = true
					}
				}
				if !matched {
					// No match — add right row with NULLs for left columns
					newRows = append(newRows, append(append(storage.Row{}, leftNulls...), rrow...))
				}
			}

		case "FULL":
			// Full join: all rows from both sides, NULL-fill unmatched
			leftNulls := make(storage.Row, len(currentSchema.Columns))
			for i := range leftNulls {
				leftNulls[i] = nil
			}
			rightNulls := make(storage.Row, len(rightSchema.Columns))
			for i := range rightNulls {
				rightNulls[i] = nil
			}

			// Track which right rows matched
			rightMatched := make(map[int]bool)

			for _, lrow := range currentRows {
				lmatched := false
				for ri, rrow := range rightRows {
					combinedRow := append(append(storage.Row{}, lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedRow, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, combinedRow)
						lmatched = true
						rightMatched[ri] = true
					}
				}
				if !lmatched {
					// Left row has no match — add with NULLs for right
					newRows = append(newRows, append(append(storage.Row{}, lrow...), rightNulls...))
				}
			}

			// Add unmatched right rows with NULLs for left
			for ri, rrow := range rightRows {
				if !rightMatched[ri] {
					newRows = append(newRows, append(append(storage.Row{}, leftNulls...), rrow...))
				}
			}
		}

		currentSchema = combinedSchema
		currentRows = newRows
	}

	return currentSchema, currentRows, nil
}

// hashJoin выполняет Hash Join для equi-join.
func compareResultCells(a, b string) int {
	af, aerr := strconv.ParseFloat(a, 64)
	bf, berr := strconv.ParseFloat(b, 64)
	if aerr == nil && berr == nil {
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(a, b)
}
