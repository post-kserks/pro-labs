package executor

// DDL commands for vacuum, migration, policies, views, triggers, functions, procedures.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
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
		return &Result{Type: "message", Message: fmt.Sprintf("Migration '%s' created.", c.stmt.Name)}, nil

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
				sqlToApply = valueToString(row[1])
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
		return &Result{Type: "message", Message: fmt.Sprintf("Migration '%s' applied.", c.stmt.Name)}, nil

	case "PREVIEW":
		previewRows, err := ctx.Storage.ReadCurrentRows(dbName, migrationTable)
		if err != nil {
			return nil, err
		}
		for _, row := range previewRows {
			if row[0] == c.stmt.Name {
				sqlToApply := valueToString(row[1])
				applied := row[2] != nil && row[2] != "NULL"
				status := "not applied"
				if applied {
					status = "already applied"
				}
				return &Result{
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
		return &Result{Type: "message", Message: fmt.Sprintf("Migration '%s' rolled back.", c.stmt.Name)}, nil
	default:
		return nil, fmt.Errorf("unknown migration operation: %s", c.stmt.Op)
	}
}

type CreatePolicyCommand struct {
	stmt *parser.CreatePolicyStatement
}

func (c *CreatePolicyCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	usingSQL := ""
	if c.stmt.Using != nil {
		usingSQL = exprToSQL(c.stmt.Using)
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
	} else if err := addViewPolicy(ctx, dbName, c.stmt.TableName, policy); err != nil {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	if ctx.Session != nil && asSession(ctx).resultCache != nil {
		func() { if rc := ctx.Session.GetResultCache(); rc != nil { rc.(*ResultCache).Invalidate(c.stmt.TableName) } }()
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE POLICY", dbName, c.stmt.Name, fmt.Sprintf("table=%s", c.stmt.TableName))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE POLICY", dbName+"."+c.stmt.Name, fmt.Sprintf("table=%s", c.stmt.TableName))
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
	if ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		if err := ctx.Storage.SetTableRLS(dbName, c.stmt.TableName, true); err != nil {
			return nil, err
		}
	} else if err := setViewRLS(ctx, dbName, c.stmt.TableName, true); err != nil {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	if ctx.Session != nil && asSession(ctx).resultCache != nil {
		func() { if rc := ctx.Session.GetResultCache(); rc != nil { rc.(*ResultCache).Invalidate(c.stmt.TableName) } }()
	}
	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("ENABLE RLS", dbName, c.stmt.TableName, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "ENABLE RLS", dbName+"."+c.stmt.TableName, "")
	}
	return &Result{Type: "message", Message: fmt.Sprintf("RLS enabled on table '%s'.", c.stmt.TableName)}, nil
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

	querySQL := parser.FormatSelectStatement(c.stmt.Query)

	vd := map[string]interface{}{
		"name":  c.stmt.Name,
		"query": querySQL,
	}

	if err := storeObject(ctx, dbName, objTypeView, c.stmt.Name, vd); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE VIEW", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE VIEW", dbName+"."+c.stmt.Name, "")
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

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP VIEW", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP VIEW", dbName+"."+c.stmt.Name, "")
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
		"name":   c.stmt.Name,
		"table":  c.stmt.TableName,
		"timing": c.stmt.Timing,
		"event":  c.stmt.Event,
		"body":   c.stmt.Body,
	}

	if err := storeObject(ctx, dbName, objTypeTrigger, c.stmt.Name, td); err != nil {
		return nil, fmt.Errorf("create trigger: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE TRIGGER", dbName, c.stmt.Name, fmt.Sprintf("table=%s event=%s", c.stmt.TableName, c.stmt.Event))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE TRIGGER", dbName+"."+c.stmt.Name, fmt.Sprintf("table=%s event=%s", c.stmt.TableName, c.stmt.Event))
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

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP TRIGGER", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP TRIGGER", dbName+"."+c.stmt.Name, "")
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
		if containsSubqueryDML(selStmt) {
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

	if err := storeObject(ctx, dbName, objTypeFunction, c.stmt.Name, fd); err != nil {
		return nil, fmt.Errorf("create function: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE FUNCTION", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE FUNCTION", dbName+"."+c.stmt.Name, "")
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

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP FUNCTION", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP FUNCTION", dbName+"."+c.stmt.Name, "")
	}
	return &Result{Type: "message", Message: fmt.Sprintf("Function '%s' dropped.", c.stmt.Name)}, nil
}

func fireTriggers(ctx *ExecutionContext, dbName, tableName, event string) {
	const maxTriggerDepth = 3
	if ctx.TriggerDepth() >= maxTriggerDepth {
		slog.Warn("trigger recursion depth limit reached", "table", tableName, "event", event)
		return
	}

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

		if triggerTable != tableName || !strings.EqualFold(triggerEvent, event) {
			continue
		}
		if timing != "AFTER" {
			continue
		}
		if body == "" {
			continue
		}
		ctx.SetTriggerDepth(ctx.TriggerDepth() + 1)
		err := executeTriggerBody(ctx, body)
		ctx.SetTriggerDepth(ctx.TriggerDepth() - 1)
		if err != nil {
			slog.Error("trigger body execution failed", "trigger", name, "error", err)
		}
	}
}

