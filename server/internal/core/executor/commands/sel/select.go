package sel

// SELECT and related commands: JOIN, GROUP BY, window functions,
// set operations, EXPLAIN, HISTORY.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"vaultdb/internal/core/executor/eval"
	"vaultdb/internal/core/executor/optimizer"
	"vaultdb/internal/core/executor/parallel"
	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

// resultCache is a local interface for the result cache so we don't import root.
type resultCache interface {
	Get(key string) *types.Result
	Put(key string, result *types.Result, tables map[string]bool)
	Invalidate(tableName string)
}

func init() {
	types.RegisterCommand("SELECT", func(stmt parser.Statement) types.Command {
		return &SelectCommand{stmt: stmt.(*parser.SelectStatement)}
	})
	types.RegisterCommand("EXPLAIN", func(stmt parser.Statement) types.Command {
		return &ExplainCommand{stmt: stmt.(*parser.ExplainStatement)}
	})
	types.RegisterCommand("HISTORY", func(stmt parser.Statement) types.Command {
		return &HistoryCommand{stmt: stmt.(*parser.HistoryStatement)}
	})
	types.RegisterCommand("SET_OPERATION", func(stmt parser.Statement) types.Command {
		return &SetOperationCommand{stmt: stmt.(*parser.SetOperationStatement)}
	})
}

// selectEvaluator adapts types hooks to the parallel.Evaluator interface.
type selectEvaluator struct{}

func (e *selectEvaluator) EvalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (bool, error) {
	return types.EvalExpr(expr, row, schema, ctx.(*types.ExecutionContext))
}

func (e *selectEvaluator) EvalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (interface{}, error) {
	return types.EvalOperand(expr, row, schema, ctx.(*types.ExecutionContext))
}

func (e *selectEvaluator) ValueToString(val interface{}) string {
	return types.ValueToString(val)
}

func (e *selectEvaluator) CollectAggregates(columns []parser.SelectColumn) []*parser.AggregateExpr {
	return collectAggregates(columns)
}

func (e *selectEvaluator) CompareValues(a, b interface{}) int {
	return eval.CompareOrdering(a, b)
}

func (e *selectEvaluator) NewAggregator(name string, distinct bool, args ...interface{}) parallel.Aggregator {
	return eval.NewAggregator(name, distinct, args...)
}

var sharedEvaluator parallel.Evaluator = &selectEvaluator{}

type SelectCommand struct {
	stmt *parser.SelectStatement
}

func (c *SelectCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if ctx.Ctx != nil {
		select {
		case <-ctx.Ctx.Done():
			return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
		default:
		}
	}

	// Fast path: simple SELECT — no joins, no aggregation, no subqueries, no CTEs
	if c.isSimpleSelect() && !ctx.Session.IsInTx() && ctx.SnapshotTxID == 0 {
		if ctx.Ctx != nil {
			select {
			case <-ctx.Ctx.Done():
				return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
			default:
			}
		}
		return c.fastPathSelect(ctx)
	}

	// Check result cache (only for simple SELECT, not CTEs or subqueries).
	// Skip cache in transactions: it doesn't account for tx-overlay (Bug #1).
	if rc, ok := ctx.Session.GetResultCache().(resultCache); ok && !ctx.Session.IsInTx() && c.stmt.TableName != "" && c.stmt.FromSubquery == nil && len(c.stmt.CTEs) == 0 {
		dbName, err := types.RequireCurrentDB(ctx)
		if err == nil {
			cacheKey := types.ResultCacheKey(c.stmt, dbName)
			if cached := rc.Get(cacheKey); cached != nil {
				return cached, nil
			}
		}
	}

	if c.stmt.TableName == "" && c.stmt.FromSubquery == nil {
		return c.executeDual(ctx)
	}

	if c.stmt.FromSubquery != nil {
		return c.executeDerivedTable(ctx)
	}

	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if len(c.stmt.CTEs) > 0 {
		return c.executeWithCTE(ctx, dbName)
	}

	result, err := c.executeSimpleSelect(ctx, dbName)

	// Cache the result (only successful SELECT without mutations). Don't cache
	// results computed over tx-overlay (Bug #1).
	if err == nil {
		if rc, ok := ctx.Session.GetResultCache().(resultCache); ok && !ctx.Session.IsInTx() && result != nil {
			cacheKey := types.ResultCacheKey(c.stmt, dbName)
			tables := map[string]bool{c.stmt.TableName: true}
			rc.Put(cacheKey, result, tables)
		}
	}

	return result, err
}

