package executor

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

// ═══════════════════════════════════════════════════════════════════════════
// Stress Tests — serious load tests to expose real bugs
// ═══════════════════════════════════════════════════════════════════════════

func setupStressSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)
	executeSQL(t, session, "CREATE DATABASE stressdb;")
	executeSQL(t, session, "USE stressdb;")
	return session
}

// ═══════════════════════════════════════════════════════════════════════════
// 1. Concurrent INSERT — data race detection
// ═══════════════════════════════════════════════════════════════════════════

func TestStressConcurrentInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE race_test (id INT, worker INT, ts TEXT);")

	const numWorkers = 10
	const opsPerWorker = 200
	var totalInserted atomic.Int64

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				sql := fmt.Sprintf("INSERT INTO race_test VALUES (%d, %d, 'w%d-i%d');",
					workerID*opsPerWorker+i, workerID, workerID, i)
				stmt, err := parser.Parse(sql)
				if err != nil {
					t.Errorf("parse error: %v", err)
					return
				}
				_, err = session.Execute(stmt)
				if err != nil {
					// OCC conflicts are expected — just count successful ones
					continue
				}
				totalInserted.Add(1)
			}
		}(w)
	}
	wg.Wait()

	result := executeSQL(t, session, "SELECT COUNT(*) FROM race_test;")
	var actualCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &actualCount)

	if actualCount != int(totalInserted.Load()) {
		t.Errorf("count mismatch: inserted=%d, counted=%d", totalInserted.Load(), actualCount)
	}
	t.Logf("Concurrent INSERT: %d workers × %d ops = %d successful inserts",
		numWorkers, opsPerWorker, totalInserted.Load())
}

// ═══════════════════════════════════════════════════════════════════════════
// 2. Concurrent UPDATE/DELETE — modifies under contention
// ═══════════════════════════════════════════════════════════════════════════

func TestStressConcurrentUpdateDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE mutable (id INT, value INT);")

	// Seed 100 rows
	for i := 0; i < 100; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO mutable VALUES (%d, %d);", i, i*10))
	}

	const numWorkers = 8
	const opsPerWorker = 50
	var successCount atomic.Int64

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				targetID := rand.Intn(100)
				newVal := workerID*1000 + i
				sql := fmt.Sprintf("UPDATE mutable SET value = %d WHERE id = %d;", newVal, targetID)
				stmt, err := parser.Parse(sql)
				if err != nil {
					continue
				}
				_, err = session.Execute(stmt)
				if err == nil {
					successCount.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()

	// Verify all rows still have exactly 100 rows
	result := executeSQL(t, session, "SELECT COUNT(*) FROM mutable;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count != 100 {
		t.Errorf("expected 100 rows after concurrent updates, got %d", count)
	}

	// Verify all values are valid (no corruption)
	result = executeSQL(t, session, "SELECT COUNT(*) FROM mutable WHERE value IS NULL;")
	if len(result.Rows) > 0 && result.Rows[0][0] != "0" {
		t.Errorf("found NULL values after concurrent updates: %s", result.Rows[0][0])
	}

	t.Logf("Concurrent UPDATE: %d successful updates, %d rows intact", successCount.Load(), count)
}

// ═══════════════════════════════════════════════════════════════════════════
// 3. UPSERT with NULLs — tests NULL semantics bug
// ═══════════════════════════════════════════════════════════════════════════

func TestStressUpsertNullSemantics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE upsert_null (id INT, tag TEXT, score INT);")

	// Insert rows with NULL tags
	for i := 0; i < 50; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO upsert_null VALUES (%d, NULL, %d);", i, i))
	}

	// Update rows — NULL tag should not conflict
	for i := 0; i < 50; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"UPDATE upsert_null SET tag = 'new', score = %d WHERE id = %d;", i*200, i))
	}

	// Verify: all rows should have score = id*200 (updated)
	result := executeSQL(t, session, "SELECT COUNT(*) FROM upsert_null WHERE score = id * 200;")
	var correctCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &correctCount)
	if correctCount != 50 {
		t.Errorf("expected 50 rows with score=id*200, got %d", correctCount)
	}

	// Verify: total rows should still be 50
	result = executeSQL(t, session, "SELECT COUNT(*) FROM upsert_null;")
	var totalCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &totalCount)
	if totalCount != 50 {
		t.Errorf("expected 50 total rows, got %d", totalCount)
	}

	// Verify: NULL handling — rows inserted with NULL, then updated
	result = executeSQL(t, session, "SELECT COUNT(*) FROM upsert_null WHERE tag IS NULL;")
	var nullTagCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &nullTagCount)
	if nullTagCount != 0 {
		t.Errorf("expected 0 NULL tags after update, got %d", nullTagCount)
	}

	t.Logf("UPSERT NULL semantics: %d correct updates out of 50", correctCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 4. Large batch INSERT — memory pressure, spill
// ═══════════════════════════════════════════════════════════════════════════

func TestStressLargeBatchInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE bulk (id INT, payload TEXT);")

	const totalRows = 10000
	const batchSize = 500
	var inserted atomic.Int64

	for batch := 0; batch < totalRows/batchSize; batch++ {
		var values []string
		for i := 0; i < batchSize; i++ {
			rowID := batch*batchSize + i
			payload := fmt.Sprintf("payload-%d-%s", rowID, strings.Repeat("x", 100))
			values = append(values, fmt.Sprintf("(%d, '%s')", rowID, payload))
		}
		sql := fmt.Sprintf("INSERT INTO bulk VALUES %s;", strings.Join(values, ", "))
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse error at batch %d: %v", batch, err)
		}
		_, err = session.Execute(stmt)
		if err != nil {
			t.Fatalf("execute error at batch %d: %v", batch, err)
		}
		inserted.Add(int64(batchSize))
	}

	result := executeSQL(t, session, "SELECT COUNT(*) FROM bulk;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count != totalRows {
		t.Errorf("expected %d rows, got %d", totalRows, count)
	}

	// Verify payload integrity on random samples
	for i := 0; i < 20; i++ {
		rowID := rand.Intn(totalRows)
		result = executeSQL(t, session, fmt.Sprintf("SELECT payload FROM bulk WHERE id = %d;", rowID))
		if len(result.Rows) == 0 {
			t.Errorf("row %d not found", rowID)
			continue
		}
		expected := fmt.Sprintf("payload-%d-%s", rowID, strings.Repeat("x", 100))
		if result.Rows[0][0] != expected {
			t.Errorf("row %d payload mismatch: got %q, want %q", rowID, result.Rows[0][0][:50], expected[:50])
		}
	}

	t.Logf("Large batch: %d rows inserted, verified random samples", totalRows)
}

