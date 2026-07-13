package executor

// DDL commands for database operations.

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

type CreateDatabaseCommand struct {
	stmt *parser.CreateDatabaseStatement
}

func (c *CreateDatabaseCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if _, err := sanitizeObjectName(c.stmt.DatabaseName); err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	// IF NOT EXISTS: skip silently if database already exists
	if c.stmt.IfNotExists && ctx.Storage.DatabaseExists(c.stmt.DatabaseName) {
		return &Result{Type: "message", Message: fmt.Sprintf("Database '%s' already exists, skipping.", c.stmt.DatabaseName)}, nil
	}
	if err := ctx.Storage.CreateDatabase(c.stmt.DatabaseName); err != nil {
		return nil, err
	}

	objectsSchema := storage.TableSchema{
		Name: systemTableName,
		Columns: []storage.ColumnSchema{
			{Name: "name", Type: "TEXT"},
			{Name: "type", Type: "TEXT"},
			{Name: "definition", Type: "TEXT"},
			{Name: "created_at", Type: "INT"},
		},
	}
	if err := ctx.Storage.CreateTable(c.stmt.DatabaseName, objectsSchema); err != nil {
		return nil, fmt.Errorf("create database: create _objects table: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE DATABASE", c.stmt.DatabaseName, "", "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE DATABASE", c.stmt.DatabaseName, "")
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
	// IF EXISTS: skip silently if database doesn't exist
	if c.stmt.IfExists && !ctx.Storage.DatabaseExists(c.stmt.DatabaseName) {
		return &Result{Type: "message", Message: fmt.Sprintf("Database '%s' does not exist, skipping.", c.stmt.DatabaseName)}, nil
	}
	if err := ctx.Storage.DropDatabase(c.stmt.DatabaseName); err != nil {
		return nil, err
	}
	if strings.EqualFold(ctx.Session.CurrentDatabase(), c.stmt.DatabaseName) {
		ctx.Session.SetCurrentDatabase("")
	}
	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP DATABASE", c.stmt.DatabaseName, "", "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP DATABASE", c.stmt.DatabaseName, "")
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
	ctx.Session.SetCurrentDatabase(c.stmt.DatabaseName)
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
