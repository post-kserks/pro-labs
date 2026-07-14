package executor

// Transaction correctness tests: read-your-own-writes (Bug #1), OCC conflict
// on autocommit write (Bug #2), NOW() stability (Bug #3), spill at COMMIT
// (Bug #4), and savepoints.

import (
	"strconv"
	"strings"
	"testing"

	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

// newTxStore creates a shared storage+txmanager and registers the items table.
// Returns a session factory sharing the same engine.
func newTxStore(t *testing.T) (func() *Session, *txmanager.Manager, storage.StorageEngine) {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	bootstrap := NewSession(store, nil, txm, nil)
	executeSQL(t, bootstrap, "CREATE DATABASE txdb;")
	executeSQL(t, bootstrap, "USE txdb;")
	executeSQL(t, bootstrap, "CREATE TABLE items (id INT, name TEXT, qty INT);")

	newSession := func() *Session {
		s := NewSession(store, nil, txm, nil)
		executeSQL(t, s, "USE txdb;")
		return s
	}
	return newSession, txm, store
}

// TestReadYourOwnWrites — Bug #1: within a transaction, INSERT/UPDATE/DELETE are visible
// to the same transaction, and ROLLBACK undoes everything.
func TestReadYourOwnWrites(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "INSERT INTO items VALUES (1, 'apple', 10);")

	executeSQL(t, s, "BEGIN;")

	// INSERT visible inside tx.
	executeSQL(t, s, "INSERT INTO items VALUES (2, 'banana', 20);")
	res := executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("after INSERT: expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}

	// UPDATE visible inside tx.
	executeSQL(t, s, "UPDATE items SET qty = 99 WHERE id = 2;")
	res = executeSQL(t, s, "SELECT qty FROM items WHERE id = 2;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "99" {
		t.Fatalf("after UPDATE: expected qty=99, got %v", res.Rows)
	}

	// DELETE visible inside tx.
	executeSQL(t, s, "DELETE FROM items WHERE id = 1;")
	res = executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "2" {
		t.Fatalf("after DELETE: expected only id=2, got %v", res.Rows)
	}

	// ROLLBACK undoes everything.
	executeSQL(t, s, "ROLLBACK;")
	res = executeSQL(t, s, "SELECT id, qty FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" || res.Rows[0][1] != "10" {
		t.Fatalf("after ROLLBACK: expected only (1,10), got %v", res.Rows)
	}
}

// TestCommitConflictWithAutocommit — Bug #2: while transaction A holds a snapshot
// of the table, a competing autocommit write bumps the table version under the same
// commit lock, and COMMIT of transaction A fails with a conflict.
func TestCommitConflictWithAutocommit(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	sessA := newSession()
	sessB := newSession()

	executeSQL(t, sessA, "INSERT INTO items VALUES (1, 'apple', 10);")

	// A opens tx and buffers UPDATE — version snapshot is captured.
	executeSQL(t, sessA, "BEGIN;")
	executeSQL(t, sessA, "UPDATE items SET qty = 1 WHERE id = 1;")

	// B (autocommit) modifies the same table and commits — version increases.
	executeSQL(t, sessB, "UPDATE items SET qty = 2 WHERE id = 1;")

	// COMMIT A must fail with a conflict.
	executeSQLExpectError(t, sessA, "COMMIT;")
}

// TestCommitConflictReadOnlyAccess — Bug #2a: even a read-only transaction
// that captured a snapshot during read conflicts with a concurrent write.
func TestCommitConflictReadOnlyAccess(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	sessA := newSession()
	sessB := newSession()

	executeSQL(t, sessA, "INSERT INTO items VALUES (1, 'apple', 10);")

	executeSQL(t, sessA, "BEGIN;")
	// Read-only — but table version snapshot is captured (RecordAccess).
	executeSQL(t, sessA, "SELECT * FROM items;")

	// Concurrent autocommit write.
	executeSQL(t, sessB, "INSERT INTO items VALUES (2, 'banana', 20);")

	executeSQLExpectError(t, sessA, "COMMIT;")
}

// TestNowStableInTx — Bug #3: NOW() value within a transaction (visible through
// overlay) matches what is persisted after COMMIT.
func TestNowStableInTx(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, "INSERT INTO items (id, name, qty) VALUES (1, NOW(), 0);")

	inTx := executeSQL(t, s, "SELECT name FROM items WHERE id = 1;")
	if len(inTx.Rows) != 1 {
		t.Fatalf("expected 1 row in tx, got %d", len(inTx.Rows))
	}
	valInTx := inTx.Rows[0][0]
	if valInTx == "" {
		t.Fatalf("NOW() produced empty value")
	}

	executeSQL(t, s, "COMMIT;")

	after := executeSQL(t, s, "SELECT name FROM items WHERE id = 1;")
	if len(after.Rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(after.Rows))
	}
	if after.Rows[0][0] != valInTx {
		t.Fatalf("NOW() diverged: in-tx=%q persisted=%q", valInTx, after.Rows[0][0])
	}
}

