package executor

// SELECT и связанные команды: JOIN, GROUP BY, оконные функции,
// set-операции, EXPLAIN, HISTORY.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

type SelectCommand struct {
	stmt *parser.SelectStatement
}

func (c *SelectCommand) hasAggregates() bool {
	for _, col := range c.stmt.Columns {
		if c.containsAggregate(col.Expr) {
			return true
		}
	}
	if c.containsAggregate(c.stmt.Having) {
		return true
	}
	return false
}

func (c *SelectCommand) containsAggregate(expr parser.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.AggregateExpr:
		return true
	case *parser.BinaryExpr:
		return c.containsAggregate(e.Left) || c.containsAggregate(e.Right)
	case *parser.AndExpr:
		return c.containsAggregate(e.Left) || c.containsAggregate(e.Right)
	case *parser.OrExpr:
		return c.containsAggregate(e.Left) || c.containsAggregate(e.Right)
	case *parser.NotExpr:
		return c.containsAggregate(e.Expr)
	case *parser.FunctionCall:
		for _, arg := range e.Args {
			if c.containsAggregate(arg) {
				return true
			}
		}
	}
	return false
}

func (c *SelectCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if c.stmt.TableName == "" {
		return c.executeDual(ctx)
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
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
		if cmp, ok := c.stmt.Where.(*parser.BinaryExpr); ok && cmp.Operator == "=" {
			if col, ok := cmp.Left.(*parser.ColumnRef); ok {
				var val parser.Value
				foundVal := false
				switch v := cmp.Right.(type) {
				case parser.Value:
					val = v
					foundVal = true
				case *parser.Value:
					val = *v
					foundVal = true
				}

				if foundVal {
					if positions, ok := ctx.Storage.IndexLookup(dbName, c.stmt.TableName, col.Name, valueToString(parserValueToRaw(val))); ok {
						rows, err = ctx.Storage.ReadRowsByPositions(dbName, c.stmt.TableName, positions)
						if err == nil {
							usedIndex = true
						}
					}
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

	// Handle GROUP BY or global aggregates
	if len(c.stmt.GroupBy) > 0 || c.hasAggregates() {
		res, err := c.executeWithGrouping(filtered, combinedSchema, asOfNote, ctx)
		if err != nil {
			return nil, err
		}
		// Convert result back to storage.Row for further pipeline steps if needed
		// Window functions, ORDER BY, LIMIT etc.
		// For simplicity, let's keep it as is for now and focus on non-grouped window functions first.
		return res, nil
	}

	// Apply Window Functions
	windowFuncs := c.extractWindowFunctions()
	if len(windowFuncs) > 0 {
		filtered, combinedSchema, err = c.applyWindowFunctions(filtered, combinedSchema, windowFuncs, ctx)
		if err != nil {
			return nil, err
		}
	}

	// Sort rows (ORDER BY)
	if len(c.stmt.OrderBy) > 0 {
		c.applyOrderBy(filtered, combinedSchema, ctx)
	}

	// Pagination (OFFSET and LIMIT)
	start := 0
	if c.stmt.HasOffset {
		start = c.stmt.Offset
		if start > len(filtered) {
			start = len(filtered)
		}
	}

	end := len(filtered)
	if c.stmt.HasLimit {
		end = start + c.stmt.Limit
		if end > len(filtered) {
			end = len(filtered)
		}
	}

	paged := filtered[start:end]

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
			// Strip table prefix if present for clean output if desired,
			// but for now let's keep it if it's there.
			projectColumns[i] = colRef.Name
		} else {
			projectColumns[i] = fmt.Sprintf("col%d", i)
		}
	}

	resultRows := make([][]string, 0, len(paged))
	for _, row := range paged {
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

	return &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
}

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

		// Nested Loop Join
		for _, lrow := range currentRows {
			for _, rrow := range rightRows {
				// Combined row
				combinedRow := append(append(storage.Row{}, lrow...), rrow...)

				// Evaluate join condition
				if join.Type == "CROSS" {
					newRows = append(newRows, combinedRow)
				} else {
					// We need to handle column resolution for multi-table schema
					// evalExpr needs to know which columns come from which table.
					// This is where resolveColumn needs to be smarter.

					// For now, let's use a temporary schema for evaluation
					// that has all columns.
					ok, err := evalExpr(join.Condition, combinedRow, combinedSchema, ctx)
					if err == nil && ok {
						newRows = append(newRows, combinedRow)
					}
				}
			}
		}

		currentSchema = combinedSchema
		currentRows = newRows
	}

	return currentSchema, currentRows, nil
}

func (c *SelectCommand) executeWithGrouping(rows []storage.Row, schema *storage.TableSchema, asOfNote string, ctx *ExecutionContext) (*Result, error) {
	groups := make(map[string][]storage.Row)
	groupOrder := make([]string, 0)

	for _, row := range rows {
		keyParts := make([]string, len(c.stmt.GroupBy))
		for i, expr := range c.stmt.GroupBy {
			val, _ := evalOperand(expr, row, schema, ctx)
			keyParts[i] = valueToString(val)
		}
		key := strings.Join(keyParts, "\x00")
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], row)
	}

	// If no GROUP BY but has aggregates, treat everything as one group
	if len(c.stmt.GroupBy) == 0 && len(groupOrder) == 0 && c.hasAggregates() {
		groupOrder = append(groupOrder, "")
		groups[""] = rows
	}

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

	resultRows := make([][]string, 0)
	for _, key := range groupOrder {
		groupRows := groups[key]

		// Create aggregators for this group
		aggregators := make([]Aggregator, len(c.stmt.Columns))
		for i, col := range c.stmt.Columns {
			if aggExpr, ok := col.Expr.(*parser.AggregateExpr); ok {
				aggregators[i] = NewAggregator(aggExpr.Name, aggExpr.Distinct)
			}
		}

		// Process all rows in group
		for _, row := range groupRows {
			for i, col := range c.stmt.Columns {
				if aggregators[i] != nil {
					aggExpr := col.Expr.(*parser.AggregateExpr)
					var val interface{}
					if len(aggExpr.Args) > 0 {
						if colRef, ok := aggExpr.Args[0].(*parser.ColumnRef); ok && colRef.Name == "*" {
							val = int64(1)
						} else {
							val, _ = evalOperand(aggExpr.Args[0], row, schema, ctx)
						}
					} else {
						val = int64(1)
					}
					aggregators[i].Add(val)
				}
			}
		}

		// Calculate result for this group
		resultRow := make([]string, len(c.stmt.Columns))
		// We need a virtual row for HAVING evaluation if it uses aggregates
		virtualRow := make(storage.Row, len(c.stmt.Columns))

		for i, col := range c.stmt.Columns {
			if aggregators[i] != nil {
				res := aggregators[i].Result()
				resultRow[i] = valueToString(res)
				virtualRow[i] = res
			} else {
				// Pick from first row of group for non-aggregates
				if len(groupRows) > 0 {
					val, _ := evalOperand(col.Expr, groupRows[0], schema, ctx)
					resultRow[i] = valueToString(val)
					virtualRow[i] = val
				} else {
					resultRow[i] = "NULL"
					virtualRow[i] = nil
				}
			}
		}

		// Handle HAVING
		if c.stmt.Having != nil {
			// Build a temporary schema for the projected results
			projectedSchema := &storage.TableSchema{
				Columns: make([]storage.ColumnSchema, len(c.stmt.Columns)),
			}
			for i, name := range projectColumns {
				projectedSchema.Columns[i] = storage.ColumnSchema{Name: name}
			}

			// Evaluate HAVING on the projected (aggregated) result row
			ok, err := evalExpr(c.stmt.Having, virtualRow, projectedSchema, ctx)
			if err != nil {
				// Fallback to original row if HAVING uses non-aggregates
				ok, err = evalExpr(c.stmt.Having, groupRows[0], schema, ctx)
				if err != nil {
					continue
				}
			}
			if !ok {
				continue
			}
		}

		resultRows = append(resultRows, resultRow)
	}

	resultRows = c.orderAndPageGrouped(resultRows, projectColumns)

	return &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
}

