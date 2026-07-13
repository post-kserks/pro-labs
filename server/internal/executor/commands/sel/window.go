package sel

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"vaultdb/internal/executor/eval"
	"vaultdb/internal/executor/types"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// windowPartitionData holds pre-computed values for a partition to avoid O(n²) rescans.
type windowPartitionData struct {
	ranks         []int64
	denseRanks    []int64
	evaluatedArgs []interface{}
	prefixSums    []float64
}

func (c *SelectCommand) extractWindowFunctions() []*parser.WindowFunctionExpr {
	var funcs []*parser.WindowFunctionExpr
	for _, col := range c.stmt.Columns {
		if wf, ok := col.Expr.(*parser.WindowFunctionExpr); ok {
			funcs = append(funcs, wf)
		}
	}
	return funcs
}

func (c *SelectCommand) applyWindowFunctions(rows []storage.Row, schema *storage.TableSchema, funcs []*parser.WindowFunctionExpr, ctx *types.ExecutionContext) ([]storage.Row, *storage.TableSchema, error) {
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
		colName := fmt.Sprintf("__window_%d", wfIdx)
		if ctx.WindowCols == nil {
			ctx.WindowCols = make(map[*parser.WindowFunctionExpr]string)
		}
		ctx.WindowCols[wf] = colName
		newSchema.Columns = append(newSchema.Columns, storage.ColumnSchema{Name: colName})

		partitions := make(map[string][]int)
		for i, row := range newRows {
			key := ""
			if len(wf.Over.PartitionBy) > 0 {
				var keyParts []string
				for _, p := range wf.Over.PartitionBy {
					val, err := types.EvalOperand(p, row, schema, ctx)
					if err != nil {
						return nil, nil, fmt.Errorf("eval partition key: %w", err)
					}
					keyParts = append(keyParts, types.ValueToString(val))
				}
				key = strings.Join(keyParts, "\x00")
			}
			partitions[key] = append(partitions[key], i)
		}

		for _, indices := range partitions {
			if len(wf.Over.OrderBy) > 0 {
				sort.SliceStable(indices, func(i, j int) bool {
					rowI, rowJ := newRows[indices[i]], newRows[indices[j]]
					for _, item := range wf.Over.OrderBy {
						vi, err := types.EvalOperand(item.Expr, rowI, schema, ctx)
						if err != nil {
							slog.Error("eval order by expression", "error", err)
							return false
						}
						vj, err := types.EvalOperand(item.Expr, rowJ, schema, ctx)
						if err != nil {
							slog.Error("eval order by expression", "error", err)
							return false
						}
						cmp := eval.CompareOrdering(vi, vj)
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

			pd := c.preComputePartition(wf, indices, newRows, schema, ctx)

			for i, globalIdx := range indices {
				val, err := c.computeWindowValue(wf, indices, newRows, i, schema, ctx, pd)
				if err != nil {
					return nil, nil, fmt.Errorf("compute window value: %w", err)
				}
				newRows[globalIdx] = append(newRows[globalIdx], val)
			}
		}
	}

	return newRows, newSchema, nil
}

func (c *SelectCommand) preComputePartition(wf *parser.WindowFunctionExpr, partitionIndices []int, allRows []storage.Row, schema *storage.TableSchema, ctx *types.ExecutionContext) *windowPartitionData {
	name := strings.ToUpper(wf.FuncName)
	pd := &windowPartitionData{}
	n := len(partitionIndices)

	switch name {
	case "RANK", "DENSE_RANK":
		pd.ranks = make([]int64, n)
		pd.denseRanks = make([]int64, n)
		if n == 0 {
			return pd
		}
		pd.ranks[0] = 1
		pd.denseRanks[0] = 1
		for i := 1; i < n; i++ {
			equal, err := c.rowsEqualByOrderBy(
				allRows[partitionIndices[i]],
				allRows[partitionIndices[i-1]],
				wf.Over.OrderBy, schema, ctx,
			)
			if err != nil {
				pd.ranks[i] = int64(i + 1)
				pd.denseRanks[i] = pd.denseRanks[i-1] + 1
				continue
			}
			if equal {
				pd.ranks[i] = pd.ranks[i-1]
				pd.denseRanks[i] = pd.denseRanks[i-1]
			} else {
				pd.ranks[i] = int64(i + 1)
				pd.denseRanks[i] = pd.denseRanks[i-1] + 1
			}
		}

	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		pd.evaluatedArgs = make([]interface{}, n)
		pd.prefixSums = make([]float64, n)
		for i, idx := range partitionIndices {
			var val interface{}
			if len(wf.Args) > 0 {
				if colRef, ok := wf.Args[0].(*parser.ColumnRef); ok && colRef.Name == "*" {
					val = int64(1)
				} else {
					v, err := types.EvalOperand(wf.Args[0], allRows[idx], schema, ctx)
					if err != nil {
						slog.Error("eval window aggregate argument", "error", err)
						val = int64(1)
					} else {
						val = v
					}
				}
			} else {
				val = int64(1)
			}
			pd.evaluatedArgs[i] = val
			f := float64(0)
			if fv, ok := eval.ToFloat(val); ok {
				f = fv
			}
			if i == 0 {
				pd.prefixSums[i] = f
			} else {
				pd.prefixSums[i] = pd.prefixSums[i-1] + f
			}
		}
	}

	return pd
}

func isRunningFrame(frame *parser.FrameSpec, currentPos, partitionSize int, hasOrderBy bool) (start, end int, ok bool) {
	if frame == nil {
		if !hasOrderBy {
			return 0, partitionSize, false
		}
		return 0, currentPos + 1, true
	}
	switch frame.StartType {
	case "UNBOUNDED PRECEDING":
	case "CURRENT ROW":
		if currentPos != 0 {
			return 0, 0, false
		}
	case "PRECEDING":
		if currentPos-frame.StartN != 0 {
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	switch frame.EndType {
	case "CURRENT ROW":
	case "UNBOUNDED FOLLOWING":
		if currentPos+1 != partitionSize {
			return 0, 0, false
		}
	case "FOLLOWING":
		if currentPos+frame.EndN+1 != currentPos+1 {
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	return 0, currentPos + 1, true
}

func (c *SelectCommand) computeWindowValue(wf *parser.WindowFunctionExpr, partitionIndices []int, allRows []storage.Row, currentPosInPartition int, schema *storage.TableSchema, ctx *types.ExecutionContext, pd *windowPartitionData) (interface{}, error) {
	name := strings.ToUpper(wf.FuncName)
	switch name {
	case "ROW_NUMBER":
		return int64(currentPosInPartition + 1), nil

	case "RANK":
		if pd != nil && pd.ranks != nil {
			return pd.ranks[currentPosInPartition], nil
		}
		return int64(currentPosInPartition + 1), nil

	case "DENSE_RANK":
		if pd != nil && pd.denseRanks != nil {
			return pd.denseRanks[currentPosInPartition], nil
		}
		return int64(currentPosInPartition + 1), nil

	case "NTILE":
		n := 1
		if len(wf.Args) > 0 {
			if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := eval.ToInt64(v); ok {
					n = int(i)
				}
			}
		}
		if n <= 0 {
			return int64(0), nil
		}
		total := len(partitionIndices)
		bucketSize := total / n
		bucket := currentPosInPartition/bucketSize + 1
		if currentPosInPartition >= bucketSize*n {
			bucket = n
		}
		return int64(bucket), nil

	case "LAG":
		offset := 1
		if len(wf.Args) >= 2 {
			if v, err := types.EvalOperand(wf.Args[1], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := eval.ToInt64(v); ok {
					offset = int(i)
				}
			}
		}
		prevPos := currentPosInPartition - offset
		if prevPos < 0 {
			if len(wf.Args) >= 2 {
				return nil, nil
			}
			if len(wf.Args) >= 1 {
				if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
					return v, nil
				}
			}
			return nil, nil
		}
		if len(wf.Args) >= 1 {
			if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[prevPos]], schema, ctx); err == nil {
				return v, nil
			}
		}
		return nil, nil

	case "LEAD":
		offset := 1
		if len(wf.Args) >= 2 {
			if v, err := types.EvalOperand(wf.Args[1], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := eval.ToInt64(v); ok {
					offset = int(i)
				}
			}
		}
		nextPos := currentPosInPartition + offset
		if nextPos >= len(partitionIndices) {
			if len(wf.Args) >= 2 {
				return nil, nil
			}
			if len(wf.Args) >= 1 {
				if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
					return v, nil
				}
			}
			return nil, nil
		}
		if len(wf.Args) >= 1 {
			if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[nextPos]], schema, ctx); err == nil {
				return v, nil
			}
		}
		return nil, nil

	case "FIRST_VALUE":
		if len(partitionIndices) == 0 {
			return nil, nil
		}
		if len(wf.Args) >= 1 {
			if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[0]], schema, ctx); err == nil {
				return v, nil
			}
		}
		return nil, nil

	case "LAST_VALUE":
		if len(partitionIndices) == 0 {
			return nil, nil
		}
		if len(wf.Args) >= 1 {
			if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[len(partitionIndices)-1]], schema, ctx); err == nil {
				return v, nil
			}
		}
		return nil, nil

	case "NTH_VALUE":
		n := 1
		if len(wf.Args) >= 2 {
			if v, err := types.EvalOperand(wf.Args[1], allRows[partitionIndices[0]], schema, ctx); err == nil {
				if i, ok := eval.ToInt64(v); ok {
					n = int(i)
				}
			}
		}
		idx := n - 1
		if idx < 0 || idx >= len(partitionIndices) {
			return nil, nil
		}
		if len(wf.Args) >= 1 {
			if v, err := types.EvalOperand(wf.Args[0], allRows[partitionIndices[idx]], schema, ctx); err == nil {
				return v, nil
			}
		}
		return nil, nil

	case "COUNT", "SUM", "AVG", "MIN", "MAX":
		return c.computeWindowAggregate(wf, partitionIndices, allRows, currentPosInPartition, schema, ctx, pd)
	}
	return nil, nil
}

