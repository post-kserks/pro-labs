package executor

import (
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

// TestGetPGLocksRows_Direct verifies GetPGLocksRows returns empty since MVCC replaced row locks.
func TestGetPGLocksRows_Direct(t *testing.T) {
	t.Run("nil argument", func(t *testing.T) {
		rows := GetPGLocksRows(nil)
		if rows == nil || len(rows) != 0 {
			t.Fatalf("expected empty non-nil slice, got len=%d, nil=%v", len(rows), rows == nil)
		}
	})
}

// TestSystemViews_SQLExecution verifies system views execution and WHERE filtering via Executor.Run.
func TestSystemViews_SQLExecution(t *testing.T) {
	t.Run("SELECT * FROM system.pg_stat_activity", func(t *testing.T) {
		exec, _, sess := setupTestExecutor(t)

		sessID2 := uint64(80002)
		GlobalRegistry.RegisterSession(sessID2, "bob", "system", "SELECT 1;", 0, nil)
		t.Cleanup(func() { GlobalRegistry.UnregisterSession(sessID2) })

		stmt, err := parser.Parse("SELECT * FROM system.pg_stat_activity;")
		if err != nil {
			t.Fatalf("failed to parse SQL: %v", err)
		}
		res, err := exec.Run(stmt, sess)
		if err != nil {
			t.Fatalf("exec.Run failed: %v", err)
		}

		expectedCols := []string{"id", "user", "db", "state", "query", "duration_ms", "tx_id"}
		if !reflect.DeepEqual(res.Columns, expectedCols) {
			t.Errorf("expected columns %v, got %v", expectedCols, res.Columns)
		}

		if len(res.Rows) < 2 {
			t.Fatalf("expected at least 2 active sessions in pg_stat_activity, got %d", len(res.Rows))
		}
	})

	t.Run("SELECT * FROM system.pg_stat_activity WHERE user = 'bob_sql_exec_test'", func(t *testing.T) {
		exec, _, sess := setupTestExecutor(t)

		sessID2 := uint64(80003)
		s2 := GlobalRegistry.RegisterSession(sessID2, "bob_sql_exec_test", "system", "SELECT 1;", 0, nil)
		s2.State = StateIdle
		t.Cleanup(func() { GlobalRegistry.UnregisterSession(sessID2) })

		stmt, err := parser.Parse("SELECT * FROM system.pg_stat_activity WHERE user = 'bob_sql_exec_test';")
		if err != nil {
			t.Fatalf("failed to parse SQL: %v", err)
		}
		res, err := exec.Run(stmt, sess)
		if err != nil {
			t.Fatalf("exec.Run with WHERE failed: %v", err)
		}

		if len(res.Rows) < 1 {
			t.Fatalf("expected at least 1 session for bob_sql_exec_test, got %d: %v", len(res.Rows), res.Rows)
		}
		if res.Rows[0][1] != "bob_sql_exec_test" {
			t.Errorf("expected bob_sql_exec_test, got %v", res.Rows[0])
		}

		// Also verify via SelectStatement AST
		selectStmt := &parser.SelectStatement{
			TableName: "system.pg_stat_activity",
			Where: &parser.BinaryExpr{
				Left:     &parser.ColumnRef{Name: "user"},
				Operator: "=",
				Right:    &parser.Value{Type: "string", StrVal: "bob_sql_exec_test"},
			},
		}
		resAST, err := exec.Run(selectStmt, sess)
		if err != nil {
			t.Fatalf("exec.Run AST with WHERE failed: %v", err)
		}
		if len(resAST.Rows) < 1 || resAST.Rows[0][1] != "bob_sql_exec_test" {
			t.Errorf("unexpected AST result: %v", resAST.Rows)
		}
	})

	t.Run("SELECT * FROM system.pg_stat_activity WHERE id = 999 (no matches)", func(t *testing.T) {
		exec, _, sess := setupTestExecutor(t)

		stmt, err := parser.Parse("SELECT * FROM system.pg_stat_activity WHERE id = 999;")
		if err != nil {
			t.Fatalf("failed to parse SQL: %v", err)
		}
		resSQL, err := exec.Run(stmt, sess)
		if err != nil {
			t.Fatalf("exec.Run with SQL string failed: %v", err)
		}
		if resSQL == nil || len(resSQL.Rows) != 0 {
			t.Errorf("expected 0 rows from SQL execution, got %v", resSQL)
		}
	})

	t.Run("SELECT * FROM system.pg_locks via SQL execution", func(t *testing.T) {
		exec, _, sess := setupTestExecutor(t)

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

		if len(res.Rows) != 0 {
			t.Fatalf("expected 0 rows in pg_locks with MVCC, got %d: %v", len(res.Rows), res.Rows)
		}

		// Also test via SelectStatement AST directly
		resAST, err := exec.Run(&parser.SelectStatement{TableName: "system.pg_locks"}, sess)
		if err != nil {
			t.Fatalf("exec.Run AST for pg_locks failed: %v", err)
		}
		if len(resAST.Rows) != 0 {
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
