package executor

import (
	"fmt"

	"vaultdb/internal/parser"
)

// RevokeTokenCommand handles REVOKE TOKEN 'xxx'.
type RevokeTokenCommand struct {
	stmt *parser.RevokeTokenStatement
}

func (c *RevokeTokenCommand) Execute(ctx *ExecutionContext) (*Result, error) {
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
	return &Result{Type: "message", Message: "Token revoked."}, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
