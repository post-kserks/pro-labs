package executor

import (
	"fmt"

	"vaultdb/internal/parser"
)

// VerifyAuditLogCommand verifies the integrity of the audit log hash chain.
type VerifyAuditLogCommand struct {
	stmt *parser.VerifyAuditLogStatement
}

func (c *VerifyAuditLogCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.AuditTable == nil {
		return nil, fmt.Errorf("audit logging is not enabled")
	}

	ok, count, err := ctx.Session.AuditTable.VerifyChain()
	if err != nil {
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Audit chain BROKEN at entry: %v", err),
		}, nil
	}

	if ok {
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Audit chain intact: %d entries verified, no tampering detected.", count),
		}, nil
	}
	return &Result{
		Type:    "message",
		Message: "Audit chain integrity check failed.",
	}, nil
}