func (c *SelectCommand) computeWindowAggregate(wf *parser.WindowFunctionExpr, partitionIndices []int, allRows []storage.Row, currentPosInPartition int, schema *storage.TableSchema, ctx *types.ExecutionContext, pd *windowPartitionData) (interface{}, error) {
	name := strings.ToUpper(wf.FuncName)
	partitionSize := len(partitionIndices)
	hasOrderBy := len(wf.Over.OrderBy) > 0

	if pd != nil && pd.evaluatedArgs != nil {
		start, end, ok := isRunningFrame(wf.Over.Frame, currentPosInPartition, partitionSize, hasOrderBy)
		if ok {
			return aggregateFromPrefix(name, pd, start, end), nil
		}

		if wf.Over.Frame != nil && wf.Over.Frame.StartType == "UNBOUNDED PRECEDING" && wf.Over.Frame.EndType == "UNBOUNDED FOLLOWING" {
			return aggregateFromPrefix(name, pd, 0, partitionSize), nil
		}
	}

	// Fallback: compute from frame indices directly.
	frameIndices := c.getFrameIndices(partitionIndices, currentPosInPartition, wf.Over.Frame, hasOrderBy)
	agg := eval.NewAggregator(name, false)
	for _, idx := range frameIndices {
		var val interface{}
		if len(wf.Args) > 0 {
			if colRef, ok := wf.Args[0].(*parser.ColumnRef); ok && colRef.Name == "*" {
				val = int64(1)
			} else {
				v, err := types.EvalOperand(wf.Args[0], allRows[idx], schema, ctx)
				if err != nil {
					slog.Error("eval window aggregate argument", "error", err)
					continue
				}
				val = v
			}
		} else {
			val = int64(1)
		}
		agg.Add(nil, val)
	}
	return agg.Result(), nil
}

