package sel_test

import (
	"testing"

	"vaultdb/internal/executor"
)

func TestWindowRankTies(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (2, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (3, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (4, 1, 90);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (5, 1, 80);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (6, 1, 80);`)

	// RANK: 100→1,100→1,100→1, 90→4, 80→5,80→5
	res := executor.ExecuteSQL(t, session, `SELECT id, RANK() OVER (ORDER BY val DESC) AS rnk FROM t ORDER BY id;`)
	expected := []string{"1", "1", "1", "4", "5", "5"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected rank %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowDenseRankTies(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (2, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (3, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (4, 1, 90);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (5, 1, 80);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (6, 1, 80);`)

	// DENSE_RANK: 100→1,100→1,100→1, 90→2, 80→3,80→3
	res := executor.ExecuteSQL(t, session, `SELECT id, DENSE_RANK() OVER (ORDER BY val DESC) AS dr FROM t ORDER BY id;`)
	expected := []string{"1", "1", "1", "2", "3", "3"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected dense_rank %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowRankWithPartition(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (2, 1, 90);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (3, 2, 100);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (4, 2, 90);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (5, 2, 80);`)

	// grp=1: 100→rank1, 90→rank2; grp=2: 100→rank1, 90→rank2, 80→rank3
	res := executor.ExecuteSQL(t, session, `SELECT id, grp, RANK() OVER (PARTITION BY grp ORDER BY val DESC) AS rnk FROM t ORDER BY id;`)
	expected := []string{"1", "2", "1", "2", "3"}
	for i, row := range res.Rows {
		if row[2] != expected[i] {
			t.Fatalf("row %d: expected rank %s, got %s", i, expected[i], row[2])
		}
	}
}

func TestWindowSumRunning(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Default frame with ORDER BY = running sum: 10, 30, 60
	res := executor.ExecuteSQL(t, session, `SELECT id, SUM(val) OVER (ORDER BY id) AS running FROM t ORDER BY id;`)
	expected := []string{"10", "30", "60"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected running sum %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowCountRunning(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Running count: 1, 2, 3
	res := executor.ExecuteSQL(t, session, `SELECT id, COUNT(val) OVER (ORDER BY id) AS cnt FROM t ORDER BY id;`)
	expected := []string{"1", "2", "3"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected count %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowAvgRunning(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executor.ExecuteSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Running avg: 10, 15, 20
	res := executor.ExecuteSQL(t, session, `SELECT id, AVG(val) OVER (ORDER BY id) AS avg FROM t ORDER BY id;`)
	expected := []string{"10", "15", "20"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected avg %s, got %s", i, expected[i], row[1])
		}
	}
}
