package executor

// Команды DDL: базы данных, таблицы, индексы, миграции, политики.

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

var validObjectName = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func sanitizeObjectName(name string) (string, error) {
	if len(name) == 0 {
		return "", fmt.Errorf("object name is empty")
	}
	if len(name) > 128 {
		return "", fmt.Errorf("object name too long (max 128): %s", name)
	}
	if !validObjectName.MatchString(name) {
		return "", fmt.Errorf("invalid object name (only letters, digits, underscores): %s", name)
	}
	return name, nil
}

type CreateDatabaseCommand struct {
	stmt *parser.CreateDatabaseStatement
}

func (c *CreateDatabaseCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if _, err := sanitizeObjectName(c.stmt.DatabaseName); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	if err := ctx.Storage.CreateDatabase(c.stmt.DatabaseName); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Database '%s' created successfully.", c.stmt.DatabaseName)}, nil
}

type DropDatabaseCommand struct {
	stmt *parser.DropDatabaseStatement
}

func (c *DropDatabaseCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if _, err := sanitizeObjectName(c.stmt.DatabaseName); err != nil {
		return nil, fmt.Errorf("drop database: %w", err)
	}
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
	if _, err := sanitizeObjectName(c.stmt.DatabaseName); err != nil {
		return nil, fmt.Errorf("use database: %w", err)
	}
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

	// Invalidate caches for this table
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}
	if ctx.Session.resultCache != nil {
		ctx.Session.resultCache.Invalidate(c.stmt.TableName)
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
			defaultVal, err = evalOperand(action.Column.Default, nil, nil, ctx)
			if err != nil {
				return nil, fmt.Errorf("evaluating default value: %w", err)
			}
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

	case *parser.AlterAddConstraint:
		// WARNING: This operation drops and recreates the table.
		// If the process crashes between Drop and Create, data is lost.
		// TODO: Use schema rewrite mechanism for atomicity.
		schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
		constraint := storage.TableConstraint{
			Name:    action.Name,
			Type:    action.Type,
			Columns: action.Columns,
			Expr:    action.CheckExpr,
		}
		schema.Constraints = append(schema.Constraints, constraint)
		if err := ctx.Storage.DropTable(dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
		if err := ctx.Storage.CreateTable(dbName, *schema); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Constraint '%s' added to table '%s'.", action.Name, c.stmt.TableName)}, nil

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

	if _, err := sanitizeObjectName(c.stmt.TableName); err != nil {
		return nil, fmt.Errorf("create table: %w", err)
	}

	columns := make([]storage.ColumnSchema, 0, len(c.stmt.Columns))
	for _, column := range c.stmt.Columns {
		columns = append(columns, storage.ColumnSchema{
			Name:       column.Name,
			Type:       column.DataType,
			VarcharLen: column.VarcharLen,
			IsComputed: column.Computed != nil,
			EnumValues: column.EnumValues,
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
	if _, err := sanitizeObjectName(c.stmt.TableName); err != nil {
		return nil, fmt.Errorf("drop table: %w", err)
	}
	// Invalidate caches for this table
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}
	if ctx.Session.resultCache != nil {
		ctx.Session.resultCache.Invalidate(c.stmt.TableName)
	}
	if err := ctx.Storage.DropTable(dbName, c.stmt.TableName); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' dropped successfully.", c.stmt.TableName)}, nil
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

	// Multi-column index
	if len(c.stmt.Columns) > 1 {
		if err := ctx.Storage.CreateIndexMulti(dbName, c.stmt.TableName, c.stmt.IndexName, c.stmt.Columns); err != nil {
			return nil, err
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Multi-column index '%s' created successfully.", c.stmt.IndexName)}, nil
	}

 column := c.stmt.Column
	if column == "" && len(c.stmt.Columns) == 1 {
		column = c.stmt.Columns[0]
	}
	if err := ctx.Storage.CreateIndex(dbName, c.stmt.TableName, c.stmt.IndexName, column); err != nil {
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
		rowIdx := -1
		for i, row := range rows {
			if row[0] == c.stmt.Name {
				if row[2] != nil && row[2] != "NULL" {
					return nil, fmt.Errorf("migration '%s' already applied", c.stmt.Name)
				}
				sqlToApply = valueToString(row[1])
				rowIdx = i
				break
			}
		}
		if rowIdx == -1 {
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

		// Mark as applied so the already-applied guard above holds.
		appliedAt := time.Now().UTC().Format(time.RFC3339)
		if _, err := ctx.Storage.UpdateRows(dbName, migrationTable, []int{rowIdx}, map[string]storage.Value{
			"applied_at": appliedAt,
		}); err != nil {
			return nil, fmt.Errorf("migration '%s' applied but recording it failed: %w", c.stmt.Name, err)
		}
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
		if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
			return nil, fmt.Errorf("create policy table: %w", err)
		}
	}

	// NOTE: The USING expression is not yet parsed or stored.
	// The policy is created but NOT enforced. This is prototype-only.
	row := storage.Row{c.stmt.Name, c.stmt.TableName, c.stmt.ToUser, "HACK_USING"}
	if _, err := ctx.Storage.InsertRows(dbName, policyTable, []storage.Row{row}); err != nil {
		return nil, fmt.Errorf("insert policy: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Policy '%s' created.", c.stmt.Name)}, nil
}

type EnableRlsCommand struct {
	stmt *parser.EnableRlsStatement
}

func (c *EnableRlsCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}
	return nil, fmt.Errorf("RLS not yet implemented; policies are stored but not enforced")
}

type CreateViewCommand struct {
	stmt *parser.CreateViewStatement
}

func (c *CreateViewCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}

	querySQL := fmt.Sprintf("%v", c.stmt.Query)

	vd := map[string]interface{}{
		"name":  c.stmt.Name,
		"query": querySQL,
	}

	if err := storeObject(ctx, dbName, objTypeView, c.stmt.Name, vd); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("View '%s' created.", c.stmt.Name)}, nil
}

type DropViewCommand struct {
	stmt *parser.DropViewStatement
}

func (c *DropViewCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop view: %w", err)
	}

	if err := deleteObject(ctx, dbName, objTypeView, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop view: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("View '%s' dropped.", c.stmt.Name)}, nil
}