// isSimpleSelect returns true when the query can use the fast path:
// single table, no joins, no aggregation, no CTEs, no subqueries, no window functions.
func (c *SelectCommand) isSimpleSelect() bool {
	if c.stmt.TableName == "" || c.stmt.FromSubquery != nil || len(c.stmt.CTEs) > 0 {
		return false
	}
	if len(c.stmt.Joins) > 0 || len(c.stmt.GroupBy) > 0 || c.stmt.Having != nil {
		return false
	}
	if c.stmt.Distinct || c.stmt.AsOf != nil || len(c.stmt.Columns) == 0 {
		return false
	}
	if c.hasAggregates() || len(c.extractWindowFunctions()) > 0 {
		return false
	}
	return true
}

// fastPathSelect reads rows directly without index lookup, view resolution,
// parallel execution, or window function handling.
func (c *SelectCommand) fastPathSelect(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		// Check if it's a view
		viewQuery, viewErr := loadViewQueryWithCtx(ctx, dbName, c.stmt.TableName)
		if viewErr != nil || viewQuery == "" {
			return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
		}
		// Delegate to full executeSimpleSelect for view handling (including view-level RLS)
		return c.executeSimpleSelect(ctx, dbName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}
	if c.stmt.Alias != "" {
		schema.Name = c.stmt.Alias
	}

	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}
	rowsScanned := len(rows)

	// Apply RLS
	rows, err = filterRowsWithRLS(rows, schema, ctx, dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// Apply optimizer predicate pushdown: filter rows early using per-table
	// predicates before the full WHERE evaluation.
	rows, err = applyPushdownFilter(dbName, c.stmt, c.stmt.TableName, rows, schema, ctx)
	if err != nil {
		return nil, err
	}

	// Build column index for O(1) lookups during expression evaluation
	types.EnsureColumnIndex(ctx, schema)

	// Extract FTS query from WHERE for 2-arg bm25_score
	ctx.FtsQuery = ""
	if c.stmt.Where != nil {
		ctx.FtsQuery = extractFtsQueryFromWhere(c.stmt.Where)
	}

	// Filter with WHERE
	var filtered []storage.Row
	if c.stmt.Where != nil {
		filtered = make([]storage.Row, 0, len(rows))
		for _, row := range rows {
			ok, err := types.EvalExpr(c.stmt.Where, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if ok {
				filtered = append(filtered, row)
			}
		}
	} else {
		filtered = rows
	}

	// Sort rows (ORDER BY)
	if len(c.stmt.OrderBy) > 0 {
		c.applyOrderBy(filtered, schema, ctx)
	}

	// Project columns
	projectColumns := make([]string, len(c.stmt.Columns))
	for i, col := range c.stmt.Columns {
		if col.Alias != "" {
			projectColumns[i] = col.Alias
		} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			projectColumns[i] = colRef.Name
		} else {
			projectColumns[i] = fmt.Sprintf("col%d", i)
		}
	}

	var finalRows [][]string
	if !c.stmt.Distinct && len(c.stmt.DistinctOn) == 0 {
		start := 0
		limit, hasLimit, offset, hasOffset := c.resolveLimitOffset(ctx)
		if hasOffset {
			start = offset
			if start > len(filtered) {
				start = len(filtered)
			}
		}
		end := len(filtered)
		if hasLimit {
			end = start + limit
			if end > len(filtered) {
				end = len(filtered)
			}
		}
		pagedFiltered := filtered[start:end]
		finalRows = make([][]string, 0, len(pagedFiltered))
		for _, row := range pagedFiltered {
			projected := make([]string, len(c.stmt.Columns))
			for i, col := range c.stmt.Columns {
				val, err := types.EvalOperand(col.Expr, row, schema, ctx)
				if err != nil {
					projected[i] = "ERR"
				} else {
					projected[i] = types.ValueToString(val)
				}
			}
			finalRows = append(finalRows, projected)
		}
	} else {
		resultRows := make([][]string, 0, len(filtered))
		for _, row := range filtered {
			projected := make([]string, len(c.stmt.Columns))
			for i, col := range c.stmt.Columns {
				val, err := types.EvalOperand(col.Expr, row, schema, ctx)
				if err != nil {
					projected[i] = "ERR"
				} else {
					projected[i] = types.ValueToString(val)
				}
			}
			resultRows = append(resultRows, projected)
		}

		// Apply DISTINCT ON after projection (fast path)
		if len(c.stmt.DistinctOn) > 0 {
			resultRows = distinctOnRows(resultRows, c.stmt.DistinctOn, c.stmt.Columns, filtered, schema, ctx)
		} else if c.stmt.Distinct {
			resultRows = distinctRows(resultRows)
		}

		// Pagination
		start := 0
		limit, hasLimit, offset, hasOffset := c.resolveLimitOffset(ctx)
		if hasOffset {
			start = offset
			if start > len(resultRows) {
				start = len(resultRows)
			}
		}
		end := len(resultRows)
		if hasLimit {
			end = start + limit
			if end > len(resultRows) {
				end = len(resultRows)
			}
		}
		finalRows = resultRows[start:end]
	}

	resultSchema := &storage.TableSchema{
		Name:    c.stmt.TableName,
		Columns: make([]storage.ColumnSchema, len(c.stmt.Columns)),
	}
	for i, col := range c.stmt.Columns {
		colType := types.InferTypeFromExpr(col.Expr, schema)
		resultSchema.Columns[i] = storage.ColumnSchema{
			Name: projectColumns[i],
			Type: colType,
		}
	}

	return &types.Result{
		Type:        "rows",
		Columns:     projectColumns,
		Rows:        finalRows,
		Schema:      resultSchema,
		RowsScanned: rowsScanned,
	}, nil
}

