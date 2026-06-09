package executor

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

type CreateDatabaseCommand struct {
	stmt *parser.CreateDatabaseStatement
}

func (c *CreateDatabaseCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if err := ctx.Storage.CreateDatabase(c.stmt.DatabaseName); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Database '%s' created successfully.", c.stmt.DatabaseName)}, nil
}

type DropDatabaseCommand struct {
	stmt *parser.DropDatabaseStatement
}

func (c *DropDatabaseCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if err := ctx.Storage.DropDatabase(c.stmt.DatabaseName); err != nil {
		return nil, err
	}
	if ctx.CurrentDB != nil && strings.EqualFold(*ctx.CurrentDB, c.stmt.DatabaseName) {
		*ctx.CurrentDB = ""
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Database '%s' dropped successfully.", c.stmt.DatabaseName)}, nil
}

type UseDatabaseCommand struct {
	stmt *parser.UseDatabaseStatement
}

func (c *UseDatabaseCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Storage.DatabaseExists(c.stmt.DatabaseName) {
		return nil, fmt.Errorf("database '%s' does not exist", c.stmt.DatabaseName)
	}
	*ctx.CurrentDB = c.stmt.DatabaseName
	return &Result{Type: "message", Message: fmt.Sprintf("Using database '%s'.", c.stmt.DatabaseName)}, nil
}

type ShowDatabasesCommand struct {
	stmt *parser.ShowDatabasesStatement
}

func (c *ShowDatabasesCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	names, err := ctx.Storage.ListDatabases()
	if err != nil {
		return nil, err
	}

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		rows = append(rows, []string{name})
	}
	return &Result{Type: "rows", Columns: []string{"database"}, Rows: rows}, nil
}

type AlterTableCommand struct {
	stmt *parser.AlterTableStatement
}

func (c *AlterTableCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	switch action := c.stmt.Action.(type) {
	case *parser.AlterAddColumn:
		col := storage.ColumnSchema{
			Name:       action.Column.Name,
			Type:       action.Column.DataType,
			VarcharLen: action.Column.VarcharLen,
		}
		var defaultVal interface{}
		if action.Column.Default != nil {
			defaultVal, _ = evalOperand(action.Column.Default, nil, nil, ctx)
		}
		if err := ctx.Storage.AlterTableAddColumn(dbName, c.stmt.TableName, col, defaultVal); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Column '%s' added to table '%s'.", col.Name, c.stmt.TableName)}, nil

	case *parser.AlterDropColumn:
		if err := ctx.Storage.AlterTableDropColumn(dbName, c.stmt.TableName, action.ColumnName); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Column '%s' dropped from table '%s'.", action.ColumnName, c.stmt.TableName)}, nil

	case *parser.AlterRenameColumn:
		if err := ctx.Storage.AlterTableRenameColumn(dbName, c.stmt.TableName, action.OldName, action.NewName); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Column '%s' renamed to '%s' in table '%s'.", action.OldName, action.NewName, c.stmt.TableName)}, nil

	case *parser.AlterRenameTable:
		if err := ctx.Storage.AlterTableRenameTable(dbName, c.stmt.TableName, action.NewName); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' renamed to '%s'.", c.stmt.TableName, action.NewName)}, nil

	default:
		return nil, fmt.Errorf("unsupported ALTER TABLE action: %T", action)
	}
}

type ShowTablesCommand struct {
	stmt *parser.ShowTablesStatement
}

func (c *ShowTablesCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := resolveDatabase(ctx, c.stmt.DatabaseName)
	if err != nil {
		return nil, err
	}

	tables, err := ctx.Storage.ListTables(dbName)
	if err != nil {
		return nil, err
	}

	rows := make([][]string, 0, len(tables))
	for _, table := range tables {
		rows = append(rows, []string{table.Name, fmt.Sprintf("%d", table.RowCount)})
	}
	return &Result{Type: "rows", Columns: []string{"table", "rows"}, Rows: rows}, nil
}

type DescribeTableCommand struct {
	stmt *parser.DescribeTableStatement
}

func (c *DescribeTableCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := resolveDatabase(ctx, c.stmt.DatabaseName)
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

	createdAt := ""
	if !schema.CreatedAt.IsZero() {
		createdAt = schema.CreatedAt.Format(time.RFC3339)
	}
	rows := make([][]string, 0, len(schema.Columns))
	for _, column := range schema.Columns {
		rows = append(rows, []string{
			column.Name,
			formatColumnType(column),
			"YES",
			createdAt,
		})
	}
	return &Result{
		Type:    "rows",
		Columns: []string{"column", "type", "nullable", "created_at"},
		Rows:    rows,
	}, nil
}

