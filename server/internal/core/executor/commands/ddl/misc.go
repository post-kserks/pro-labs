package ddl

// DDL commands for vacuum, migration, policies, views, triggers, functions, procedures.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/executor/types"
)

// validateWASMPath resolves a WASM file:// URI and ensures it is contained within
// the data directory. Rejects absolute paths, path traversal, and escapes.
func validateWASMPath(rawBody string, dataDir string) (string, error) {
	raw := strings.TrimPrefix(rawBody, "file://")

	if filepath.IsAbs(raw) {
		return "", fmt.Errorf("WASM path must not be absolute: %s", raw)
	}

	// Resolve relative to dataDir and verify containment.
	absPath := filepath.Clean(filepath.Join(dataDir, raw))
	absDataDir := filepath.Clean(dataDir)
	if !strings.HasPrefix(absPath, absDataDir+string(os.PathSeparator)) && absPath != absDataDir {
		return "", fmt.Errorf("WASM path escapes data directory: %s", raw)
	}

	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("WASM module not found: %s", absPath)
	}
	return absPath, nil
}

type VacuumCommand struct {
	stmt *parser.VacuumStatement
}

func init() {
	types.RegisterCommand("VACUUM", func(stmt parser.Statement) types.Command {
		return &VacuumCommand{stmt: stmt.(*parser.VacuumStatement)}
	})
}

func (c *VacuumCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
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

	return &types.Result{
		Type:    "rows",
		Columns: columns,
		Rows:    resRows,
	}, nil
}

type MigrationCommand struct {
	stmt *parser.MigrationStatement
}

func init() {
	types.RegisterCommand("MIGRATION", func(stmt parser.Statement) types.Command {
		return &MigrationCommand{stmt: stmt.(*parser.MigrationStatement)}
	})
}

func (c *MigrationCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

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
		// Validate migration SQL at CREATE time to prevent storing unsafe statements
		innerStmt, err := parser.Parse(c.stmt.SQL)
		if err != nil {
			return nil, fmt.Errorf("migration SQL is invalid: %w", err)
		}
		if !isMigrationSafe(innerStmt) {
			return nil, fmt.Errorf("migration contains unsafe statements: only DML (INSERT/UPDATE/DELETE/SELECT) and safe DDL (CREATE TABLE/INDEX/VIEW) are allowed")
		}

		row := storage.Row{c.stmt.Name, c.stmt.SQL, nil}
		if _, err := ctx.Storage.InsertRows(dbName, migrationTable, []storage.Row{row}); err != nil {
			return nil, err
		}
		return &types.Result{Type: "message", Message: fmt.Sprintf("Migration '%s' created.", c.stmt.Name)}, nil

	case "APPLY":
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
				sqlToApply = types.ValueToString(row[1])
				rowIdx = i
				break
			}
		}
		if rowIdx == -1 {
			return nil, fmt.Errorf("migration '%s' not found", c.stmt.Name)
		}

		innerStmt, err := parser.Parse(sqlToApply)
		if err != nil {
			return nil, fmt.Errorf("failed to parse migration SQL: %w", err)
		}
		if _, err := ctx.Session.Execute(innerStmt); err != nil {
			return nil, fmt.Errorf("failed to apply migration: %w", err)
		}

		appliedAt := time.Now().UTC().Format(time.RFC3339)
		if _, err := ctx.Storage.UpdateRows(dbName, migrationTable, []int{rowIdx}, map[string]storage.Value{
			"applied_at": appliedAt,
		}); err != nil {
			return nil, fmt.Errorf("migration '%s' applied but recording it failed: %w", c.stmt.Name, err)
		}
		return &types.Result{Type: "message", Message: fmt.Sprintf("Migration '%s' applied.", c.stmt.Name)}, nil

	case "PREVIEW":
		previewRows, err := ctx.Storage.ReadCurrentRows(dbName, migrationTable)
		if err != nil {
			return nil, err
		}
		for _, row := range previewRows {
			if row[0] == c.stmt.Name {
				sqlToApply := types.ValueToString(row[1])
				applied := row[2] != nil && row[2] != "NULL"
				status := "not applied"
				if applied {
					status = "already applied"
				}
				return &types.Result{
					Type:    "rows",
					Columns: []string{"migration", "status", "sql"},
					Rows:    [][]string{{c.stmt.Name, status, sqlToApply}},
				}, nil
			}
		}
		return nil, fmt.Errorf("migration '%s' not found", c.stmt.Name)

	case "ROLLBACK":
		rollbackRows, err := ctx.Storage.ReadCurrentRows(dbName, migrationTable)
		if err != nil {
			return nil, err
		}
		rowIdx := -1
		for i, row := range rollbackRows {
			if row[0] == c.stmt.Name {
				if row[2] == nil || row[2] == "NULL" {
					return nil, fmt.Errorf("migration '%s' is not applied", c.stmt.Name)
				}
				rowIdx = i
				break
			}
		}
		if rowIdx == -1 {
			return nil, fmt.Errorf("migration '%s' not found", c.stmt.Name)
		}
		if _, err := ctx.Storage.UpdateRows(dbName, migrationTable, []int{rowIdx}, map[string]storage.Value{
			"applied_at": nil,
		}); err != nil {
			return nil, fmt.Errorf("migration rollback failed: %w", err)
		}
		return &types.Result{Type: "message", Message: fmt.Sprintf("Migration '%s' rolled back.", c.stmt.Name)}, nil
	default:
		return nil, fmt.Errorf("unknown migration operation: %s", c.stmt.Op)
	}
}