func (c *SelectCommand) executeWithCTE(ctx *types.ExecutionContext, dbName string) (*types.Result, error) {
	return types.ExecuteSelectWithCTE(c.stmt, ctx)
}

func (c *SelectCommand) executeSimpleSelect(ctx *types.ExecutionContext, dbName string) (*types.Result, error) {
	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		viewQuery, err := loadViewQueryWithCtx(ctx, dbName, c.stmt.TableName)
		if err == nil && viewQuery != "" {
			subStmt, err := parser.Parse(viewQuery)
			if err != nil {
				return nil, fmt.Errorf("view '%s': parse error: %w", c.stmt.TableName, err)
			}
			subSel, ok := subStmt.(*parser.SelectStatement)
			if !ok {
				return nil, fmt.Errorf("view '%s': body is not a SELECT", c.stmt.TableName)
			}
			if c.stmt.Where != nil {
				if subSel.Where != nil {
					subSel.Where = &parser.AndExpr{Left: subSel.Where, Right: c.stmt.Where}
				} else {
					subSel.Where = c.stmt.Where
				}
			}
			if len(c.stmt.OrderBy) > 0 {
				subSel.OrderBy = c.stmt.OrderBy
			}
			if c.stmt.HasLimit {
				subSel.HasLimit = true
				subSel.Limit = c.stmt.Limit
			}
			if c.stmt.LimitExpr != nil {
				subSel.LimitExpr = c.stmt.LimitExpr
			}
			if c.stmt.HasOffset {
				subSel.HasOffset = true
				subSel.Offset = c.stmt.Offset
			}
			if c.stmt.OffsetExpr != nil {
				subSel.OffsetExpr = c.stmt.OffsetExpr
			}
			if c.stmt.Distinct {
				subSel.Distinct = true
			}

			// Check view-level RLS before replacing columns — RLS policies may
			// reference columns that the outer query doesn't select.
			rlsEnabled, rlsPolicies, err := types.LoadViewRLS(ctx, dbName, c.stmt.TableName)
			if err != nil {
				return nil, err
			}

			// Block view query if RLS is enabled but no policies exist
			if rlsEnabled && len(rlsPolicies) == 0 {
				return nil, fmt.Errorf("RLS is enabled on view '%s' but no policies are defined", c.stmt.TableName)
			}

			if rlsEnabled && len(rlsPolicies) > 0 {
				// When view has RLS, keep the view's original columns so RLS
				// policies can reference all view columns. Project afterwards.
				origCols := subSel.Columns
				cmd := &SelectCommand{stmt: subSel}
				result, err := cmd.Execute(ctx)
				if err != nil {
					return nil, err
				}

				// Build a synthetic schema for RLS filtering using the view's columns
				viewSchema := &storage.TableSchema{
					Name:       c.stmt.TableName,
					Database:   dbName,
					Columns:    make([]storage.ColumnSchema, len(origCols)),
					RLSEnabled: true,
					Policies:   rlsPolicies,
				}
				for i, col := range origCols {
					name := parser.FormatExpression(col.Expr)
					if col.Alias != "" {
						name = col.Alias
					}
					viewSchema.Columns[i] = storage.ColumnSchema{Name: name, Type: "TEXT"}
				}

				// Convert result rows to storage.Row for RLS filtering
				storageRows := make([]storage.Row, len(result.Rows))
				for i, r := range result.Rows {
					row := make(storage.Row, len(r))
					for j, v := range r {
						row[j] = v
					}
					storageRows[i] = row
				}
				filtered, err := filterRowsWithRLS(storageRows, viewSchema, ctx, dbName, c.stmt.TableName)
				if err != nil {
					return nil, err
				}

				// Rebuild string rows from filtered storage rows
				filteredRows := make([][]string, len(filtered))
				for i, r := range filtered {
					row := make([]string, len(r))
					for j, v := range r {
						row[j] = fmt.Sprintf("%v", v)
					}
					filteredRows[i] = row
				}
				result.Rows = filteredRows

				// Project only the outer query's requested columns
				if len(c.stmt.Columns) > 0 && len(c.stmt.Columns) < len(result.Columns) {
					projected := make([][]string, len(result.Rows))
					for i, r := range result.Rows {
						projRow := make([]string, len(c.stmt.Columns))
						for j, col := range c.stmt.Columns {
							colRef, ok := col.Expr.(*parser.ColumnRef)
							if !ok {
								projRow[j] = fmt.Sprintf("%v", col.Expr)
								continue
							}
							// Find the column index in the view result
							for k, rc := range result.Columns {
								if strings.EqualFold(rc, colRef.Name) {
									if k < len(r) {
										projRow[j] = r[k]
									}
									break
								}
							}
						}
						projected[i] = projRow
					}
					result.Rows = projected
					// Update column names
					projCols := make([]string, len(c.stmt.Columns))
					for i, col := range c.stmt.Columns {
						if col.Alias != "" {
							projCols[i] = col.Alias
						} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
							projCols[i] = colRef.Name
						} else {
							projCols[i] = fmt.Sprintf("col%d", i)
						}
					}
					result.Columns = projCols
				}

				return result, nil
			}

			// No view-level RLS: original behavior
			if len(c.stmt.Columns) > 0 {
				subSel.Columns = c.stmt.Columns
			}
			cmd := &SelectCommand{stmt: subSel}
			return cmd.Execute(ctx)
		}
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	mainSchema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}
	if c.stmt.Alias != "" {
		mainSchema.Name = c.stmt.Alias
	}

	// If table is partitioned, read from all partitions and merge
	if mainSchema.PartitionBy != nil {
		return c.executePartitionedSelect(ctx, dbName, mainSchema)
	}

	var rows []storage.Row
	var asOfNote string
	var rowsScanned int

	// Try index lookup (only for single table for now)
	// Within a transaction we skip the index — it would bypass tx-overlay (Bug #1).
	usedIndex := false
	indexOnlyResult := false
	if len(c.stmt.Joins) == 0 && c.stmt.Where != nil && c.stmt.AsOf == nil && !ctx.Session.IsInTx() {
		if positions, ok := tryIndexLookup(ctx, dbName, c.stmt.TableName, c.stmt.Where); ok {
			// Check if we can use index-only scan
			if storedCols, ok := tryIndexOnlyScan(ctx, dbName, c.stmt.TableName, c.stmt.Where, positions); ok {
				rows, err = buildRowsFromStoredColumns(storedCols, c.stmt.Columns, mainSchema)
				if err == nil {
					usedIndex = true
					indexOnlyResult = true
				}
			}
			if !indexOnlyResult {
				rows, err = ctx.Storage.ReadRowsByPositions(dbName, c.stmt.TableName, positions)
				if err == nil {
					usedIndex = true
				}
			}
		}
	}

	if !usedIndex {
		rows, asOfNote, err = c.resolveRows(ctx, dbName)
		if err != nil {
			return nil, err
		}
	}
	rowsScanned = len(rows)

	// Apply RLS before JOINs and WHERE
	if c.stmt.TableName != "" {
		rows, err = filterRowsWithRLS(rows, mainSchema, ctx, dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
	}

	// Apply optimizer predicate pushdown: filter main table rows early
	// using per-table predicates before JOINs reduce row count further.
	rows, err = applyPushdownFilter(dbName, c.stmt, c.stmt.TableName, rows, mainSchema, ctx)
	if err != nil {
		return nil, err
	}

	// Combined schema and rows for JOIN
	combinedSchema := mainSchema
	combinedRows := rows

	if len(c.stmt.Joins) > 0 {
		combinedSchema, combinedRows, err = c.executeJoins(ctx, dbName, combinedSchema, combinedRows)
		if err != nil {
			return nil, err
		}
	}

	// Build column index for O(1) lookups during expression evaluation.
	types.EnsureColumnIndex(ctx, combinedSchema)

	// Extract FTS query from WHERE for 2-arg bm25_score
	ctx.FtsQuery = ""
	if c.stmt.Where != nil {
		ctx.FtsQuery = extractFtsQueryFromWhere(c.stmt.Where)
	}

	// Filter rows (WHERE) — use parallel execution for large tables
	var filtered []storage.Row
	if parallel.ShouldUseParallel(ctx.Parallel, len(combinedRows), len(c.stmt.Joins) > 0, len(c.stmt.OrderBy) > 0) && c.stmt.Where != nil {
		pc := parallel.NewParallelCoordinator(ctx.Parallel.NumWorkers, sharedEvaluator)
		filtered = pc.ParallelFilter(combinedRows, combinedSchema, c.stmt.Where, ctx)
	} else {
		filtered = make([]storage.Row, 0, len(combinedRows))
		for _, row := range combinedRows {
			ok, err := types.EvalExpr(c.stmt.Where, row, combinedSchema, ctx)
			if err != nil {
				return nil, err
			}
			if ok {
				filtered = append(filtered, row)
			}
		}
	}

	// Apply server-side max rows limit (before GROUP BY to limit aggregation input)
	if ctx.Session != nil && ctx.Session.GetMaxRows() > 0 && !c.hasAggregates() && len(c.stmt.GroupBy) == 0 {
		if maxRows := ctx.Session.GetMaxRows(); len(filtered) > maxRows {
			filtered = filtered[:maxRows]
		}
	}

	// Handle GROUP BY or global aggregates
	if len(c.stmt.GroupBy) > 0 || c.hasAggregates() {
		res, err := c.executeWithGrouping(filtered, combinedSchema, asOfNote, ctx)
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	// Apply Window Functions
	windowFuncs := c.extractWindowFunctions()
	if len(windowFuncs) > 0 {
		if ctx.WindowCols == nil {
			ctx.WindowCols = make(map[*parser.WindowFunctionExpr]string)
		}
		filtered, combinedSchema, err = c.applyWindowFunctions(filtered, combinedSchema, windowFuncs, ctx)
		if err != nil {
			return nil, err
		}
	}

	// Sort rows (ORDER BY)
	if len(c.stmt.OrderBy) > 0 {
		c.applyOrderBy(filtered, combinedSchema, ctx)
	}

	// Project columns
	effectiveColumns := c.stmt.Columns
	if len(effectiveColumns) == 0 {
		// Expand '*'
		effectiveColumns = make([]parser.SelectColumn, len(combinedSchema.Columns))
		for i, col := range combinedSchema.Columns {
			effectiveColumns[i] = parser.SelectColumn{
				Expr: &parser.ColumnRef{Name: col.Name},
			}
		}
	}

	projectColumns := make([]string, len(effectiveColumns))
	for i, col := range effectiveColumns {
		if col.Alias != "" {
			projectColumns[i] = col.Alias
		} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			projectColumns[i] = colRef.Name
		} else {
			projectColumns[i] = fmt.Sprintf("col%d", i)
		}
	}

	resultRows := make([][]string, 0, len(filtered))
	if parallel.ShouldUseParallel(ctx.Parallel, len(filtered), false, false) {
		pc := parallel.NewParallelCoordinator(ctx.Parallel.NumWorkers, sharedEvaluator)
		resultRows = pc.ParallelProject(filtered, effectiveColumns, combinedSchema, ctx)
	} else {
		for _, row := range filtered {
			projected := make([]string, len(effectiveColumns))
			for i, col := range effectiveColumns {
				val, err := types.EvalOperand(col.Expr, row, combinedSchema, ctx)
				if err != nil {
					projected[i] = "ERR"
				} else {
					projected[i] = types.ValueToString(val)
				}
			}
			resultRows = append(resultRows, projected)
		}
	}

	// Apply DISTINCT after projection
	if len(c.stmt.DistinctOn) > 0 {
		resultRows = distinctOnRows(resultRows, c.stmt.DistinctOn, effectiveColumns, filtered, combinedSchema, ctx)
	} else if c.stmt.Distinct {
		resultRows = distinctRows(resultRows)
	}

	// Pagination (OFFSET and LIMIT) after DISTINCT
	start := 0
	limit, hasLimit, offset, hasOffset := c.resolveLimitOffset(ctx)
	if hasOffset {
		start = offset
		if start > len(resultRows) {
			start = len(resultRows)
		}
	}

	end := len(resultRows)
	if hasLimit {
		end = start + limit
		if end > len(resultRows) {
			end = len(resultRows)
		}
	}

	finalRows := resultRows[start:end]

	resultSchema := &storage.TableSchema{
		Name:    c.stmt.TableName,
		Columns: make([]storage.ColumnSchema, len(effectiveColumns)),
	}
	for i, col := range effectiveColumns {
		colType := types.InferTypeFromExpr(col.Expr, combinedSchema)
		resultSchema.Columns[i] = storage.ColumnSchema{
			Name: projectColumns[i],
			Type: colType,
		}
	}

	return &types.Result{
		Type:        "rows",
		Columns:     projectColumns,
		Rows:        finalRows,
		Schema:      resultSchema,
		AsOfNote:    asOfNote,
		RowsScanned: rowsScanned,
	}, nil
}

