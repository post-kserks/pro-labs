package executor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vaultdb/internal/audit"
	"vaultdb/internal/parser"
)

func TestArchiveAuditLogExportsData(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "audit_archive.json")

	// Create a mock audit table with entries
	store := NewMockStorage()
	log := audit.NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := log.Append(audit.Entry{
			Actor:  "admin",
			Action: "INSERT",
			Target: "users",
		}); err != nil {
			t.Fatal(err)
		}
	}

	sess := &Session{
		AuditTable:  log,
		ArchivePath: outPath,
	}

	cmd := &ArchiveAuditLogCommand{
		stmt: &parser.ArchiveAuditLogStatement{
			Path: outPath,
		},
	}

	ctx := &ExecutionContext{
		Session: sess,
	}

	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}

	// Verify file was created
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read archive file: %v", err)
	}

	var export archiveExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatalf("failed to unmarshal archive: %v", err)
	}

	if export.EntryCount != 5 {
		t.Errorf("expected 5 entries, got %d", export.EntryCount)
	}
	if export.ChainHash == "" {
		t.Error("expected non-empty chain hash")
	}
	if len(export.Entries) != 5 {
		t.Errorf("expected 5 entries in export, got %d", len(export.Entries))
	}
}

func TestArchiveAuditLogEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "audit_archive.json")

	store := NewMockStorage()
	log := audit.NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	sess := &Session{
		AuditTable:  log,
		ArchivePath: outPath,
	}

	cmd := &ArchiveAuditLogCommand{
		stmt: &parser.ArchiveAuditLogStatement{
			Path: outPath,
		},
	}

	ctx := &ExecutionContext{
		Session: sess,
	}

	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Message != "Audit log is empty, nothing to archive." {
		t.Errorf("unexpected message: %s", result.Message)
	}

	// Verify file was NOT created
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Error("archive file should not exist for empty log")
	}
}

func TestArchiveAuditLogNoPath(t *testing.T) {
	store := NewMockStorage()
	log := audit.NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(audit.Entry{Actor: "admin", Action: "INSERT", Target: "t"}); err != nil {
		t.Fatal(err)
	}

	sess := &Session{
		AuditTable: log,
	}

	cmd := &ArchiveAuditLogCommand{
		stmt: &parser.ArchiveAuditLogStatement{},
	}

	ctx := &ExecutionContext{
		Session: sess,
	}

	_, err := cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected error when no path specified")
	}
}

func TestArchiveAuditLogKeepLast(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "audit_archive.json")

	store := NewMockStorage()
	log := audit.NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := log.Append(audit.Entry{
			Actor:  "admin",
			Action: "INSERT",
			Target: "t",
		}); err != nil {
			t.Fatal(err)
		}
	}

	sess := &Session{
		AuditTable:  log,
		ArchivePath: outPath,
	}

	cmd := &ArchiveAuditLogCommand{
		stmt: &parser.ArchiveAuditLogStatement{
			Path:      outPath,
			KeepCount: 3,
		},
	}

	ctx := &ExecutionContext{
		Session: sess,
	}

	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if result.Message != "Archived 10 audit entries to "+outPath+". Kept last 3 entries." {
		t.Errorf("unexpected message: %s", result.Message)
	}

	// Verify remaining entries
	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 remaining entries, got %d", len(entries))
	}

	// Verify archive file
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var export archiveExport
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatal(err)
	}
	if export.EntryCount != 10 {
		t.Errorf("expected 10 archived entries, got %d", export.EntryCount)
	}
}

func TestArchiveAuditLogNoAuditLog(t *testing.T) {
	sess := &Session{}

	cmd := &ArchiveAuditLogCommand{
		stmt: &parser.ArchiveAuditLogStatement{
			Path: "/tmp/test.json",
		},
	}

	ctx := &ExecutionContext{
		Session: sess,
	}

	_, err := cmd.Execute(ctx)
	if err == nil {
		t.Fatal("expected error when audit logging is not enabled")
	}
}
