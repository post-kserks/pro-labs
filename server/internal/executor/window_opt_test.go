package executor

import (
	"fmt"
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════════
// Correctness tests for O(n) window function optimization
// ═══════════════════════════════════════════════════════════════════════════

func TestWindowRankTies(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	// 3-way tie at val=100, then val=90, then val=80, then val=80
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 1, 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (5, 1, 80);`)
	executeSQL(t, session, `INSERT INTO t VALUES (6, 1, 80);`)

	// RANK: 100→1,100→1,100→1, 90→4, 80→5,80→5
	res := executeSQL(t, session, `SELECT id, RANK() OVER (ORDER BY val DESC) AS rnk FROM t ORDER BY id;`)
	expected := []string{"1", "1", "1", "4", "5", "5"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected rank %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowDenseRankTies(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 1, 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (5, 1, 80);`)
	executeSQL(t, session, `INSERT INTO t VALUES (6, 1, 80);`)

	// DENSE_RANK: 100→1,100→1,100→1, 90→2, 80→3,80→3
	res := executeSQL(t, session, `SELECT id, DENSE_RANK() OVER (ORDER BY val DESC) AS dr FROM t ORDER BY id;`)
	expected := []string{"1", "1", "1", "2", "3", "3"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected dense_rank %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowRankWithPartition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 2, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 2, 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (5, 2, 80);`)

	// grp=1: 100→rank1, 90→rank2; grp=2: 100→rank1, 90→rank2, 80→rank3
	res := executeSQL(t, session, `SELECT id, grp, RANK() OVER (PARTITION BY grp ORDER BY val DESC) AS rnk FROM t ORDER BY id;`)
	expected := []string{"1", "2", "1", "2", "3"}
	for i, row := range res.Rows {
		if row[2] != expected[i] {
			t.Fatalf("row %d: expected rank %s, got %s", i, expected[i], row[2])
		}
	}
}

func TestWindowDenseRankWithPartition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 2, 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 2, 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (5, 2, 80);`)

	// grp=1: 100→1, 90→2; grp=2: 100→1, 90→2, 80→3
	res := executeSQL(t, session, `SELECT id, grp, DENSE_RANK() OVER (PARTITION BY grp ORDER BY val DESC) AS dr FROM t ORDER BY id;`)
	expected := []string{"1", "2", "1", "2", "3"}
	for i, row := range res.Rows {
		if row[2] != expected[i] {
			t.Fatalf("row %d: expected dense_rank %s, got %s", i, expected[i], row[2])
		}
	}
}

func TestWindowRankAllEqual(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 50);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 50);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 50);`)

	// All equal: all should get rank 1
	res := executeSQL(t, session, `SELECT id, RANK() OVER (ORDER BY val) AS rnk FROM t ORDER BY id;`)
	for _, row := range res.Rows {
		if row[1] != "1" {
			t.Fatalf("expected rank 1 for all equal values, got %s", row[1])
		}
	}
}

func TestWindowSumRunning(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Default frame with ORDER BY = running sum: 10, 30, 60
	res := executeSQL(t, session, `SELECT id, SUM(val) OVER (ORDER BY id) AS running FROM t ORDER BY id;`)
	expected := []string{"10", "30", "60"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected running sum %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowSumWholePartition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 2, 30);`)

	// Whole partition sum (no ORDER BY): grp=1→30, grp=2→30
	res := executeSQL(t, session, `SELECT id, grp, SUM(val) OVER (PARTITION BY grp) AS total FROM t ORDER BY id;`)
	expected := []string{"30", "30", "30"}
	for i, row := range res.Rows {
		if row[2] != expected[i] {
			t.Fatalf("row %d: expected sum %s, got %s", i, expected[i], row[2])
		}
	}
}

