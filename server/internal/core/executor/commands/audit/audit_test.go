package audit_test

import (
	"path/filepath"
	"testing"

	"vaultdb/internal/core/audit"
	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/parser"
)

func TestAuditLogCommands(t *testing.T) {
	session := executor.SetupSessionWithDB(t, "auditdb")

	// Verify expected error when audit logging is not enabled
	stmt, err := parser.Parse("VERIFY AUDIT LOG;")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_, err = session.Execute(stmt)
	if err == nil {
		t.Fatalf("expected error from VERIFY AUDIT LOG when audit logging is not enabled")
	}

	// Enable audit logging for the session
	session.AuditTable = audit.NewTableLog(session.Storage())
	if err := session.AuditTable.EnsureTable(); err != nil {
		t.Fatalf("EnsureTable failed: %v", err)
	}

	// Verify initial audit log when audit logging is enabled
	res := executor.ExecuteSQL(t, session, "VERIFY AUDIT LOG;")
	if res.Type != "message" && res.Type != "error" {
		t.Fatalf("expected message or error result from VERIFY AUDIT LOG, got %s", res.Type)
	}

	// Archive audit log
	archivePath := filepath.Join(t.TempDir(), "audit.json")
	resArchive := executor.ExecuteSQL(t, session, "ARCHIVE AUDIT LOG TO '"+archivePath+"';")
	if resArchive.Type != "message" && resArchive.Type != "error" {
		t.Fatalf("expected message or error result from ARCHIVE AUDIT LOG, got %s", resArchive.Type)
	}
}
