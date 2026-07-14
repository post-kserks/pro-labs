package tx_test

import (
	"testing"

	"vaultdb/internal/core/executor"
)

func setupTxSession(t *testing.T) *executor.Session {
	t.Helper()
	session := executor.SetupSessionWithDB(t, "txdb")
	executor.ExecuteSQL(t, session, "CREATE TABLE items (id INT, name TEXT, qty INT);")
	return session
}

func TestUndoInsert(t *testing.T) {
	session := setupTxSession(t)

	// Seed some initial data outside of a transaction
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	// Start a transaction, insert more, then rollback
	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (3, 'cherry', 30);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (4, 'date', 40);")
	executor.ExecuteSQL(t, session, "ROLLBACK;")

	// Verify only original rows remain
	result := executor.ExecuteSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after undo insert, got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "1" || result.Rows[1][0] != "2" {
		t.Fatalf("unexpected rows after undo: %v", result.Rows)
	}
}

func TestUndoUpdate(t *testing.T) {
	session := setupTxSession(t)

	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "UPDATE items SET qty = 99 WHERE id = 1;")
	executor.ExecuteSQL(t, session, "ROLLBACK;")

	result := executor.ExecuteSQL(t, session, "SELECT qty FROM items WHERE id = 1;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "10" {
		t.Fatalf("expected qty=10 after undo update, got %v", result.Rows)
	}
}

func TestUndoDelete(t *testing.T) {
	session := setupTxSession(t)

	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "DELETE FROM items WHERE id = 1;")
	executor.ExecuteSQL(t, session, "ROLLBACK;")

	result := executor.ExecuteSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after undo delete, got %d: %v", len(result.Rows), result.Rows)
	}
}

func TestUndoInsertPartialCommitFailure(t *testing.T) {
	session := setupTxSession(t)

	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")

	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (3, 'cherry', 30);")

	// Rollback should undo both inserts
	executor.ExecuteSQL(t, session, "ROLLBACK;")

	result := executor.ExecuteSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after rollback, got %d: %v", len(result.Rows), result.Rows)
	}
}

func TestCommitInsert(t *testing.T) {
	session := setupTxSession(t)

	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")
	executor.ExecuteSQL(t, session, "COMMIT;")

	result := executor.ExecuteSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after commit, got %d: %v", len(result.Rows), result.Rows)
	}
}

func TestCommitUpdate(t *testing.T) {
	session := setupTxSession(t)

	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")

	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "UPDATE items SET qty = 99 WHERE id = 1;")
	executor.ExecuteSQL(t, session, "COMMIT;")

	result := executor.ExecuteSQL(t, session, "SELECT qty FROM items WHERE id = 1;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "99" {
		t.Fatalf("expected qty=99 after commit, got %v", result.Rows)
	}
}

func TestCommitDelete(t *testing.T) {
	session := setupTxSession(t)

	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	executor.ExecuteSQL(t, session, "BEGIN;")
	executor.ExecuteSQL(t, session, "DELETE FROM items WHERE id = 1;")
	executor.ExecuteSQL(t, session, "COMMIT;")

	result := executor.ExecuteSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "2" {
		t.Fatalf("expected row with id=2, got %v", result.Rows)
	}
}