// orderAndPageGrouped applies ORDER BY / OFFSET / LIMIT to grouped output.
// Sort keys are resolved against the projected columns: by alias or column
// name, or by 1-based position (ORDER BY 2).
func (c *SelectCommand) orderAndPageGrouped(rows [][]string, projectColumns []string) [][]string {
	if len(c.stmt.OrderBy) > 0 {
		colIndexByName := make(map[string]int, len(projectColumns))
		for i, name := range projectColumns {
			colIndexByName[strings.ToLower(name)] = i
		}

		type sortKey struct {
			idx  int
			desc bool
		}
		keys := make([]sortKey, 0, len(c.stmt.OrderBy))
		for _, item := range c.stmt.OrderBy {
			idx := -1
			switch expr := item.Expr.(type) {
			case *parser.ColumnRef:
				if i, ok := colIndexByName[strings.ToLower(expr.Name)]; ok {
					idx = i
				}
			case parser.Value:
				if expr.Type == "int" && expr.IntVal >= 1 && int(expr.IntVal) <= len(projectColumns) {
					idx = int(expr.IntVal) - 1
				}
			case *parser.Value:
				if expr.Type == "int" && expr.IntVal >= 1 && int(expr.IntVal) <= len(projectColumns) {
					idx = int(expr.IntVal) - 1
				}
			}
			if idx >= 0 {
				keys = append(keys, sortKey{idx: idx, desc: item.Direction == "DESC"})
			}
		}

		if len(keys) > 0 {
			sort.SliceStable(rows, func(i, j int) bool {
				for _, k := range keys {
					cmp := compareResultCells(rows[i][k.idx], rows[j][k.idx])
					if cmp == 0 {
						continue
					}
					if k.desc {
						return cmp > 0
					}
					return cmp < 0
				}
				return false
			})
		}
	}

	start := 0
	if c.stmt.HasOffset {
		start = c.stmt.Offset
		if start > len(rows) {
			start = len(rows)
		}
	}
	end := len(rows)
	if c.stmt.HasLimit {
		end = start + c.stmt.Limit
		if end > len(rows) {
			end = len(rows)
		}
	}
	return rows[start:end]
}

