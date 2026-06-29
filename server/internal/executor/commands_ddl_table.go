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

	columns := make([]storage.ColumnSchema, 0, len(c.stmt.Columns))
	for _, column := range c.stmt.Columns {
		columns = append(columns, storage.ColumnSchema{
			Name:       column.Name,
			Type:       column.DataType,
			VarcharLen: column.VarcharLen,
			IsComputed: column.Computed != nil,
			PrimaryKey: column.PrimaryKey,
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
	if err := ctx.Storage.DropIndex(dbName, c.stmt.IndexName); err != nil {
		return nil, err
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Index '%s' dropped successfully.", c.stmt.IndexName)}, nil
}
