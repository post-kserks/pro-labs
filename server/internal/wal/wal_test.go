package wal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendAndRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	txID, err := w.Append(OpInsert, map[string]interface{}{
		"db":    "mydb",
		"table": "users",
	})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if txID == 0 {
		t.Fatalf("expected non-zero txID")
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].TxID != txID || entries[0].OpType != OpInsert {
		t.Fatalf("unexpected entry: %#v", entries[0])
	}
}

func TestRecoverIgnoresCorruptedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if _, err := w.Append(OpInsert, map[string]interface{}{"table": "t"}); err != nil {
		t.Fatalf("append 1 failed: %v", err)
	}
	if _, err := w.Append(OpDelete, map[string]interface{}{"table": "t"}); err != nil {
		t.Fatalf("append 2 failed: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(bytes) < 8 {
		t.Fatalf("unexpected wal length: %d", len(bytes))
	}
	bytes[len(bytes)-1] ^= 0xFF
	if err := os.WriteFile(path, bytes, 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	w2, err := Open(path)
	if err != nil {
		t.Fatalf("re-open failed: %v", err)
	}
	defer w2.Close()

	entries, err := w2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 valid entry after corruption, got %d", len(entries))
	}
	if entries[0].OpType != OpInsert {
		t.Fatalf("expected first entry to survive corruption, got op=%d", entries[0].OpType)
	}
}

func TestCheckpointTruncatesLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	if _, err := w.Append(OpInsert, map[string]interface{}{"v": 1}); err != nil {
		t.Fatalf("append 1 failed: %v", err)
	}
	if _, err := w.Append(OpInsert, map[string]interface{}{"v": 2}); err != nil {
		t.Fatalf("append 2 failed: %v", err)
	}
	if err := w.Checkpoint(); err != nil {
		t.Fatalf("checkpoint failed: %v", err)
	}
	if _, err := w.Append(OpInsert, map[string]interface{}{"v": 3}); err != nil {
		t.Fatalf("append 3 failed: %v", err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after checkpoint, got %d", len(entries))
	}
}
