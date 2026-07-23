package wal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogicalDecoder(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "logical_decoder_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	walPath := filepath.Join(tmpDir, "00000000000000000001.wal")
	w, err := Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	// Tx 1: Insert row
	_, err = w.AppendWithTx(1, OpPageInsert, WALPageInsertPayload{
		DB:        "testdb",
		Table:     "users",
		TupleData: []byte("row1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tx 2: Insert row, but will be aborted
	_, err = w.AppendWithTx(2, OpPageInsert, WALPageInsertPayload{
		DB:        "testdb",
		Table:     "users",
		TupleData: []byte("row2"),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tx 1: Commit
	_, err = w.AppendWithTx(1, OpCommit, CommitPayload{
		Timestamp: time.Now().UnixNano(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tx 2: Abort
	_, err = w.AppendWithTx(2, OpAbort, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Tx 3: Delete row
	_, err = w.AppendWithTx(3, OpPageDelete, WALPageDeletePayload{
		DB:    "testdb",
		Table: "users",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tx 3: Commit
	_, err = w.AppendWithTx(3, OpCommit, CommitPayload{
		Timestamp: time.Now().UnixNano(),
	})
	if err != nil {
		t.Fatal(err)
	}

	w.Close()

	// Decode the file
	decoder := NewLogicalDecoder()

	var events []LogicalEvent
	err = decoder.DecodeFile(walPath, func(ev LogicalEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	if events[0].TxID != 1 || events[0].Type != EventInsert || string(events[0].TupleData) != "row1" {
		t.Errorf("unexpected event 0: %+v", events[0])
	}
	if events[1].TxID != 1 || events[1].Type != EventCommit {
		t.Errorf("unexpected event 1: %+v", events[1])
	}
	if events[2].TxID != 3 || events[2].Type != EventDelete {
		t.Errorf("unexpected event 2: %+v", events[2])
	}
	if events[3].TxID != 3 || events[3].Type != EventCommit {
		t.Errorf("unexpected event 3: %+v", events[3])
	}
}