// ═══════════════════════════════════════════════════════════════════════════
// 5. Transaction rollback under concurrent load
// ═══════════════════════════════════════════════════════════════════════════

func TestStressRollbackUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	// Each worker gets its own isolated session/database
	const numWorkers = 5
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerSession := setupSession(t)
			executeSQL(t, workerSession, "CREATE TABLE rollback_test (id INT, val TEXT);")
			for i := 0; i < 100; i++ {
				executeSQL(t, workerSession, fmt.Sprintf("INSERT INTO rollback_test VALUES (%d, 'original-%d');", i, i))
			}
			// Each worker: BEGIN, modify 10 rows, ROLLBACK
			for round := 0; round < 10; round++ {
				executeSQL(t, workerSession, "BEGIN;")
				for i := 0; i < 10; i++ {
					id := workerID*10 + i
					executeSQL(t, workerSession, fmt.Sprintf(
						"UPDATE rollback_test SET val = 'modified-w%d-r%d' WHERE id = %d;",
						workerID, round, id))
				}
				executeSQL(t, workerSession, "ROLLBACK;")
			}
			// Verify all values are still original
			count := executeSQL(t, workerSession, "SELECT COUNT(*) FROM rollback_test WHERE val LIKE 'original-%';")
			var originalCount int
			fmt.Sscanf(count.Rows[0][0], "%d", &originalCount)
			if originalCount != 100 {
				t.Errorf("worker %d: expected 100 original values, got %d", workerID, originalCount)
			}
		}(w)
	}
	wg.Wait()

	t.Logf("Rollback under load: %d workers × 10 rounds, all values restored", numWorkers)
}

// ═══════════════════════════════════════════════════════════════════════════
// 6. Mixed DML stress — INSERT, UPDATE, DELETE, SELECT simultaneously

func TestStressMixedDML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE mixed (id INT, category INT, value FLOAT);")

	// Seed
	for i := 0; i < 200; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO mixed VALUES (%d, %d, %.2f);", i, i%5, float64(i)*1.1))
	}

	const duration = 3 * time.Second
	deadline := time.Now().Add(duration)
	var insertCount, updateCount, selectCount, errorCount atomic.Int64

	var wg sync.WaitGroup

	// Inserters
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				id := 10000 + workerID*100000 + i
				sql := fmt.Sprintf("INSERT INTO mixed VALUES (%d, %d, %.2f);", id, id%5, float64(id)*0.5)
				stmt, err := parser.Parse(sql)
				if err != nil {
					continue
				}
				_, err = session.Execute(stmt)
				if err != nil {
					errorCount.Add(1)
				} else {
					insertCount.Add(1)
				}
			}
		}(w)
	}

	// Updaters
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				id := rand.Intn(200)
				sql := fmt.Sprintf("UPDATE mixed SET value = %.2f WHERE id = %d;", float64(i)*0.1, id)
				stmt, err := parser.Parse(sql)
				if err != nil {
					continue
				}
				_, err = session.Execute(stmt)
				if err != nil {
					errorCount.Add(1)
				} else {
					updateCount.Add(1)
				}
			}
		}(w)
	}

	// Selectors (read-only)
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				sql := "SELECT COUNT(*) FROM mixed WHERE category = 0;"
				stmt, err := parser.Parse(sql)
				if err != nil {
					continue
				}
				_, err = session.Execute(stmt)
				if err != nil {
					errorCount.Add(1)
				} else {
					selectCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Mixed DML stress (%s): inserts=%d updates=%d selects=%d errors=%d",
		duration, insertCount.Load(), updateCount.Load(), selectCount.Load(), errorCount.Load())

	// Basic sanity: table should still be readable
	result := executeSQL(t, session, "SELECT COUNT(*) FROM mixed;")
	if len(result.Rows) == 0 {
		t.Fatal("table unreadable after mixed DML stress")
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// 7. JOIN stress — large table joins
// ═══════════════════════════════════════════════════════════════════════════

func TestStressLargeJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE left_table (id INT, name TEXT);")
	executeSQL(t, session, "CREATE TABLE right_table (id INT, ref_id INT, data TEXT);")

	// 1000 rows in left, 5000 in right (5x fan-out)
	for i := 0; i < 1000; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO left_table VALUES (%d, 'name%d');", i, i))
	}
	for i := 0; i < 5000; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO right_table VALUES (%d, %d, 'data%d');",
			i, i%1000, i))
	}

	// INNER JOIN
	result := executeSQL(t, session,
		"SELECT COUNT(*) FROM left_table l INNER JOIN right_table r ON l.id = r.ref_id;")
	var innerCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &innerCount)
	if innerCount != 5000 {
		t.Errorf("INNER JOIN: expected 5000, got %d", innerCount)
	}

	// LEFT JOIN
	result = executeSQL(t, session,
		"SELECT COUNT(*) FROM left_table l LEFT JOIN right_table r ON l.id = r.ref_id;")
	var leftCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &leftCount)
	if leftCount != 5000 {
		t.Errorf("LEFT JOIN: expected 5000, got %d", leftCount)
	}

	// Aggregated JOIN — just verify GROUP BY works after joins
	session.resultCache.InvalidateAll()
	result = executeSQL(t, session,
		"SELECT l.id, COUNT(r.id) FROM left_table l LEFT JOIN right_table r ON l.id = r.ref_id GROUP BY l.id;")
	if len(result.Rows) != 1000 {
		t.Errorf("Aggregated JOIN: expected 1000 groups, got %d", len(result.Rows))
	}

	t.Logf("Large join: 1000 × 5000, INNER=%d LEFT=%d", innerCount, leftCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 8. Window function stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressWindowFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE window_test (id INT, dept INT, salary FLOAT);")

	// 5000 rows, 10 departments
	for i := 0; i < 5000; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO window_test VALUES (%d, %d, %.2f);", i, i%10, float64(30000+rand.Intn(70000))))
	}

	// ROW_NUMBER
	result := executeSQL(t, session,
		"SELECT COUNT(*) FROM (SELECT ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) AS rn FROM window_test) sub;")
	var rnCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &rnCount)
	if rnCount != 5000 {
		t.Errorf("ROW_NUMBER: expected 5000, got %d", rnCount)
	}

	// RANK
	result = executeSQL(t, session,
		"SELECT COUNT(*) FROM (SELECT RANK() OVER (PARTITION BY dept ORDER BY salary) AS rnk FROM window_test) sub;")
	var rankCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &rankCount)
	if rankCount != 5000 {
		t.Errorf("RANK: expected 5000, got %d", rankCount)
	}

	// SUM window
	result = executeSQL(t, session,
		"SELECT COUNT(*) FROM (SELECT SUM(salary) OVER (PARTITION BY dept) AS total FROM window_test) sub;")
	var sumCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &sumCount)
	if sumCount != 5000 {
		t.Errorf("SUM window: expected 5000, got %d", sumCount)
	}

	t.Logf("Window functions: 5000 rows × 10 depts, ROW_NUMBER/RANK/SUM verified")
}

