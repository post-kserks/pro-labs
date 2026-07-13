package parallel

import (
	"runtime"
	"sort"
	"strings"
	"sync"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// Evaluator provides expression evaluation capabilities needed by parallel
// operations. The executor package implements this interface to avoid a
// circular dependency between executor and parallel.
type Evaluator interface {
	EvalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (bool, error)
	EvalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (interface{}, error)
	ValueToString(val interface{}) string
	CollectAggregates(columns []parser.SelectColumn) []*parser.AggregateExpr
	CompareValues(a, b interface{}) int
	NewAggregator(name string, distinct bool, args ...interface{}) Aggregator
}

// Aggregator is the interface for aggregate operations.
type Aggregator interface {
	Add(key, value interface{})
	Result() interface{}
}

// Result represents a query result from parallel operations.
type Result struct {
	Type     string
	Columns  []string
	Rows     [][]string
	AsOfNote string
}

// ParallelConfig controls parallel query execution behavior.
type ParallelConfig struct {
	Enabled    bool `yaml:"enabled"`
	NumWorkers int  `yaml:"num_workers"` // default: runtime.NumCPU()
	MinRows    int  `yaml:"min_rows"`    // minimum rows to trigger parallelism
}

// DefaultParallelConfig returns sensible defaults for parallel execution.
func DefaultParallelConfig() ParallelConfig {
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	return ParallelConfig{
		Enabled:    true,
		NumWorkers: n,
		MinRows:    10000,
	}
}

// ParallelCoordinator manages parallel scan workers.
type ParallelCoordinator struct {
	numWorkers int
	wg         sync.WaitGroup
	eval       Evaluator
}

// NewParallelCoordinator creates a coordinator with the given worker count.
func NewParallelCoordinator(numWorkers int, eval Evaluator) *ParallelCoordinator {
	if numWorkers < 1 {
		numWorkers = 2
	}
	return &ParallelCoordinator{numWorkers: numWorkers, eval: eval}
}

// ParallelFilter splits rows into chunks and filters each in a separate goroutine.
func (pc *ParallelCoordinator) ParallelFilter(
	rows []storage.Row,
	schema *storage.TableSchema,
	where parser.Expression,
	ctx interface{},
) []storage.Row {
	if len(rows) == 0 {
		return nil
	}

	chunks := pc.splitRows(rows)
	results := make([][]storage.Row, len(chunks))

	for i, chunk := range chunks {
		pc.wg.Add(1)
		go func(idx int, r []storage.Row) {
			defer pc.wg.Done()
			filtered := make([]storage.Row, 0, len(r)/2)
			for _, row := range r {
				ok, err := pc.eval.EvalExpr(where, row, schema, ctx)
				if err == nil && ok {
					filtered = append(filtered, row)
				}
			}
			results[idx] = filtered
		}(i, chunk)
	}

	pc.wg.Wait()

	total := 0
	for _, r := range results {
		total += len(r)
	}
	all := make([]storage.Row, 0, total)
	for _, r := range results {
		all = append(all, r...)
	}
	return all
}

// ParallelProject splits rows into chunks and projects columns in parallel.
func (pc *ParallelCoordinator) ParallelProject(
	rows []storage.Row,
	columns []parser.SelectColumn,
	schema *storage.TableSchema,
	ctx interface{},
) [][]string {
	if len(rows) == 0 {
		return nil
	}

	chunks := pc.splitRows(rows)
	results := make([][][]string, len(chunks))

	for i, chunk := range chunks {
		pc.wg.Add(1)
		go func(idx int, r []storage.Row) {
			defer pc.wg.Done()
			projected := make([][]string, 0, len(r))
			for _, row := range r {
				projectedRow := make([]string, len(columns))
				for j, col := range columns {
					val, err := pc.eval.EvalOperand(col.Expr, row, schema, ctx)
					if err != nil {
						projectedRow[j] = "ERR"
					} else {
						projectedRow[j] = pc.eval.ValueToString(val)
					}
				}
				projected = append(projected, projectedRow)
			}
			results[idx] = projected
		}(i, chunk)
	}

	pc.wg.Wait()

	total := 0
	for _, r := range results {
		total += len(r)
	}
	all := make([][]string, 0, total)
	for _, r := range results {
		all = append(all, r...)
	}
	return all
}

// ParallelGroupAndAggregate parallelizes GROUP BY + aggregation by splitting rows
// into chunks, computing partial groups per chunk, then merging partial group maps.
func (pc *ParallelCoordinator) ParallelGroupAndAggregate(
	stmt *parser.SelectStatement,
	rows []storage.Row,
	schema *storage.TableSchema,
	asOfNote string,
	ctx interface{},
) (*Result, error) {
	type partialResult struct {
		groups     map[string][]storage.Row
		groupOrder []string
	}

	chunks := pc.splitRows(rows)
	partials := make([]partialResult, len(chunks))

	for i, chunk := range chunks {
		pc.wg.Add(1)
		go func(idx int, r []storage.Row) {
			defer pc.wg.Done()
			groups := make(map[string][]storage.Row)
			var groupOrder []string
			for _, row := range r {
				keyParts := make([]string, len(stmt.GroupBy))
				for gi, expr := range stmt.GroupBy {
					val, err := pc.eval.EvalOperand(expr, row, schema, ctx)
					if err != nil {
						continue
					}
					keyParts[gi] = pc.eval.ValueToString(val)
				}
				key := strings.Join(keyParts, "\x00")
				if _, ok := groups[key]; !ok {
					groupOrder = append(groupOrder, key)
				}
				groups[key] = append(groups[key], row)
			}
			partials[idx] = partialResult{groups: groups, groupOrder: groupOrder}
		}(i, chunk)
	}

	pc.wg.Wait()

	// Merge partial results preserving group order
	mergedGroups := make(map[string][]storage.Row)
	var mergedOrder []string
	orderSeen := make(map[string]bool)

	for _, p := range partials {
		for _, key := range p.groupOrder {
			if !orderSeen[key] {
				mergedOrder = append(mergedOrder, key)
				orderSeen[key] = true
			}
			mergedGroups[key] = append(mergedGroups[key], p.groups[key]...)
		}
	}

	// Handle global aggregates with no GROUP BY
	if len(stmt.GroupBy) == 0 && len(mergedOrder) == 0 {
		mergedOrder = append(mergedOrder, "")
		mergedGroups[""] = rows
	}

	// Compute aggregates on merged groups
	projectColumns := make([]string, len(stmt.Columns))
	for i, col := range stmt.Columns {
		if col.Alias != "" {
			projectColumns[i] = col.Alias
		} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			projectColumns[i] = colRef.Name
		} else {
			projectColumns[i] = "col" + string(rune('0'+i))
		}
	}

	resultRows := make([][]string, 0)
	for _, key := range mergedOrder {
		groupRows := mergedGroups[key]
		if groupRows == nil {
			continue
		}

		allAggExprs := pc.eval.CollectAggregates(stmt.Columns)
		aggMap := make(map[*parser.AggregateExpr]Aggregator)
		for _, aggExpr := range allAggExprs {
			if _, exists := aggMap[aggExpr]; !exists {
				aggArgs := make([]interface{}, len(aggExpr.Args))
				if len(groupRows) > 0 {
					for j, argExpr := range aggExpr.Args {
						argVal, err := pc.eval.EvalOperand(argExpr, groupRows[0], schema, ctx)
						if err != nil {
							argVal = nil
						}
						aggArgs[j] = argVal
					}
				}
				aggMap[aggExpr] = pc.eval.NewAggregator(aggExpr.Name, aggExpr.Distinct, aggArgs...)
			}
		}

		aggregators := make([]Aggregator, len(stmt.Columns))
		for i, col := range stmt.Columns {
			if aggExpr, ok := col.Expr.(*parser.AggregateExpr); ok {
				aggregators[i] = aggMap[aggExpr]
			}
		}

		for _, row := range groupRows {
			for aggExpr, agg := range aggMap {
				var key, val interface{}
				if len(aggExpr.Args) > 0 {
					if colRef, ok := aggExpr.Args[0].(*parser.ColumnRef); ok && colRef.Name == "*" {
						val = int64(1)
					} else {
						val, _ = pc.eval.EvalOperand(aggExpr.Args[0], row, schema, ctx)
					}
				} else {
					val = int64(1)
				}
				agg.Add(key, val)
			}
		}

		resultRow := make([]string, len(stmt.Columns))
		virtualRow := make(storage.Row, len(stmt.Columns))
		for i, col := range stmt.Columns {
			if aggregators[i] != nil {
				res := aggregators[i].Result()
				resultRow[i] = pc.eval.ValueToString(res)
				virtualRow[i] = res
			} else {
				val, err := pc.eval.EvalOperand(col.Expr, groupRows[0], schema, ctx)
				if err != nil {
					resultRow[i] = "ERR"
				} else {
					resultRow[i] = pc.eval.ValueToString(val)
					virtualRow[i] = val
				}
			}
		}

		if stmt.Having != nil {
			ok, err := pc.eval.EvalExpr(stmt.Having, virtualRow, schema, ctx)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}

		resultRows = append(resultRows, resultRow)
	}

	// Sort by ORDER BY columns
	if len(stmt.OrderBy) > 0 {
		pc.sortResultRowsByOrder(resultRows, stmt.OrderBy, schema, stmt.Columns, ctx)
	}

	// LIMIT/OFFSET
	start := 0
	if stmt.HasOffset {
		start = stmt.Offset
		if start > len(resultRows) {
			start = len(resultRows)
		}
	}
	end := len(resultRows)
	if stmt.HasLimit {
		end = start + stmt.Limit
		if end > len(resultRows) {
			end = len(resultRows)
		}
	}

	return &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows[start:end],
		AsOfNote: asOfNote,
	}, nil
}

