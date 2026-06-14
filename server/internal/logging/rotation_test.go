package logging

import (
	"os"
	"path/filepath"
	"strings"
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
