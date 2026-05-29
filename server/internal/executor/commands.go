package executor

import (
	"fmt"
	"log/slog"
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

func (c *SelectCommand) Execute(ctx *ExecutionContext) (*Result, error) {
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

	projectIndices, projectColumns, err := resolveProjection(schema, c.stmt.Columns)
	if err != nil {
		return nil, err
	}

	var rows []storage.Row
	var asOfNote string

	// Try index lookup
	usedIndex := false
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
			return nil, err
		}
	}

	if c.stmt.CountAll {
		count := 0
		for _, row := range rows {
			ok, err := evalExpr(c.stmt.Where, row, schema)
			if err != nil {
				return nil, err
			}
			if ok {
				count++
			}
		}
		return &Result{
			Type:     "rows",
			Columns:  []string{"count"},
			Rows:     [][]string{{fmt.Sprintf("%d", count)}},
			AsOfNote: asOfNote,
		}, nil
	}

	if c.stmt.HasLimit && c.stmt.Limit == 0 {
		return &Result{
			Type:     "rows",
			Columns:  projectColumns,
			Rows:     [][]string{},
			AsOfNote: asOfNote,
		}, nil
	}

	resultRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		ok, err := evalExpr(c.stmt.Where, row, schema)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		projected := make([]string, len(projectIndices))
		for i, idx := range projectIndices {
			projected[i] = valueToString(row[idx])
		}
		resultRows = append(resultRows, projected)
		if c.stmt.HasLimit && len(resultRows) >= c.stmt.Limit {
			break
		}
	}

	return &Result{
		Type:     "rows",
		Columns:  projectColumns,
		Rows:     resultRows,
		AsOfNote: asOfNote,
	}, nil
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

	if c.stmt.Where != nil {
		// slog.Info("WHERE expression", "type", fmt.Sprintf("%T", c.stmt.Where))
	}

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

	projectIndices, projectColumns, err := resolveProjection(schema, c.stmt.Columns)
	if err != nil {
		return nil, nil, err
	}

	resultRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		ok, err := evalExpr(c.stmt.Where, row, schema)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			stats.RowsFiltered++
			continue
		}
		stats.RowsMatched++

		projected := make([]string, len(projectIndices))
		for i, idx := range projectIndices {
			projected[i] = valueToString(row[idx])
		}

		if !c.stmt.HasLimit || len(resultRows) < c.stmt.Limit {
			resultRows = append(resultRows, projected)
		}
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

	rowsToInsert, err := c.buildRows(schema)
	if err != nil {
		return nil, err
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *InsertCommand) buildRows(schema *storage.TableSchema) ([]storage.Row, error) {
	result := make([]storage.Row, 0, len(c.stmt.Rows))

	if len(c.stmt.Columns) == 0 {
		for rowIndex, row := range c.stmt.Rows {
			if len(row) != len(schema.Columns) {
				return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(schema.Columns))
			}
			normalized := make(storage.Row, len(row))
			for i, expr := range row {
				var val parser.Value
				switch v := expr.(type) {
				case parser.Value:
					val = v
				case *parser.Value:
					val = *v
				default:
					return nil, fmt.Errorf("column '%s': expected literal value, got %T", schema.Columns[i].Name, expr)
				}
				converted, err := parserValueToColumnType(val, schema.Columns[i])

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
		for i, expr := range row {
			colIdx := mappedColumns[i]
			var val parser.Value
			switch v := expr.(type) {
			case parser.Value:
				val = v
			case *parser.Value:
				val = *v
			default:
				return nil, fmt.Errorf("column '%s': expected literal value, got %T", schema.Columns[colIdx].Name, expr)
			}
			converted, err := parserValueToColumnType(val, schema.Columns[colIdx])
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			normalized[colIdx] = converted
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
		match, err := evalExpr(c.stmt.Where, row, schema)
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
		match, err := evalExpr(c.stmt.Where, row, schema)
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

	switch column.Type {
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
