package executor

import (
	"testing"

	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func setupTxSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := NewSession(store, nil, txm, nil)
	executeSQL(t, session, "CREATE DATABASE txdb;")
	executeSQL(t, session, "USE txdb;")
	executeSQL(t, session, "CREATE TABLE items (id INT, name TEXT, qty INT);")
	return session
}

func TestUndoInsert(t *testing.T) {
	session := setupTxSession(t)

	// Seed some initial data outside of a transaction
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	// Start a transaction, insert more, then rollback
	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (3, 'cherry', 30);")
	executeSQL(t, session, "INSERT INTO items VALUES (4, 'date', 40);")
	executeSQL(t, session, "ROLLBACK;")

	// Verify only original rows remain
	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after undo insert, got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "1" || result.Rows[1][0] != "2" {
		t.Fatalf("unexpected rows after undo: %v", result.Rows)
	}
}

func TestUndoUpdate(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "UPDATE items SET qty = 99 WHERE id = 1;")
	executeSQL(t, session, "ROLLBACK;")

	result := executeSQL(t, session, "SELECT qty FROM items WHERE id = 1;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "10" {
		t.Fatalf("expected qty=10 after undo update, got %v", result.Rows)
	}
}

func TestUndoDelete(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "DELETE FROM items WHERE id = 1;")
	executeSQL(t, session, "ROLLBACK;")

	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after undo delete, got %d: %v", len(result.Rows), result.Rows)
	}
}

func TestUndoInsertPartialCommitFailure(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")
	executeSQL(t, session, "INSERT INTO items VALUES (3, 'cherry', 30);")

	// Rollback should undo both inserts
	executeSQL(t, session, "ROLLBACK;")

	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after rollback, got %d: %v", len(result.Rows), result.Rows)
	}
}

func TestCommitInsert(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")
	executeSQL(t, session, "COMMIT;")

	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after commit, got %d: %v", len(result.Rows), result.Rows)
	}
}

func TestBufferedOpsNotVisibleBeforeCommit(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")

	// Check that buffered inserts aren't visible outside tx
	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows before commit, got %d", len(result.Rows))
	}

	executeSQL(t, session, "COMMIT;")

	result = executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(result.Rows))
	}
}

func TestRollbackClearsPendingOps(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "ROLLBACK;")

	// After rollback, a new transaction should have no ops
	executeSQL(t, session, "BEGIN;")
	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows in new tx, got %d", len(result.Rows))
	}
	executeSQL(t, session, "COMMIT;")
}

func TestUndoTypeAssertionSafety(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := NewSession(store, nil, txm, nil)
	executeSQL(t, session, "CREATE DATABASE safetydb;")
	executeSQL(t, session, "USE safetydb;")
	executeSQL(t, session, "CREATE TABLE items (id INT, name TEXT, qty INT);")

	ctx := &ExecutionContext{
		Storage: store,
		Session: session,
	}

	t.Run("undoInsert wrong payload type", func(t *testing.T) {
		op := txmanager.PendingOp{
			Type:    "insert",
			DB:      "safetydb",
			Table:   "items",
			Payload: "not an InsertStatement",
		}
		err := undoInsert(ctx, op)
		if err == nil {
			t.Fatal("expected error for undoInsert with wrong payload type, got nil")
		}
	})

	t.Run("undoInsert nil payload", func(t *testing.T) {
		op := txmanager.PendingOp{
			Type:    "insert",
			DB:      "safetydb",
			Table:   "items",
			Payload: nil,
		}
		err := undoInsert(ctx, op)
		if err == nil {
			t.Fatal("expected error for undoInsert with nil payload, got nil")
		}
	})

	t.Run("undoUpdate wrong OldRow type", func(t *testing.T) {
		op := txmanager.PendingOp{
			Type:   "update",
			DB:     "safetydb",
			Table:  "items",
			OldRow: "not a []storage.Row",
		}
		err := undoUpdate(ctx, op)
		if err == nil {
			t.Fatal("expected error for undoUpdate with wrong OldRow type")
		}
	})

	t.Run("undoDelete wrong Row type", func(t *testing.T) {
		op := txmanager.PendingOp{
			Type:  "delete",
			DB:    "safetydb",
			Table: "items",
			Row:   "not a []storage.Row",
		}
		err := undoDelete(ctx, op)
		if err == nil {
			t.Fatal("expected error for undoDelete with wrong Row type")
		}
	})
}

func TestWALAbortOnRollback(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess := NewSession(store, nil, txm, nil)

	executeSQL(t, sess, "CREATE DATABASE waldb;")
	executeSQL(t, sess, "USE waldb;")
	executeSQL(t, sess, "CREATE TABLE t (id INT);")

	// Begin + insert + rollback should write abort to WAL
	executeSQL(t, sess, "BEGIN;")
	executeSQL(t, sess, "INSERT INTO t VALUES (1);")
	executeSQL(t, sess, "ROLLBACK;")

	// Verify table is empty
	result := executeSQL(t, sess, "SELECT * FROM t;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback with WAL, got %d", len(result.Rows))
	}
}
