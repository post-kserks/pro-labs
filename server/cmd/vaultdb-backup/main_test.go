package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBackupMode(t *testing.T) {
	bin := buildBinary(t)
	srcDir := t.TempDir()
	outFile := filepath.Join(t.TempDir(), "backup.tar.gz")

	// Create required pagedb and wal dirs
	os.MkdirAll(filepath.Join(srcDir, "pagedb"), 0o755)
	os.MkdirAll(filepath.Join(srcDir, "wal"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "wal", "test.wal"), []byte("wal-data"), 0o644)

	cmd := exec.Command(bin, "-mode", "backup", "-data", srcDir, "-output", outFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(outFile); os.IsNotExist(err) {
		t.Error("backup file was not created")
	}
}

func TestRestoreMode(t *testing.T) {
	bin := buildBinary(t)

	// Create a source with data, back it up, then restore to a new dir
	srcDir := t.TempDir()
	backupFile := filepath.Join(t.TempDir(), "backup.tar.gz")
	restoreDir := t.TempDir()

	os.MkdirAll(filepath.Join(srcDir, "pagedb", "mydb", "users"), 0o755)
	os.MkdirAll(filepath.Join(srcDir, "wal"), 0o755)
	os.WriteFile(filepath.Join(srcDir, "pagedb", "mydb", "users", "heap_0001.page"), []byte("page-data"), 0o644)
	os.WriteFile(filepath.Join(srcDir, "wal", "vaultdb.wal"), []byte("wal-data"), 0o644)

	// Backup first
	cmd := exec.Command(bin, "-mode", "backup", "-data", srcDir, "-output", backupFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("backup step failed: %v\n%s", err, out)
	}

	// Restore
	cmd = exec.Command(bin, "-mode", "restore", "-data", restoreDir, "-output", backupFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restore failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(restoreDir, "wal", "vaultdb.wal"))
	if err != nil {
		t.Fatalf("restored wal not found: %v", err)
	}
	if string(data) != "wal-data" {
		t.Errorf("wal content = %q, want %q", string(data), "wal-data")
	}
}

func TestBackupMissingOutput(t *testing.T) {
	bin := buildBinary(t)
	srcDir := t.TempDir()

	cmd := exec.Command(bin, "-mode", "backup", "-data", srcDir)
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit for missing output in backup mode")
	}
}

func TestRestoreMissingOutput(t *testing.T) {
	bin := buildBinary(t)
	dataDir := t.TempDir()

	cmd := exec.Command(bin, "-mode", "restore", "-data", dataDir)
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit for missing output in restore mode")
	}
}

func TestUnknownMode(t *testing.T) {
	bin := buildBinary(t)

	cmd := exec.Command(bin, "-mode", "bogus")
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit for unknown mode")
	}
}

func TestRestoreMissingArchive(t *testing.T) {
	bin := buildBinary(t)
	dataDir := t.TempDir()

	cmd := exec.Command(bin, "-mode", "restore", "-data", dataDir, "-output", filepath.Join(t.TempDir(), "nope.tar.gz"))
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit when archive does not exist")
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "vaultdb-backup")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = t.TempDir()
	// Build from the cmd/vaultdb-backup directory
	cmd.Dir = filepath.Join("..", "..", "cmd", "vaultdb-backup")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}