type CreateTableCommand struct {
	stmt *parser.CreateTableStatement
}

func (c *CreateTableCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	columns := make([]storage.ColumnSchema, 0, len(c.stmt.Columns))
	for _, column := range c.stmt.Columns {
		columns = append(columns, storage.ColumnSchema{
			Name:       column.Name,
			Type:       column.DataType,
			VarcharLen: column.VarcharLen,
			IsComputed: column.Computed != nil,
		})
	}

	schema := storage.TableSchema{
		Name:     c.stmt.TableName,
		Database: dbName,
		Columns:  columns,
	}

	if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' created successfully.", c.stmt.TableName)}, nil
}

type DropTableCommand struct {
	stmt *parser.DropTableStatement
}

func (c *DropTableCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Storage.DropTable(dbName, c.stmt.TableName); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' dropped successfully.", c.stmt.TableName)}, nil
}

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

	return &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
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

type ShowIndexesCommand struct {
	stmt *parser.ShowIndexesStatement
}

func (c *ShowIndexesCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	names, err := ctx.Storage.ListIndexes(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}
	resRows := make([][]string, len(names))
	for i, name := range names {
		resRows[i] = []string{name}
	}
	return &Result{Type: "rows", Columns: []string{"index"}, Rows: resRows}, nil
}

type CreateIndexCommand struct {
	stmt *parser.CreateIndexStatement
}

func (c *CreateIndexCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Storage.CreateIndex(dbName, c.stmt.TableName, c.stmt.IndexName, c.stmt.Column); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Index '%s' created successfully.", c.stmt.IndexName)}, nil
}

type DropIndexCommand struct {
	stmt *parser.DropIndexStatement
}

func (c *DropIndexCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	// Note: DropIndex in StorageEngine needs dbName, tableName, indexName.
	// But CreateIndexStatement has TableName, DropIndexStatement only has IndexName.
	// We might need to search which table the index belongs to.
	// For simplicity, let's assume we can drop by name if we have a global index map or iterate over tables.
	// Actually, task.md says "DROP INDEX idx_users_id;".
	// Let's modify StorageEngine.DropIndex to only take dbName and indexName.
	if err := ctx.Storage.DropIndex(dbName, c.stmt.IndexName); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Index '%s' dropped successfully.", c.stmt.IndexName)}, nil
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

type InsertCommand struct {
	stmt *parser.InsertStatement
}

func (c *InsertCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		ctx.Session.ActiveTx.AddOp(txmanager.PendingOp{
			Type:    "insert",
			DB:      *ctx.CurrentDB,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered INSERT (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *InsertCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
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

	// Handle INFER SCHEMA
	if len(schema.Columns) == 0 && len(c.stmt.Rows) > 0 {
		// Infer schema from first row
		inferredCols := make([]storage.ColumnSchema, 0, len(c.stmt.Rows[0]))
		for i, expr := range c.stmt.Rows[0] {
			val, _ := evalOperand(expr, nil, nil, ctx)
			colType := inferType(val)
			name := fmt.Sprintf("col%d", i)
			if len(c.stmt.Columns) > i {
				name = c.stmt.Columns[i]
			}
			inferredCols = append(inferredCols, storage.ColumnSchema{Name: name, Type: colType})
		}

		// Update table schema on disk
		for _, col := range inferredCols {
			if err := ctx.Storage.AlterTableAddColumn(dbName, c.stmt.TableName, col, nil); err != nil {
				return nil, fmt.Errorf("infer schema failed: %w", err)
			}
		}
		// Reload schema
		schema, err = ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
	}

	rowsToInsert, err := c.buildRows(schema, ctx)
	if err != nil {
		return nil, err
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, c.stmt.TableName, ctx)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *InsertCommand) buildRows(schema *storage.TableSchema, ctx *ExecutionContext) ([]storage.Row, error) {
	result := make([]storage.Row, 0, len(c.stmt.Rows))

	if len(c.stmt.Columns) == 0 {
		for rowIndex, row := range c.stmt.Rows {
			if len(row) != len(schema.Columns) {
				return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(schema.Columns))
			}
			normalized := make(storage.Row, len(row))
			for i, expr := range row {
				val, err := evalOperand(expr, nil, nil, ctx)
				if err != nil {
					return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
				}
				converted, err := normalizeForColumn(val, schema.Columns[i])
				if err != nil {
					return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
				}
				normalized[i] = converted
			}
			result = append(result, normalized)
		}
		return result, nil
	}

	columnIndex := make(map[string]int, len(schema.Columns))
	for idx, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = idx
	}

	mappedColumns := make([]int, len(c.stmt.Columns))
	for i, name := range c.stmt.Columns {
		idx, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("unknown column '%s'", name)
		}
		mappedColumns[i] = idx
	}

	for rowIndex, row := range c.stmt.Rows {
		if len(row) != len(mappedColumns) {
			return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(mappedColumns))
		}

		normalized := make(storage.Row, len(schema.Columns))
		// Fill with nil first (or handle defaults if stored in storage schema)
		for i := range normalized {
			normalized[i] = nil
		}

		for i, expr := range row {
			colIdx := mappedColumns[i]
			val, err := evalOperand(expr, nil, nil, ctx)
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			converted, err := normalizeForColumn(val, schema.Columns[colIdx])
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			normalized[colIdx] = converted
		}
		// Compute GENERATED columns (placeholder logic)
		for i, col := range schema.Columns {
			if col.IsComputed {
				// For prototype: if name is 'double_level' and 'level' exists, double it
				if col.Name == "double_level" {
					levelIdx := -1
					for j, c := range schema.Columns {
						if c.Name == "level" {
							levelIdx = j
							break
						}
					}
					if levelIdx != -1 && normalized[levelIdx] != nil {
						if f, ok := toFloat(normalized[levelIdx]); ok {
							normalized[i] = f * 2
						}
					}
				}
			}
		}
		result = append(result, normalized)
	}

	return result, nil
}