var indexOperators = map[string]bool{
	"=":  true,
	">":  true,
	"<":  true,
	">=": true,
	"<=": true,
	"@@": true,
	"@>": true,
	"<@": true,
	"?":  true,
}

func tryIndexLookup(ctx *types.ExecutionContext, dbName, tableName string, where parser.Expression) ([]int, bool) {
	// Handle both BinaryExpr and JSONAccess for index operators
	var op string
	var left parser.Expression
	var right parser.Expression

	switch e := where.(type) {
	case *parser.BinaryExpr:
		if !indexOperators[e.Operator] {
			return nil, false
		}
		op = e.Operator
		left = e.Left
		right = e.Right
	case *parser.JSONAccess:
		if !indexOperators[e.Operator] {
			return nil, false
		}
		op = e.Operator
		left = e.Expr
		right = e.Argument
	default:
		return nil, false
	}

	col, ok := left.(*parser.ColumnRef)
	if !ok {
		return nil, false
	}

	// Range operators: > < >= <=
	switch op {
	case ">", "<", ">=", "<=":
		val := types.ValueToString(types.EvalOperandRaw(right))
		if val == "" {
			return nil, false
		}
		var low, high string
		switch op {
		case ">":
			low = val
		case ">=":
			low = val
		case "<":
			high = val
		case "<=":
			high = val
		}
		positions, ok := ctx.Storage.IndexRangeLookup(dbName, tableName, col.Name, low, high)
		if !ok || len(positions) == 0 {
			return nil, false
		}
		return positions, true
	}

	// Equality and other operators
	switch op {
	case "LIKE":
		// LIKE '%pattern%' can use GIN index
		val := types.ValueToString(types.EvalOperandRaw(right))
		if val == "" || !strings.HasPrefix(val, "%") || !strings.HasSuffix(val, "%") {
			return nil, false
		}
		pattern := val[1 : len(val)-1] // strip %%
		if pattern == "" {
			return nil, false
		}
		positions, ok := ctx.Storage.IndexFTSLookup(dbName, tableName, col.Name, pattern)
		if !ok || len(positions) == 0 {
			return nil, false
		}
		return positions, true
	default:
		var queryVal string
		if op == "=" {
			var val parser.Value
			switch v := right.(type) {
			case parser.Value:
				val = v
			case *parser.Value:
				val = *v
			default:
				return nil, false
			}
			queryVal = types.ValueToString(types.ParserValueToRaw(val))
		} else {
			queryVal = types.ValueToString(types.EvalOperandRaw(right))
		}

		if queryVal == "" {
			return nil, false
		}

		positions, ok := ctx.Storage.IndexLookup(dbName, tableName, col.Name, queryVal)
		if !ok || len(positions) == 0 {
			return nil, false
		}
		return positions, true
	}
}

