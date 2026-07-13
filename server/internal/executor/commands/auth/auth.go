package auth

import (
	"fmt"

	"vaultdb/internal/executor/types"
	"vaultdb/internal/parser"
)

func init() {
	types.RegisterCommand("REVOKE_TOKEN", func(stmt parser.Statement) types.Command {
		return &RevokeTokenCommand{stmt: stmt.(*parser.RevokeTokenStatement)}
	})
}

// RevokeTokenCommand handles REVOKE TOKEN 'xxx'.
type RevokeTokenCommand struct {
	stmt *parser.RevokeTokenStatement
}

func (c *RevokeTokenCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if c.stmt.Token == "" {
		return nil, fmt.Errorf("REVOKE TOKEN: token string is required")
	}

	mgr := ctx.Session.GetAuthManager()
	if mgr == nil {
		return nil, fmt.Errorf("REVOKE TOKEN: auth manager not configured")
	}

	mgr.RevokeToken(c.stmt.Token)

	if auditLog := ctx.Session.GetAuditLog(); auditLog != nil {
		auditLog.LogDDL("REVOKE TOKEN", "", "", fmt.Sprintf("token=%s", c.stmt.Token[:min(len(c.stmt.Token), 8)]))
	}
	if ctx.Session.GetAuditTable() != nil {
		ctx.Session.LogAudit("session", "REVOKE TOKEN", "", fmt.Sprintf("token=%s", c.stmt.Token[:min(len(c.stmt.Token), 8)]))
	}
	return &types.Result{Type: "message", Message: "Token revoked."}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