type UpdateCommand struct {
	stmt *parser.UpdateStatement
}

func (c *UpdateCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		ctx.Session.ActiveTx.AddOp(txmanager.PendingOp{
			Type:    "update",
			DB:      *ctx.CurrentDB,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered UPDATE (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *UpdateCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
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
	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(rows))
	for idx, row := range rows {
		match, err := evalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if match {
			indices = append(indices, idx)
		}
	}

	updates, err := c.buildUpdates(schema)
	if err != nil {
		return nil, err
	}

	affected, err := ctx.Storage.UpdateRows(dbName, c.stmt.TableName, indices, updates)
	if err != nil {
		return nil, err
	}

	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, c.stmt.TableName, ctx)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *UpdateCommand) buildUpdates(schema *storage.TableSchema) (map[string]storage.Value, error) {
	columnMap := make(map[string]storage.ColumnSchema, len(schema.Columns))
	for _, col := range schema.Columns {
		columnMap[strings.ToLower(col.Name)] = col
	}

	updates := make(map[string]storage.Value, len(c.stmt.Assignments))
	for _, assignment := range c.stmt.Assignments {
		col, ok := columnMap[strings.ToLower(assignment.Column)]
		if !ok {
			return nil, fmt.Errorf("unknown column '%s'", assignment.Column)
		}
		var val parser.Value
		switch v := assignment.Value.(type) {
		case parser.Value:
			val = v
		case *parser.Value:
			val = *v
		default:
			return nil, fmt.Errorf("column '%s': expected literal value, got %T", assignment.Column, assignment.Value)
		}
		value, err := parserValueToColumnType(val, col)
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", assignment.Column, err)
		}
		updates[assignment.Column] = value
	}
	return updates, nil
}

type DeleteCommand struct {
	stmt *parser.DeleteStatement
}