// tryIndexOnlyScan attempts to retrieve stored columns from an index for index-only scan.
func tryIndexOnlyScan(ctx *types.ExecutionContext, dbName, tableName string, where parser.Expression, positions []int) (map[int]map[string]interface{}, bool) {
	// Handle both BinaryExpr and JSONAccess
	var op string
	var left parser.Expression

	switch e := where.(type) {
	case *parser.BinaryExpr:
		op = e.Operator
		left = e.Left
	case *parser.JSONAccess:
		op = e.Operator
		left = e.Expr
	default:
		return nil, false
	}

	if !indexOperators[op] {
		return nil, false
	}
	col, ok := left.(*parser.ColumnRef)
	if !ok {
		return nil, false
	}

	// Try to find a btree index with stored columns for this column
	indexName, ok := ctx.Storage.FindIndexForColumn(dbName, tableName, col.Name)
	if !ok {
		return nil, false
	}

	// Get the index and check if it has stored columns
	idx, ok := ctx.Storage.GetIndex(dbName, tableName, indexName)
	if !ok {
		return nil, false
	}

	if !idx.HasStoredColumns() {
		return nil, false
	}

	// Collect stored columns for all positions
	resultCols := make(map[int]map[string]interface{}, len(positions))
	for _, pos := range positions {
		if cols, ok := idx.GetStoredColumns(pos); ok {
			resultCols[pos] = cols
		}
	}

	if len(resultCols) == 0 {
		return nil, false
	}

	return resultCols, true
}