type CreatePolicyCommand struct {
	stmt *parser.CreatePolicyStatement
}

func init() {
	types.RegisterCommand("CREATE_POLICY", func(stmt parser.Statement) types.Command {
		return &CreatePolicyCommand{stmt: stmt.(*parser.CreatePolicyStatement)}
	})
}

func (c *CreatePolicyCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	usingSQL := ""
	if c.stmt.Using != nil {
		usingSQL = types.ExprToSQL(c.stmt.Using)
	}
	policy := storage.RLSPolicy{
		Name:      c.stmt.Name,
		ToUser:    c.stmt.ToUser,
		UsingExpr: usingSQL,
	}

	// Check if target is a table or a view
	if ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		if err := ctx.Storage.AddPolicy(dbName, c.stmt.TableName, policy); err != nil {
			return nil, fmt.Errorf("add policy: %w", err)
		}
	} else if err := types.AddViewPolicy(ctx, dbName, c.stmt.TableName, policy); err != nil {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	if ctx.Session != nil {
		ctx.Session.InvalidateResultCache(c.stmt.TableName)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE POLICY", dbName, c.stmt.Name, fmt.Sprintf("table=%s", c.stmt.TableName))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE POLICY", dbName+"."+c.stmt.Name, fmt.Sprintf("table=%s", c.stmt.TableName))
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Policy '%s' created.", c.stmt.Name)}, nil
}

type EnableRlsCommand struct {
	stmt *parser.EnableRlsStatement
}

func init() {
	types.RegisterCommand("ENABLE_RLS", func(stmt parser.Statement) types.Command {
		return &EnableRlsCommand{stmt: stmt.(*parser.EnableRlsStatement)}
	})
}

func (c *EnableRlsCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}
	if ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		if err := ctx.Storage.SetTableRLS(dbName, c.stmt.TableName, true); err != nil {
			return nil, err
		}
	} else if err := types.SetViewRLS(ctx, dbName, c.stmt.TableName, true); err != nil {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	if ctx.Session != nil {
		ctx.Session.InvalidateResultCache(c.stmt.TableName)
	}
	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("ENABLE RLS", dbName, c.stmt.TableName, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "ENABLE RLS", dbName+"."+c.stmt.TableName, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("RLS enabled on table '%s'.", c.stmt.TableName)}, nil
}

type CreateViewCommand struct {
	stmt *parser.CreateViewStatement
}

func init() {
	types.RegisterCommand("CREATE_VIEW", func(stmt parser.Statement) types.Command {
		return &CreateViewCommand{stmt: stmt.(*parser.CreateViewStatement)}
	})
}

func (c *CreateViewCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}

	querySQL := parser.FormatSelectStatement(c.stmt.Query)

	vd := map[string]interface{}{
		"name":  c.stmt.Name,
		"query": querySQL,
	}

	if err := types.StoreObject(ctx, dbName, types.ObjTypeView, c.stmt.Name, vd); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE VIEW", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE VIEW", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("View '%s' created.", c.stmt.Name)}, nil
}