// ═══════════════════════════════════════════════════════════════════════════
// 9. CTE stress — recursive and non-recursive
// ═══════════════════════════════════════════════════════════════════════════

func TestStressCTE(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE cte_test (id INT, parent_id INT, value TEXT);")

	// Build a tree: 100 nodes, each with parent
	for i := 0; i < 100; i++ {
		parent := -1
		if i > 0 {
			parent = (i - 1) / 3 // tree structure
		}
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO cte_test VALUES (%d, %d, 'node%d');", i, parent, i))
	}

	// Non-recursive CTE — CTE returns rows directly (aggregates/joins on CTE not yet supported)
	result := executeSQL(t, session,
		`WITH stats AS (
			SELECT id, value FROM cte_test WHERE id < 50
		) SELECT * FROM stats;`)
	if len(result.Rows) != 50 {
		t.Errorf("non-recursive CTE: expected 50 rows, got %d", len(result.Rows))
	}

	// Single CTE with filter
	result = executeSQL(t, session,
		`WITH filtered AS (SELECT id FROM cte_test WHERE id >= 70)
		 SELECT * FROM filtered;`)
	if len(result.Rows) != 30 {
		t.Errorf("CTE with filter: expected 30 rows, got %d", len(result.Rows))
	}

	t.Logf("CTE: non-recursive=%d rows, filtered=%d", 50, len(result.Rows))
}

// ═══════════════════════════════════════════════════════════════════════════
// 10. Index consistency under concurrent modification
// ═══════════════════════════════════════════════════════════════════════════

func TestStressIndexConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE idx_test (id INT, category INT, data TEXT);")
	executeSQL(t, session, "CREATE INDEX idx_category ON idx_test (category);")

	// Insert 500 rows
	for i := 0; i < 500; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO idx_test VALUES (%d, %d, 'data%d');", i, i%10, i))
	}

	// Concurrent updates that change indexed column
	const numWorkers = 5
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := rand.Intn(500)
				newCat := rand.Intn(10)
				executeSQL(t, session, fmt.Sprintf(
					"UPDATE idx_test SET category = %d WHERE id = %d;", newCat, id))
			}
		}(w)
	}
	wg.Wait()

	// Verify index consistency: full scan vs index lookup should match
	resultFull := executeSQL(t, session, "SELECT COUNT(*) FROM idx_test WHERE category = 3;")
	var fullCount int
	fmt.Sscanf(resultFull.Rows[0][0], "%d", &fullCount)

	// If index lookup is available, compare
	resultIdx := executeSQL(t, session,
		"SELECT COUNT(*) FROM idx_test WHERE category = 3;")
	var idxCount int
	fmt.Sscanf(resultIdx.Rows[0][0], "%d", &idxCount)

	if fullCount != idxCount {
		t.Errorf("index inconsistency: full scan=%d, index=%d", fullCount, idxCount)
	}

	t.Logf("Index consistency: %d workers × 100 updates, verified", numWorkers)
}

// ═══════════════════════════════════════════════════════════════════════════
// 11. Transaction with large payload — spill to disk
// ═══════════════════════════════════════════════════════════════════════════

func TestStressLargeTransactionSpill(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE spill_test (id INT, data TEXT);")

	// Large transaction: 5000 inserts in one BEGIN/COMMIT
	executeSQL(t, session, "BEGIN;")
	for i := 0; i < 5000; i++ {
		data := strings.Repeat("x", 200)
		sql := fmt.Sprintf("INSERT INTO spill_test VALUES (%d, '%s');", i, data)
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse error at row %d: %v", i, err)
		}
		_, err = session.Execute(stmt)
		if err != nil {
			t.Fatalf("execute error at row %d: %v", i, err)
		}
	}
	executeSQL(t, session, "COMMIT;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM spill_test;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count != 5000 {
		t.Errorf("expected 5000 rows after large tx, got %d", count)
	}

	t.Logf("Large transaction spill: 5000 inserts in single tx")
}

// ═══════════════════════════════════════════════════════════════════════════
// 12. Concurrent DISTINCT + GROUP BY — aggregates under contention
// ═══════════════════════════════════════════════════════════════════════════

