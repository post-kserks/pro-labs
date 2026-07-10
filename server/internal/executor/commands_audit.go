package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"vaultdb/internal/audit"
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

// ArchiveAuditLogCommand exports audit log entries to a JSON file and optionally truncates old entries.
type ArchiveAuditLogCommand struct {
	stmt *parser.ArchiveAuditLogStatement
}

// archiveExport is the top-level JSON structure written to the archive file.
type archiveExport struct {
	ArchivedAt string        `json:"archived_at"`
	EntryCount int           `json:"entry_count"`
	ChainHash  string        `json:"chain_hash"`
	KeepCount  int           `json:"keep_count"`
	Entries    []audit.Entry `json:"entries"`
}

func (c *ArchiveAuditLogCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.AuditTable == nil {
		return nil, fmt.Errorf("audit logging is not enabled")
	}

	entries, err := ctx.Session.AuditTable.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}

	if len(entries) == 0 {
		return &Result{
			Type:    "message",
			Message: "Audit log is empty, nothing to archive.",
		}, nil
	}

	// Determine output path
	outPath := c.stmt.Path
	if outPath == "" {
		outPath = ctx.Session.ArchivePath
	}
	if outPath == "" {
		return nil, fmt.Errorf("archive path not specified (use TO 'path' or set audit.archive_path)")
	}

	// Get chain hash at export time
	chainHash, err := ctx.Session.AuditTable.LastHash()
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

	// Ensure parent directory exists
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create archive directory: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0600); err != nil {
		return nil, fmt.Errorf("write archive file: %w", err)
	}

	// Optionally truncate old entries
	keepCount := c.stmt.KeepCount
	if keepCount < 0 {
		keepCount = 0
	}

	if keepCount > 0 || c.stmt.KeepCount == 0 && len(entries) > 0 {
		// keepCount 0 means truncate all after archive
		if keepCount < len(entries) {
			// Truncate table and re-insert only the kept entries
			if err := ctx.Session.AuditTable.TruncateKeepLast(keepCount); err != nil {
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

	return &Result{
		Type:    "message",
		Message: msg,
	}, nil
}
