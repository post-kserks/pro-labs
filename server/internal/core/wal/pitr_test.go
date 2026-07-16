package wal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPITRRestore(t *testing.T) {
	walDir := t.TempDir()
	archiveDir := filepath.Join(walDir, "archive")
	os.MkdirAll(archiveDir, 0755)

	walPath := filepath.Join(walDir, "wal.log")
	w, err := Open(walPath)
	if err != nil {
		t.Fatalf("failed to open wal: %v", err)
	}

	// Tx 1: T=100
	w.AppendWithTx(1, OpPageInsert, WALPageInsertPayload{DB: "db1", Table: "t1"})

	ts1 := time.Now().Add(-1 * time.Hour)
	w.AppendWithTx(1, OpCommit, CommitPayload{Timestamp: ts1.UnixNano()})

	// Rotate implicitly by renaming
	w.Close()
	os.Rename(walPath, filepath.Join(walDir, "wal-1.log"))

	// Tx 2: T=200
	w, _ = Open(walPath)
	w.AppendWithTx(2, OpPageInsert, WALPageInsertPayload{DB: "db1", Table: "t1"})

	ts2 := time.Now().Add(-30 * time.Minute)
	w.AppendWithTx(2, OpCommit, CommitPayload{Timestamp: ts2.UnixNano()})

	// Tx 3: T=300
	w.AppendWithTx(3, OpPageInsert, WALPageInsertPayload{DB: "db1", Table: "t1"})

	ts3 := time.Now()
	w.AppendWithTx(3, OpCommit, CommitPayload{Timestamp: ts3.UnixNano()})
	w.Close()

	// Archive the segment
	archiver := NewArchiverWorker(walDir, archiveDir, 100*time.Millisecond)
	archiver.archiveSegments() // manual invocation

	// Now try to restore as of ts2
	wNew, _ := Open(walPath)
	target := ts2
	wNew.PITRTarget = &target
	wNew.PITRArchiveDir = archiveDir

	entries, err := wNew.Recover()
	if err != nil {
		t.Fatalf("failed to recover: %v", err)
	}

	// We expect Tx 1 to be fully replayed, and Tx 2 to be the stopping point since ts2 >= target.
	// Actually, RestoreAsOf stops exactly AT the first transaction whose timestamp is >= targetTimestamp.
	// So Tx 2 timestamp is >= ts2 (it's == ts2).
	// Therefore, it returns before adding Tx 2's commit. BUT wait, does it include Tx 2's ops?
	// It stops when it sees OpCommit with ts >= target. So it might return ops for Tx2 but not the commit?
	// Wait, we probably don't want partial transactions.
	// In VaultDB recovery, if OpCommit is missing, the transaction is rolled back.

	hasTx1Commit := false
	hasTx2Commit := false
	hasTx3Commit := false

	for _, e := range entries {
		if e.OpType == OpCommit {
			if e.TxID == 1 {
				hasTx1Commit = true
			}
			if e.TxID == 2 {
				hasTx2Commit = true
			}
			if e.TxID == 3 {
				hasTx3Commit = true
			}
		}
	}

	if !hasTx1Commit {
		t.Errorf("expected Tx1 to be committed")
	}
	if hasTx2Commit {
		t.Errorf("did not expect Tx2 to be committed since it hit the target timestamp")
	}
	if hasTx3Commit {
		t.Errorf("did not expect Tx3 to be committed")
	}
}