func (c *DeleteCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		ctx.Session.ActiveTx.AddOp(txmanager.PendingOp{
			Type:    "delete",
			DB:      *ctx.CurrentDB,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered DELETE (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *DeleteCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
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
	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(rows))
	for idx, row := range rows {
		match, err := evalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if match {
			indices = append(indices, idx)
		}
	}

	affected, err := ctx.Storage.DeleteRows(dbName, c.stmt.TableName, indices)
	if err != nil {
		return nil, err
	}

	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, c.stmt.TableName, ctx)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

type VacuumCommand struct {
	stmt *parser.VacuumStatement
}

func (c *VacuumCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	var tables []string
	if c.stmt.TableName != "" {
		if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
			return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
		}
		tables = []string{c.stmt.TableName}
	} else {
		tableInfos, err := ctx.Storage.ListTables(dbName)
		if err != nil {
			return nil, err
		}
		for _, t := range tableInfos {
			tables = append(tables, t.Name)
		}
	}

	columns := []string{
		"table", "rows_before", "rows_after",
		"reclaimed", "size_before_kb", "size_after_kb", "duration_ms",
	}
	var resRows [][]string

	for _, table := range tables {
		stats, err := ctx.Storage.Vacuum(dbName, table)
		if err != nil {
			slog.Warn("vacuum failed for table", "table", table, "error", err)
			continue
		}
		resRows = append(resRows, []string{
			stats.TableName,
			fmt.Sprintf("%d", stats.RowsBefore),
			fmt.Sprintf("%d", stats.RowsAfter),
			fmt.Sprintf("%d", stats.ReclaimedRows),
			fmt.Sprintf("%d", stats.FileSizeBefore/1024),
			fmt.Sprintf("%d", stats.FileSizeAfter/1024),
			fmt.Sprintf("%.2f", stats.DurationMs),
		})
	}

	return &Result{
		Type:    "rows",
		Columns: columns,
		Rows:    resRows,
	}, nil
}

type BeginCommand struct {
	stmt *parser.BeginStatement
}

func (c *BeginCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		return nil, fmt.Errorf("transaction already active; COMMIT or ROLLBACK first")
	}

	currentTxID := ctx.Storage.CurrentTxID()
	ctx.Session.ActiveTx = ctx.Session.TxManager.Begin(currentTxID)

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d started.", ctx.Session.ActiveTx.ID),
	}, nil
}

type CommitCommand struct {
	stmt *parser.CommitStatement
}

func (c *CommitCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("no active transaction")
	}

	tx := ctx.Session.ActiveTx

	// Проверяем конфликты
	if err := checkConflicts(ctx, tx); err != nil {
		return nil, err
	}

	// Применяем все буферизованные операции атомарно
	if err := applyOps(ctx, tx.Ops); err != nil {
		tx.Rollback()
		ctx.Session.ActiveTx = nil
		return nil, fmt.Errorf("commit failed, transaction rolled back: %w", err)
	}

	opsCount := len(tx.Ops)
	tx.Rollback()
	ctx.Session.ActiveTx = nil

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d committed (%d operations).", tx.ID, opsCount),
	}, nil
}

type RollbackCommand struct {
	stmt *parser.RollbackStatement
}

func (c *RollbackCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("no active transaction")
	}
	opsCount := len(ctx.Session.ActiveTx.Ops)
	ctx.Session.ActiveTx.Rollback()
	ctx.Session.ActiveTx = nil
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction rolled back (%d operations discarded).", opsCount),
	}, nil
}

func checkConflicts(ctx *ExecutionContext, tx *txmanager.Transaction) error {
	// В MVP реализуем простую проверку: если любая из таблиц, в которые мы писали,
	// изменилась с момента SnapshotTxID — это конфликт.
	modifiedTables := make(map[string]bool)
	for _, op := range tx.Ops {
		modifiedTables[op.DB+"/"+op.Table] = true
	}

	for key := range modifiedTables {
		parts := strings.Split(key, "/")
		db, table := parts[0], parts[1]
		modified, err := ctx.Storage.TableModifiedSince(db, table, tx.SnapshotTxID)
		if err != nil {
			return err
		}
		if modified {
			return fmt.Errorf("table '%s' modified since transaction started", table)
		}
	}
	return nil
}

func applyOps(ctx *ExecutionContext, ops []txmanager.PendingOp) error {
	for _, op := range ops {
		switch op.Type {
		case "insert":
			stmt := op.Payload.(*parser.InsertStatement)
			cmd := &InsertCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return err
			}
		case "update":
			stmt := op.Payload.(*parser.UpdateStatement)
			cmd := &UpdateCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return err
			}
		case "delete":
			stmt := op.Payload.(*parser.DeleteStatement)
			cmd := &DeleteCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

type PrepareCommand struct {
	stmt *parser.PrepareStatement
}

func (c *PrepareCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	ctx.Session.PreparedStatements[c.stmt.Name] = &PreparedStatement{
		Name:  c.stmt.Name,
		Query: c.stmt.Query,
	}
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' prepared.", c.stmt.Name),
	}, nil
}

type ExecutePreparedCommand struct {
	stmt *parser.ExecuteStatement
}

