package executor

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

// setupTestExecutor creates a clean Executor, TxManager, and Session for SQL execution tests.
func setupTestExecutor(t *testing.T) (*Executor, *txmanager.Manager, *Session) {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatalf("failed to create storage engine: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	exec := New(store, metrics.New(), txm, NewBroadcaster())
	sess := NewSession(store, metrics.New(), txm, NewBroadcaster())
	sess.SetUser("testadmin")
	sess.SetCurrentDatabase("system")
	return exec, txm, sess
}

// TestGetPGStatActivityRows_Direct verifies GetPGStatActivityRows directly after registering
// sessions in GlobalRegistry.
func TestGetPGStatActivityRows_Direct(t *testing.T) {
	sessID1 := uint64(90001)
	sessID2 := uint64(90002)

	t.Cleanup(func() {
		GlobalRegistry.UnregisterSession(sessID1)
		GlobalRegistry.UnregisterSession(sessID2)
	})

	startTime := time.Now().Add(-150 * time.Millisecond)
	s1 := GlobalRegistry.RegisterSession(sessID1, "alice", "analytics", "SELECT count(*) FROM users;", 500, func() {})
	s1.StartedAt = startTime
	s1.State = StateRunning

	s2 := GlobalRegistry.RegisterSession(sessID2, "bob", "billing", "SELECT 1;", 0, nil)
	s2.State = StateIdle

	rows := GetPGStatActivityRows()

	var row1, row2 storage.Row
	for _, r := range rows {
		if len(r) >= 7 && r[0] == int64(sessID1) {
			row1 = r
		} else if len(r) >= 7 && r[0] == int64(sessID2) {
			row2 = r
		}
	}

	if row1 == nil {
		t.Fatalf("expected row for session %d not found in GetPGStatActivityRows result", sessID1)
	}
	if row2 == nil {
		t.Fatalf("expected row for session %d not found in GetPGStatActivityRows result", sessID2)
	}

	// Verify columns [id, user, db, state, query, duration_ms, tx_id] for row1 (RUNNING)
	if row1[0] != int64(sessID1) || row1[1] != "alice" || row1[2] != "analytics" || row1[3] != "RUNNING" || row1[4] != "SELECT count(*) FROM users;" || row1[6] != int64(500) {
		t.Errorf("unexpected values in row1: %#v", row1)
	}
	durationMs, ok := row1[5].(int64)
	if !ok || durationMs < 100 {
		t.Errorf("expected duration_ms >= 100 for running session, got %v (%T)", row1[5], row1[5])
	}

	// Verify columns for row2 (IDLE)
	if row2[0] != int64(sessID2) || row2[1] != "bob" || row2[2] != "billing" || row2[3] != "IDLE" || row2[4] != "SELECT 1;" || row2[5] != int64(0) || row2[6] != int64(0) {
		t.Errorf("unexpected values in row2: %#v", row2)
	}
}

// TestGetPGLocksRows_Direct verifies GetPGLocksRows directly with both nil and initialized RowLockManager.
func TestGetPGLocksRows_Direct(t *testing.T) {
	t.Run("nil manager", func(t *testing.T) {
		rows := GetPGLocksRows(nil)
		if rows == nil || len(rows) != 0 {
			t.Fatalf("expected empty non-nil slice when rowLocks is nil, got len=%d, nil=%v", len(rows), rows == nil)
		}
	})

	t.Run("initialized empty manager", func(t *testing.T) {
		rlm := storage.NewRowLockManager(10 * time.Second)
		rows := GetPGLocksRows(rlm)
		if rows == nil || len(rows) != 0 {
			t.Fatalf("expected empty non-nil slice when no active locks exist, got len=%d", len(rows))
		}
	})

	t.Run("active locks acquired and held", func(t *testing.T) {
		rlm := storage.NewRowLockManager(10 * time.Second)
		ctx := context.Background()

		// Acquire exclusive lock on row1 by tx 100
		if err := rlm.LockRow(ctx, "mydb", "mytable", "row1", 100, storage.LockExclusive); err != nil {
			t.Fatalf("failed to lock row1: %v", err)
		}

		// Acquire shared lock on row2 by tx 200 and tx 201
		if err := rlm.LockRow(ctx, "mydb", "mytable", "row2", 200, storage.LockShared); err != nil {
			t.Fatalf("failed to lock row2 (tx 200): %v", err)
		}
		if err := rlm.LockRow(ctx, "mydb", "mytable", "row2", 201, storage.LockShared); err != nil {
			t.Fatalf("failed to lock row2 (tx 201): %v", err)
		}

		rows := GetPGLocksRows(rlm)
		if len(rows) != 2 {
			t.Fatalf("expected 2 lock rows, got %d", len(rows))
		}

		// GetPGLocksRows sorts by key ascending: "mydb/mytable/row1" < "mydb/mytable/row2"
		r0 := rows[0]
		expectedKey0 := "mydb/mytable/row1"
		if r0[0] != expectedKey0 || r0[1] != "EXCLUSIVE" || r0[2] != "100" || r0[3] != int64(0) {
			t.Errorf("row 0 mismatch: expected [%q EXCLUSIVE 100 0], got %#v", expectedKey0, r0)
		}

		r1 := rows[1]
		expectedKey1 := "mydb/mytable/row2"
		if r1[0] != expectedKey1 || r1[1] != "SHARED" || r1[2] != "200,201" || r1[3] != int64(0) {
			t.Errorf("row 1 mismatch: expected [%q SHARED 200,201 0], got %#v", expectedKey1, r1)
		}
	})
}

// TestSystemViews_SQLExecution verifies system views execution and WHERE filtering via Executor.Run.
func TestSystemViews_SQLExecution(t *testing.T) {
	t.Run("SELECT * FROM system.pg_stat_activity via AST struct and SQL execution", func(t *testing.T) {
		exec, _, sess := setupTestExecutor(t)

		testSessID := uint64(80001)
		GlobalRegistry.RegisterSession(testSessID, "testuser", "system", "SELECT 1;", 100, nil)
		t.Cleanup(func() {
			GlobalRegistry.UnregisterSession(testSessID)
		})

		// 1. Execute query via AST SelectStatement
		resAST, err := exec.Run(&parser.SelectStatement{TableName: "system.pg_stat_activity"}, sess)
		if err != nil {
			t.Fatalf("exec.Run with AST failed: %v", err)
		}
		if resAST == nil || len(resAST.Columns) != 7 {
			t.Fatalf("expected 7 columns in result, got %v", resAST)
		}

		expectedCols := []string{"id", "user", "db", "state", "query", "duration_ms", "tx_id"}
		if !reflect.DeepEqual(resAST.Columns, expectedCols) {
			t.Errorf("expected columns %v, got %v", expectedCols, resAST.Columns)
		}

		foundTestSess := false
		for _, row := range resAST.Rows {
			if len(row) >= 7 && row[0] == fmt.Sprintf("%d", testSessID) {
				foundTestSess = true
				if row[1] != "testuser" || row[2] != "system" || row[4] != "SELECT 1;" || row[6] != "100" {
					t.Errorf("unexpected row values for testSessID: %v", row)
				}
			}
		}
		if !foundTestSess {
			t.Errorf("test session %d not found in pg_stat_activity rows: %v", testSessID, resAST.Rows)
		}

		// 2. Execute query via SQL string
		stmt, err := parser.Parse("SELECT * FROM system.pg_stat_activity;")
		if err != nil {
			t.Fatalf("failed to parse SQL: %v", err)
		}
		resSQL, err := exec.Run(stmt, sess)
		if err != nil {
			t.Fatalf("exec.Run with SQL string failed: %v", err)
		}
		if resSQL == nil || len(resSQL.Rows) == 0 {
			t.Errorf("expected rows from SQL execution, got %v", resSQL)
		}
	})

	t.Run("SELECT * FROM system.pg_locks via SQL execution", func(t *testing.T) {
		exec, txm, sess := setupTestExecutor(t)

		if err := txm.RowLocks.LockRow(context.Background(), "mydb", "orders", "order-123", 42, storage.LockExclusive); err != nil {
			t.Fatalf("failed to lock row: %v", err)
		}

		stmt, err := parser.Parse("SELECT * FROM system.pg_locks;")
		if err != nil {
			t.Fatalf("failed to parse SQL: %v", err)
		}
		res, err := exec.Run(stmt, sess)
		if err != nil {
			t.Fatalf("exec.Run for pg_locks failed: %v", err)
		}

		expectedCols := []string{"key", "mode", "holders", "waiters"}
		if !reflect.DeepEqual(res.Columns, expectedCols) {
			t.Errorf("expected columns %v, got %v", expectedCols, res.Columns)
		}

		if len(res.Rows) != 1 {
			t.Fatalf("expected 1 row in pg_locks, got %d: %v", len(res.Rows), res.Rows)
		}

		row := res.Rows[0]
		expectedKey := "mydb/orders/order-123"
		if row[0] != expectedKey || row[1] != "EXCLUSIVE" || row[2] != "42" || row[3] != "0" {
			t.Errorf("unexpected row in pg_locks: %v", row)
		}

		// Also test via SelectStatement AST directly
		resAST, err := exec.Run(&parser.SelectStatement{TableName: "system.pg_locks"}, sess)
		if err != nil {
			t.Fatalf("exec.Run AST for pg_locks failed: %v", err)
		}
		if len(resAST.Rows) != 1 || resAST.Rows[0][0] != expectedKey {
			t.Errorf("unexpected AST result for pg_locks: %v", resAST.Rows)
		}
	})

	t.Run("Filtering via WHERE: SELECT id, query FROM system.pg_stat_activity WHERE state = 'RUNNING'", func(t *testing.T) {
		exec, _, sess := setupTestExecutor(t)

		runningID := uint64(80010)
		idleID := uint64(80011)

		sRunning := GlobalRegistry.RegisterSession(runningID, "user_run", "system", "SELECT count(*) FROM big_table;", 300, nil)
		sRunning.State = StateRunning

		sIdle := GlobalRegistry.RegisterSession(idleID, "user_idle", "system", "SELECT 2;", 301, nil)
		sIdle.State = StateIdle

		t.Cleanup(func() {
			GlobalRegistry.UnregisterSession(runningID)
			GlobalRegistry.UnregisterSession(idleID)
		})

		sql := "SELECT id, query FROM system.pg_stat_activity WHERE state = 'RUNNING';"
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("failed to parse SQL %q: %v", sql, err)
		}

		res, err := exec.Run(stmt, sess)
		if err != nil {
			t.Fatalf("exec.Run for WHERE query failed: %v", err)
		}

		expectedCols := []string{"id", "query"}
		if !reflect.DeepEqual(res.Columns, expectedCols) {
			t.Errorf("expected columns %v, got %v", expectedCols, res.Columns)
		}

		foundRunning := false
		foundIdle := false
		for _, r := range res.Rows {
			if len(r) >= 2 {
				if r[0] == fmt.Sprintf("%d", runningID) {
					foundRunning = true
					if r[1] != "SELECT count(*) FROM big_table;" {
						t.Errorf("unexpected query string for running session: %v", r[1])
					}
				}
				if r[0] == fmt.Sprintf("%d", idleID) {
					foundIdle = true
				}
			}
		}

		if !foundRunning {
			t.Errorf("expected running session %d in filtered results, got %v", runningID, res.Rows)
		}
		if foundIdle {
			t.Errorf("expected idle session %d to be filtered out by WHERE state = 'RUNNING', but found in %v", idleID, res.Rows)
		}
	})
}
