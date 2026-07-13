package auth

// RBAC commands: CREATE ROLE, DROP ROLE, GRANT, REVOKE.

import (
	"fmt"
	"strings"
	"time"

	"vaultdb/internal/executor/types"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

const (
	rbacDB      = "system"
	rolesTable  = "roles"
	grantsTable = "grants"
)

var validPrivileges = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true,
	"DELETE": true, "CREATE": true, "ALL": true,
}

func init() {
	types.RegisterCommand("CREATE_ROLE", func(stmt parser.Statement) types.Command {
		return &CreateRoleCommand{stmt: stmt.(*parser.CreateRoleStatement)}
	})
	types.RegisterCommand("DROP_ROLE", func(stmt parser.Statement) types.Command {
		return &DropRoleCommand{stmt: stmt.(*parser.DropRoleStatement)}
	})
	types.RegisterCommand("GRANT", func(stmt parser.Statement) types.Command {
		return &GrantCommand{stmt: stmt.(*parser.GrantStatement)}
	})
	types.RegisterCommand("REVOKE", func(stmt parser.Statement) types.Command {
		return &RevokeCommand{stmt: stmt.(*parser.RevokeStatement)}
	})
}

func ensureRBACTables(ctx *types.ExecutionContext) error {
	if ctx.Storage.TableExists(rbacDB, rolesTable) {
		return nil
	}
	_ = ctx.Storage.CreateDatabase(rbacDB)

	rolesSchema := storage.TableSchema{
		Name: rolesTable,
		Columns: []storage.ColumnSchema{
			{Name: "name", Type: "VARCHAR", VarcharLen: 200},
			{Name: "password_hash", Type: "TEXT"},
			{Name: "created_at", Type: "TIMESTAMP"},
		},
	}
	if err := ctx.Storage.CreateTable(rbacDB, rolesSchema); err != nil {
		return fmt.Errorf("create roles table: %w", err)
	}

	grantsSchema := storage.TableSchema{
		Name: grantsTable,
		Columns: []storage.ColumnSchema{
			{Name: "role", Type: "VARCHAR", VarcharLen: 200},
			{Name: "privilege", Type: "VARCHAR", VarcharLen: 50},
			{Name: "object", Type: "VARCHAR", VarcharLen: 200},
			{Name: "granted_at", Type: "TIMESTAMP"},
		},
	}
	if err := ctx.Storage.CreateTable(rbacDB, grantsSchema); err != nil {
		return fmt.Errorf("create grants table: %w", err)
	}
	return nil
}

type CreateRoleCommand struct {
	stmt *parser.CreateRoleStatement
}