func aggregateFromPrefix(name string, pd *windowPartitionData, start, end int) interface{} {
	if start >= end {
		return defaultAggregateValue(name)
	}
	count := int64(end - start)
	sum := pd.prefixSums[end-1]
	if start > 0 {
		sum -= pd.prefixSums[start-1]
	}

	switch name {
	case "COUNT":
		return count
	case "SUM":
		if count == 1 {
			return pd.evaluatedArgs[start]
		}
		return float64(int64(sum*1000)) / 1000
	case "AVG":
		if count == 0 {
			return float64(0)
		}
		avg := sum / float64(count)
		return float64(int64(avg*1000)) / 1000
	case "MIN", "MAX":
		result := pd.evaluatedArgs[start]
		cmpFn := eval.CompareOrdering
		for i := start + 1; i < end; i++ {
			c := cmpFn(pd.evaluatedArgs[i], result)
			if (name == "MIN" && c < 0) || (name == "MAX" && c > 0) {
				result = pd.evaluatedArgs[i]
			}
		}
		return result
	}
	return nil
}

func defaultAggregateValue(name string) interface{} {
	switch name {
	case "COUNT":
		return int64(0)
	case "SUM", "AVG":
		return float64(0)
	}
	return nil
}

func (c *SelectCommand) rowsEqualByOrderBy(r1, r2 storage.Row, orderBy []parser.OrderItem, schema *storage.TableSchema, ctx *types.ExecutionContext) (bool, error) {
	for _, item := range orderBy {
		v1, err := types.EvalOperand(item.Expr, r1, schema, ctx)
		if err != nil {
			return false, fmt.Errorf("eval order by expression: %w", err)
		}
		v2, err := types.EvalOperand(item.Expr, r2, schema, ctx)
		if err != nil {
			return false, fmt.Errorf("eval order by expression: %w", err)
		}
		if eval.CompareOrdering(v1, v2) != 0 {
			return false, nil
		}
	}
	return true, nil
}

func (c *SelectCommand) getFrameIndices(partitionIndices []int, currentPos int, frame *parser.FrameSpec, hasOrderBy bool) []int {
	if frame == nil {
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