func executeTriggerBody(ctx *ExecutionContext, body string) error {
	stmt, err := parser.Parse(body)
	if err != nil {
		return fmt.Errorf("trigger body parse: %w", err)
	}
	_, err = ctx.RunSubquery.RunSubquery(ctx, stmt)
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

	if err := storeObject(ctx, dbName, objTypeProcedure, c.stmt.Name, pd); err != nil {
		return nil, fmt.Errorf("create procedure: %w", err)
	}

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("CREATE PROCEDURE", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE PROCEDURE", dbName+"."+c.stmt.Name, "")
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

	if ctx.Session.GetAuditLog() != nil {
		ctx.Session.GetAuditLog().LogDDL("DROP PROCEDURE", dbName, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP PROCEDURE", dbName+"."+c.stmt.Name, "")
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

	// Parser requires trailing semicolon — append if missing
	if !strings.HasSuffix(strings.TrimSpace(body), ";") {
		body += ";"
	}

	// Support multi-statement bodies: split by ; and execute each
	var lastResult *Result
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
	return &Result{Type: "message", Message: "procedure executed (no result)"}, nil
}

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

// containsSubqueryDML recursively walks a SELECT statement's expressions and
// subqueries to detect any non-SELECT (INSERT/UPDATE/DELETE) DML.
func containsSubqueryDML(sel *parser.SelectStatement) bool {
	// Walk CTEs
	for _, cte := range sel.CTEs {
		if containsStatementDML(cte.Query) {
			return true
		}
	}
	// Walk FROM subquery
	if sel.FromSubquery != nil {
		if containsSubqueryDML(sel.FromSubquery) {
			return true
		}
	}
	// Walk JOINs
	for _, j := range sel.Joins {
		if containsExprDML(j.Condition) {
			return true
		}
	}
	// Walk column expressions
	for _, col := range sel.Columns {
		if containsExprDML(col.Expr) {
			return true
		}
	}
	// Walk WHERE, HAVING
	if containsExprDML(sel.Where) {
		return true
	}
	if containsExprDML(sel.Having) {
		return true
	}
	// Walk GROUP BY, ORDER BY expressions
	for _, e := range sel.GroupBy {
		if containsExprDML(e) {
			return true
		}
	}
	for _, o := range sel.OrderBy {
		if containsExprDML(o.Expr) {
			return true
		}
	}
	return false
}

// containsStatementDML checks if a Statement contains DML subqueries.
func containsStatementDML(stmt parser.Statement) bool {
	if sel, ok := stmt.(*parser.SelectStatement); ok {
		return containsSubqueryDML(sel)
	}
	return false // non-SELECT statements as CTE body are fine here
}

// containsExprDML checks if an Expression tree contains a subquery with DML.
func containsExprDML(expr parser.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.SubqueryExpr:
		if sel, ok := e.Query.(*parser.SelectStatement); ok {
			return containsSubqueryDML(sel)
		}
		return true // non-SELECT subquery is DML
	case *parser.ExistsExpr:
		if sel, ok := e.Select.(*parser.SelectStatement); ok {
			return containsSubqueryDML(sel)
		}
		return true
	case *parser.ComparisonSubqueryExpr:
		if sel, ok := e.Subquery.(*parser.SelectStatement); ok {
			return containsSubqueryDML(sel)
		}
		return true
	case *parser.BinaryExpr:
		return containsExprDML(e.Left) || containsExprDML(e.Right)
	case *parser.AndExpr:
		return containsExprDML(e.Left) || containsExprDML(e.Right)
	case *parser.OrExpr:
		return containsExprDML(e.Left) || containsExprDML(e.Right)
	case *parser.NotExpr:
		return containsExprDML(e.Expr)
	case *parser.InExpr:
		if containsExprDML(e.Left) {
			return true
		}
		for _, r := range e.Right {
			if containsExprDML(r) {
				return true
			}
		}
		return false
	case *parser.BetweenExpr:
		return containsExprDML(e.Expr) || containsExprDML(e.Lower) || containsExprDML(e.Upper)
	case *parser.CaseExpr:
		if e.Base != nil && containsExprDML(e.Base) {
			return true
		}
		for _, w := range e.Whens {
			if containsExprDML(w.Condition) || containsExprDML(w.Result) {
				return true
			}
		}
		return e.Else != nil && containsExprDML(e.Else)
	case *parser.CastExpr:
		return containsExprDML(e.Expr)
	case *parser.FunctionCall:
		for _, a := range e.Args {
			if containsExprDML(a) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		for _, a := range e.Args {
			if containsExprDML(a) {
				return true
			}
		}
		return false
	case *parser.WindowFunctionExpr:
		for _, a := range e.Args {
			if containsExprDML(a) {
				return true
			}
		}
		for _, p := range e.Over.PartitionBy {
			if containsExprDML(p) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

type ShowEncryptionStatusCommand struct {
	stmt *parser.ShowEncryptionStatusStatement
}

func (c *ShowEncryptionStatusCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
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
	return &Result{
		Type:    "rows",
		Columns: []string{"database", "encrypted", "algorithm", "key_source"},
		Rows:    rows,
	}, nil
}