// TestSpilledCommitApplied — Bug #4: when forcing spill (low
// SpillThreshold), an operation with a large payload is not lost and is applied at COMMIT.
func TestSpilledCommitApplied(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	txm.SpillThreshold = 1 // spill immediately
	txm.SpillDir = t.TempDir()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	s := NewSession(store, nil, txm, nil)
	executeSQL(t, s, "CREATE DATABASE sdb;")
	executeSQL(t, s, "USE sdb;")
	executeSQL(t, s, "CREATE TABLE items (id INT, name TEXT, qty INT);")

	// One INSERT with many rows: serialized operation (one spill file line)
	// clearly exceeds 64KB — the old bufio.Scanner with default limit
	// would have silently lost it (Bug #4).
	const nRows = 5000
	var b strings.Builder
	b.WriteString("INSERT INTO items VALUES ")
	for i := 0; i < nRows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(")
		b.WriteString(itoa(i))
		b.WriteString(", 'name', ")
		b.WriteString(itoa(i))
		b.WriteString(")")
	}
	b.WriteString(";")

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, b.String())

	// read-your-writes over spill must also work.
	inTx := executeSQL(t, s, "SELECT COUNT(*) FROM items;")
	if len(inTx.Rows) != 1 || inTx.Rows[0][0] != itoa(nRows) {
		t.Fatalf("expected %d rows in tx over spill, got %v", nRows, inTx.Rows)
	}

	executeSQL(t, s, "COMMIT;")

	res := executeSQL(t, s, "SELECT COUNT(*) FROM items;")
	if len(res.Rows) != 1 || res.Rows[0][0] != itoa(nRows) {
		t.Fatalf("expected %d rows after spilled commit (no silent loss), got %v", nRows, res.Rows)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// TestSavepoints — savepoints over the buffered model.
func TestSavepoints(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, "INSERT INTO items VALUES (1, 'a', 1);")
	executeSQL(t, s, "SAVEPOINT s1;")
	executeSQL(t, s, "INSERT INTO items VALUES (2, 'b', 2);")

	// Before rollback both visible.
	res := executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows before rollback to savepoint, got %d", len(res.Rows))
	}

	executeSQL(t, s, "ROLLBACK TO SAVEPOINT s1;")

	// After rollback to s1, only 'a' is visible.
	res = executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("expected only id=1 after rollback to savepoint, got %v", res.Rows)
	}

	executeSQL(t, s, "COMMIT;")

	res = executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("expected only id=1 persisted, got %v", res.Rows)
	}
}

// TestSavepointReleaseThenRollbackErrors — RELEASE removes the marker, after which
// ROLLBACK TO it must return an error.
func TestSavepointReleaseThenRollbackErrors(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, "INSERT INTO items VALUES (1, 'a', 1);")
	executeSQL(t, s, "SAVEPOINT s1;")
	executeSQL(t, s, "RELEASE SAVEPOINT s1;")
	executeSQLExpectError(t, s, "ROLLBACK TO SAVEPOINT s1;")
	executeSQL(t, s, "COMMIT;")
}

// TestSavepointOutsideTxErrors — savepoint outside transaction is forbidden.
func TestSavepointOutsideTxErrors(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()
	executeSQLExpectError(t, s, "SAVEPOINT s1;")
	executeSQLExpectError(t, s, "ROLLBACK TO SAVEPOINT s1;")
	executeSQLExpectError(t, s, "RELEASE SAVEPOINT nope;")
}
