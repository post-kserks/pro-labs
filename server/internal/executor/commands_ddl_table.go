package executor

// DDL commands for table and index operations.

import (
	"fmt"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

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

	// IF NOT EXISTS: skip silently if table already exists
	if c.stmt.IfNotExists && ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' already exists, skipping.", c.stmt.TableName)}, nil
	}

	columns := make([]storage.ColumnSchema, 0, len(c.stmt.Columns))
	for _, column := range c.stmt.Columns {
		col := storage.ColumnSchema{
			Name:          column.Name,
			Type:          column.DataType,
			VarcharLen:    column.VarcharLen,
			IsComputed:    column.Computed != nil,
			ComputedExpr:  parser.FormatExpression(column.Computed),
			PrimaryKey:    column.PrimaryKey,
			NotNull:       column.NotNull,
			Unique:        column.Unique,
			EnumValues:    column.EnumValues,
			AutoIncrement: column.AutoIncrement,
		}
		if column.Default != nil {
			val, err := evalOperand(column.Default, nil, nil, ctx)
			if err != nil {
				return nil, fmt.Errorf("evaluating default for column '%s': %w", column.Name, err)
			}
			converted, err := normalizeForColumn(val, col)
			if err != nil {
				return nil, fmt.Errorf("normalizing default for column '%s': %w", column.Name, err)
			}
			col.Default = &converted
		}
		columns = append(columns, col)
	}

	schema := storage.TableSchema{
		Name:     c.stmt.TableName,
		Database: dbName,
		Columns:  columns,
	}

	// Convert parser PartitionSpec to storage PartitionSpec
	if c.stmt.PartitionBy != nil {
		storageSpec := &storage.PartitionSpec{
			Type:     c.stmt.PartitionBy.Type,
			Columns:  c.stmt.PartitionBy.Columns,
			NumParts: c.stmt.PartitionBy.NumParts,
		}
		for _, pd := range c.stmt.PartitionBy.Partitions {
			storageSpec.Partitions = append(storageSpec.Partitions, storage.PartitionDef{
				Name:  pd.Name,
				Bound: pd.Bound,
			})
		}
		schema.PartitionBy = storageSpec
	}

	if err := ctx.Storage.CreateTable(dbName, schema); err != nil {
		return nil, err
	}

	// Auto-create FTS indexes for FULLTEXT columns
	if len(c.stmt.FullTextColumns) > 0 {
		for _, col := range c.stmt.FullTextColumns {
			indexName := "fts_" + c.stmt.TableName + "_" + col
			if err := ctx.Storage.CreateIndex(dbName, c.stmt.TableName, indexName, col, "GIN"); err != nil {
				return nil, fmt.Errorf("create fulltext index for column '%s': %w", col, err)
			}
		}
	}
	// Also collect inline FULLTEXT column constraints
	for _, col := range c.stmt.Columns {
		if col.FullText {
			indexName := "fts_" + c.stmt.TableName + "_" + col.Name
			if err := ctx.Storage.CreateIndex(dbName, c.stmt.TableName, indexName, col.Name, "GIN"); err != nil {
				return nil, fmt.Errorf("create fulltext index for column '%s': %w", col.Name, err)
			}
		}
	}

	// If partitioned, create physical partition tables
	if schema.PartitionBy != nil {
		pt := storage.NewPartitionedTable(&schema)
		for _, part := range pt.Partitions {
			partSchema := storage.TableSchema{
				Name:     part.TableName,
				Database: dbName,
				Columns:  columns,
			}
			if err := ctx.Storage.CreateTable(dbName, partSchema); err != nil {
				return nil, fmt.Errorf("create partition '%s': %w", part.Name, err)
			}
		}
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE TABLE", dbName, c.stmt.TableName, fmt.Sprintf("columns=%d", len(columns)))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE TABLE", dbName+"."+c.stmt.TableName, fmt.Sprintf("columns=%d", len(columns)))
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

	// IF EXISTS: skip silently if table doesn't exist
	if c.stmt.IfExists && !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' does not exist, skipping.", c.stmt.TableName)}, nil
	}

	if asSession(ctx).planCache != nil {
		func() { if pc := ctx.Session.GetPlanCache(); pc != nil { pc.(*PlanCache).Invalidate(c.stmt.TableName) } }()
	}
	if asSession(ctx).resultCache != nil {
		func() { if rc := ctx.Session.GetResultCache(); rc != nil { rc.(*ResultCache).Invalidate(c.stmt.TableName) } }()
	}
	if err := ctx.Storage.DropTable(dbName, c.stmt.TableName); err != nil {
		return nil, err
	}
	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP TABLE", dbName, c.stmt.TableName, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP TABLE", dbName+"."+c.stmt.TableName, "")
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' dropped successfully.", c.stmt.TableName)}, nil
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

	if asSession(ctx).planCache != nil {
		func() { if pc := ctx.Session.GetPlanCache(); pc != nil { pc.(*PlanCache).Invalidate(c.stmt.TableName) } }()
	}
	if asSession(ctx).resultCache != nil {
		func() { if rc := ctx.Session.GetResultCache(); rc != nil { rc.(*ResultCache).Invalidate(c.stmt.TableName) } }()
	}

	switch action := c.stmt.Action.(type) {
	case *parser.AlterAddColumn:
		col := storage.ColumnSchema{
			Name:         action.Column.Name,
			Type:         action.Column.DataType,
			VarcharLen:   action.Column.VarcharLen,
			IsComputed:   action.Column.Computed != nil,
			ComputedExpr: parser.FormatExpression(action.Column.Computed),
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
		if ctx.Session.GetAuditLog() != nil {
			ctx.Session.GetAuditLog().LogDDL("ALTER TABLE ADD COLUMN", dbName, c.stmt.TableName, fmt.Sprintf("column=%s", col.Name))
		}
		if ctx.Session.GetAuditTable() != nil {
			ctx.Session.LogAudit("session", "ALTER TABLE ADD COLUMN", dbName+"."+c.stmt.TableName, fmt.Sprintf("column=%s", col.Name))
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Column '%s' added to table '%s'.", col.Name, c.stmt.TableName)}, nil

	case *parser.AlterDropColumn:
		if err := ctx.Storage.AlterTableDropColumn(dbName, c.stmt.TableName, action.ColumnName); err != nil {
			return nil, err
		}
		if ctx.Session.GetAuditLog() != nil {
			ctx.Session.GetAuditLog().LogDDL("ALTER TABLE DROP COLUMN", dbName, c.stmt.TableName, fmt.Sprintf("column=%s", action.ColumnName))
		}
		if ctx.Session.GetAuditTable() != nil {
			ctx.Session.LogAudit("session", "ALTER TABLE DROP COLUMN", dbName+"."+c.stmt.TableName, fmt.Sprintf("column=%s", action.ColumnName))
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Column '%s' dropped from table '%s'.", action.ColumnName, c.stmt.TableName)}, nil

	case *parser.AlterRenameColumn:
		if err := ctx.Storage.AlterTableRenameColumn(dbName, c.stmt.TableName, action.OldName, action.NewName); err != nil {
			return nil, err
		}
		if ctx.Session.GetAuditLog() != nil {
			ctx.Session.GetAuditLog().LogDDL("ALTER TABLE RENAME COLUMN", dbName, c.stmt.TableName, fmt.Sprintf("from=%s to=%s", action.OldName, action.NewName))
		}
		if ctx.Session.GetAuditTable() != nil {
			ctx.Session.LogAudit("session", "ALTER TABLE RENAME COLUMN", dbName+"."+c.stmt.TableName, fmt.Sprintf("from=%s to=%s", action.OldName, action.NewName))
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Column '%s' renamed to '%s' in table '%s'.", action.OldName, action.NewName, c.stmt.TableName)}, nil

	case *parser.AlterRenameTable:
		if err := ctx.Storage.AlterTableRenameTable(dbName, c.stmt.TableName, action.NewName); err != nil {
			return nil, err
		}
		if ctx.Session.GetAuditLog() != nil {
			ctx.Session.GetAuditLog().LogDDL("ALTER TABLE RENAME", dbName, c.stmt.TableName, fmt.Sprintf("to=%s", action.NewName))
		}
		if ctx.Session.GetAuditTable() != nil {
			ctx.Session.LogAudit("session", "ALTER TABLE RENAME", dbName+"."+c.stmt.TableName, fmt.Sprintf("to=%s", action.NewName))
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Table '%s' renamed to '%s'.", c.stmt.TableName, action.NewName)}, nil

	case *parser.AlterAddConstraint:
		schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
		constraint := storage.TableConstraint{
			Name:            action.Name,
			Type:            action.Type,
			Columns:         action.Columns,
			Expr:            action.CheckExpr,
			RefTable:        action.RefTable,
			RefCols:         action.RefCols,
			OnDeleteCascade: action.OnDeleteCascade,
		}
		schema.Constraints = append(schema.Constraints, constraint)
		rows, _ := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
		if err := ctx.Storage.DropTable(dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
		if err := ctx.Storage.CreateTable(dbName, *schema); err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			if _, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rows); err != nil {
				return nil, err
			}
		}
		if ctx.Session.GetAuditLog() != nil {
			ctx.Session.GetAuditLog().LogDDL("ALTER TABLE ADD CONSTRAINT", dbName, c.stmt.TableName, fmt.Sprintf("constraint=%s", action.Name))
		}
		if ctx.Session.GetAuditTable() != nil {
			ctx.Session.LogAudit("session", "ALTER TABLE ADD CONSTRAINT", dbName+"."+c.stmt.TableName, fmt.Sprintf("constraint=%s", action.Name))
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
		if table.Name == systemTableName {
			continue
		}
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
		nullable := "YES"
		if column.NotNull {
			nullable = "NO"
		}
		rows = append(rows, []string{
			column.Name,
			formatColumnType(column),
			nullable,
			createdAt,
		})
	}
	return &Result{
		Type:    "rows",
		Columns: []string{"column", "type", "nullable", "created_at"},
		Rows:    rows,
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

	if len(c.stmt.Columns) > 1 {
		if c.stmt.Unique {
			err = ctx.Storage.CreateIndexMultiUnique(dbName, c.stmt.TableName, c.stmt.IndexName, c.stmt.Columns)
		} else {
			err = ctx.Storage.CreateIndexMulti(dbName, c.stmt.TableName, c.stmt.IndexName, c.stmt.Columns)
		}
		if err != nil {
			return nil, err
		}
		if ctx.Session.GetAuditLog() != nil {
			ctx.Session.GetAuditLog().LogDDL("CREATE INDEX", dbName, c.stmt.IndexName, fmt.Sprintf("table=%s columns=%v", c.stmt.TableName, c.stmt.Columns))
		}
		if ctx.Session.GetAuditTable() != nil {
			ctx.Session.LogAudit("session", "CREATE INDEX", dbName+"."+c.stmt.IndexName, fmt.Sprintf("table=%s columns=%v", c.stmt.TableName, c.stmt.Columns))
		}
		return &Result{Type: "message", Message: fmt.Sprintf("Multi-column index '%s' created successfully.", c.stmt.IndexName)}, nil
	}

	column := c.stmt.Column
	if column == "" && len(c.stmt.Columns) == 1 {
		column = c.stmt.Columns[0]
	}
	if c.stmt.Unique {
		err = ctx.Storage.CreateIndexUnique(dbName, c.stmt.TableName, c.stmt.IndexName, column, c.stmt.IndexType)
	} else {
		err = ctx.Storage.CreateIndex(dbName, c.stmt.TableName, c.stmt.IndexName, column, c.stmt.IndexType)
	}
	if err != nil {
		return nil, err
	}
	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE INDEX", dbName, c.stmt.IndexName, fmt.Sprintf("table=%s column=%s", c.stmt.TableName, column))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE INDEX", dbName+"."+c.stmt.IndexName, fmt.Sprintf("table=%s column=%s", c.stmt.TableName, column))
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
	if err := ctx.Storage.DropIndex(dbName, c.stmt.IndexName); err != nil {
		return nil, err
	}
	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP INDEX", dbName, c.stmt.IndexName, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP INDEX", dbName+"."+c.stmt.IndexName, "")
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Index '%s' dropped successfully.", c.stmt.IndexName)}, nil
}
