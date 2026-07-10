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

func TestBufferedOpsVisibleToOwnTxButNotOthers(t *testing.T) {
	// Read-your-own-writes (Bug #1): own transaction sees buffered
	// insert via tx-overlay, but ANOTHER session does not until COMMIT (Bug #1
	// isolation). Sessions share one storage/txmanager.
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sessA := NewSession(store, nil, txm, nil)
	sessB := NewSession(store, nil, txm, nil)
	executeSQL(t, sessA, "CREATE DATABASE txdb;")
	executeSQL(t, sessA, "USE txdb;")
	executeSQL(t, sessA, "CREATE TABLE items (id INT, name TEXT, qty INT);")
	executeSQL(t, sessB, "USE txdb;")

	executeSQL(t, sessA, "BEGIN;")
	executeSQL(t, sessA, "INSERT INTO items VALUES (1, 'apple', 10);")

	// Own transaction sees the insert.
	resA := executeSQL(t, sessA, "SELECT * FROM items;")
	if len(resA.Rows) != 1 {
		t.Fatalf("own tx: expected 1 row (read-your-writes), got %d", len(resA.Rows))
	}

	// Other session (outside transaction) does NOT see the uncommitted insert.
	resB := executeSQL(t, sessB, "SELECT * FROM items;")
	if len(resB.Rows) != 0 {
		t.Fatalf("other session: expected 0 rows before commit, got %d", len(resB.Rows))
	}

	executeSQL(t, sessA, "COMMIT;")

	// Fresh session sees the committed row (no per-session cache influence).
	sessC := NewSession(store, nil, txm, nil)
	executeSQL(t, sessC, "USE txdb;")
	resC := executeSQL(t, sessC, "SELECT * FROM items;")
	if len(resC.Rows) != 1 {
		t.Fatalf("fresh session: expected 1 row after commit, got %d", len(resC.Rows))
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

// TestUndoUpdatePartialCommitPerRowRestore: при откате частично применённого
// коммита один UPDATE, затронувший несколько строк с РАЗНЫМИ старыми
// значениями, должен восстановить каждой строке ЕЁ значение, а не одно общее.
// Регресс на слияние per-row карт в mergedUpdates (порча данных).
func TestUndoUpdatePartialCommitPerRowRestore(t *testing.T) {
	session := setupTxSession(t)
	executeSQL(t, session, "CREATE TABLE accounts (id INT, balance INT);")
	executeSQL(t, session, "ALTER TABLE accounts ADD CONSTRAINT chk_bal CHECK (balance >= 0);")
	executeSQL(t, session, "INSERT INTO accounts VALUES (1, 10);")
	executeSQL(t, session, "INSERT INTO accounts VALUES (2, 20);")

	executeSQL(t, session, "BEGIN;")
	// Один UPDATE на две строки с разными старыми значениями (10 и 20).
	executeSQL(t, session, "UPDATE accounts SET balance = 500 WHERE id <= 2;")
	// Второй UPDATE падает на apply (CHECK balance>=0) → частичный коммит,
	// первый UPDATE откатывается, и обе строки должны вернуть СВОИ 10 и 20.
	executeSQL(t, session, "UPDATE accounts SET balance = -5 WHERE id = 2;")
	executeSQLExpectError(t, session, "COMMIT;")

	res := executeSQL(t, session, "SELECT balance FROM accounts ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "10" || res.Rows[1][0] != "20" {
		t.Fatalf("per-row restore corrupted: want [10 20], got [%s %s]",
			res.Rows[0][0], res.Rows[1][0])
	}
}

// TestTruncateRollback: после TRUNCATE + ROLLBACK данные должны быть восстановлены.
func TestTruncateRollback(t *testing.T) {
	session := setupTxSession(t)

	// Seed data
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")
	executeSQL(t, session, "INSERT INTO items VALUES (3, 'cherry', 30);")

	// Verify initial state
	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows before truncate, got %d", len(result.Rows))
	}

	// Truncate inside transaction, then rollback
	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "TRUNCATE TABLE items;")

	// Table should appear empty inside the tx
	result = executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows inside tx after truncate, got %d", len(result.Rows))
	}

	executeSQL(t, session, "ROLLBACK;")

	// After rollback, all 3 rows must be restored
	result = executeSQL(t, session, "SELECT * FROM items ORDER BY id;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows after truncate rollback, got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "1" || result.Rows[1][0] != "2" || result.Rows[2][0] != "3" {
		t.Fatalf("rows corrupted after truncate rollback: %v", result.Rows)
	}
}