func TestWindowCountRunning(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Running count: 1, 2, 3
	res := executeSQL(t, session, `SELECT id, COUNT(val) OVER (ORDER BY id) AS cnt FROM t ORDER BY id;`)
	expected := []string{"1", "2", "3"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected count %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowAvgRunning(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Running avg: 10, 15, 20
	res := executeSQL(t, session, `SELECT id, AVG(val) OVER (ORDER BY id) AS avg FROM t ORDER BY id;`)
	expected := []string{"10", "15", "20"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected avg %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowSumWithPartitionAndOrder(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 2, 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 2, 40);`)

	// Running sum per partition: grp1: 10,30; grp2: 30,70
	res := executeSQL(t, session, `SELECT id, grp, SUM(val) OVER (PARTITION BY grp ORDER BY id) AS running FROM t ORDER BY id;`)
	expected := []string{"10", "30", "30", "70"}
	for i, row := range res.Rows {
		if row[2] != expected[i] {
			t.Fatalf("row %d: expected running sum %s, got %s", i, expected[i], row[2])
		}
	}
}

func TestWindowMinRunning(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 20);`)

	// Running min: 30, 10, 10
	res := executeSQL(t, session, `SELECT id, MIN(val) OVER (ORDER BY id) AS rmin FROM t ORDER BY id;`)
	expected := []string{"30", "10", "10"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected min %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowMaxRunning(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 20);`)

	// Running max: 30, 30, 30
	res := executeSQL(t, session, `SELECT id, MAX(val) OVER (ORDER BY id) AS rmax FROM t ORDER BY id;`)
	expected := []string{"30", "30", "30"}
	for i, row := range res.Rows {
		if row[1] != expected[i] {
			t.Fatalf("row %d: expected max %s, got %s", i, expected[i], row[1])
		}
	}
}

func TestWindowMultipleFunctionsCombined(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 1, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 1, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 2, 50);`)

	res := executeSQL(t, session, `SELECT id,
		RANK() OVER (PARTITION BY grp ORDER BY val DESC) AS rnk,
		DENSE_RANK() OVER (PARTITION BY grp ORDER BY val DESC) AS dr,
		SUM(val) OVER (PARTITION BY grp ORDER BY val DESC) AS running_sum,
		COUNT(*) OVER (PARTITION BY grp ORDER BY val DESC) AS running_count
	FROM t ORDER BY id;`)

	type expected struct {
		rank, dr, sum, cnt string
	}
	byID := map[string]expected{}
	for _, row := range res.Rows {
		byID[row[0]] = expected{row[1], row[2], row[3], row[4]}
	}

	// grp=1 sorted by val DESC: [id=2,val=20], [id=3,val=20], [id=1,val=10]
	// grp=2 sorted by val DESC: [id=4,val=50]
	// Running frame (UNBOUNDED PRECEDING to CURRENT ROW):
	// id=1 (val=10, grp=1, pos=2): rank=3, dr=2, running_sum=50, running_count=3
	// id=2 (val=20, grp=1, pos=0): rank=1, dr=1, running_sum=20, running_count=1
	// id=3 (val=20, grp=1, pos=1): rank=1, dr=1, running_sum=40, running_count=2
	// id=4 (val=50, grp=2, pos=0): rank=1, dr=1, running_sum=50, running_count=1
	checks := []struct {
		id string
		expected
	}{
		{"1", expected{"3", "2", "50", "3"}},
		{"2", expected{"1", "1", "20", "1"}},
		{"3", expected{"1", "1", "40", "2"}},
		{"4", expected{"1", "1", "50", "1"}},
	}
	for _, c := range checks {
		got := byID[c.id]
		if got != c.expected {
			t.Fatalf("id=%s: expected %+v, got %+v", c.id, c.expected, got)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Benchmarks
// ═══════════════════════════════════════════════════════════════════════════

func benchWindowSetup(b *testing.B, table string, n int) *Session {
	b.Helper()
	session := setupBenchSession(b)
	executeSQLBench(b, session, fmt.Sprintf(`CREATE TABLE %s (id INT, grp TEXT, val INT);`, table))
	for i := 0; i < n; i++ {
		executeSQLBench(b, session, fmt.Sprintf(`INSERT INTO %s VALUES (%d, 'g%d', %d);`, table, i, i%10, i%100))
	}
	return session
}

func BenchmarkWindowRANK(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			session := benchWindowSetup(b, "bench_rank", n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				executeSQLBench(b, session, `SELECT RANK() OVER (PARTITION BY grp ORDER BY val DESC) FROM bench_rank;`)
			}
		})
	}
}

func BenchmarkWindowDENSE_RANK(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			session := benchWindowSetup(b, "bench_dr", n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				executeSQLBench(b, session, `SELECT DENSE_RANK() OVER (PARTITION BY grp ORDER BY val DESC) FROM bench_dr;`)
			}
		})
	}
}

func BenchmarkWindowSUM(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			session := benchWindowSetup(b, "bench_sum", n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				executeSQLBench(b, session, `SELECT SUM(val) OVER (PARTITION BY grp ORDER BY id) FROM bench_sum;`)
			}
		})
	}
}

func BenchmarkWindowCOUNT(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			session := benchWindowSetup(b, "bench_cnt", n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				executeSQLBench(b, session, `SELECT COUNT(val) OVER (PARTITION BY grp ORDER BY id) FROM bench_cnt;`)
			}
		})
	}
}

func BenchmarkWindowAVG(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			session := benchWindowSetup(b, "bench_avg", n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				executeSQLBench(b, session, `SELECT AVG(val) OVER (PARTITION BY grp ORDER BY id) FROM bench_avg;`)
			}
		})
	}
}