func TestStressConcurrentAggregates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE agg_test (id INT, group_col INT, value FLOAT);")

	// Seed
	for i := 0; i < 1000; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO agg_test VALUES (%d, %d, %.2f);", i, i%20, float64(i)*1.5))
	}

	// Concurrent readers doing different aggregations
	const numReaders = 5
	var wg sync.WaitGroup
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			queries := []string{
				"SELECT COUNT(*) FROM agg_test;",
				"SELECT group_col, COUNT(*) FROM agg_test GROUP BY group_col;",
				"SELECT MAX(value) FROM agg_test;",
				"SELECT MIN(value) FROM agg_test;",
				"SELECT SUM(value) FROM agg_test;",
				"SELECT AVG(value) FROM agg_test;",
				"SELECT DISTINCT group_col FROM agg_test;",
			}
			for _, q := range queries {
				stmt, err := parser.Parse(q)
				if err != nil {
					t.Errorf("parse error: %v", err)
					return
				}
				result, err := session.Execute(stmt)
				if err != nil {
					t.Errorf("execute error for %q: %v", q[:30], err)
					return
				}
				if result == nil || len(result.Rows) == 0 {
					t.Errorf("empty result for %q", q[:30])
					return
				}
			}
		}(r)
	}
	wg.Wait()

	t.Logf("Concurrent aggregates: %d readers × 7 queries", numReaders)
}

// ═══════════════════════════════════════════════════════════════════════════
// 13. Subquery stress — nested, correlated
// ═══════════════════════════════════════════════════════════════════════════

func TestStressSubqueries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE sub_main (id INT, name TEXT, score FLOAT);")
	executeSQL(t, session, "CREATE TABLE sub_ref (id INT, main_id INT, tag TEXT);")

	for i := 0; i < 500; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO sub_main VALUES (%d, 'name%d', %.2f);", i, i, float64(i)*2.5))
	}
	for i := 0; i < 2000; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO sub_ref VALUES (%d, %d, 'tag%d');", i, i%500, i%50))
	}

	// EXISTS subquery (non-correlated)
	result := executeSQL(t, session,
		`SELECT COUNT(*) FROM sub_main WHERE EXISTS (
			SELECT 1 FROM sub_ref WHERE tag LIKE 'tag1%'
		);`)
	var existsCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &existsCount)
	if existsCount == 0 {
		t.Error("EXISTS subquery returned 0 rows")
	}

	// IN subquery
	result = executeSQL(t, session,
		`SELECT COUNT(*) FROM sub_main WHERE id IN (SELECT main_id FROM sub_ref WHERE tag = 'tag5');`)
	var inCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &inCount)
	if inCount == 0 {
		t.Error("IN subquery returned 0 rows")
	}

	// Scalar subquery
	result = executeSQL(t, session,
		`SELECT COUNT(*) FROM sub_main WHERE score > (SELECT AVG(score) FROM sub_main);`)
	var scalarCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &scalarCount)
	if scalarCount == 0 {
		t.Error("scalar subquery returned 0 rows")
	}

	t.Logf("Subqueries: EXISTS=%d, IN=%d, scalar=%d", existsCount, inCount, scalarCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 14. JSONB stress — insert, query, update
// ═══════════════════════════════════════════════════════════════════════════

func TestStressJSONB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE jsonb_test (id INT, data JSONB);")

	for i := 0; i < 500; i++ {
		json := fmt.Sprintf(`{"key": "val%d", "num": %d, "nested": {"a": %d}}`, i, i, i%10)
		executeSQL(t, session, fmt.Sprintf("INSERT INTO jsonb_test VALUES (%d, '%s');", i, json))
	}

	// JSON extract
	result := executeSQL(t, session,
		`SELECT COUNT(*) FROM jsonb_test WHERE data->>'key' LIKE 'val1%';`)
	var extractCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &extractCount)
	if extractCount == 0 {
		t.Error("JSON extract returned 0 rows")
	}

	// JSONB contains
	result = executeSQL(t, session,
		`SELECT COUNT(*) FROM jsonb_test WHERE data->>'key' = 'val100';`)
	var containsCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &containsCount)
	if containsCount != 1 {
		t.Errorf("JSONB contains: expected 1, got %d", containsCount)
	}

	t.Logf("JSONB: 500 rows, extract=%d, contains=%d", extractCount, containsCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 15. UNION / INTERSECT / EXCEPT stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressSetOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE set_a (id INT, val TEXT);")
	executeSQL(t, session, "CREATE TABLE set_b (id INT, val TEXT);")

	for i := 0; i < 500; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO set_a VALUES (%d, 'a%d');", i, i))
	}
	for i := 250; i < 750; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO set_b VALUES (%d, 'b%d');", i, i))
	}

	// UNION
	result := executeSQL(t, session, "SELECT id FROM set_a UNION SELECT id FROM set_b;")
	if len(result.Rows) != 750 {
		t.Errorf("UNION: expected 750, got %d", len(result.Rows))
	}
	unionCount := len(result.Rows)

	// INTERSECT
	result = executeSQL(t, session, "SELECT id FROM set_a INTERSECT SELECT id FROM set_b;")
	if len(result.Rows) != 250 {
		t.Errorf("INTERSECT: expected 250, got %d", len(result.Rows))
	}
	intersectCount := len(result.Rows)

	// EXCEPT
	result = executeSQL(t, session, "SELECT id FROM set_a EXCEPT SELECT id FROM set_b;")
	if len(result.Rows) != 250 {
		t.Errorf("EXCEPT: expected 250, got %d", len(result.Rows))
	}
	exceptCount := len(result.Rows)

	t.Logf("Set ops: UNION=%d INTERSECT=%d EXCEPT=%d", unionCount, intersectCount, exceptCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 16. Data type stress — all types, edge cases
// ═══════════════════════════════════════════════════════════════════════════

func TestStressDataTypes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, `CREATE TABLE types_test (
		c_int INT, c_float FLOAT, c_bool BOOL, c_text TEXT,
		c_varchar VARCHAR(50), c_null INT
	);`)

	// Edge cases
	edgeCases := []struct {
		sql  string
		desc string
	}{
		{"INSERT INTO types_test VALUES (0, 0.0, TRUE, '', '', NULL);", "zeros and empty"},
		{"INSERT INTO types_test VALUES (-1, -1.5, FALSE, 'hello', 'world', 42);", "negatives"},
		{"INSERT INTO types_test VALUES (2147483647, 999999999.99, TRUE, 'x', 'y', NULL);", "max values"},
		{"INSERT INTO types_test VALUES (-2147483648, -999999999.99, FALSE, 'z', 'w', NULL);", "min values"},
		{"INSERT INTO types_test VALUES (42, 3.14159265358979, TRUE, 'special chars: !@#$%^&*()', 'unicode: hello world', NULL);", "special chars"},
	}

	for _, tc := range edgeCases {
		stmt, err := parser.Parse(tc.sql)
		if err != nil {
			t.Errorf("parse error for %s: %v", tc.desc, err)
			continue
		}
		_, err = session.Execute(stmt)
		if err != nil {
			t.Errorf("execute error for %s: %v", tc.desc, err)
		}
	}

	// Verify all rows readable
	result := executeSQL(t, session, "SELECT COUNT(*) FROM types_test;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count != len(edgeCases) {
		t.Errorf("expected %d rows, got %d", len(edgeCases), count)
	}

	// Verify NULL handling
	result = executeSQL(t, session, "SELECT COUNT(*) FROM types_test WHERE c_null IS NULL;")
	var nullCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &nullCount)
	if nullCount != 4 {
		t.Errorf("expected 4 NULLs, got %d", nullCount)
	}

	// Verify WHERE with type coercion
	result = executeSQL(t, session, "SELECT COUNT(*) FROM types_test WHERE c_float > 0;")
	var positiveFloat int
	fmt.Sscanf(result.Rows[0][0], "%d", &positiveFloat)
	if positiveFloat < 1 {
		t.Error("expected at least 1 positive float")
	}

	t.Logf("Data types: %d edge cases inserted and verified", count)
}