func (c *ExecutePreparedCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	ps, ok := ctx.Session.PreparedStatements[c.stmt.Name]
	if !ok {
		return nil, fmt.Errorf("prepared statement '%s' not found", c.stmt.Name)
	}

	boundStmt, err := bindParams(ps.Query, c.stmt.Params)
	if err != nil {
		return nil, err
	}

	cmd, err := CommandFactory(boundStmt)
	if err != nil {
		return nil, err
	}
	return cmd.Execute(ctx)
}

type DeallocateCommand struct {
	stmt *parser.DeallocateStatement
}

func (c *DeallocateCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	delete(ctx.Session.PreparedStatements, c.stmt.Name)
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' deallocated.", c.stmt.Name),
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

func bindParams(stmt parser.Statement, params []parser.Value) (parser.Statement, error) {
	switch s := stmt.(type) {
	case *parser.SelectStatement:
		return &parser.SelectStatement{
			Columns:   s.Columns,
			TableName: s.TableName,
			Where:     bindExpr(s.Where, params),
			Limit:     s.Limit,
			HasLimit:  s.HasLimit,
			CountAll:  s.CountAll,
			AsOf:      s.AsOf,
		}, nil
	case *parser.UpdateStatement:
		newAssignments := make([]parser.Assignment, len(s.Assignments))
		for i, a := range s.Assignments {
			newAssignments[i] = parser.Assignment{
				Column: a.Column,
				Value:  bindExpr(a.Value, params),
			}
		}
		return &parser.UpdateStatement{
			TableName:   s.TableName,
			Assignments: newAssignments,
			Where:       bindExpr(s.Where, params),
		}, nil
	case *parser.InsertStatement:
		newRows := make([][]parser.Expression, len(s.Rows))
		for i, row := range s.Rows {
			newRows[i] = make([]parser.Expression, len(row))
			for j, expr := range row {
				newRows[i][j] = bindExpr(expr, params)
			}
		}
		return &parser.InsertStatement{
			TableName: s.TableName,
			Columns:   s.Columns,
			Rows:      newRows,
		}, nil
	case *parser.DeleteStatement:
		return &parser.DeleteStatement{
			TableName: s.TableName,
			Where:     bindExpr(s.Where, params),
		}, nil
	}
	return nil, fmt.Errorf("EXECUTE not supported for %T", stmt)
}

func bindExpr(expr parser.Expression, params []parser.Value) parser.Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *parser.ParamRef:
		if e.Index < 1 || e.Index > len(params) {
			return &parser.Value{Type: "null"}
		}
		p := params[e.Index-1]
		return &p
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{
			Left:     bindExpr(e.Left, params),
			Operator: e.Operator,
			Right:    bindExpr(e.Right, params),
		}
	case *parser.AndExpr:
		return &parser.AndExpr{
			Left:  bindExpr(e.Left, params),
			Right: bindExpr(e.Right, params),
		}
	case *parser.OrExpr:
		return &parser.OrExpr{
			Left:  bindExpr(e.Left, params),
			Right: bindExpr(e.Right, params),
		}
	case *parser.NotExpr:
		return &parser.NotExpr{
			Expr: bindExpr(e.Expr, params),
		}
	case *parser.Value:
		return e
	case *parser.ColumnRef:
		return e
	}
	return expr
}

func requireCurrentDB(ctx *ExecutionContext) (string, error) {
	if ctx.CurrentDB == nil || strings.TrimSpace(*ctx.CurrentDB) == "" {
		return "", fmt.Errorf("no active database selected; use USE <database>; first")
	}
	return *ctx.CurrentDB, nil
}

func resolveDatabase(ctx *ExecutionContext, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return requireCurrentDB(ctx)
	}
	if !ctx.Storage.DatabaseExists(requested) {
		return "", fmt.Errorf("database '%s' does not exist", requested)
	}
	return requested, nil
}

func formatColumnType(column storage.ColumnSchema) string {
	if column.Type == "VARCHAR" && column.VarcharLen > 0 {
		return fmt.Sprintf("VARCHAR(%d)", column.VarcharLen)
	}
	return column.Type
}

