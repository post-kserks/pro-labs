package executor

import (
	"fmt"
	"sort"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func (c *SelectCommand) extractWindowFunctions() []*parser.WindowFunctionExpr {
	var funcs []*parser.WindowFunctionExpr
	for _, col := range c.stmt.Columns {
		if wf, ok := col.Expr.(*parser.WindowFunctionExpr); ok {
			funcs = append(funcs, wf)
		}
	}
	return funcs
}

func (c *SelectCommand) applyWindowFunctions(rows []storage.Row, schema *storage.TableSchema, funcs []*parser.WindowFunctionExpr, ctx *ExecutionContext) ([]storage.Row, *storage.TableSchema, error) {
	newSchema := &storage.TableSchema{
		Name:    schema.Name,
		Columns: make([]storage.ColumnSchema, len(schema.Columns)),
	}
	copy(newSchema.Columns, schema.Columns)

	newRows := make([]storage.Row, len(rows))
	for i, row := range rows {
		newRows[i] = make(storage.Row, len(row))
		copy(newRows[i], row)
	}

	for wfIdx, wf := range funcs {
		// Add a uniquely named column per window function so each expression
		// resolves to its own values.
		colName := fmt.Sprintf("__window_%d", wfIdx)
		if ctx.WindowCols == nil {
			ctx.WindowCols = make(map[*parser.WindowFunctionExpr]string)
		}
		ctx.WindowCols[wf] = colName
		newSchema.Columns = append(newSchema.Columns, storage.ColumnSchema{Name: colName})

		// Partition rows
		partitions := make(map[string][]int)
		for i, row := range newRows {
			key := ""
			if len(wf.Over.PartitionBy) > 0 {
				var keyParts []string
				for _, p := range wf.Over.PartitionBy {
					val, _ := evalOperand(p, row, schema, ctx)
					keyParts = append(keyParts, valueToString(val))
				}
				key = strings.Join(keyParts, "\x00")
			}
			partitions[key] = append(partitions[key], i)
		}

		for _, indices := range partitions {
			// Sort within partition
			if len(wf.Over.OrderBy) > 0 {
				sort.SliceStable(indices, func(i, j int) bool {
					rowI, rowJ := newRows[indices[i]], newRows[indices[j]]
					for _, item := range wf.Over.OrderBy {
						vi, _ := evalOperand(item.Expr, rowI, schema, ctx)
						vj, _ := evalOperand(item.Expr, rowJ, schema, ctx)
						cmp := CompareValues(vi, vj)
						if cmp == 0 {
							continue
						}
						if item.Direction == "DESC" {
							return cmp > 0
						}
						return cmp < 0
					}
					return false
				})
			}

			// Compute window function
			for i, globalIdx := range indices {
				val := c.computeWindowValue(wf, indices, newRows, i, schema, ctx)
				newRows[globalIdx] = append(newRows[globalIdx], val)
			}
		}
	}

	return newRows, newSchema, nil
}

func (c *SelectCommand) computeWindowValue(wf *parser.WindowFunctionExpr, partitionIndices []int, allRows []storage.Row, currentPosInPartition int, schema *storage.TableSchema, ctx *ExecutionContext) interface{} {
	name := strings.ToUpper(wf.FuncName)
	switch name {
	case "ROW_NUMBER":
		return int64(currentPosInPartition + 1)
	case "RANK":
		rank := 1
		currentRow := allRows[partitionIndices[currentPosInPartition]]
		for i := 0; i < currentPosInPartition; i++ {
			prevRow := allRows[partitionIndices[i]]
			if !c.rowsEqualByOrderBy(currentRow, prevRow, wf.Over.OrderBy, schema, ctx) {
				rank = i + 2
			}
		}
		return int64(rank)
	case "DENSE_RANK":
		rank := 1
		currentRow := allRows[partitionIndices[currentPosInPartition]]
		for i := 0; i < currentPosInPartition; i++ {
			prevRow := allRows[partitionIndices[i]]
			if !c.rowsEqualByOrderBy(currentRow, prevRow, wf.Over.OrderBy, schema, ctx) {
				rank++
			}
		}
		return int64(rank)
	case "NTILE":
		n := 1
		if len(wf.Args) > 0 {
			if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := toInt64(v); ok {
					n = int(i)
				}
			}
		}
		if n <= 0 {
			return int64(0)
		}
		total := len(partitionIndices)
		bucketSize := total / n
		bucket := currentPosInPartition/bucketSize + 1
		if currentPosInPartition >= bucketSize*n {
			bucket = n
		}
		return int64(bucket)
	case "LAG":
		offset := 1
		if len(wf.Args) >= 2 {
			if v, err := evalOperand(wf.Args[1], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := toInt64(v); ok {
					offset = int(i)
				}
			}
		}
		prevPos := currentPosInPartition - offset
		if prevPos < 0 {
			// Default value
			if len(wf.Args) >= 2 {
				return nil
			}
			if len(wf.Args) >= 1 {
				if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
					return v
				}
			}
			return nil
		}
		if len(wf.Args) >= 1 {
			if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[prevPos]], schema, ctx); err == nil {
				return v
			}
		}
		return nil
	case "LEAD":
		offset := 1
		if len(wf.Args) >= 2 {
			if v, err := evalOperand(wf.Args[1], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := toInt64(v); ok {
					offset = int(i)
				}
			}
		}
		nextPos := currentPosInPartition + offset
		if nextPos >= len(partitionIndices) {
			// Default value
			if len(wf.Args) >= 2 {
				return nil
			}
			if len(wf.Args) >= 1 {
				if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
					return v
				}
			}
			return nil
		}
		if len(wf.Args) >= 1 {
			if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[nextPos]], schema, ctx); err == nil {
				return v
			}
		}
		return nil
	case "FIRST_VALUE":
		if len(partitionIndices) == 0 {
			return nil
		}
		if len(wf.Args) >= 1 {
			if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
				return v
			}
		}
		return nil
	case "LAST_VALUE":
		if len(partitionIndices) == 0 {
			return nil
		}
		if len(wf.Args) >= 1 {
			if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[len(partitionIndices)-1]], schema, ctx); err == nil {
				return v
			}
		}
		return nil
	case "NTH_VALUE":
		n := 1
		if len(wf.Args) >= 2 {
			if v, err := evalOperand(wf.Args[1], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := toInt64(v); ok {
					n = int(i)
				}
			}
		}
		idx := n - 1
		if idx < 0 || idx >= len(partitionIndices) {
			return nil
		}
		if len(wf.Args) >= 1 {
			if v, err := evalOperand(wf.Args[0], allRows[partitionIndices[idx]], schema, ctx); err == nil {
				return v
			}
		}
		return nil
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		frameIndices := c.getFrameIndices(partitionIndices, currentPosInPartition, wf.Over.Frame, len(wf.Over.OrderBy) > 0)
		agg := NewAggregator(name, false)
		for _, idx := range frameIndices {
			var val interface{}
			if len(wf.Args) > 0 {
				if colRef, ok := wf.Args[0].(*parser.ColumnRef); ok && colRef.Name == "*" {
					val = int64(1)
				} else {
					val, _ = evalOperand(wf.Args[0], allRows[idx], schema, ctx)
				}
			} else {
				val = int64(1)
			}
			agg.Add(nil, val)
		}
		return agg.Result()
	}
	return nil
}