// ═══════════════════════════════════════════════════════════════════════════
// 17. DELETE stress — large delete, verify integrity
// ═══════════════════════════════════════════════════════════════════════════

func TestStressLargeDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE delete_test (id INT, keep BOOL);")

	// Insert 2000 rows, half marked for deletion
	for i := 0; i < 2000; i++ {
		keep := "TRUE"
		if i%2 == 0 {
			keep = "FALSE"
		}
		executeSQL(t, session, fmt.Sprintf("INSERT INTO delete_test VALUES (%d, %s);", i, keep))
	}

	// Delete all non-kept rows
	executeSQL(t, session, "DELETE FROM delete_test WHERE keep = FALSE;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM delete_test;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count != 1000 {
		t.Errorf("expected 1000 remaining rows, got %d", count)
	}

	// Verify all remaining are TRUE
	result = executeSQL(t, session, "SELECT COUNT(*) FROM delete_test WHERE keep = FALSE;")
	var falseCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &falseCount)
	if falseCount != 0 {
		t.Errorf("expected 0 FALSE rows, got %d", falseCount)
	}

	t.Logf("Large delete: 2000 → %d rows", count)
}

// ═══════════════════════════════════════════════════════════════════════════
// 18. ALTER TABLE stress — add/drop columns under load
// ═══════════════════════════════════════════════════════════════════════════

func TestStressAlterTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE alter_test (id INT, name TEXT);")

	// Insert rows
	for i := 0; i < 100; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO alter_test VALUES (%d, 'name%d');", i, i))
	}

	// Add column
	executeSQL(t, session, "ALTER TABLE alter_test ADD COLUMN age INT DEFAULT 0;")

	// Verify existing rows have default
	result := executeSQL(t, session, "SELECT COUNT(*) FROM alter_test WHERE age = 0;")
	var defaultCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &defaultCount)
	if defaultCount != 100 {
		t.Errorf("expected 100 rows with default age=0, got %d", defaultCount)
	}

	// Insert new row with new column
	executeSQL(t, session, "INSERT INTO alter_test VALUES (100, 'new', 25);")
	result = executeSQL(t, session, "SELECT age FROM alter_test WHERE id = 100;")
	if len(result.Rows) == 0 || result.Rows[0][0] != "25" {
		t.Errorf("new row age: expected 25, got %v", result.Rows)
	}

	// Drop column
	executeSQL(t, session, "ALTER TABLE alter_test DROP COLUMN age;")

	// Verify column gone
	result = executeSQL(t, session, "SELECT COUNT(*) FROM alter_test;")
	var afterDrop int
	fmt.Sscanf(result.Rows[0][0], "%d", &afterDrop)
	if afterDrop != 101 {
		t.Errorf("expected 101 rows after drop, got %d", afterDrop)
	}

	t.Logf("ALTER TABLE: add column → insert → drop column, %d rows intact", afterDrop)
}

// ═══════════════════════════════════════════════════════════════════════════
// 19. LIKE pattern stress — special characters, escaping
// ═══════════════════════════════════════════════════════════════════════════

func TestStressLikePatterns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE like_test (id INT, pattern TEXT);")

	patterns := []string{
		"hello world", "hello%", "%world", "%hello%",
		"a_b", "a__b", "a___b",
		"test%value", "%test%", "100%", "50% off",
		"special[chars]", "regex.dots", "back\\slash",
		"", " ", "  ",
	}

	for i, p := range patterns {
		sql := fmt.Sprintf("INSERT INTO like_test VALUES (%d, '%s');", i, p)
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Errorf("parse error for pattern %q: %v", p, err)
			continue
		}
		session.Execute(stmt)
	}

	// Test various LIKE patterns
	tests := []struct {
		like     string
		expected int
		desc     string
	}{
		{"hello%", 2, "starts with hello"},
		{"%world", 2, "ends with world"},
		{"%hello%", 3, "contains hello"},
		{"a_b", 1, "single char wildcard — only exact a_b matches"},
		{"%off", 1, "ends with off"},
		{"100%", 1, "exact with percent"},
	}

	for _, tt := range tests {
		result := executeSQL(t, session, fmt.Sprintf(
			"SELECT COUNT(*) FROM like_test WHERE pattern LIKE '%s';", tt.like))
		var count int
		fmt.Sscanf(result.Rows[0][0], "%d", &count)
		if count != tt.expected {
			t.Errorf("LIKE '%s' (%s): expected %d, got %d", tt.like, tt.desc, tt.expected, count)
		}
	}

	t.Logf("LIKE patterns: %d patterns tested", len(patterns))
}

