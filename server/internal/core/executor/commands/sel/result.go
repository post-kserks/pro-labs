package sel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"vaultdb/internal/core/executor/eval"
	"vaultdb/internal/core/executor/optimizer"
	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

// resolveLimitOffset evaluates LimitExpr/OffsetExpr (parameterised LIMIT/OFFSET)
// and returns the resolved integer values.
func (c *SelectCommand) resolveLimitOffset(ctx *types.ExecutionContext) (int, bool, int, bool) {
	limit, hasLimit := c.stmt.Limit, c.stmt.HasLimit
	offset, hasOffset := c.stmt.Offset, c.stmt.HasOffset

	if c.stmt.LimitExpr != nil {
		val, err := types.EvalOperand(c.stmt.LimitExpr, nil, nil, ctx)
		if err == nil {
			if v, ok := eval.ToInt64(val); ok {
				limit = int(v)
				hasLimit = true
			}
		}
	}
	if c.stmt.OffsetExpr != nil {
		val, err := types.EvalOperand(c.stmt.OffsetExpr, nil, nil, ctx)
		if err == nil {
			if v, ok := eval.ToInt64(val); ok {
				offset = int(v)
				hasOffset = true
			}
		}
	}
	return limit, hasLimit, offset, hasOffset
}

func (c *SelectCommand) applyOrderBy(rows []storage.Row, schema *storage.TableSchema, ctx *types.ExecutionContext) {
	sort.SliceStable(rows, func(i, j int) bool {
		for _, item := range c.stmt.OrderBy {
			vi, err := types.EvalOperand(item.Expr, rows[i], schema, ctx)
			if err != nil {
				continue
			}
			vj, err := types.EvalOperand(item.Expr, rows[j], schema, ctx)
			if err != nil {
				continue
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

func (c *SelectCommand) resolveRows(ctx *types.ExecutionContext, dbName string) ([]storage.Row, string, error) {
	if c.stmt.AsOf == nil {
		if ctx.SnapshotTxID > 0 {
			rows, err := ctx.Storage.ReadRowsAsOf(dbName, c.stmt.TableName, ctx.SnapshotTxID)
			return rows, "", err
		}
		rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
		if err != nil {
			return nil, "", err
		}
		// read-your-own-writes: apply buffered transaction operations
		rows, err = types.ApplyTxOverlay(ctx, dbName, c.stmt.TableName, rows)
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

func (c *SelectCommand) executeWithStats(ctx *types.ExecutionContext) (*PlanStats, *types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
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

	// Try index lookup. Within a transaction we skip the index.
	if c.stmt.Where != nil && c.stmt.AsOf == nil && !ctx.Session.IsInTx() {
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
					if positions, ok := ctx.Storage.IndexLookup(dbName, c.stmt.TableName, col.Name, types.ValueToString(types.ParserValueToRaw(val))); ok {
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

	projectIndices, projectColumns, err := types.ResolveProjection(schema, requestedCols)
	if err != nil {
		return nil, nil, err
	}

	// Filter rows (WHERE)
	filtered := make([]storage.Row, 0, len(rows))
	for _, row := range rows {
		ok, err := types.EvalExpr(c.stmt.Where, row, schema, ctx)
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

	paged := filtered[start:end]

	resultRows := make([][]string, 0, len(paged))
	for _, row := range paged {
		projected := make([]string, len(projectIndices))
		for i, idx := range projectIndices {
			projected[i] = types.ValueToString(row[idx])
		}
		resultRows = append(resultRows, projected)
	}

	return stats, &types.Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
}

// PlanStats holds statistics for EXPLAIN ANALYZE.
type PlanStats struct {
	RowsTotal    int
	RowsScanned  int
	RowsMatched  int
	RowsFiltered int
	UsedIndex    bool
	ExecutionMs  float64
}

type ExplainCommand struct {
	stmt *parser.ExplainStatement
}

func (c *ExplainCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	// Use optimized plan if available
	opt := optimizer.NewOptimizer(ctx.Storage)
	optPlan, err := opt.OptimizePlan(dbName, c.stmt.Inner)
	if err != nil {
		return nil, err
	}

	if !c.stmt.Analyze {
		return &types.Result{
			Type:    "message",
			Message: optPlan.FormatOptimizedPlan(),
		}, nil
	}

	// EXPLAIN ANALYZE: execute and collect actual stats
	execStart := time.Now()
	selectCmd := &SelectCommand{stmt: c.stmt.Inner}
	stats, _, err := selectCmd.executeWithStats(ctx)
	if err != nil {
		return nil, err
	}
	stats.ExecutionMs = float64(time.Since(execStart).Microseconds()) / 1000.0

	// Merge planned and actual stats
	var b strings.Builder
	b.WriteString(optPlan.FormatOptimizedPlan())
	b.WriteString(fmt.Sprintf("\nActual Execution Time: %.2f ms\n", stats.ExecutionMs))
	b.WriteString(fmt.Sprintf("Actual Rows: %d\n", stats.RowsMatched))

	return &types.Result{
		Type:    "message",
		Message: b.String(),
	}, nil
}

type HistoryCommand struct {
	stmt *parser.HistoryStatement
}

func (c *HistoryCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
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

	var history []storage.VersionedRow
	if c.stmt.Key != nil {
		var val parser.Value
		switch v := c.stmt.Key.(type) {
		case parser.Value:
			val = v
		case *parser.Value:
			val = *v
		default:
			return nil, fmt.Errorf("expected literal value for key, got %T", c.stmt.Key)
		}
		history, err = ctx.Storage.RowHistory(dbName, c.stmt.TableName, types.ParserValueToRaw(val))
	} else {
		history, err = ctx.Storage.AllRowHistory(dbName, c.stmt.TableName)
	}
	if err != nil {
		return nil, err
	}

	columns := []string{"created_tx", "deleted_tx"}
	for _, col := range schema.Columns {
		columns = append(columns, col.Name)
	}

	// Build history schema for WHERE evaluation.
	histSchema := &storage.TableSchema{
		Name:     c.stmt.TableName,
		Database: dbName,
	}
	histSchema.Columns = append(histSchema.Columns,
		storage.ColumnSchema{Name: "created_tx", Type: "INT"},
		storage.ColumnSchema{Name: "deleted_tx", Type: "TEXT"},
	)
	histSchema.Columns = append(histSchema.Columns, schema.Columns...)
	colIdx := eval.BuildColumnIndex(histSchema)

	var filteredRows [][]string
	for _, version := range history {
		// Build eval row: created_tx, deleted_tx, data...
		evalRow := make(storage.Row, 0, 2+len(version.Data))
		evalRow = append(evalRow, int64(version.CreatedTx))
		if version.DeletedTx == 0 {
			evalRow = append(evalRow, "CURRENT")
		} else {
			evalRow = append(evalRow, fmt.Sprintf("%d", version.DeletedTx))
		}
		evalRow = append(evalRow, version.Data...)

		// Apply WHERE filter.
		if c.stmt.Where != nil {
			ok, err := types.EvalExpr(c.stmt.Where, evalRow, histSchema, ctx)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}

		row := make([]string, 0, len(columns))
		for _, v := range evalRow {
			row = append(row, types.ValueToString(v))
		}
		filteredRows = append(filteredRows, row)
	}

	_ = colIdx // used via buildColumnIndex for resolveColumn in EvalExpr
	return &types.Result{
		Type:    "rows",
		Columns: columns,
		Rows:    filteredRows,
	}, nil
}

type SetOperationCommand struct {
	stmt *parser.SetOperationStatement
}

func (c *SetOperationCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	leftRes, err := ctx.RunSubquery.RunSubquery(ctx, c.stmt.Left)
	if err != nil {
		return nil, err
	}
	rightRes, err := ctx.RunSubquery.RunSubquery(ctx, c.stmt.Right)
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

	return &types.Result{
		Type:    "rows",
		Columns: leftRes.Columns,
		Rows:    resultRows,
	}, nil
}

func (c *SelectCommand) executeDerivedTable(ctx *types.ExecutionContext) (*types.Result, error) {
	subResult, err := ctx.RunSubquery.RunSubquery(ctx, c.stmt.FromSubquery)
	if err != nil {
		return nil, fmt.Errorf("derived table: %w", err)
	}

	subSchema := &storage.TableSchema{
		Name:    c.stmt.FromAlias,
		Columns: make([]storage.ColumnSchema, len(subResult.Columns)),
	}
	for i, col := range subResult.Columns {
		colName := col
		if c.stmt.FromAlias != "" {
			colName = c.stmt.FromAlias + "." + col
		}
		colType := "TEXT"
		for _, row := range subResult.Rows {
			if i < len(row) && row[i] != "" {
				if _, err := strconv.ParseInt(row[i], 10, 64); err == nil {
					colType = "INT"
				} else if _, err := strconv.ParseFloat(row[i], 64); err == nil {
					colType = "FLOAT"
				} else if row[i] == "true" || row[i] == "false" {
					colType = "BOOL"
				}
				break
			}
		}
		subSchema.Columns[i] = storage.ColumnSchema{Name: colName, Type: colType}
	}

	subRows := make([]storage.Row, len(subResult.Rows))
	for i, row := range subResult.Rows {
		storageRow := make(storage.Row, len(row))
		for j, val := range row {
			if j < len(subSchema.Columns) {
				converted, err := convertStringToValue(val, subSchema.Columns[j])
				if err == nil {
					storageRow[j] = converted
				} else {
					storageRow[j] = val
				}
			} else {
				storageRow[j] = val
			}
		}
		subRows[i] = storageRow
	}

	combinedSchema := subSchema
	combinedRows := subRows

	if len(c.stmt.Joins) > 0 {
		combinedSchema, combinedRows, err = c.executeJoins(ctx, "", combinedSchema, combinedRows)
		if err != nil {
			return nil, err
		}
	}

	var filtered []storage.Row
	if c.stmt.Where != nil {
		for _, row := range combinedRows {
			match, err := types.EvalExpr(c.stmt.Where, row, combinedSchema, ctx)
			if err != nil {
				return nil, err
			}
			if match {
				filtered = append(filtered, row)
			}
		}
	} else {
		filtered = combinedRows
	}

	if len(c.stmt.GroupBy) > 0 || c.hasAggregates() {
		return c.executeWithGrouping(filtered, combinedSchema, "", ctx)
	}

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

	if len(c.stmt.OrderBy) > 0 {
		c.applyOrderBy(filtered, combinedSchema, ctx)
	}

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
	paged := filtered[start:end]

	effectiveColumns := c.stmt.Columns
	if len(effectiveColumns) == 0 {
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
			name := colRef.Name
			if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
				name = parts[1]
			}
			projectColumns[i] = name
		} else {
			projectColumns[i] = fmt.Sprintf("col%d", i)
		}
	}

	resultRows := make([][]string, 0, len(paged))
	for _, row := range paged {
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

	if c.stmt.Distinct {
		resultRows = distinctRows(resultRows)
	}

	return &types.Result{
		Type:    "rows",
		Columns: projectColumns,
		Rows:    resultRows,
	}, nil
}

func convertStringToValue(s string, col storage.ColumnSchema) (storage.Value, error) {
	switch strings.ToUpper(col.Type) {
	case "INT":
		val, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to INT", s)
		}
		return val, nil
	case "FLOAT":
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to FLOAT", s)
		}
		return val, nil
	case "BOOL":
		val, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to BOOL", s)
		}
		return val, nil
	case "TEXT", "VARCHAR":
		return s, nil
	default:
		return s, nil
	}
}
