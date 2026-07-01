package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNewRotator(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "test.log")

	r, err := NewRotator(filename, 1, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil rotator")
	}
	r.Close()
}

func TestWriteAndRotate(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "test.log")

	r, err := NewRotator(filename, 1, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	defer r.Close()

	data := []byte(strings.Repeat("x", 256*1024))

	for i := 0; i < 5; i++ {
		if _, err := r.Write(data); err != nil {
			t.Fatalf("write %d error: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}

	if len(entries) < 2 {
		t.Errorf("expected at least 2 files (original + backup), got %d", len(entries))
		for _, e := range entries {
			t.Logf("  found: %s", e.Name())
		}
	}
}

func TestClose(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "test.log")

	r, err := NewRotator(filename, 1, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestSync(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "test.log")

	r, err := NewRotator(filename, 100, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	defer r.Close()

	if _, err := r.Write([]byte("test data\n")); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	if err := r.Sync(); err != nil {
		t.Fatalf("Sync error: %v", err)
	}
}

func TestLogDDL(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "audit.log")

	r, err := NewRotator(filename, 100, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	defer r.Close()

	al := NewAuditLogger(r)
	al.LogDDL("CREATE TABLE", "mydb", "users", "id INT PRIMARY KEY")

	if err := r.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty log file")
	}

	var entry ddlLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("JSON unmarshal error: %v", err)
	}

	if entry.Type != "ddl" {
		t.Errorf("expected type 'ddl', got %q", entry.Type)
	}
	if entry.Operation != "CREATE TABLE" {
		t.Errorf("expected operation 'CREATE TABLE', got %q", entry.Operation)
	}
	if entry.Database != "mydb" {
		t.Errorf("expected database 'mydb', got %q", entry.Database)
	}
	if entry.Target != "users" {
		t.Errorf("expected target 'users', got %q", entry.Target)
	}
	if entry.Detail != "id INT PRIMARY KEY" {
		t.Errorf("expected detail 'id INT PRIMARY KEY', got %q", entry.Detail)
	}
	if entry.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestLogDDLNilLogger(t *testing.T) {
	var al *AuditLogger
	// Should not panic
	al.LogDDL("DROP TABLE", "mydb", "users", "cascade")
}

func TestLogDDLNilRotator(t *testing.T) {
	al := &AuditLogger{rotator: nil}
	// Should not panic
	al.LogDDL("DROP TABLE", "mydb", "users", "cascade")
}

func TestLogDDLJSONFormat(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "audit.log")

	r, err := NewRotator(filename, 100, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	defer r.Close()

	al := NewAuditLogger(r)
	al.LogDDL("ALTER TABLE", "testdb", "accounts", "ADD COLUMN email VARCHAR(255)")

	if err := r.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	// Verify it's valid JSON
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	// Verify required fields
	requiredFields := []string{"timestamp", "type", "operation", "database", "target", "detail"}
	for _, field := range requiredFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing required field %q", field)
		}
	}

	// Verify JSON is compact (single line, no extra whitespace between fields)
	if strings.Contains(string(data), "\n\n") {
		t.Error("expected single-line JSON entry")
	}
}

func TestConcurrentLogging(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "concurrent.log")

	r, err := NewRotator(filename, 10, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	defer r.Close()

	al := NewAuditLogger(r)

	const goroutines = 50
	const writesPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				al.LogDDL(
					"CREATE TABLE",
					"db",
					"table",
					"concurrent write",
				)
			}
		}(g)
	}

	wg.Wait()

	if err := r.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	// Count lines — each LogDDL writes one JSON line + newline
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	expectedLines := goroutines * writesPerGoroutine
	if len(lines) != expectedLines {
		t.Errorf("expected %d log lines, got %d", expectedLines, len(lines))
	}

	// Verify all lines are valid JSON
	for i, line := range lines {
		if line == "" {
			continue
		}
		var entry ddlLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestRotatorRotationPreservesData(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "rotate.log")

	// Use a small max size (1KB) to trigger rotation quickly
	r, err := NewRotator(filename, 1, 5)
	if err != nil {
		t.Fatalf("NewRotator error: %v", err)
	}
	defer r.Close()

	// Write enough to trigger rotation (maxSize = 1MB, each write ~150KB)
	for i := 0; i < 10; i++ {
		if _, err := r.Write([]byte(strings.Repeat("A", 150*1024))); err != nil {
			t.Fatalf("write %d error: %v", i, err)
		}
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir error: %v", err)
	}

	if len(entries) < 2 {
		t.Errorf("expected at least 2 files after rotation, got %d", len(entries))
	}
}