// ShouldUseParallel determines if a query should use parallel execution.
func ShouldUseParallel(config ParallelConfig, numRows int, hasJoins bool, hasOrderBy bool) bool {
	if !config.Enabled {
		return false
	}
	if numRows < config.MinRows {
		return false
	}
	// Don't parallelize when ORDER BY or JOINs are present.
	if hasJoins || hasOrderBy {
		return false
	}
	return true
}

// splitRows divides rows into numWorkers roughly-equal chunks.
func (pc *ParallelCoordinator) splitRows(rows []storage.Row) [][]storage.Row {
	chunkSize := len(rows) / pc.numWorkers
	if chunkSize == 0 {
		chunkSize = 1
	}
	var chunks [][]storage.Row
	for i := 0; i < len(rows); i += chunkSize {
		end := i + chunkSize
		if end > len(rows) {
			end = len(rows)
		}
		chunks = append(chunks, rows[i:end])
	}
	return chunks
}

// sortResultRowsByOrder sorts result string rows by the given ORDER BY spec.
func (pc *ParallelCoordinator) sortResultRowsByOrder(
	rows [][]string,
	orderBy []parser.OrderItem,
	schema *storage.TableSchema,
	columns []parser.SelectColumn,
	ctx interface{},
) {
	// Build a name→index map for column references
	colIndex := make(map[string]int, len(columns))
	for i, col := range columns {
		name := ""
		if col.Alias != "" {
			name = col.Alias
		} else if ref, ok := col.Expr.(*parser.ColumnRef); ok {
			name = ref.Name
		}
		if name != "" {
			colIndex[strings.ToLower(name)] = i
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		for _, item := range orderBy {
			// Resolve column index for the order expression
			colIdx := -1
			if ref, ok := item.Expr.(*parser.ColumnRef); ok {
				if idx, ok := colIndex[strings.ToLower(ref.Name)]; ok {
					colIdx = idx
				}
			}

			var vi, vj interface{}
			if colIdx >= 0 && colIdx < len(rows[i]) && colIdx < len(rows[j]) {
				vi = rows[i][colIdx]
				vj = rows[j][colIdx]
			} else {
				continue
			}

			cmp := pc.eval.CompareValues(vi, vj)
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
