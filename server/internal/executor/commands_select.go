package executor

// SELECT и связанные команды: JOIN, GROUP BY, оконные функции,
// set-операции, EXPLAIN, HISTORY.

import (
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

	// Check result cache (only for simple SELECT, not CTEs or subqueries)
	if ctx.Session.resultCache != nil && c.stmt.TableName != "" && c.stmt.FromSubquery == nil && len(c.stmt.CTEs) == 0 {
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

	// Cache the result (only successful SELECT without mutations)
	if err == nil && ctx.Session.resultCache != nil && result != nil {
		cacheKey := ResultCacheKey(c.stmt, dbName)
		tables := map[string]bool{c.stmt.TableName: true}
		ctx.Session.resultCache.Put(cacheKey, result, tables)
	}

	return result, err
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
			if c.stmt.HasOffset {
				subSel.HasOffset = true
				subSel.Offset = c.stmt.Offset
			}
			if c.stmt.Distinct {
				subSel.Distinct = true
			}
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

	var rows []storage.Row
	var asOfNote string

	// Try index lookup (only for single table for now)
	usedIndex := false
	if len(c.stmt.Joins) == 0 && c.stmt.Where != nil && c.stmt.AsOf == nil {
		if positions, ok := tryIndexLookup(ctx, dbName, c.stmt.TableName, c.stmt.Where); ok {
			rows, err = ctx.Storage.ReadRowsByPositions(dbName, c.stmt.TableName, positions)
			if err == nil {
				usedIndex = true
			}
		}
	}

	if !usedIndex {
		rows, asOfNote, err = c.resolveRows(ctx, dbName)
		if err != nil {
			return nil, err
		}
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

	// Filter rows (WHERE)
	filtered := make([]storage.Row, 0, len(combinedRows))
	for _, row := range combinedRows {
		ok, err := evalExpr(c.stmt.Where, row, combinedSchema, ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			filtered = append(filtered, row)
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

	// Apply DISTINCT after projection
	if c.stmt.Distinct {
		resultRows = distinctRows(resultRows)
	}

	// Pagination (OFFSET and LIMIT) after DISTINCT
	start := 0
	if c.stmt.HasOffset {
		start = c.stmt.Offset
		if start > len(resultRows) {
			start = len(resultRows)
		}
	}

	end := len(resultRows)
	if c.stmt.HasLimit {
		end = start + c.stmt.Limit
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
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     finalRows,
		Schema:   resultSchema,
		AsOfNote: asOfNote,
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
	cmp, ok := where.(*parser.BinaryExpr)
	if !ok {
		return nil, false
	}
	if !indexOperators[cmp.Operator] {
		return nil, false
	}
	col, ok := cmp.Left.(*parser.ColumnRef)
	if !ok {
		return nil, false
	}

	// Range operators: > < >= <=
	switch cmp.Operator {
	case ">", "<", ">=", "<=":
		val := valueToString(evalOperandRaw(cmp.Right))
		if val == "" {
			return nil, false
		}
		var low, high string
		switch cmp.Operator {
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
	switch cmp.Operator {
	case "LIKE":
		// LIKE '%pattern%' can use GIN index
		val := valueToString(evalOperandRaw(cmp.Right))
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
		if cmp.Operator == "=" {
			var val parser.Value
			switch v := cmp.Right.(type) {
			case parser.Value:
				val = v
			case *parser.Value:
				val = *v
			default:
				return nil, false
			}
			queryVal = valueToString(parserValueToRaw(val))
		} else {
			queryVal = valueToString(evalOperandRaw(cmp.Right))
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

// distinctRows удаляет дубликаты строк из результата.
func distinctRows(rows [][]string) [][]string {
	seen := make(map[string]bool)
	result := make([][]string, 0, len(rows))
	for _, row := range rows {
		key := strings.Join(row, "\x00")
		if !seen[key] {
			seen[key] = true
			result = append(result, row)
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