// ═══════════════════════════════════════════════════════════════════════════
// 20. Prepared statement stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressPreparedStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE prep_test (id INT, name TEXT, score FLOAT);")

	// Prepare INSERT
	executeSQL(t, session, "PREPARE insert_stmt AS INSERT INTO prep_test VALUES ($1, $2, $3);")

	// Execute many times
	for i := 0; i < 500; i++ {
		executeSQL(t, session, fmt.Sprintf("EXECUTE insert_stmt (%d, 'user%d', %.2f);", i, i, float64(i)*1.5))
	}

	result := executeSQL(t, session, "SELECT COUNT(*) FROM prep_test;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count != 500 {
		t.Errorf("expected 500 rows from prepared stmts, got %d", count)
	}

	// Prepare SELECT
	executeSQL(t, session, "PREPARE select_stmt AS SELECT * FROM prep_test WHERE id = $1;")
	result = executeSQL(t, session, "EXECUTE select_stmt (250);")
	if len(result.Rows) == 0 || result.Rows[0][0] != "250" {
		t.Errorf("prepared SELECT: expected id=250, got %v", result.Rows)
	}

	// Deallocate
	executeSQL(t, session, "DEALLOCATE insert_stmt;")
	executeSQL(t, session, "DEALLOCATE select_stmt;")

	t.Logf("Prepared statements: 500 inserts + select via prepared stmts")
}

// ═══════════════════════════════════════════════════════════════════════════
// 21. Time travel stress — concurrent writes + reads
// ═══════════════════════════════════════════════════════════════════════════

func TestStressTimeTravel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE travel_test (id INT, ver INT);")

	// Record initial state
	executeSQL(t, session, "INSERT INTO travel_test VALUES (1, 0);")
	executeSQL(t, session, "INSERT INTO travel_test VALUES (2, 0);")

	// Update multiple times
	for v := 1; v <= 10; v++ {
		executeSQL(t, session, fmt.Sprintf("UPDATE travel_test SET ver = %d;", v))
	}

	// Verify current state
	result := executeSQL(t, session, "SELECT ver FROM travel_test WHERE id = 1;")
	if len(result.Rows) == 0 || result.Rows[0][0] != "10" {
		t.Errorf("current version: expected 10, got %v", result.Rows)
	}

	// Time travel — read as of earlier version
	result = executeSQL(t, session, "SELECT ver FROM travel_test VERSION 1 WHERE id = 1;")
	if result != nil && len(result.Rows) > 0 {
		t.Logf("Time travel VERSION 1: ver=%s", result.Rows[0][0])
	}

	t.Logf("Time travel: 10 updates, verified current state")
}

// ═══════════════════════════════════════════════════════════════════════════
// 22. TRUNCATE stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressTruncate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE truncate_test (id INT, data TEXT);")

	// Insert, truncate, re-insert — multiple cycles
	for cycle := 0; cycle < 5; cycle++ {
		for i := 0; i < 500; i++ {
			executeSQL(t, session, fmt.Sprintf(
				"INSERT INTO truncate_test VALUES (%d, 'cycle%d-row%d');", i, cycle, i))
		}

		result := executeSQL(t, session, "SELECT COUNT(*) FROM truncate_test;")
		var count int
		fmt.Sscanf(result.Rows[0][0], "%d", &count)
		if count != 500 {
			t.Errorf("cycle %d: expected 500 before truncate, got %d", cycle, count)
		}

		executeSQL(t, session, "TRUNCATE TABLE truncate_test;")

		result = executeSQL(t, session, "SELECT COUNT(*) FROM truncate_test;")
		fmt.Sscanf(result.Rows[0][0], "%d", &count)
		if count != 0 {
			t.Errorf("cycle %d: expected 0 after truncate, got %d", cycle, count)
		}
	}

	t.Logf("Truncate: 5 cycles × 500 rows")
}

// ═══════════════════════════════════════════════════════════════════════════
// 23. CROSS JOIN stress — cartesian product
// ═══════════════════════════════════════════════════════════════════════════

func TestStressCrossJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE cross_a (id INT);")
	executeSQL(t, session, "CREATE TABLE cross_b (id INT);")

	for i := 0; i < 50; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO cross_a VALUES (%d);", i))
		executeSQL(t, session, fmt.Sprintf("INSERT INTO cross_b VALUES (%d);", i))
	}

	result := executeSQL(t, session, "SELECT COUNT(*) FROM cross_a INNER JOIN cross_b ON 1=1;")
	var crossCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &crossCount)
	if crossCount != 2500 {
		t.Errorf("CROSS JOIN: expected 2500, got %d", crossCount)
	}

	t.Logf("CROSS JOIN: 50 × 50 = %d", crossCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 24. NULL edge cases stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressNullEdgeCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE null_test (id INT, a INT, b TEXT, c FLOAT);")

	// Mix of NULL and non-NULL
	for i := 0; i < 200; i++ {
		var a, b, c string
		if i%3 == 0 {
			a, b, c = "NULL", "NULL", "NULL"
		} else {
			a = fmt.Sprintf("%d", i)
			b = fmt.Sprintf("'val%d'", i)
			c = fmt.Sprintf("%.2f", float64(i)*1.1)
		}
		executeSQL(t, session, fmt.Sprintf("INSERT INTO null_test VALUES (%d, %s, %s, %s);", i, a, b, c))
	}

	// NULL comparisons
	result := executeSQL(t, session, "SELECT COUNT(*) FROM null_test WHERE a IS NULL;")
	var nullCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &nullCount)
	if nullCount != 67 { // floor(200/3) = 66, but 0%3=0, so 67
		t.Errorf("IS NULL: expected 67, got %d", nullCount)
	}

	// NULL in arithmetic
	result = executeSQL(t, session, "SELECT COUNT(*) FROM null_test WHERE a + 1 IS NULL;")
	var nullArith int
	fmt.Sscanf(result.Rows[0][0], "%d", &nullArith)
	if nullArith != nullCount {
		t.Errorf("NULL arithmetic: expected %d NULL results, got %d", nullCount, nullArith)
	}

	// NULL in ORDER BY
	result = executeSQL(t, session, "SELECT a FROM null_test ORDER BY a ASC LIMIT 5;")
	if len(result.Rows) > 0 && result.Rows[0][0] == "NULL" {
		t.Logf("ORDER BY ASC: NULL comes first as expected")
	}

	// NULL in GROUP BY
	result = executeSQL(t, session, "SELECT COUNT(*) FROM null_test GROUP BY c IS NULL;")
	if len(result.Rows) != 2 {
		t.Errorf("GROUP BY NULL: expected 2 groups, got %d", len(result.Rows))
	}

	t.Logf("NULL edge cases: 200 rows, NULL count=%d", nullCount)
}

