package executor

// SELECT и связанные команды: JOIN, GROUP BY, оконные функции,
// set-операции, EXPLAIN, HISTORY.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

type SelectCommand struct {
	stmt *parser.SelectStatement
}

func (c *SelectCommand) Execute(ctx *ExecutionContext) (*Result, error) {
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
	// В транзакции кэш пропускаем: он не учитывает tx-overlay (Bug #1).
	if ctx.Session.resultCache != nil && !ctx.Session.IsInTx() && c.stmt.TableName != "" && c.stmt.FromSubquery == nil && len(c.stmt.CTEs) == 0 {
		dbName, err := requireCurrentDB(ctx)
		if err == nil {
			cacheKey := ResultCacheKey(c.stmt, dbName)
			if cached := ctx.Session.resultCache.Get(cacheKey); cached != nil {
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

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if len(c.stmt.CTEs) > 0 {
		return c.executeWithCTE(ctx, dbName)
	}

	result, err := c.executeSimpleSelect(ctx, dbName)

	// Cache the result (only successful SELECT without mutations). Не кэшируем
	// результаты, посчитанные поверх tx-overlay (Bug #1).
	if err == nil && ctx.Session.resultCache != nil && !ctx.Session.IsInTx() && result != nil {
		cacheKey := ResultCacheKey(c.stmt, dbName)
		tables := map[string]bool{c.stmt.TableName: true}
		ctx.Session.resultCache.Put(cacheKey, result, tables)
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
func (c *SelectCommand) fastPathSelect(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
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
	rows = applyPushdownFilter(dbName, c.stmt, c.stmt.TableName, rows, schema, ctx)

	// Build column index for O(1) lookups during expression evaluation
	ensureColumnIndex(ctx, schema)

	// Filter with WHERE
	var filtered []storage.Row
	if c.stmt.Where != nil {
		filtered = make([]storage.Row, 0, len(rows))
		for _, row := range rows {
			ok, err := evalExpr(c.stmt.Where, row, schema, ctx)
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

	resultRows := make([][]string, 0, len(filtered))
	for _, row := range filtered {
		projected := make([]string, len(c.stmt.Columns))
		for i, col := range c.stmt.Columns {
			val, err := evalOperand(col.Expr, row, schema, ctx)
			if err != nil {
				projected[i] = "ERR"
			} else {
				projected[i] = valueToString(val)
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

	resultSchema := &storage.TableSchema{
		Name:    c.stmt.TableName,
		Columns: make([]storage.ColumnSchema, len(c.stmt.Columns)),
	}
	for i, col := range c.stmt.Columns {
		colType := inferTypeFromExpr(col.Expr, schema)
		resultSchema.Columns[i] = storage.ColumnSchema{
			Name: projectColumns[i],
			Type: colType,
		}
	}

	return &Result{
		Type:        "rows",
		Columns:     projectColumns,
		Rows:        resultRows[start:end],
		Schema:      resultSchema,
		RowsScanned: rowsScanned,
	}, nil
}

func (c *SelectCommand) executeWithCTE(ctx *ExecutionContext, dbName string) (*Result, error) {
	return ExecuteSelectWithCTE(c.stmt, ctx)
}

func (c *SelectCommand) executeSimpleSelect(ctx *ExecutionContext, dbName string) (*Result, error) {
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
			rlsEnabled, rlsPolicies, err := loadViewRLS(ctx, dbName, c.stmt.TableName)
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
	// Внутри транзакции индекс пропускаем — он обошёл бы tx-overlay (Bug #1).
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
	rows = applyPushdownFilter(dbName, c.stmt, c.stmt.TableName, rows, mainSchema, ctx)

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
	ensureColumnIndex(ctx, combinedSchema)

	// Filter rows (WHERE) — use parallel execution for large tables
	var filtered []storage.Row
	if ShouldUseParallel(ctx.Parallel, len(combinedRows), len(c.stmt.Joins) > 0, len(c.stmt.OrderBy) > 0) && c.stmt.Where != nil {
		pc := NewParallelCoordinator(ctx.Parallel.NumWorkers)
		filtered = pc.ParallelFilter(combinedRows, combinedSchema, c.stmt.Where, ctx)
	} else {
		filtered = make([]storage.Row, 0, len(combinedRows))
		for _, row := range combinedRows {
			ok, err := evalExpr(c.stmt.Where, row, combinedSchema, ctx)
			if err != nil {
				return nil, err
			}
			if ok {
				filtered = append(filtered, row)
			}
		}
	}

	// Apply server-side max rows limit (before GROUP BY to limit aggregation input)
	if ctx.Session != nil && ctx.Session.executor.maxRows > 0 && !c.hasAggregates() && len(c.stmt.GroupBy) == 0 {
		if len(filtered) > ctx.Session.executor.maxRows {
			filtered = filtered[:ctx.Session.executor.maxRows]
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
	if ShouldUseParallel(ctx.Parallel, len(filtered), false, false) {
		pc := NewParallelCoordinator(ctx.Parallel.NumWorkers)
		resultRows = pc.ParallelProject(filtered, effectiveColumns, combinedSchema, ctx)
	} else {
		for _, row := range filtered {
			projected := make([]string, len(effectiveColumns))
			for i, col := range effectiveColumns {
				val, err := evalOperand(col.Expr, row, combinedSchema, ctx)
				if err != nil {
					projected[i] = "ERR"
				} else {
					projected[i] = valueToString(val)
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
		colType := inferTypeFromExpr(col.Expr, combinedSchema)
		resultSchema.Columns[i] = storage.ColumnSchema{
			Name: projectColumns[i],
			Type: colType,
		}
	}

	return &Result{
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

func tryIndexLookup(ctx *ExecutionContext, dbName, tableName string, where parser.Expression) ([]int, bool) {
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
		val := valueToString(evalOperandRaw(right))
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
		val := valueToString(evalOperandRaw(right))
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
			queryVal = valueToString(parserValueToRaw(val))
		} else {
			queryVal = valueToString(evalOperandRaw(right))
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
// Returns the stored columns map if the index has stored data for the given positions.
func tryIndexOnlyScan(ctx *ExecutionContext, dbName, tableName string, where parser.Expression, positions []int) (map[int]map[string]interface{}, bool) {
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

	// Determine which columns we need
	neededCols := make(map[string]bool)
	for _, col := range selectCols {
		if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			neededCols[colRef.Name] = true
		}
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

func (c *SelectCommand) executeDual(ctx *ExecutionContext) (*Result, error) {
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
		val, err := evalOperand(col.Expr, nil, nil, ctx)
		if err != nil {
			row[i] = "ERR"
		} else {
			row[i] = valueToString(val)
		}
	}

	return &Result{
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

// distinctRows удаляет дубликаты строк из результата.
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
// It evaluates the DISTINCT ON expressions against raw rows to create a grouping key,
// then keeps only the first projected row for each unique key.
func distinctOnRows(projected [][]string, distinctOn []parser.Expression, columns []parser.SelectColumn, rawRows []storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) [][]string {
	if len(rawRows) != len(projected) {
		return projected
	}
	seen := make(map[string]bool)
	result := make([][]string, 0, len(projected))
	for i, rawRow := range rawRows {
		// Build key from DISTINCT ON expression values
		keyParts := make([]string, len(distinctOn))
		for j, expr := range distinctOn {
			val, err := evalOperand(expr, rawRow, schema, ctx)
			if err != nil {
				keyParts[j] = "ERR"
			} else {
				keyParts[j] = valueToString(val)
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

func loadViewQueryWithCtx(ctx *ExecutionContext, dbName, viewName string) (string, error) {
	def, err := loadObject(ctx, dbName, objTypeView, viewName)
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
func (c *SelectCommand) executePartitionedSelect(ctx *ExecutionContext, dbName string, schema *storage.TableSchema) (*Result, error) {
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

	// Apply tx overlay (read-your-wown-writes for transactions)
	allRows, err := applyTxOverlay(ctx, dbName, c.stmt.TableName, allRows)
	if err != nil {
		return nil, err
	}

	// Apply RLS
	allRows, err = filterRowsWithRLS(allRows, schema, ctx, dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// Apply optimizer predicate pushdown: filter rows early using per-table
	// predicates before the full WHERE evaluation.
	allRows = applyPushdownFilter(dbName, c.stmt, c.stmt.TableName, allRows, schema, ctx)

	// Build column index for O(1) lookups
	ensureColumnIndex(ctx, schema)

	// Filter with WHERE
	var filtered []storage.Row
	if c.stmt.Where != nil {
		filtered = make([]storage.Row, 0, len(allRows))
		for _, row := range allRows {
			ok, err := evalExpr(c.stmt.Where, row, schema, ctx)
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
			val, err := evalOperand(col.Expr, row, schema, ctx)
			if err != nil {
				projected[i] = "ERR"
			} else {
				projected[i] = valueToString(val)
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

	return &Result{Type: "rows", Columns: columns, Rows: resultRows, RowsScanned: rowsScanned}, nil
}