func (c *CreateRoleCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if err := storage.ValidateObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("create role: %w", err)
	}
	if err := ensureRBACTables(ctx); err != nil {
		return nil, err
	}

	rows, err := ctx.Storage.ReadCurrentRows(rbacDB, rolesTable)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if strings.EqualFold(types.ValueToString(row[0]), c.stmt.Name) {
			return nil, fmt.Errorf("role '%s' already exists", c.stmt.Name)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	row := storage.Row{c.stmt.Name, c.stmt.Password, now}
	if _, err := ctx.Storage.InsertRows(rbacDB, rolesTable, []storage.Row{row}); err != nil {
		return nil, err
	}

	if al := ctx.Session.GetAuditLog(); al != nil {
		al.LogDDL("CREATE ROLE", rbacDB, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "CREATE ROLE", rbacDB+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Role '%s' created.", c.stmt.Name)}, nil
}

type DropRoleCommand struct {
	stmt *parser.DropRoleStatement
}

func (c *DropRoleCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if err := storage.ValidateObjectName(c.stmt.Name); err != nil {
		return nil, fmt.Errorf("drop role: %w", err)
	}
	if err := ensureRBACTables(ctx); err != nil {
		return nil, err
	}

	rows, err := ctx.Storage.ReadCurrentRows(rbacDB, rolesTable)
	if err != nil {
		return nil, err
	}
	rowIdx := -1
	for i, row := range rows {
		if strings.EqualFold(types.ValueToString(row[0]), c.stmt.Name) {
			rowIdx = i
			break
		}
	}
	if rowIdx == -1 {
		if c.stmt.IfExists {
			return &types.Result{Type: "message", Message: fmt.Sprintf("Role '%s' does not exist (ignoring).", c.stmt.Name)}, nil
		}
		return nil, fmt.Errorf("role '%s' does not exist", c.stmt.Name)
	}

	if _, err := ctx.Storage.DeleteRows(rbacDB, rolesTable, []int{rowIdx}); err != nil {
		return nil, err
	}

	grantRows, err := ctx.Storage.ReadCurrentRows(rbacDB, grantsTable)
	if err == nil {
		var toDelete []int
		for i, row := range grantRows {
			if strings.EqualFold(types.ValueToString(row[0]), c.stmt.Name) {
				toDelete = append(toDelete, i)
			}
		}
		if len(toDelete) > 0 {
			_, _ = ctx.Storage.DeleteRows(rbacDB, grantsTable, toDelete)
		}
	}

	if al := ctx.Session.GetAuditLog(); al != nil {
		al.LogDDL("DROP ROLE", rbacDB, c.stmt.Name, "")
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "DROP ROLE", rbacDB+"."+c.stmt.Name, "")
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Role '%s' dropped.", c.stmt.Name)}, nil
}

type GrantCommand struct {
	stmt *parser.GrantStatement
}

func (c *GrantCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if err := ensureRBACTables(ctx); err != nil {
		return nil, err
	}

	if err := requireRoleExists(ctx, c.stmt.To); err != nil {
		return nil, err
	}

	for _, priv := range c.stmt.Privileges {
		if !validPrivileges[priv] {
			return nil, fmt.Errorf("unknown privilege: %s", priv)
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, priv := range c.stmt.Privileges {
		grantRows, err := ctx.Storage.ReadCurrentRows(rbacDB, grantsTable)
		if err != nil {
			return nil, err
		}
		duplicate := false
		for _, row := range grantRows {
			if strings.EqualFold(types.ValueToString(row[0]), c.stmt.To) &&
				strings.EqualFold(types.ValueToString(row[1]), priv) &&
				strings.EqualFold(types.ValueToString(row[2]), c.stmt.On) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}

		row := storage.Row{c.stmt.To, priv, c.stmt.On, now}
		if _, err := ctx.Storage.InsertRows(rbacDB, grantsTable, []storage.Row{row}); err != nil {
			return nil, err
		}
	}

	if al := ctx.Session.GetAuditLog(); al != nil {
		al.LogDDL("GRANT", rbacDB, c.stmt.To, fmt.Sprintf("privileges=%s on=%s", strings.Join(c.stmt.Privileges, ","), c.stmt.On))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "GRANT", rbacDB+"."+c.stmt.To, fmt.Sprintf("privileges=%s on=%s", strings.Join(c.stmt.Privileges, ","), c.stmt.On))
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Granted privileges on '%s' to role '%s'.", c.stmt.On, c.stmt.To)}, nil
}

type RevokeCommand struct {
	stmt *parser.RevokeStatement
}

func (c *RevokeCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if err := ensureRBACTables(ctx); err != nil {
		return nil, err
	}

	if err := requireRoleExists(ctx, c.stmt.From); err != nil {
		return nil, err
	}

	grantRows, err := ctx.Storage.ReadCurrentRows(rbacDB, grantsTable)
	if err != nil {
		return nil, err
	}

	var toDelete []int
	for i, row := range grantRows {
		if !strings.EqualFold(types.ValueToString(row[0]), c.stmt.From) {
			continue
		}
		if !strings.EqualFold(types.ValueToString(row[2]), c.stmt.On) {
			continue
		}
		for _, priv := range c.stmt.Privileges {
			if strings.EqualFold(types.ValueToString(row[1]), priv) || strings.EqualFold(types.ValueToString(row[1]), "ALL") {
				toDelete = append(toDelete, i)
				break
			}
		}
	}

	if len(toDelete) > 0 {
		if _, err := ctx.Storage.DeleteRows(rbacDB, grantsTable, toDelete); err != nil {
			return nil, err
		}
	}

	if al := ctx.Session.GetAuditLog(); al != nil {
		al.LogDDL("REVOKE", rbacDB, c.stmt.From, fmt.Sprintf("privileges=%s on=%s", strings.Join(c.stmt.Privileges, ","), c.stmt.On))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "REVOKE", rbacDB+"."+c.stmt.From, fmt.Sprintf("privileges=%s on=%s", strings.Join(c.stmt.Privileges, ","), c.stmt.On))
	}
	return &types.Result{Type: "message", Message: fmt.Sprintf("Revoked privileges on '%s' from role '%s'.", c.stmt.On, c.stmt.From)}, nil
}

func requireRoleExists(ctx *types.ExecutionContext, roleName string) error {
	rows, err := ctx.Storage.ReadCurrentRows(rbacDB, rolesTable)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strings.EqualFold(types.ValueToString(row[0]), roleName) {
			return nil
		}
	}
	return fmt.Errorf("role '%s' does not exist", roleName)
}

// GetRoleGrants returns all grants for a given role from the system.grants table.
func GetRoleGrants(store storage.StorageEngine, roleName string) (map[string][]string, error) {
	if !store.TableExists(rbacDB, grantsTable) {
		return nil, nil
	}
	rows, err := store.ReadCurrentRows(rbacDB, grantsTable)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, row := range rows {
		if strings.EqualFold(types.ValueToString(row[0]), roleName) {
			object := types.ValueToString(row[2])
			privilege := types.ValueToString(row[1])
			result[object] = append(result[object], privilege)
		}
	}
	return result, nil
}

// DBGrantsProvider implements auth.GrantsProvider backed by the storage engine.
type DBGrantsProvider struct {
	Store storage.StorageEngine
}

// GetRoleGrants returns the dynamic grants for the given role from the system.grants table.
func (p *DBGrantsProvider) GetRoleGrants(roleName string) (map[string][]string, error) {
	if p.Store == nil {
		return nil, nil
	}
	return GetRoleGrants(p.Store, roleName)
}
