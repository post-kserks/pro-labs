package executor

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func (c *SelectCommand) executeJoins(ctx *ExecutionContext, dbName string, leftSchema *storage.TableSchema, leftRows []storage.Row) (*storage.TableSchema, []storage.Row, error) {
	currentSchema := leftSchema
	currentRows := leftRows
	// Reusable buffer for building combined rows during join iteration.
	// Allocated once and reset to zero-length before each cross-product pair,
	// eliminating per-row allocation from append(append(Row{}, lrow...), rrow...).
	var combinedBuf storage.Row

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
		rightRows, err = applyTxOverlay(ctx, dbName, join.TableName, rightRows)
		if err != nil {
			return nil, nil, err
		}

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

		// Try hash join for equi-joins; fall back to nested loop
		switch join.Type {
		case "CROSS":
			for _, lrow := range currentRows {
				for _, rrow := range rightRows {
					combinedBuf = append(append(combinedBuf[:0], lrow...), rrow...)
					newRows = append(newRows, storage.Row(append(storage.Row{}, combinedBuf...)))
				}
			}

		case "INNER", "":
			if join.Condition != nil && tryHashJoin(&newRows, join.Condition, currentRows, rightRows, currentSchema, rightSchema, &combinedBuf) {
				break
			}
			for _, lrow := range currentRows {
				for _, rrow := range rightRows {
					combinedBuf = append(append(combinedBuf[:0], lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedBuf, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, storage.Row(append(storage.Row{}, combinedBuf...)))
					}
				}
			}

		case "LEFT":
			if join.Condition != nil && tryHashJoinLeft(&newRows, join.Condition, currentRows, rightRows, currentSchema, rightSchema, &combinedBuf) {
				break
			}
			rightNulls := make(storage.Row, len(rightSchema.Columns))
			for i := range rightNulls {
				rightNulls[i] = nil
			}
			for _, lrow := range currentRows {
				matched := false
				for _, rrow := range rightRows {
					combinedBuf = append(append(combinedBuf[:0], lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedBuf, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, storage.Row(append(storage.Row{}, combinedBuf...)))
						matched = true
					}
				}
				if !matched {
					newRows = append(newRows, append(append(storage.Row{}, lrow...), rightNulls...))
				}
			}

		case "RIGHT":
			if join.Condition != nil && tryHashJoinRight(&newRows, join.Condition, currentRows, rightRows, currentSchema, rightSchema, &combinedBuf) {
				break
			}
			leftNulls := make(storage.Row, len(currentSchema.Columns))
			for i := range leftNulls {
				leftNulls[i] = nil
			}
			for _, rrow := range rightRows {
				matched := false
				for _, lrow := range currentRows {
					combinedBuf = append(append(combinedBuf[:0], lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedBuf, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, storage.Row(append(storage.Row{}, combinedBuf...)))
						matched = true
					}
				}
				if !matched {
					newRows = append(newRows, append(append(storage.Row{}, leftNulls...), rrow...))
				}
			}

		case "FULL":
			leftNulls := make(storage.Row, len(currentSchema.Columns))
			for i := range leftNulls {
				leftNulls[i] = nil
			}
			rightNulls := make(storage.Row, len(rightSchema.Columns))
			for i := range rightNulls {
				rightNulls[i] = nil
			}
			rightMatched := make(map[int]bool)

			for _, lrow := range currentRows {
				lmatched := false
				for ri, rrow := range rightRows {
					combinedBuf = append(append(combinedBuf[:0], lrow...), rrow...)
					ok, err := evalExpr(join.Condition, combinedBuf, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, storage.Row(append(storage.Row{}, combinedBuf...)))
						lmatched = true
						rightMatched[ri] = true
					}
				}
				if !lmatched {
					newRows = append(newRows, append(append(storage.Row{}, lrow...), rightNulls...))
				}
			}

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

// tryHashJoin attempts an equi-join hash join for INNER JOIN.
func tryHashJoin(result *[]storage.Row, cond parser.Expression, leftRows, rightRows []storage.Row, leftSchema, rightSchema *storage.TableSchema, buf *storage.Row) bool {
	leftIdx, rightIdx, ok := extractEquiJoinCols(cond, leftSchema, rightSchema)
	if !ok {
		return false
	}

	hash := buildHashTable(rightRows, rightIdx)
	for _, lrow := range leftRows {
		key := valueToString(lrow[leftIdx])
		if buckets, ok := hash[key]; ok {
			for _, ri := range buckets {
				*buf = append(append((*buf)[:0], lrow...), rightRows[ri]...)
				*result = append(*result, storage.Row(append(storage.Row{}, (*buf)...)))
			}
		}
	}
	return true
}

// tryHashJoinLeft does hash join for LEFT JOIN.
func tryHashJoinLeft(result *[]storage.Row, cond parser.Expression, leftRows, rightRows []storage.Row, leftSchema, rightSchema *storage.TableSchema, buf *storage.Row) bool {
	leftIdx, rightIdx, ok := extractEquiJoinCols(cond, leftSchema, rightSchema)
	if !ok {
		return false
	}

	hash := buildHashTable(rightRows, rightIdx)
	rightNulls := make(storage.Row, len(rightSchema.Columns))

	for _, lrow := range leftRows {
		key := valueToString(lrow[leftIdx])
		if buckets, ok := hash[key]; ok {
			for _, ri := range buckets {
				*buf = append(append((*buf)[:0], lrow...), rightRows[ri]...)
				*result = append(*result, storage.Row(append(storage.Row{}, (*buf)...)))
			}
		} else {
			*result = append(*result, append(append(storage.Row{}, lrow...), rightNulls...))
		}
	}
	return true
}

// tryHashJoinRight does hash join for RIGHT JOIN.
func tryHashJoinRight(result *[]storage.Row, cond parser.Expression, leftRows, rightRows []storage.Row, leftSchema, rightSchema *storage.TableSchema, buf *storage.Row) bool {
	leftIdx, rightIdx, ok := extractEquiJoinCols(cond, leftSchema, rightSchema)
	if !ok {
		return false
	}

	hash := buildHashTable(rightRows, rightIdx)
	leftNulls := make(storage.Row, len(leftSchema.Columns))
	matched := make(map[int]bool)

	for _, lrow := range leftRows {
		key := valueToString(lrow[leftIdx])
		if buckets, ok := hash[key]; ok {
			for _, ri := range buckets {
				*buf = append(append((*buf)[:0], lrow...), rightRows[ri]...)
				*result = append(*result, storage.Row(append(storage.Row{}, (*buf)...)))
				matched[ri] = true
			}
		}
	}

	for ri, rrow := range rightRows {
		if !matched[ri] {
			*result = append(*result, append(append(storage.Row{}, leftNulls...), rrow...))
		}
	}
	return true
}

// extractEquiJoinCols extracts left and right column indices for an equi-join condition.
func extractEquiJoinCols(cond parser.Expression, leftSchema, rightSchema *storage.TableSchema) (int, int, bool) {
	bin, ok := cond.(*parser.BinaryExpr)
	if !ok || bin.Operator != "=" {
		return 0, 0, false
	}
	leftCol, ok := bin.Left.(*parser.ColumnRef)
	if !ok {
		return 0, 0, false
	}
	rightCol, ok := bin.Right.(*parser.ColumnRef)
	if !ok {
		return 0, 0, false
	}

	// Strip table prefix (e.g. "l.id" → "id")
	leftName := stripTablePrefix(leftCol.Name)
	rightName := stripTablePrefix(rightCol.Name)

	leftIdx := findColumnIndex(leftSchema, leftName)
	rightIdx := findColumnIndex(rightSchema, rightName)
	if leftIdx >= 0 && rightIdx >= 0 {
		return leftIdx, rightIdx, true
	}

	// Try swapped (expression might have columns reversed)
	leftIdx = findColumnIndex(leftSchema, rightName)
	rightIdx = findColumnIndex(rightSchema, leftName)
	if leftIdx >= 0 && rightIdx >= 0 {
		return leftIdx, rightIdx, true
	}
	return 0, 0, false
}

// stripTablePrefix removes the table name or alias prefix from a qualified column name.
// "l.id" → "id", "left_table.name" → "name", "id" → "id"
func stripTablePrefix(name string) string {
	if idx := strings.IndexByte(name, '.'); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// findColumnIndex finds a column by name in the schema (matches unqualified names).
func findColumnIndex(schema *storage.TableSchema, name string) int {
	for i, col := range schema.Columns {
		if col.Name == name {
			return i
		}
	}
	return -1
}

// buildHashTable groups right-side rows by the value at a column index.
func buildHashTable(rows []storage.Row, colIdx int) map[string][]int {
	hash := make(map[string][]int, len(rows))
	for i, row := range rows {
		if colIdx < len(row) && row[colIdx] != nil {
			key := valueToString(row[colIdx])
			hash[key] = append(hash[key], i)
		}
	}
	return hash
}

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
