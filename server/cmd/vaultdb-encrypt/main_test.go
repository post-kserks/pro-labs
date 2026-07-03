package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildEncrypt(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/vaultdb-encrypt"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func TestCmdNoArgs(t *testing.T) {
	bin := buildEncrypt(t)
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for no args")
	}
	if got := string(out); got == "" {
		t.Error("expected usage message on stderr/stdout")
	}
}

func TestCmdUnknown(t *testing.T) {
	bin := buildEncrypt(t)
	cmd := exec.Command(bin, "bogus")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown command")
	}
	if got := string(out); got == "" {
		t.Error("expected error message for unknown command")
	}
}

func TestCmdInitMissingArgs(t *testing.T) {
	bin := buildEncrypt(t)
	cmd := exec.Command(bin, "init")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for missing flags")
	}
	if got := string(out); got == "" {
		t.Error("expected error about required flags")
	}
}

func TestCmdInitSuccess(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	cmd := exec.Command(bin, "init", "--database", dbDir)
	cmd.Env = append(os.Environ(), "VAULTDB_ENCRYPTION_PASSPHRASE=test-passphrase-123")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected success message")
	}

	dekPath := filepath.Join(dbDir, ".dek.enc")
	if _, err := os.Stat(dekPath); os.IsNotExist(err) {
		t.Error("DEK file not created")
	}

	saltPath := filepath.Join(dbDir, ".salt")
	if _, err := os.Stat(saltPath); os.IsNotExist(err) {
		t.Error("salt file not created")
	}

	metaPath := filepath.Join(dbDir, ".encryption_meta.json")
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("metadata file not created")
	}
}

func TestCmdInitReadsFromEnvVar(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	cmd := exec.Command(bin, "init", "--database", dbDir)
	cmd.Env = append(os.Environ(), "VAULTDB_ENCRYPTION_PASSPHRASE=env-secret-pass")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init with env var failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected success message")
	}

	dekPath := filepath.Join(dbDir, ".dek.enc")
	if _, err := os.Stat(dekPath); os.IsNotExist(err) {
		t.Error("DEK file not created")
	}
}

func TestCmdStatusNotInitialized(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	cmd := exec.Command(bin, "status", "--database", dbDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected output")
	}
}

func TestCmdStatusInitialized(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	// Initialize first
	cmd := exec.Command(bin, "init", "--database", dbDir)
	cmd.Env = append(os.Environ(), "VAULTDB_ENCRYPTION_PASSPHRASE=test-passphrase-123")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	// Check status
	cmd = exec.Command(bin, "status", "--database", dbDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("status failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected status output")
	}
}

func TestCmdGenerateKey(t *testing.T) {
	bin := buildEncrypt(t)
	outPath := filepath.Join(t.TempDir(), "test-key.bin")

	cmd := exec.Command(bin, "generate-key", "--output", outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate-key failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected success message")
	}

	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Error("key file not created")
	}
}

func TestCmdMigrateMissingArgs(t *testing.T) {
	bin := buildEncrypt(t)
	cmd := exec.Command(bin, "migrate")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for missing flags")
	}
	if got := string(out); got == "" {
		t.Error("expected error about required flags")
	}
}

func TestCmdMigrateSuccess(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	cmd := exec.Command(bin, "migrate", "--database", dbDir)
	cmd.Env = append(os.Environ(), "VAULTDB_ENCRYPTION_PASSPHRASE=test-passphrase-123")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected success message")
	}

	dekPath := filepath.Join(dbDir, ".dek.enc")
	if _, err := os.Stat(dekPath); os.IsNotExist(err) {
		t.Error("DEK file not created")
	}

	saltPath := filepath.Join(dbDir, ".salt")
	if _, err := os.Stat(saltPath); os.IsNotExist(err) {
		t.Error("salt file not created")
	}
}

func TestCmdMigrateReuseExistingSalt(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	// Create a salt file first
	saltPath := filepath.Join(dbDir, ".salt")
	salt := []byte("0123456789abcdef")
	if err := os.WriteFile(saltPath, salt, 0600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "migrate", "--database", dbDir)
	cmd.Env = append(os.Environ(), "VAULTDB_ENCRYPTION_PASSPHRASE=test-passphrase-123")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}

	// Verify same salt was used (file unchanged)
	data, err := os.ReadFile(saltPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(salt) {
		t.Error("salt file was overwritten instead of reused")
	}
}

func TestCmdRotateKEKMissingArgs(t *testing.T) {
	bin := buildEncrypt(t)
	cmd := exec.Command(bin, "rotate-kek")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for missing flags")
	}
	if got := string(out); got == "" {
		t.Error("expected error about required flags")
	}
}

func TestCmdRotateKEKSuccess(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	// Initialize first
	cmd := exec.Command(bin, "init", "--database", dbDir)
	cmd.Env = append(os.Environ(), "VAULTDB_ENCRYPTION_PASSPHRASE=old-passphrase")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}

	// Read original DEK
	dekPath := filepath.Join(dbDir, ".dek.enc")
	origEncDEK, err := os.ReadFile(dekPath)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate KEK
	cmd = exec.Command(bin, "rotate-kek", "--database", dbDir)
	cmd.Env = append(os.Environ(),
		"VAULTDB_ENCRYPTION_PASSPHRASE=old-passphrase",
		"VAULTDB_ENCRYPTION_PASSPHRASE_NEW=new-passphrase",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rotate-kek failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected success message")
	}

	// Verify DEK file was updated
	newEncDEK, err := os.ReadFile(dekPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(origEncDEK) == string(newEncDEK) {
		t.Error("DEK file was not updated")
	}
}

func TestCmdRotateDEK(t *testing.T) {
	bin := buildEncrypt(t)
	dbDir := t.TempDir()

	cmd := exec.Command(bin, "rotate-dek", "--database", dbDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rotate-dek failed: %v\n%s", err, out)
	}

	if got := string(out); got == "" {
		t.Error("expected output message")
	}
}
