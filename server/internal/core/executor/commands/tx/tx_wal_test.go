package tx_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/wal"
)

func setupTxSessionWithWAL(t *testing.T) (*executor.Session, *wal.WAL, string) {
	t.Helper()
	session := setupTxSession(t)
	path := filepath.Join(t.TempDir(), "tx.wal")
	w, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	session.SetWAL(w)
	return session, w, path
}

func beginTx(t *testing.T, session *executor.Session) uint64 {
	t.Helper()
	res := executor.ExecuteSQL(t, session, "BEGIN;")
	var id uint64
	if _, err := fmt.Sscanf(res.Message, "Transaction %d started.", &id); err != nil {
		t.Fatalf("cannot parse tx id from %q: %v", res.Message, err)
	}
	return id
}

func walEntries(t *testing.T, w *wal.WAL, path string) []wal.Entry {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var out []wal.Entry
	if err := r.Replay(func(e wal.Entry) error {
		out = append(out, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// ROLLBACK must log the abort under the transaction's own ID so recovery can
// match it, not under a freshly allocated txID.
func TestRollbackAbortRecordUsesTxID(t *testing.T) {
	session, w, path := setupTxSessionWithWAL(t)

	txID := beginTx(t, session)
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "ROLLBACK;")

	var aborts []uint64
	for _, e := range walEntries(t, w, path) {
		if e.OpType == wal.OpAbort {
			aborts = append(aborts, e.TxID)
		}
	}
	if len(aborts) != 1 || aborts[0] != txID {
		t.Fatalf("expected one abort record for tx %d, got %v", txID, aborts)
	}
}

// With synchronous_commit = on (default), COMMIT must not return before the
// commit record is fsynced.
func TestCommitIsDurableWhenSynchronousCommitOn(t *testing.T) {
	session, w, path := setupTxSessionWithWAL(t)

	txID := beginTx(t, session)
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "COMMIT;")

	if n := w.UnsyncedRecords(); n != 0 {
		t.Fatalf("COMMIT returned with %d unsynced WAL records", n)
	}

	found := false
	for _, e := range walEntries(t, w, path) {
		if e.OpType == wal.OpCommit && e.TxID == txID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no commit record for tx %d", txID)
	}
}

// synchronous_commit = off must not wait for an fsync.
func TestCommitSynchronousCommitOffDoesNotFsync(t *testing.T) {
	session, w, _ := setupTxSessionWithWAL(t)
	session.SetVariable("synchronous_commit", "off")

	beginTx(t, session)
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "COMMIT;")

	if n := w.UnsyncedRecords(); n == 0 {
		t.Fatal("expected asynchronous commit to leave the commit record unsynced")
	}
}
