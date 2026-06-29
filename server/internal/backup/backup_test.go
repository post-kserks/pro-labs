package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackupRestore(t *testing.T) {
	// Create source data directory
	srcDir := t.TempDir()
	backupPath := filepath.Join(t.TempDir(), "backup.tar.gz")
	restoreDir := t.TempDir()

	// Create pagedb structure
	pagedbDir := filepath.Join(srcDir, "pagedb")
	if err := os.MkdirAll(filepath.Join(pagedbDir, "testdb", "users"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagedbDir, "_catalog.json"), []byte(`{"databases":{}}`), 0o644); err != nil {
		t.Fatalf("write catalog failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagedbDir, "testdb", "users", "_schema.json"), []byte(`{"name":"users","columns":[]}`), 0o644); err != nil {
		t.Fatalf("write schema failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pagedbDir, "testdb", "users", "heap_0001.page"), []byte("page data"), 0o644); err != nil {
		t.Fatalf("write heap page failed: %v", err)
	}

	// Create wal structure
	walDir := filepath.Join(srcDir, "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		t.Fatalf("mkdir wal failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(walDir, "vaultdb.wal"), []byte("wal data"), 0o644); err != nil {
		t.Fatalf("write wal failed: %v", err)
	}

	// Create backup
	if err := Backup(srcDir, backupPath); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup file exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatalf("backup file does not exist")
	}

	// Restore backup
	if err := Restore(backupPath, restoreDir); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify restored files
	verifyFile(t, filepath.Join(restoreDir, "pagedb", "_catalog.json"), `{"databases":{}}`)
	verifyFile(t, filepath.Join(restoreDir, "pagedb", "testdb", "users", "_schema.json"), `{"name":"users","columns":[]}`)
	verifyFile(t, filepath.Join(restoreDir, "pagedb", "testdb", "users", "heap_0001.page"), "page data")
	verifyFile(t, filepath.Join(restoreDir, "wal", "vaultdb.wal"), "wal data")
}

func verifyFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("read %s failed: %v", path, err)
		return
	}
	if string(data) != expected {
		t.Errorf("file %s: got %q, want %q", path, string(data), expected)
	}
}