type DropViewCommand struct {
	stmt *parser.DropViewStatement
}

func init() {
	types.RegisterCommand("DROP_VIEW", func(stmt parser.Statement) types.Command {
		return &DropViewCommand{stmt: stmt.(*parser.DropViewStatement)}
	})
}

func (c *DropViewCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop view: %w", err)
	}

	if err := types.DeleteObject(ctx, dbName, types.ObjTypeView, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop view: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP VIEW", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP VIEW", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("View '%s' dropped.", c.stmt.Name)}, nil
}

type CreateTriggerCommand struct {
	stmt *parser.CreateTriggerStatement
}

func init() {
	types.RegisterCommand("CREATE_TRIGGER", func(stmt parser.Statement) types.Command {
		return &CreateTriggerCommand{stmt: stmt.(*parser.CreateTriggerStatement)}
	})
}

func (c *CreateTriggerCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	td := map[string]interface{}{
		"name":   c.stmt.Name,
		"table":  c.stmt.TableName,
		"timing": c.stmt.Timing,
		"event":  c.stmt.Event,
		"body":   c.stmt.Body,
	}

	if err := types.StoreObject(ctx, dbName, types.ObjTypeTrigger, c.stmt.Name, td); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE TRIGGER", dbName, c.stmt.Name, fmt.Sprintf("table=%s event=%s", c.stmt.TableName, c.stmt.Event))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE TRIGGER", dbName+"."+c.stmt.Name, fmt.Sprintf("table=%s event=%s", c.stmt.TableName, c.stmt.Event))
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Trigger '%s' created on table '%s'.", c.stmt.Name, c.stmt.TableName)}, nil
}

type DropTriggerCommand struct {
	stmt *parser.DropTriggerStatement
}

func init() {
	types.RegisterCommand("DROP_TRIGGER", func(stmt parser.Statement) types.Command {
		return &DropTriggerCommand{stmt: stmt.(*parser.DropTriggerStatement)}
	})
}

func (c *DropTriggerCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop trigger: %w", err)
	}

	if err := types.DeleteObject(ctx, dbName, types.ObjTypeTrigger, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop trigger: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP TRIGGER", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP TRIGGER", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Trigger '%s' dropped.", c.stmt.Name)}, nil
}

type CreateFunctionCommand struct {
	stmt *parser.CreateFunctionStatement
}

func init() {
	types.RegisterCommand("CREATE_FUNCTION", func(stmt parser.Statement) types.Command {
		return &CreateFunctionCommand{stmt: stmt.(*parser.CreateFunctionStatement)}
	})
}

func (c *CreateFunctionCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create function: %w", err)
	}

	if strings.EqualFold(c.stmt.Language, "sql") {
		bodyStmt, err := parser.Parse(c.stmt.Body)
		if err != nil {
			return nil, fmt.Errorf("function body is invalid SQL: %w", err)
		}
		var selStmt *parser.SelectStatement
		switch s := bodyStmt.(type) {
		case *parser.SelectStatement:
			selStmt = s
		case *parser.CTEStatement:
			if inner, ok := s.Body.(*parser.SelectStatement); ok {
				selStmt = inner
			} else {
				return nil, fmt.Errorf("function body must be a SELECT statement")
			}
		default:
			return nil, fmt.Errorf("function body must be a SELECT statement")
		}
		if types.ContainsSubqueryDML(selStmt) {
			return nil, fmt.Errorf("function body contains DML in subqueries")
		}
	}

	if strings.EqualFold(c.stmt.Language, "wasm") {
		if _, err := validateWASMPath(c.stmt.Body, ctx.Storage.DataDir()); err != nil {
			return nil, fmt.Errorf("create function: %w", err)
		}
		for key := range c.stmt.Options {
			if key != "memory_limit" && key != "timeout" {
				return nil, fmt.Errorf("unknown WASM option: %s", key)
			}
		}
	}

	fd := map[string]interface{}{
		"name":        c.stmt.Name,
		"params":      c.stmt.Params,
		"return_type": c.stmt.ReturnType,
		"body":        c.stmt.Body,
		"language":    c.stmt.Language,
	}
	if len(c.stmt.Options) > 0 {
		fd["options"] = c.stmt.Options
	}

	if err := types.StoreObject(ctx, dbName, types.ObjTypeFunction, c.stmt.Name, fd); err != nil {
		return nil, fmt.Errorf("create function: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE FUNCTION", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE FUNCTION", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Function '%s' created.", c.stmt.Name)}, nil
}

