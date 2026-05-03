package executor

import (
	"fmt"
	"strings"
	"time"

	"pixeldb/internal/parser"
	"pixeldb/internal/storage"
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

	rows, err := ctx.Storage.SelectRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	projectIndices, projectColumns, err := resolveProjection(schema, c.stmt.Columns)
	if err != nil {
		return nil, err
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
			Type:    "rows",
			Columns: []string{"count"},
			Rows:    [][]string{{fmt.Sprintf("%d", count)}},
		}, nil
	}

	if c.stmt.HasLimit && c.stmt.Limit == 0 {
		return &Result{
			Type:    "rows",
			Columns: projectColumns,
			Rows:    [][]string{},
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
		Type:    "rows",
		Columns: projectColumns,
		Rows:    resultRows,
	}, nil
}

type InsertCommand struct {
	stmt *parser.InsertStatement
}

func (c *InsertCommand) Execute(ctx *ExecutionContext) (*Result, error) {
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
			for i, value := range row {
				converted, err := parserValueToColumnType(value, schema.Columns[i])
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
		for i, value := range row {
			colIdx := mappedColumns[i]
			converted, err := parserValueToColumnType(value, schema.Columns[colIdx])
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
	rows, err := ctx.Storage.SelectRows(dbName, c.stmt.TableName)
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
		value, err := parserValueToColumnType(assignment.Value, col)
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
	rows, err := ctx.Storage.SelectRows(dbName, c.stmt.TableName)
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
