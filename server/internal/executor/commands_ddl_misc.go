package executor

// DDL commands for vacuum, migration, policies, views, triggers, functions, procedures.

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

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
	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
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
	if err := ctx.Storage.AddPolicy(dbName, c.stmt.TableName, policy); err != nil {
		return nil, fmt.Errorf("add policy: %w", err)
	}

	if ctx.Session != nil && ctx.Session.resultCache != nil {
		ctx.Session.resultCache.Invalidate(c.stmt.TableName)
	}

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("CREATE POLICY", dbName, c.stmt.Name, fmt.Sprintf("table=%s", c.stmt.TableName))
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
	if err := ctx.Storage.SetTableRLS(dbName, c.stmt.TableName, true); err != nil {
		return nil, err
	}

	if ctx.Session != nil && ctx.Session.resultCache != nil {
		ctx.Session.resultCache.Invalidate(c.stmt.TableName)
	}
	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("ENABLE RLS", dbName, c.stmt.TableName, "")
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

	querySQL := fmt.Sprintf("%v", c.stmt.Query)

	vd := map[string]interface{}{
		"name":  c.stmt.Name,
		"query": querySQL,
	}

	if err := storeObject(ctx, dbName, objTypeView, c.stmt.Name, vd); err != nil {
		return nil, fmt.Errorf("create view: %w", err)
	}

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("CREATE VIEW", dbName, c.stmt.Name, "")
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("DROP VIEW", dbName, c.stmt.Name, "")
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("CREATE TRIGGER", dbName, c.stmt.Name, fmt.Sprintf("table=%s event=%s", c.stmt.TableName, c.stmt.Event))
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("DROP TRIGGER", dbName, c.stmt.Name, "")
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("CREATE FUNCTION", dbName, c.stmt.Name, "")
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("DROP FUNCTION", dbName, c.stmt.Name, "")
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

		if triggerTable != tableName || !strings.EqualFold(triggerEvent, event) {
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("CREATE PROCEDURE", dbName, c.stmt.Name, "")
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

	if ctx.Session.AuditLog != nil {
		ctx.Session.AuditLog.LogDDL("DROP PROCEDURE", dbName, c.stmt.Name, "")
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
	parts := strings.Split(body, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		stmt, err := parser.Parse(part + ";")
		if err != nil {
			return nil, fmt.Errorf("procedure '%s' body: %w", c.stmt.Name, err)
		}
		cmd, err := CommandFactory(stmt)
		if err != nil {
			return nil, fmt.Errorf("procedure '%s': %w", c.stmt.Name, err)
		}
		result, err := cmd.Execute(ctx)
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