type DropFunctionCommand struct {
	stmt *parser.DropFunctionStatement
}

func init() {
	types.RegisterCommand("DROP_FUNCTION", func(stmt parser.Statement) types.Command {
		return &DropFunctionCommand{stmt: stmt.(*parser.DropFunctionStatement)}
	})
}

func (c *DropFunctionCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop function: %w", err)
	}

	if err := types.DeleteObject(ctx, dbName, types.ObjTypeFunction, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop function: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP FUNCTION", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP FUNCTION", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Function '%s' dropped.", c.stmt.Name)}, nil
}

type CreateProcedureCommand struct {
	stmt *parser.CreateProcedureStatement
}

func init() {
	types.RegisterCommand("CREATE_PROCEDURE", func(stmt parser.Statement) types.Command {
		return &CreateProcedureCommand{stmt: stmt.(*parser.CreateProcedureStatement)}
	})
}

func (c *CreateProcedureCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create procedure: %w", err)
	}

	if strings.EqualFold(c.stmt.Language, "sql") {
		body := c.stmt.Body
		if !strings.HasSuffix(strings.TrimSpace(body), ";") {
			body += ";"
		}
		parts := splitSQLStatements(body)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			bodyStmt, err := parser.Parse(part)
			if err != nil {
				return nil, fmt.Errorf("procedure body is invalid SQL: %w", err)
			}
			if !isProcedureBodySafe(bodyStmt) {
				return nil, fmt.Errorf("procedure body contains disallowed statements")
			}
		}
	}

	if strings.EqualFold(c.stmt.Language, "wasm") {
		if _, err := validateWASMPath(c.stmt.Body, ctx.Storage.DataDir()); err != nil {
			return nil, fmt.Errorf("create procedure: %w", err)
		}
		for key := range c.stmt.Options {
			if key != "memory_limit" && key != "timeout" {
				return nil, fmt.Errorf("unknown WASM option: %s", key)
			}
		}
	}

	pd := map[string]interface{}{
		"name":     c.stmt.Name,
		"params":   c.stmt.Params,
		"body":     c.stmt.Body,
		"language": c.stmt.Language,
	}
	if len(c.stmt.Options) > 0 {
		pd["options"] = c.stmt.Options
	}

	if err := types.StoreObject(ctx, dbName, types.ObjTypeProcedure, c.stmt.Name, pd); err != nil {
		return nil, fmt.Errorf("create procedure: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE PROCEDURE", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE PROCEDURE", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Procedure '%s' created.", c.stmt.Name)}, nil
}

type DropProcedureCommand struct {
	stmt *parser.DropProcedureStatement
}

func init() {
	types.RegisterCommand("DROP_PROCEDURE", func(stmt parser.Statement) types.Command {
		return &DropProcedureCommand{stmt: stmt.(*parser.DropProcedureStatement)}
	})
}

func (c *DropProcedureCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop procedure: %w", err)
	}

	if err := types.DeleteObject(ctx, dbName, types.ObjTypeProcedure, c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop procedure: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP PROCEDURE", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP PROCEDURE", dbName+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Procedure '%s' dropped.", c.stmt.Name)}, nil
}

type CallProcedureCommand struct {
	stmt *parser.CallProcedureStatement
}

func init() {
	types.RegisterCommand("CALL_PROCEDURE", func(stmt parser.Statement) types.Command {
		return &CallProcedureCommand{stmt: stmt.(*parser.CallProcedureStatement)}
	})
}