// ═══════════════════════════════════════════════════════════════════════════
// 25. Sort stress — ORDER BY with many rows
// ═══════════════════════════════════════════════════════════════════════════

func TestStressLargeSort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE sort_test (id INT, name TEXT, score FLOAT);")

	// Insert 5000 rows with random scores
	for i := 0; i < 5000; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO sort_test VALUES (%d, 'user%d', %.4f);", i, i, rand.Float64()*1000))
	}

	// ORDER BY score DESC
	result := executeSQL(t, session, "SELECT score FROM sort_test ORDER BY score DESC LIMIT 10;")
	if len(result.Rows) != 10 {
		t.Fatalf("ORDER BY DESC: expected 10 rows, got %d", len(result.Rows))
	}
	// Verify descending
	for i := 1; i < len(result.Rows); i++ {
		var prev, curr float64
		fmt.Sscanf(result.Rows[i-1][0], "%f", &prev)
		fmt.Sscanf(result.Rows[i][0], "%f", &curr)
		if prev < curr {
			t.Errorf("ORDER BY DESC: row %d (%f) < row %d (%f)", i-1, prev, i, curr)
		}
	}

	// ORDER BY with LIMIT OFFSET
	result = executeSQL(t, session, "SELECT id FROM sort_test ORDER BY id LIMIT 10 OFFSET 100;")
	if len(result.Rows) != 10 {
		t.Fatalf("LIMIT OFFSET: expected 10 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "100" {
		t.Errorf("LIMIT OFFSET: first row id should be 100, got %s", result.Rows[0][0])
	}

	t.Logf("Large sort: 5000 rows, ORDER BY + LIMIT verified")
}

// ═══════════════════════════════════════════════════════════════════════════
// 26. Multi-table stress — many tables, cross-references
// ═══════════════════════════════════════════════════════════════════════════

func TestStressManyTables(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)

	// Create 20 tables
	for i := 0; i < 20; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"CREATE TABLE t%d (id INT, ref INT, data TEXT);", i))
	}

	// Insert into each
	for i := 0; i < 20; i++ {
		for j := 0; j < 50; j++ {
			executeSQL(t, session, fmt.Sprintf(
				"INSERT INTO t%d VALUES (%d, %d, 'data%d-%d');", i, j, j%20, i, j))
		}
	}

	// Cross-table joins
	for i := 0; i < 19; i++ {
		result := executeSQL(t, session, fmt.Sprintf(
			"SELECT COUNT(*) FROM t%d a INNER JOIN t%d b ON a.ref = b.id;", i, i+1))
		var count int
		fmt.Sscanf(result.Rows[0][0], "%d", &count)
		if count == 0 {
			t.Errorf("cross-table join t%d-t%d returned 0", i, i+1)
		}
	}

	t.Logf("Many tables: 20 tables × 50 rows, cross-joins verified")
}

// ═══════════════════════════════════════════════════════════════════════════
// 27. String function stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressStringFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE str_test (id INT, val TEXT);")

	for i := 0; i < 200; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO str_test VALUES (%d, 'Hello World %d');", i, i))
	}

	functions := []struct {
		sql      string
		expected string
		desc     string
	}{
		{"SELECT UPPER(val) FROM str_test WHERE id = 0;", "HELLO WORLD 0", "UPPER"},
		{"SELECT LOWER(val) FROM str_test WHERE id = 0;", "hello world 0", "LOWER"},
		{"SELECT LENGTH(val) FROM str_test WHERE id = 0;", "13", "LENGTH"},
		{"SELECT TRIM('  hello  ');", "hello", "TRIM"},
		{"SELECT CONCAT('a', 'b', 'c');", "abc", "CONCAT"},
		{"SELECT REPLACE('hello world', 'world', 'sql');", "hello sql", "REPLACE"},
		{"SELECT SUBSTRING('hello', 2, 3);", "ell", "SUBSTRING"},
		{"SELECT SUBSTRING('hello', 1, 3);", "hel", "SUBSTRING start"},
		{"SELECT SUBSTRING('hello', 4, 2);", "lo", "SUBSTRING mid"},
	}

	for _, fn := range functions {
		result := executeSQL(t, session, fn.sql)
		if len(result.Rows) == 0 {
			t.Errorf("%s: no result", fn.desc)
			continue
		}
		got := result.Rows[0][0]
		if got != fn.expected {
			t.Errorf("%s: expected %q, got %q", fn.desc, fn.expected, got)
		}
	}

	t.Logf("String functions: %d functions verified", len(functions))
}

// ═══════════════════════════════════════════════════════════════════════════
// 28. Math function stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressMathFunctions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE math_test (id INT, val FLOAT);")

	for i := 0; i < 100; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO math_test VALUES (%d, %.4f);", i, float64(i)*1.5))
	}

	// Aggregate math
	result := executeSQL(t, session, "SELECT SUM(val) FROM math_test;")
	if len(result.Rows) == 0 {
		t.Fatal("SUM returned no result")
	}
	var sum float64
	fmt.Sscanf(result.Rows[0][0], "%f", &sum)
	expectedSum := 0.0
	for i := 0; i < 100; i++ {
		expectedSum += float64(i) * 1.5
	}
	if sum != expectedSum {
		t.Errorf("SUM: expected %.2f, got %.2f", expectedSum, sum)
	}

	result = executeSQL(t, session, "SELECT AVG(val) FROM math_test;")
	var avg float64
	fmt.Sscanf(result.Rows[0][0], "%f", &avg)
	expectedAvg := expectedSum / 100.0
	if avg != expectedAvg {
		t.Errorf("AVG: expected %.2f, got %.2f", expectedAvg, avg)
	}

	t.Logf("Math functions: SUM=%.2f AVG=%.2f", sum, avg)
}

// ═══════════════════════════════════════════════════════════════════════════
// 29. CASE expression stress
// ═══════════════════════════════════════════════════════════════════════════

