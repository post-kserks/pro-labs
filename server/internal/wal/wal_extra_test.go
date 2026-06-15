package wal

import (
	"path/filepath"
	"testing"
)

func TestWALCheckpointEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	if err := w.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint on empty WAL failed: %v", err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after checkpoint on empty WAL, got %d", len(entries))
	}
}

func TestWALAnalyzeTransactions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	tx1, _ := w.AppendWithTx(1, OpInsert, map[string]interface{}{"table": "t"})
	w.AppendWithTx(tx1, OpCommit, nil)

	w.AppendWithTx(2, OpInsert, map[string]interface{}{"table": "t"})

	w.AppendWithTx(3, OpInsert, map[string]interface{}{"table": "t"})
	w.AppendWithTx(3, OpAbort, nil)

	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatalf("AnalyzeTransactions failed: %v", err)
	}

	if !committed[1] {
		t.Fatal("tx 1 should be committed")
	}
	if inProgress[1] {
		t.Fatal("tx 1 should not be in progress")
	}

	if !inProgress[2] {
		t.Fatal("tx 2 should be in progress")
	}
	if committed[2] {
		t.Fatal("tx 2 should not be committed")
	}

	if inProgress[3] {
		t.Fatal("tx 3 should not be in progress (aborted)")
	}
	if committed[3] {
		t.Fatal("tx 3 should not be committed (aborted)")
	}
}

func TestWALReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	w.Append(OpInsert, map[string]interface{}{"v": 1})
	w.Append(OpUpdate, map[string]interface{}{"v": 2})
	w.Append(OpDelete, map[string]interface{}{"v": 3})

	var count int
	err = w.Replay(func(entry Entry) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if count != 3 {
		t.Fatalf("Replay called callback %d times, want 3", count)
	}
}

func TestWALAppendWithTx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	txID, err := w.AppendWithTx(42, OpInsert, map[string]interface{}{"table": "users"})
	if err != nil {
		t.Fatalf("AppendWithTx failed: %v", err)
	}
	if txID != 42 {
		t.Fatalf("expected txID 42, got %d", txID)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].TxID != 42 {
		t.Fatalf("expected entry TxID 42, got %d", entries[0].TxID)
	}
}

func TestWALMultipleOps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer w.Close()

	w.Append(OpCreateDatabase, map[string]interface{}{"db": "testdb"})
	w.Append(OpCreateTable, map[string]interface{}{"db": "testdb", "table": "users"})
	w.Append(OpInsert, map[string]interface{}{"db": "testdb", "table": "users"})

	entries, err := w.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	expectedOps := []byte{OpCreateDatabase, OpCreateTable, OpInsert}
	for i, entry := range entries {
		if entry.OpType != expectedOps[i] {
			t.Errorf("entry %d op = %d, want %d", i, entry.OpType, expectedOps[i])
		}
	}
}
