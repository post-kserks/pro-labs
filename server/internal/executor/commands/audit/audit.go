package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"vaultdb/internal/audit"
	"vaultdb/internal/executor/types"
	"vaultdb/internal/parser"
)

func init() {
	types.RegisterCommand("VERIFY_AUDIT_LOG", func(stmt parser.Statement) types.Command {
		return &VerifyAuditLogCommand{stmt: stmt.(*parser.VerifyAuditLogStatement)}
	})
	types.RegisterCommand("ARCHIVE_AUDIT_LOG", func(stmt parser.Statement) types.Command {
		return &ArchiveAuditLogCommand{stmt: stmt.(*parser.ArchiveAuditLogStatement)}
	})
}

// VerifyAuditLogCommand verifies the integrity of the audit log hash chain.
type VerifyAuditLogCommand struct {
	stmt *parser.VerifyAuditLogStatement
}

func (c *VerifyAuditLogCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if ctx.Session.GetAuditTable() == nil {
		return nil, fmt.Errorf("audit logging is not enabled")
	}

	ok, count, err := ctx.Session.GetAuditTable().VerifyChain()
	if err != nil {
		return &types.Result{
			Type:    "message",
			Message: fmt.Sprintf("Audit chain BROKEN at entry: %v", err),
		}, nil
	}

	if ok {
		return &types.Result{
			Type:    "message",
			Message: fmt.Sprintf("Audit chain intact: %d entries verified, no tampering detected.", count),
		}, nil
	}
	return &types.Result{
		Type:    "message",
		Message: "Audit chain integrity check failed.",
	}, nil
}

// ArchiveAuditLogCommand exports audit log entries to a JSON file and optionally truncates old entries.
type ArchiveAuditLogCommand struct {
	stmt *parser.ArchiveAuditLogStatement
}

type archiveExport struct {
	ArchivedAt string        `json:"archived_at"`
	EntryCount int           `json:"entry_count"`
	ChainHash  string        `json:"chain_hash"`
	KeepCount  int           `json:"keep_count"`
	Entries    []audit.Entry `json:"entries"`
}

func (c *ArchiveAuditLogCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if ctx.Session.GetAuditTable() == nil {
		return nil, fmt.Errorf("audit logging is not enabled")
	}

	entries, err := ctx.Session.GetAuditTable().ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	if len(entries) == 0 {
		return &types.Result{
			Type:    "message",
			Message: "Audit log is empty, nothing to archive.",
		}, nil
	}

	outPath := c.stmt.Path
	if outPath == "" {
		outPath = ctx.Session.GetArchivePath()
	}
	if outPath == "" {
		return nil, fmt.Errorf("archive path not specified (use TO 'path' or set audit.archive_path)")
	}

	chainHash, err := ctx.Session.GetAuditTable().LastHash()
	if err != nil {
		return nil, fmt.Errorf("get chain hash: %w", err)
	}

	export := archiveExport{
		ArchivedAt: time.Now().UTC().Format(time.RFC3339Nano),
		EntryCount: len(entries),
		ChainHash:  chainHash,
		KeepCount:  c.stmt.KeepCount,
		Entries:    entries,
	}

	data, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal archive: %w", err)
	}

	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create archive directory: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0600); err != nil {
		return nil, fmt.Errorf("write archive file: %w", err)
	}

	keepCount := c.stmt.KeepCount
	if keepCount < 0 {
		keepCount = 0
	}

	if keepCount > 0 || c.stmt.KeepCount == 0 && len(entries) > 0 {
		if keepCount < len(entries) {
			if err := ctx.Session.GetAuditTable().TruncateKeepLast(keepCount); err != nil {
				return nil, fmt.Errorf("truncate audit log: %w", err)
			}
		}
	}

	msg := fmt.Sprintf("Archived %d audit entries to %s.", len(entries), outPath)
	if keepCount > 0 {
		msg += fmt.Sprintf(" Kept last %d entries.", keepCount)
	} else {
		msg += " Truncated all entries."
	}

	return &types.Result{
		Type:    "message",
		Message: msg,
	}, nil
}