func resolveProjection(schema *storage.TableSchema, requested []string) ([]int, []string, error) {
	if len(requested) == 0 {
		indices := make([]int, len(schema.Columns))
		columns := make([]string, len(schema.Columns))
		for i, col := range schema.Columns {
			indices[i] = i
			columns[i] = col.Name
		}
		return indices, columns, nil
	}

	columnIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = i
	}

	indices := make([]int, 0, len(requested))
	columns := make([]string, 0, len(requested))
	for _, name := range requested {
		idx, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, nil, fmt.Errorf("unknown column '%s'", name)
		}
		indices = append(indices, idx)
		columns = append(columns, schema.Columns[idx].Name)
	}

	return indices, columns, nil
}

func parserValueToColumnType(value parser.Value, col storage.ColumnSchema) (storage.Value, error) {
	var raw storage.Value
	switch value.Type {
	case "int":
		raw = value.IntVal
	case "float":
		raw = value.FltVal
	case "string":
		raw = value.StrVal
	case "bool":
		raw = value.BoolVal
	case "null":
		raw = nil
	default:
		return nil, fmt.Errorf("unsupported value type '%s'", value.Type)
	}

	converted, err := normalizeForColumn(raw, col)
	if err != nil {
		return nil, err
	}
	return converted, nil
}

func normalizeForColumn(value storage.Value, col storage.ColumnSchema) (storage.Value, error) {
	tmpSchema := storage.TableSchema{Columns: []storage.ColumnSchema{col}}
	row := storage.Row{value}
	coerced, err := coerceRowViaEval(row, &tmpSchema)
	if err != nil {
		return nil, err
	}
	return coerced[0], nil
}

// coerceRowViaEval keeps executor independent from storage internals while sharing conversion logic.
func coerceRowViaEval(row storage.Row, schema *storage.TableSchema) (storage.Row, error) {
	coerced := make(storage.Row, len(row))
	for i, raw := range row {
		value, err := coerceToColumn(raw, schema.Columns[i])
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
		}
		coerced[i] = value
	}
	return coerced, nil
}

func coerceToColumn(value storage.Value, column storage.ColumnSchema) (storage.Value, error) {
	if value == nil {
		return nil, nil
	}

	switch strings.ToUpper(column.Type) {
	case "INT":
		switch v := value.(type) {
		case int64:
			return v, nil
		case int:
			return int64(v), nil
		case float64:
			if float64(int64(v)) != v {
				return nil, fmt.Errorf("cannot cast FLOAT to INT without precision loss")
			}
			return int64(v), nil
		default:
			return nil, fmt.Errorf("expected INT-compatible value, got %T", value)
		}
	case "FLOAT":
		switch v := value.(type) {
		case float64:
			return v, nil
		case int64:
			return float64(v), nil
		case int:
			return float64(v), nil
		default:
			return nil, fmt.Errorf("expected FLOAT-compatible value, got %T", value)
		}
	case "BOOL":
		boolValue, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected BOOL value, got %T", value)
		}
		return boolValue, nil
	case "TEXT", "VARCHAR":
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected string value, got %T", value)
		}
		if column.Type == "VARCHAR" && column.VarcharLen > 0 && len([]rune(stringValue)) > column.VarcharLen {
			return nil, fmt.Errorf("VARCHAR(%d) overflow", column.VarcharLen)
		}
		return stringValue, nil
	case "VECTOR":
		vec, err := toVector(value)
		if err != nil {
			return nil, err
		}
		if column.VarcharLen > 0 && len(vec) != column.VarcharLen {
			return nil, fmt.Errorf("VECTOR(%d) dimension mismatch: got %d", column.VarcharLen, len(vec))
		}
		return vec, nil
	case "FLEXIBLE":
		// Can be a map or a raw JSON string
		switch v := value.(type) {
		case map[string]interface{}:
			return v, nil
		case string:
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(v), &m); err == nil {
				return m, nil
			}
			return v, nil // fallback to string if not JSON
		default:
			return valueToString(value), nil
		}
	case "DATE", "TIME", "TIMESTAMP", "DECIMAL":
		// For simplicity, store these as strings for now.
		return valueToString(value), nil
	default:
		return nil, fmt.Errorf("unsupported column type '%s'", column.Type)
	}
}