// buildRowsFromStoredColumns constructs storage.Row slices from index stored columns.
func buildRowsFromStoredColumns(storedCols map[int]map[string]interface{}, selectCols []parser.SelectColumn, schema *storage.TableSchema) ([]storage.Row, error) {
	// Build column name to index mapping
	colIndex := make(map[string]int)
	for i, col := range schema.Columns {
		colIndex[col.Name] = i
	}

	// Build rows
	rows := make([]storage.Row, 0, len(storedCols))
	for _, cols := range storedCols {
		row := make(storage.Row, len(schema.Columns))
		for colName, val := range cols {
			if idx, ok := colIndex[colName]; ok {
				row[idx] = val
			}
		}
		rows = append(rows, row)
	}

	return rows, nil
}

func (c *SelectCommand) executeDual(ctx *types.ExecutionContext) (*types.Result, error) {
	effectiveColumns := c.stmt.Columns
	if len(effectiveColumns) == 0 {
		return nil, fmt.Errorf("SELECT without FROM must have at least one column")
	}

	projectColumns := make([]string, len(effectiveColumns))
	for i, col := range effectiveColumns {
		if col.Alias != "" {
			projectColumns[i] = col.Alias
		} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			projectColumns[i] = colRef.Name
		} else {
			projectColumns[i] = fmt.Sprintf("col%d", i)
		}
	}

	row := make([]string, len(effectiveColumns))
	for i, col := range effectiveColumns {
		val, err := types.EvalOperand(col.Expr, nil, nil, ctx)
		if err != nil {
			row[i] = "ERR"
		} else {
			row[i] = types.ValueToString(val)
		}
	}

	return &types.Result{
		Type:    "rows",
		Columns: projectColumns,
		Rows:    [][]string{row},
	}, nil
}