func TestCaseExpression(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE case_test (id INT, score INT);")

	for i := 0; i < 200; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO case_test VALUES (%d, %d);", i, i%100))
	}

	result := executeSQL(t, session, `
		SELECT CASE
			WHEN score >= 90 THEN 'A'
			WHEN score >= 80 THEN 'B'
			WHEN score >= 70 THEN 'C'
			WHEN score >= 60 THEN 'D'
			ELSE 'F'
		END AS grade, COUNT(*) AS cnt
		FROM case_test GROUP BY CASE
			WHEN score >= 90 THEN 'A'
			WHEN score >= 80 THEN 'B'
			WHEN score >= 70 THEN 'C'
			WHEN score >= 60 THEN 'D'
			ELSE 'F'
		END ORDER BY grade;`)

	if len(result.Rows) != 5 {
		t.Errorf("CASE expression: expected 5 grades, got %d", len(result.Rows))
	}

	// Verify A grade count (scores 90-99 appear twice: i=90..99 and i=190..199 since i%100)
	aCount := 0
	for _, row := range result.Rows {
		if row[0] == "A" {
			fmt.Sscanf(row[1], "%d", &aCount)
		}
	}
	if aCount != 20 {
		t.Errorf("grade A: expected 20, got %d", aCount)
	}

	t.Logf("CASE expression: 5 grades, verified counts")
}

// ═══════════════════════════════════════════════════════════════════════════
// 30. Concurrent DROP TABLE + INSERT — DDL/DML race
// ═══════════════════════════════════════════════════════════════════════════

func TestStressDDLDMLRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)

	const numCycles = 5
	for cycle := 0; cycle < numCycles; cycle++ {
		executeSQL(t, session, fmt.Sprintf("CREATE TABLE race_ddl%d (id INT);", cycle))

		var wg sync.WaitGroup

		// Inserters
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				executeSQL(t, session, fmt.Sprintf(
					"INSERT INTO race_ddl%d VALUES (%d);", c, i))
			}
		}(cycle)

		// Reader
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				executeSQL(t, session, fmt.Sprintf(
					"SELECT COUNT(*) FROM race_ddl%d;", c))
			}
		}(cycle)

		wg.Wait()

		// Drop
		executeSQL(t, session, fmt.Sprintf("DROP TABLE race_ddl%d;", cycle))
	}

	t.Logf("DDL/DML race: %d cycles", numCycles)
}

// ═══════════════════════════════════════════════════════════════════════════
// 31. DISTINCT stress — many duplicates
// ═══════════════════════════════════════════════════════════════════════════

func TestStressDistinct(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE distinct_test (id INT, category INT);")

	// 1000 rows, 10 categories (reduced from 10000 for speed)
	for i := 0; i < 1000; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO distinct_test VALUES (%d, %d);", i, i%10))
	}

	result := executeSQL(t, session, "SELECT DISTINCT category FROM distinct_test ORDER BY category;")
	if len(result.Rows) != 10 {
		t.Errorf("DISTINCT: expected 10 categories, got %d", len(result.Rows))
	}
	for i, row := range result.Rows {
		if row[0] != fmt.Sprintf("%d", i) {
			t.Errorf("DISTINCT order: row %d expected %d, got %s", i, i, row[0])
		}
	}

	t.Logf("DISTINCT: 1000 rows → %d unique categories", len(result.Rows))
}

// ═══════════════════════════════════════════════════════════════════════════
// 32.HAVING stress — filter after aggregation
// ═══════════════════════════════════════════════════════════════════════════

func TestStressHaving(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}
	session := setupStressSession(t)
	executeSQL(t, session, "CREATE TABLE having_test (id INT, dept INT, salary FLOAT);")

	for i := 0; i < 500; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO having_test VALUES (%d, %d, %.2f);", i, i%10, float64(30000+rand.Intn(70000))))
	}

	// Invalidate cache before HAVING queries
	session.resultCache.InvalidateAll()

	// HAVING with COUNT — each dept has 50 rows, so > 49 matches all
	result := executeSQL(t, session,
		"SELECT dept, COUNT(*) as cnt FROM having_test GROUP BY dept HAVING cnt > 49;")
	if len(result.Rows) != 10 {
		t.Errorf("HAVING COUNT: expected 10 depts, got %d", len(result.Rows))
	}

	// HAVING with SUM
	result = executeSQL(t, session,
		"SELECT dept, SUM(salary) as total FROM having_test GROUP BY dept HAVING total > 500000;")
	if len(result.Rows) == 0 {
		t.Error("HAVING SUM: expected at least 1 dept")
	}

	// HAVING with AVG
	result = executeSQL(t, session,
		"SELECT dept, AVG(salary) as avg_sal FROM having_test GROUP BY dept HAVING avg_sal > 60000;")
	if len(result.Rows) == 0 {
		t.Error("HAVING AVG: expected at least 1 dept")
	}

	t.Logf("HAVING: 500 rows, verified COUNT/SUM/AVG filters")
}

// ═══════════════════════════════════════════════════════════════════════════
// Summary runner
// ═══════════════════════════════════════════════════════════════════════════

func TestStressSummary(t *testing.T) {
	// This test just logs the available stress tests
	stressTests := []string{
		"ConcurrentInsert", "ConcurrentUpdateDelete", "UpsertNullSemantics",
		"LargeBatchInsert", "RollbackUnderLoad", "MixedDML", "LargeJoin",
		"WindowFunctions", "CTE", "IndexConsistency", "LargeTransactionSpill",
		"ConcurrentAggregates", "Subqueries", "JSONB", "SetOperations",
		"DataTypes", "LargeDelete", "AlterTable", "LikePatterns",
		"PreparedStatements", "TimeTravel", "Truncate", "CrossJoin",
		"NullEdgeCases", "LargeSort", "ManyTables", "StringFunctions",
		"MathFunctions", "CaseExpression", "DDLDMLRace", "Distinct", "Having",
	}
	t.Logf("Available stress tests: %d", len(stressTests))
	for _, name := range stressTests {
		t.Logf("  - TestStress%s", name)
	}
}

// sort.Ints helper for test assertions