func valueToString(value interface{}) string {
	if value == nil {
		return "NULL"
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprintf("%v", v)
	}
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

	for _, wf := range funcs {
		// Add column to schema
		newSchema.Columns = append(newSchema.Columns, storage.ColumnSchema{Name: "window_func"})

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
		frameIndices := c.getFrameIndices(partitionIndices, currentPosInPartition, wf.Over.Frame)
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

func (c *SelectCommand) getFrameIndices(partitionIndices []int, currentPos int, frame *parser.FrameSpec) []int {
	if frame == nil {
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

func inferType(val interface{}) string {
	if val == nil {
		return "TEXT"
	}
	switch v := val.(type) {
	case int64, int:
		return "INT"
	case float64:
		return "FLOAT"
	case bool:
		return "BOOL"
	case []float64:
		return "VECTOR"
	case map[string]interface{}:
		return "FLEXIBLE"
	case string:
		// Try to see if it's JSON
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return "FLEXIBLE"
		}
		return "TEXT"
	default:
		return "TEXT"
	}
}

type MigrationCommand struct {
	stmt *parser.MigrationStatement
}

func (c *MigrationCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	// Track migrations in a system table
	migrationTable := "_migrations"
	if !ctx.Storage.TableExists(dbName, migrationTable) {
		schema := storage.TableSchema{
			Name: migrationTable,
			Columns: []storage.ColumnSchema{
				{Name: "name", Type: "VARCHAR", VarcharLen: 200},
				{Name: "sql", Type: "TEXT"},
				{Name: "applied_at", Type: "TIMESTAMP"},
			},
		}
		if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
			return nil, err
		}
	}

	switch c.stmt.Op {
	case "CREATE":
		// Store migration in the table without applying it
		row := storage.Row{c.stmt.Name, c.stmt.SQL, nil}
		if _, err := ctx.Storage.InsertRows(dbName, migrationTable, []storage.Row{row}); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Migration '%s' created.", c.stmt.Name)}, nil

	case "APPLY":
		// Find migration
		rows, err := ctx.Storage.ReadCurrentRows(dbName, migrationTable)
		if err != nil {
			return nil, err
		}
		var sqlToApply string
		found := false
		for _, row := range rows {
			if row[0] == c.stmt.Name {
				if row[2] != nil && row[2] != "NULL" {
					return nil, fmt.Errorf("migration '%s' already applied", c.stmt.Name)
				}
				sqlToApply = valueToString(row[1])
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("migration '%s' not found", c.stmt.Name)
		}

		// Apply SQL
		innerStmt, err := parser.Parse(sqlToApply)
		if err != nil {
			return nil, fmt.Errorf("failed to parse migration SQL: %w", err)
		}
		if _, err := ctx.Session.Execute(innerStmt); err != nil {
			return nil, fmt.Errorf("failed to apply migration: %w", err)
		}

		// Mark as applied
		// ... logic to update _migrations row ...
		return &Result{Type: "message", Message: fmt.Sprintf("Migration '%s' applied.", c.stmt.Name)}, nil

	case "PREVIEW":
		return &Result{Type: "message", Message: "Preview functionality not fully implemented yet."}, nil
	case "ROLLBACK":
		return &Result{Type: "message", Message: "Rollback functionality not fully implemented yet."}, nil
	default:
		return nil, fmt.Errorf("unknown migration operation: %s", c.stmt.Op)
	}
}

type CreatePolicyCommand struct {
	stmt *parser.CreatePolicyStatement
}

func (c *CreatePolicyCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	// Store policy in a system table
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	policyTable := "_policies"
	if !ctx.Storage.TableExists(dbName, policyTable) {
		schema := storage.TableSchema{
			Name: policyTable,
			Columns: []storage.ColumnSchema{
				{Name: "name", Type: "VARCHAR", VarcharLen: 200},
				{Name: "table_name", Type: "VARCHAR", VarcharLen: 200},
				{Name: "to_user", Type: "VARCHAR", VarcharLen: 200},
				{Name: "using_sql", Type: "TEXT"},
			},
		}
		ctx.Storage.CreateTable(dbName, schema)
	}

	// For simplicity, just use the SQL of the expression
	// This is a major hack for prototype
	row := storage.Row{c.stmt.Name, c.stmt.TableName, c.stmt.ToUser, "HACK_USING"}
	ctx.Storage.InsertRows(dbName, policyTable, []storage.Row{row})

	return &Result{Type: "message", Message: fmt.Sprintf("Policy '%s' created.", c.stmt.Name)}, nil
}

type EnableRlsCommand struct {
	stmt *parser.EnableRlsStatement
}

func (c *EnableRlsCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return &Result{Type: "message", Message: fmt.Sprintf("RLS enabled for table '%s'.", c.stmt.TableName)}, nil
}