type CreateTriggerCommand struct {
	stmt *parser.CreateTriggerStatement
}

func (c *CreateTriggerCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	td := map[string]interface{}{
		"name":      c.stmt.Name,
		"table":     c.stmt.TableName,
		"timing":    c.stmt.Timing,
		"event":     c.stmt.Event,
		"body":      c.stmt.Body,
	}

	if err := storeObject(ctx, dbName, objTypeTrigger, c.stmt.Name, td); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Trigger '%s' created on table '%s'.", c.stmt.Name, c.stmt.TableName)}, nil
}

type DropTriggerCommand struct {
	stmt *parser.DropTriggerStatement
}

func (c *DropTriggerCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop trigger: %w", err)
	}

	if err := deleteObject(ctx, dbName, objTypeTrigger, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop trigger: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Trigger '%s' dropped.", c.stmt.Name)}, nil
}

type CreateFunctionCommand struct {
	stmt *parser.CreateFunctionStatement
}

func (c *CreateFunctionCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create function: %w", err)
	}

	fd := map[string]interface{}{
		"name":        c.stmt.Name,
		"params":      c.stmt.Params,
		"return_type": c.stmt.ReturnType,
		"body":        c.stmt.Body,
		"language":    c.stmt.Language,
	}

	if err := storeObject(ctx, dbName, objTypeFunction, c.stmt.Name, fd); err != nil {
		return nil, fmt.Errorf("create function: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Function '%s' created.", c.stmt.Name)}, nil
}

type DropFunctionCommand struct {
	stmt *parser.DropFunctionStatement
}

func (c *DropFunctionCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop function: %w", err)
	}

	if err := deleteObject(ctx, dbName, objTypeFunction, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop function: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Function '%s' dropped.", c.stmt.Name)}, nil
}

func fireTriggers(ctx *ExecutionContext, dbName, tableName, event string) {
	triggers, err := loadAllObjectsByType(ctx, dbName, objTypeTrigger)
	if err != nil {
		slog.Warn("failed to load triggers", "error", err)
		return
	}
	for _, td := range triggers {
		triggerTable, _ := td["table"].(string)
		triggerEvent, _ := td["event"].(string)
		timing, _ := td["timing"].(string)
		body, _ := td["body"].(string)
		name, _ := td["name"].(string)

		if triggerTable != tableName || strings.ToUpper(triggerEvent) != strings.ToUpper(event) {
			continue
		}
		if timing != "AFTER" {
			continue
		}
		if body == "" {
			continue
		}
		if err := executeTriggerBody(ctx, body); err != nil {
			slog.Error("trigger body execution failed", "trigger", name, "error", err)
		}
	}
}

func executeTriggerBody(ctx *ExecutionContext, body string) error {
	stmt, err := parser.Parse(body)
	if err != nil {
		return fmt.Errorf("trigger body parse: %w", err)
	}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		return fmt.Errorf("trigger body command: %w", err)
	}
	_, err = cmd.Execute(ctx)
	return err
}

type CreateProcedureCommand struct {
	stmt *parser.CreateProcedureStatement
}

func (c *CreateProcedureCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create procedure: %w", err)
	}

	pd := map[string]interface{}{
		"name":     c.stmt.Name,
		"params":   c.stmt.Params,
		"body":     c.stmt.Body,
		"language": c.stmt.Language,
	}

	if err := storeObject(ctx, dbName, objTypeProcedure, c.stmt.Name, pd); err != nil {
		return nil, fmt.Errorf("create procedure: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Procedure '%s' created.", c.stmt.Name)}, nil
}

type DropProcedureCommand struct {
	stmt *parser.DropProcedureStatement
}

func (c *DropProcedureCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop procedure: %w", err)
	}

	if err := deleteObject(ctx, dbName, objTypeProcedure, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop procedure: %w", err)
	}

	return &Result{Type: "message", Message: fmt.Sprintf("Procedure '%s' dropped.", c.stmt.Name)}, nil
}

type CallProcedureCommand struct {
	stmt *parser.CallProcedureStatement
}

func (c *CallProcedureCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("call procedure: %w", err)
	}

	pd, err := loadObject(ctx, dbName, objTypeProcedure, c.stmt.Name)
	if err != nil {
		return nil, fmt.Errorf("call procedure: %w", err)
	}
	if pd == nil {
		return nil, fmt.Errorf("procedure '%s' not found", c.stmt.Name)
	}

	body, _ := pd["body"].(string)
	if body == "" {
		return nil, fmt.Errorf("procedure '%s' has no body", c.stmt.Name)
	}

	stmt, err := parser.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("procedure '%s' body parse: %w", c.stmt.Name, err)
	}

	cmd, err := CommandFactory(stmt)
	if err != nil {
		return nil, fmt.Errorf("procedure '%s': %w", c.stmt.Name, err)
	}

	return cmd.Execute(ctx)
}