func (c *CallProcedureCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := sanitizeObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("call procedure: %w", err)
	}

	pd, err := types.LoadObject(ctx, dbName, types.ObjTypeProcedure, c.stmt.Name)
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

	// Parser requires trailing semicolon — append if missing
	if !strings.HasSuffix(strings.TrimSpace(body), ";") {
		body += ";"
	}

	// Support multi-statement bodies: split by ; and execute each
	var lastResult *types.Result
	parts := splitSQLStatements(body)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		stmt, err := parser.Parse(part)
		if err != nil {
			return nil, fmt.Errorf("procedure '%s' body: %w", c.stmt.Name, err)
		}
		result, err := ctx.RunSubquery.RunSubquery(ctx, stmt)
		if err != nil {
			return nil, err
		}
		lastResult = result
	}

	if lastResult != nil {
		return lastResult, nil
	}
	return &types.Result{Type: "message", Message: "procedure executed (no result)"}, nil
}

type ShowEncryptionStatusCommand struct {
	stmt *parser.ShowEncryptionStatusStatement
}

func init() {
	types.RegisterCommand("SHOW_ENCRYPTION_STATUS", func(stmt parser.Statement) types.Command {
		return &ShowEncryptionStatusCommand{stmt: stmt.(*parser.ShowEncryptionStatusStatement)}
	})
}

func (c *ShowEncryptionStatusCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	dekPath := filepath.Join(ctx.Storage.DataDir(), dbName, ".dek.enc")
	metaPath := filepath.Join(ctx.Storage.DataDir(), dbName, ".encryption_meta.json")

	encrypted := false
	algorithm := "-"
	keySource := "-"

	if _, err := os.Stat(dekPath); err == nil {
		encrypted = true
		algorithm = "AES-256-GCM"

		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				KeySource string `json:"key_source"`
			}
			if json.Unmarshal(data, &meta) == nil && meta.KeySource != "" {
				keySource = meta.KeySource
			}
		}
	}

	encStr := "no"
	if encrypted {
		encStr = "yes"
	}

	rows := [][]string{{dbName, encStr, algorithm, keySource}}
	return &types.Result{
		Type:    "rows",
		Columns: []string{"database", "encrypted", "algorithm", "key_source"},
		Rows:    rows,
	}, nil
}

// ─── Local helpers ─────────────────────────────────────────────────────────

func isProcedureBodySafe(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.SelectStatement, *parser.InsertStatement, *parser.UpdateStatement, *parser.DeleteStatement:
		return true
	case *parser.BeginStatement:
		return true
	default:
		return false
	}
}

// isMigrationSafe checks if a parsed statement is safe for use in migrations.
// Only DML (INSERT/UPDATE/DELETE/SELECT) and safe DDL (CREATE TABLE/INDEX/VIEW,
// restricted ALTER TABLE) are allowed. DROP TABLE, DROP INDEX, and destructive
// ALTER TABLE variants are rejected. System tables are rejected.
func isMigrationSafe(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.SelectStatement, *parser.InsertStatement, *parser.UpdateStatement, *parser.DeleteStatement:
		return true
	case *parser.CreateTableStatement:
		name := strings.ToLower(s.TableName)
		return !strings.HasPrefix(name, "_") && name != "vaultdb_audit_log"
	case *parser.CreateIndexStatement:
		return true
	case *parser.CreateViewStatement:
		return true
	case *parser.AlterTableStatement:
		return isAlterTableSafe(s)
	default:
		return false
	}
}

// isAlterTableSafe returns true only for ADD COLUMN and ADD CONSTRAINT actions.
func isAlterTableSafe(stmt *parser.AlterTableStatement) bool {
	switch stmt.Action.(type) {
	case *parser.AlterAddColumn, *parser.AlterAddConstraint:
		return true
	default:
		return false
	}
}

// splitSQLStatements splits a SQL string on semicolons that are outside of
// single-quoted or double-quoted string literals.
func splitSQLStatements(sql string) []string {
	var parts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	escaped := false

	for _, ch := range sql {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' && (inSingleQuote || inDoubleQuote) {
			current.WriteRune(ch)
			escaped = true
			continue
		}
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
			current.WriteRune(ch)
			continue
		}
		if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
			current.WriteRune(ch)
			continue
		}
		if ch == ';' && !inSingleQuote && !inDoubleQuote {
			parts = append(parts, current.String())
			current.Reset()
			continue
		}
		current.WriteRune(ch)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