// compareResultCells compares rendered cells numerically when both parse as
// numbers, lexically otherwise.
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

func (c *SelectCommand) applyOrderBy(rows []storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, item := range c.stmt.OrderBy {
			vi, err := evalOperand(item.Expr, rows[i], schema, ctx)
			if err != nil {
				continue
			}
			vj, err := evalOperand(item.Expr, rows[j], schema, ctx)
			if err != nil {
				continue
			}

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

func (c *SelectCommand) resolveRows(ctx *ExecutionContext, dbName string) ([]storage.Row, string, error) {
	if c.stmt.AsOf == nil {
		rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
		return rows, "", err
	}

	if c.stmt.AsOf.UseVersion {
		rows, err := ctx.Storage.ReadRowsAsOf(dbName, c.stmt.TableName, c.stmt.AsOf.Version)
		return rows, fmt.Sprintf("AS OF VERSION %d", c.stmt.AsOf.Version), err
	}

	txID, err := ctx.Storage.TxIDAtTimestamp(dbName, c.stmt.AsOf.Timestamp)
	if err != nil {
		return nil, "", err
	}
	rows, err := ctx.Storage.ReadRowsAsOf(dbName, c.stmt.TableName, txID)
	if err != nil {
		return nil, "", err
	}
	return rows, fmt.Sprintf("AS OF %s", c.stmt.AsOf.Timestamp), nil
}

func (c *SelectCommand) executeWithStats(ctx *ExecutionContext) (*PlanStats, *Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, nil, err
	}

	var rows []storage.Row
	var asOfNote string
	usedIndex := false

	// Try index lookup
	if c.stmt.Where != nil && c.stmt.AsOf == nil {
		if cmp, ok := c.stmt.Where.(*parser.BinaryExpr); ok && cmp.Operator == "=" {
			if col, ok := cmp.Left.(*parser.ColumnRef); ok {
				var val parser.Value
				foundVal := false
				switch v := cmp.Right.(type) {
				case parser.Value:
					val = v
					foundVal = true
				case *parser.Value:
					val = *v
					foundVal = true
				}

				if foundVal {
					if positions, ok := ctx.Storage.IndexLookup(dbName, c.stmt.TableName, col.Name, valueToString(parserValueToRaw(val))); ok {
						rows, err = ctx.Storage.ReadRowsByPositions(dbName, c.stmt.TableName, positions)
						if err == nil {
							usedIndex = true
						}
					}
				}
			}
		}
	}

	if !usedIndex {
		rows, asOfNote, err = c.resolveRows(ctx, dbName)
		if err != nil {
			return nil, nil, err
		}
	}

	totalRows, _ := ctx.Storage.CountRows(dbName, c.stmt.TableName)

	stats := &PlanStats{
		RowsTotal:   totalRows,
		RowsScanned: len(rows),
		UsedIndex:   usedIndex,
	}

	requestedCols := make([]string, len(c.stmt.Columns))
	for i, col := range c.stmt.Columns {
		if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
			requestedCols[i] = colRef.Name
		} else {
			requestedCols[i] = "expr"
		}
	}

	projectIndices, projectColumns, err := resolveProjection(schema, requestedCols)
	if err != nil {
		return nil, nil, err
	}

	// Filter rows (WHERE)
	filtered := make([]storage.Row, 0, len(rows))
	for _, row := range rows {
		ok, err := evalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			stats.RowsFiltered++
			continue
		}
		stats.RowsMatched++
		filtered = append(filtered, row)
	}

	// Sort rows (ORDER BY)
	if len(c.stmt.OrderBy) > 0 {
		c.applyOrderBy(filtered, schema, ctx)
	}

	// Pagination (OFFSET and LIMIT)
	start := 0
	if c.stmt.HasOffset {
		start = c.stmt.Offset
		if start > len(filtered) {
			start = len(filtered)
		}
	}

	end := len(filtered)
	if c.stmt.HasLimit {
		end = start + c.stmt.Limit
		if end > len(filtered) {
			end = len(filtered)
		}
	}

	paged := filtered[start:end]

	resultRows := make([][]string, 0, len(paged))
	for _, row := range paged {
		projected := make([]string, len(projectIndices))
		for i, idx := range projectIndices {
			projected[i] = valueToString(row[idx])
		}
		resultRows = append(resultRows, projected)
	}

	return stats, &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
}