// hashRow computes a SHA-256 hash of a row to use as a dedup key.
func hashRow(row []string) [32]byte {
	h := sha256.New()
	for _, s := range row {
		var lenBuf [4]byte
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(s)))
		h.Write(lenBuf[:])
		h.Write([]byte(s))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// distinctRows removes duplicate rows from the result.
func distinctRows(rows [][]string) [][]string {
	seen := make(map[[32]byte]bool)
	result := make([][]string, 0, len(rows))
	for _, row := range rows {
		key := hashRow(row)
		if !seen[key] {
			seen[key] = true
			result = append(result, row)
		}
	}
	return result
}

// distinctOnRows deduplicates projected rows by DISTINCT ON expressions.
func distinctOnRows(projected [][]string, distinctOn []parser.Expression, columns []parser.SelectColumn, rawRows []storage.Row, schema *storage.TableSchema, ctx *types.ExecutionContext) [][]string {
	if len(rawRows) != len(projected) {
		return projected
	}
	seen := make(map[string]bool)
	result := make([][]string, 0, len(projected))
	for i, rawRow := range rawRows {
		// Build key from DISTINCT ON expression values
		keyParts := make([]string, len(distinctOn))
		for j, expr := range distinctOn {
			val, err := types.EvalOperand(expr, rawRow, schema, ctx)
			if err != nil {
				keyParts[j] = "ERR"
			} else {
				keyParts[j] = types.ValueToString(val)
			}
		}
		key := strings.Join(keyParts, "\x00")
		if !seen[key] {
			seen[key] = true
			result = append(result, projected[i])
		}
	}
	return result
}

func loadViewQueryWithCtx(ctx *types.ExecutionContext, dbName, viewName string) (string, error) {
	def, err := types.LoadObject(ctx, dbName, types.ObjTypeView, viewName)
	if err != nil {
		return "", err
	}
	if def == nil {
		return "", fmt.Errorf("view '%s' not found", viewName)
	}
	if query, ok := def["query"].(string); ok {
		return query, nil
	}
	return "", fmt.Errorf("view query not found")
}