// TestTruncateCommitRollback: truncate commits successfully, rollback is not triggered.
func TestTruncateCommit(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "TRUNCATE TABLE items;")
	executeSQL(t, session, "COMMIT;")

	result := executeSQL(t, session, "SELECT * FROM items;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows after truncate commit, got %d", len(result.Rows))
	}
}

// TestTruncatePartialCommitFailure: truncate followed by a failing op triggers undo,
// restoring the truncated rows.
func TestTruncatePartialCommitFailure(t *testing.T) {
	session := setupTxSession(t)
	executeSQL(t, session, "CREATE TABLE bigtable (id INT, val TEXT);")
	executeSQL(t, session, "ALTER TABLE bigtable ADD CONSTRAINT chk_val CHECK (val != '');")
	executeSQL(t, session, "INSERT INTO bigtable VALUES (1, 'a');")
	executeSQL(t, session, "INSERT INTO bigtable VALUES (2, 'b');")

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "TRUNCATE TABLE bigtable;")
	// This insert violates CHECK (empty val) — should fail at commit time.
	executeSQL(t, session, "INSERT INTO bigtable VALUES (3, '');")
	executeSQLExpectError(t, session, "COMMIT;")

	// Truncate should have been undone, original rows restored.
	result := executeSQL(t, session, "SELECT * FROM bigtable ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after truncate undo on partial commit failure, got %d: %v",
			len(result.Rows), result.Rows)
	}
}

// TestSessionCloseWithActiveTx: Close() on a session with an active transaction
// should call Rollback(), not silently nil out ActiveTx.
func TestSessionCloseWithActiveTx(t *testing.T) {
	session := setupTxSession(t)
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")

	// Start a transaction and add a row
	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	// Check that транзакция активна
	if !session.IsInTx() {
		t.Fatal("expected active transaction before close")
	}

	// Close() should roll back the transaction
	session.Close()

	// After close, transaction should not be active
	session.mu.RLock()
	tx := session.ActiveTx
	session.mu.RUnlock()
	if tx != nil {
		t.Fatal("ActiveTx should be nil after Close()")
	}

	// Check that данные откатались — новая сессия видит только ту строку,
	// что была закоммичена до BEGIN
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fresh := NewSession(store, nil, txm, nil)
	executeSQL(t, fresh, "CREATE DATABASE txdb;")
	executeSQL(t, fresh, "USE txdb;")
	executeSQL(t, fresh, "CREATE TABLE items (id INT, name TEXT, qty INT);")
	executeSQL(t, fresh, "INSERT INTO items VALUES (1, 'apple', 10);")

	result := executeSQL(t, fresh, "SELECT * FROM items;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row after session close rolled back tx, got %d: %v", len(result.Rows), result.Rows)
	}
}

// TestRollbackClearsTxState: RollbackCommand should correctly clean up
// the transaction and return the correct count of discarded operations.
func TestRollbackClearsTxState(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'apple', 10);")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'banana', 20);")

	result := executeSQL(t, session, "ROLLBACK;")

	// Rollback message should report 2 discarded operations
	if result.Message != "Transaction rolled back (2 operations discarded)." {
		t.Fatalf("unexpected rollback message: %s", result.Message)
	}

	// Transaction should be cleared
	if session.IsInTx() {
		t.Fatal("transaction should be cleared after rollback")
	}

	// Verify no data persisted
	res := executeSQL(t, session, "SELECT * FROM items;")
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d: %v", len(res.Rows), res.Rows)
	}
}

// TestRollbackEmptyTx: RollbackCommand on an empty transaction (no ops).
func TestRollbackEmptyTx(t *testing.T) {
	session := setupTxSession(t)

	executeSQL(t, session, "BEGIN;")
	result := executeSQL(t, session, "ROLLBACK;")

	if result.Message != "Transaction rolled back (0 operations discarded)." {
		t.Fatalf("unexpected rollback message: %s", result.Message)
	}
	if session.IsInTx() {
		t.Fatal("transaction should be cleared after rollback")
	}
}