func (c *SelectCommand) rowsEqualByOrderBy(r1, r2 storage.Row, orderBy []parser.OrderItem, schema *storage.TableSchema, ctx *ExecutionContext) bool {
	for _, item := range orderBy {
		v1, _ := evalOperand(item.Expr, r1, schema, ctx)
		v2, _ := evalOperand(item.Expr, r2, schema, ctx)
		if CompareValues(v1, v2) != 0 {
			return false
		}
	}
	return true
}

func (c *SelectCommand) getFrameIndices(partitionIndices []int, currentPos int, frame *parser.FrameSpec, hasOrderBy bool) []int {
	if frame == nil {
		// SQL default: with ORDER BY the frame runs up to the current row
		// (running total); without it the frame is the whole partition.
		if !hasOrderBy {
			return partitionIndices
		}
		return partitionIndices[:currentPos+1]
	}

	start := 0
	switch frame.StartType {
	case "UNBOUNDED PRECEDING":
		start = 0
	case "CURRENT ROW":
		start = currentPos
	case "PRECEDING":
		start = currentPos - frame.StartN
		if start < 0 {
			start = 0
		}
	}

	end := len(partitionIndices)
	switch frame.EndType {
	case "UNBOUNDED FOLLOWING":
		end = len(partitionIndices)
	case "CURRENT ROW":
		end = currentPos + 1
	case "FOLLOWING":
		end = currentPos + frame.EndN + 1
		if end > len(partitionIndices) {
			end = len(partitionIndices)
		}
	}

	if start > end {
		return nil
	}
	return partitionIndices[start:end]
}