// executePartitionedSelect reads rows from all partitions of a partitioned table
// and merges them before applying WHERE, ORDER BY, etc.
func (c *SelectCommand) executePartitionedSelect(ctx *types.ExecutionContext, dbName string, schema *storage.TableSchema) (*types.Result, error) {
	pt := storage.NewPartitionedTable(schema)
	if pt == nil {
		return nil, fmt.Errorf("table '%s' has partition spec but could not initialize", c.stmt.TableName)
	}

	// Prune partitions based on WHERE clause predicates
	partitions := pt.PrunePartitions(c.stmt.Where)

	// Read from pruned partitions and merge
	var allRows []storage.Row
	for _, part := range partitions {
		if !ctx.Storage.TableExists(dbName, part.TableName) {
			continue
		}
		rows, err := ctx.Storage.ReadCurrentRows(dbName, part.TableName)
		if err != nil {
			return nil, fmt.Errorf("read partition '%s': %w", part.Name, err)
		}
		allRows = append(allRows, rows...)
	}
	rowsScanned := len(allRows)

	// Apply tx overlay (read-your-own-writes for transactions)
	allRows, err := types.ApplyTxOverlay(ctx, dbName, c.stmt.TableName, allRows)
	if err != nil {
		return nil, err
	}

	// Apply RLS
	allRows, err = filterRowsWithRLS(allRows, schema, ctx, dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// Apply optimizer predicate pushdown
	allRows, err = applyPushdownFilter(dbName, c.stmt, c.stmt.TableName, allRows, schema, ctx)
	if err != nil {
		return nil, err
	}

	// Build column index for O(1) lookups
	types.EnsureColumnIndex(ctx, schema)

	// Extract FTS query from WHERE for 2-arg bm25_score
	ctx.FtsQuery = ""
	if c.stmt.Where != nil {
		ctx.FtsQuery = extractFtsQueryFromWhere(c.stmt.Where)
	}

	// Filter with WHERE
	var filtered []storage.Row
	if c.stmt.Where != nil {
		filtered = make([]storage.Row, 0, len(allRows))
		for _, row := range allRows {
			ok, err := types.EvalExpr(c.stmt.Where, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if ok {
				filtered = append(filtered, row)
			}
		}
	} else {
		filtered = allRows
	}

	// Apply ORDER BY
	if len(c.stmt.OrderBy) > 0 {
		c.applyOrderBy(filtered, schema, ctx)
	}

	// Project columns
	resultRows := make([][]string, 0, len(filtered))
	for _, row := range filtered {
		projected := make([]string, len(c.stmt.Columns))
		for i, col := range c.stmt.Columns {
			val, err := types.EvalOperand(col.Expr, row, schema, ctx)
			if err != nil {
				projected[i] = "ERR"
			} else {
				projected[i] = types.ValueToString(val)
			}
		}
		resultRows = append(resultRows, projected)
	}

	// Apply OFFSET and LIMIT
	start := 0
	if c.stmt.HasOffset {
		start = c.stmt.Offset
	}
	if start > len(resultRows) {
		resultRows = nil
	} else {
		resultRows = resultRows[start:]
	}
	if c.stmt.HasLimit && c.stmt.Limit < len(resultRows) {
		resultRows = resultRows[:c.stmt.Limit]
	}

	columns := make([]string, len(c.stmt.Columns))
	for i, col := range c.stmt.Columns {
		if col.Alias != "" {
			columns[i] = col.Alias
		} else if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			columns[i] = colRef.Name
		} else {
			columns[i] = fmt.Sprintf("col%d", i)
		}
	}

	return &types.Result{Type: "rows", Columns: columns, Rows: resultRows, RowsScanned: rowsScanned}, nil
}

// ─── Local helpers that use types hooks ──────────────────────────────────────

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

// applyPushdownFilter runs the optimizer on a cloned statement, extracts
// TablePredicates, and filters the given rows.
func applyPushdownFilter(dbName string, stmt *parser.SelectStatement, tableName string, rows []storage.Row, schema *storage.TableSchema, ctx *types.ExecutionContext) ([]storage.Row, error) {
	if stmt.Where == nil || len(rows) == 0 {
		return rows, nil
	}

	store := ctx.Storage
	opt := optimizer.NewOptimizer(store)

	clone := cloneSelectStatement(stmt)
	plan, err := opt.OptimizePlan(dbName, clone)
	if err != nil || plan == nil {
		return rows, nil
	}

	pred, ok := plan.TablePredicates[tableName]
	if !ok || pred == nil {
		return rows, nil
	}

	types.EnsureColumnIndex(ctx, schema)

	filtered := make([]storage.Row, 0, len(rows))
	for _, row := range rows {
		ok, err := types.EvalExpr(pred, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func cloneSelectStatement(stmt *parser.SelectStatement) *parser.SelectStatement {
	clone := *stmt
	if len(stmt.Joins) > 0 {
		clone.Joins = make([]parser.JoinClause, len(stmt.Joins))
		copy(clone.Joins, stmt.Joins)
	}
	if len(stmt.Columns) > 0 {
		clone.Columns = make([]parser.SelectColumn, len(stmt.Columns))
		copy(clone.Columns, stmt.Columns)
	}
	if len(stmt.GroupBy) > 0 {
		clone.GroupBy = make([]parser.Expression, len(stmt.GroupBy))
		copy(clone.GroupBy, stmt.GroupBy)
	}
	if len(stmt.OrderBy) > 0 {
		clone.OrderBy = make([]parser.OrderItem, len(stmt.OrderBy))
		copy(clone.OrderBy, stmt.OrderBy)
	}
	if len(stmt.DistinctOn) > 0 {
		clone.DistinctOn = make([]parser.Expression, len(stmt.DistinctOn))
		copy(clone.DistinctOn, stmt.DistinctOn)
	}
	return &clone
}

// extractFtsQueryFromWhere walks a WHERE clause AST and returns the search
// query from the first FTS_MATCH or @@ predicate it finds.
func extractFtsQueryFromWhere(where parser.Expression) string {
	if where == nil {
		return ""
	}
	switch e := where.(type) {
	case *parser.BinaryExpr:
		if e.Operator == "FTS_MATCH" || e.Operator == "@" || e.Operator == "MATCH" {
			if val, ok := e.Right.(*parser.Value); ok {
				return val.StrVal
			}
		}
		if q := extractFtsQueryFromWhere(e.Left); q != "" {
			return q
		}
		if q := extractFtsQueryFromWhere(e.Right); q != "" {
			return q
		}
	case *parser.AndExpr:
		if q := extractFtsQueryFromWhere(e.Left); q != "" {
			return q
		}
		return extractFtsQueryFromWhere(e.Right)
	case *parser.OrExpr:
		if q := extractFtsQueryFromWhere(e.Left); q != "" {
			return q
		}
		return extractFtsQueryFromWhere(e.Right)
	case *parser.NotExpr:
		return extractFtsQueryFromWhere(e.Expr)
	}
	return ""
}