type ExplainCommand struct {
	stmt *parser.ExplainStatement
}

func (c *ExplainCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	planStart := time.Now()
	plan, err := buildPlan(ctx, dbName, c.stmt.Inner)
	if err != nil {
		return nil, err
	}
	plan.PlanningMs = float64(time.Since(planStart).Microseconds()) / 1000.0

	if !c.stmt.Analyze {
		return formatPlan(plan), nil
	}

	execStart := time.Now()
	selectCmd := &SelectCommand{stmt: c.stmt.Inner}
	stats, _, err := selectCmd.executeWithStats(ctx)
	if err != nil {
		return nil, err
	}
	stats.ExecutionMs = float64(time.Since(execStart).Microseconds()) / 1000.0
	plan.Root.Stats = stats

	return formatPlan(plan), nil
}

type HistoryCommand struct {
	stmt *parser.HistoryStatement
}

func (c *HistoryCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	var val parser.Value
	switch v := c.stmt.Key.(type) {
	case parser.Value:
		val = v
	case *parser.Value:
		val = *v
	default:
		return nil, fmt.Errorf("expected literal value for key, got %T", c.stmt.Key)
	}

	history, err := ctx.Storage.RowHistory(dbName, c.stmt.TableName, parserValueToRaw(val))
	if err != nil {
		return nil, err
	}

	columns := []string{"created_tx", "deleted_tx"}
	for _, col := range schema.Columns {
		columns = append(columns, col.Name)
	}

	rows := make([][]string, 0, len(history))
	for _, version := range history {
		row := make([]string, 0, 2+len(version.Data))
		row = append(row, fmt.Sprintf("%d", version.CreatedTx))
		if version.DeletedTx == 0 {
			row = append(row, "CURRENT")
		} else {
			row = append(row, fmt.Sprintf("%d", version.DeletedTx))
		}
		for _, value := range version.Data {
			row = append(row, valueToString(value))
		}
		rows = append(rows, row)
	}

	return &Result{
		Type:    "rows",
		Columns: columns,
		Rows:    rows,
	}, nil
}

type SetOperationCommand struct {
	stmt *parser.SetOperationStatement
}

func (c *SetOperationCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	leftCmd, err := CommandFactory(c.stmt.Left)
	if err != nil {
		return nil, err
	}
	rightCmd, err := CommandFactory(c.stmt.Right)
	if err != nil {
		return nil, err
	}

	leftRes, err := leftCmd.Execute(ctx)
	if err != nil {
		return nil, err
	}
	rightRes, err := rightCmd.Execute(ctx)
	if err != nil {
		return nil, err
	}

	if len(leftRes.Columns) != len(rightRes.Columns) {
		return nil, fmt.Errorf("queries in set operation must have the same number of columns")
	}

	var resultRows [][]string

	switch c.stmt.Op {
	case "UNION ALL":
		resultRows = append(leftRes.Rows, rightRes.Rows...)

	case "UNION":
		seen := make(map[string]bool)
		for _, row := range leftRes.Rows {
			key := strings.Join(row, "\x00")
			if !seen[key] {
				resultRows = append(resultRows, row)
				seen[key] = true
			}
		}
		for _, row := range rightRes.Rows {
			key := strings.Join(row, "\x00")
			if !seen[key] {
				resultRows = append(resultRows, row)
				seen[key] = true
			}
		}

	case "INTERSECT":
		rightMap := make(map[string]bool)
		for _, row := range rightRes.Rows {
			rightMap[strings.Join(row, "\x00")] = true
		}
		seen := make(map[string]bool)
		for _, row := range leftRes.Rows {
			key := strings.Join(row, "\x00")
			if rightMap[key] && !seen[key] {
				resultRows = append(resultRows, row)
				seen[key] = true
			}
		}

	case "EXCEPT":
		rightMap := make(map[string]bool)
		for _, row := range rightRes.Rows {
			rightMap[strings.Join(row, "\x00")] = true
		}
		seen := make(map[string]bool)
		for _, row := range leftRes.Rows {
			key := strings.Join(row, "\x00")
			if !rightMap[key] && !seen[key] {
				resultRows = append(resultRows, row)
				seen[key] = true
			}
		}
	}

	return &Result{
		Type:    "rows",
		Columns: leftRes.Columns,
		Rows:    resultRows,
	}, nil
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
			agg.Add(val)
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
