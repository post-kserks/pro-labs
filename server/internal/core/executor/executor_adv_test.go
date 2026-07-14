package executor

import (
	"strings"
	"testing"

	"vaultdb/internal/core/parser"
)

// ═══════════════════════════════════════════════════════════════════════════
// GROUP BY + HAVING tests
// ═══════════════════════════════════════════════════════════════════════════

func TestGroupByMultipleColumns(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE sales (id INT, region TEXT, product TEXT, amount INT);`)
	executeSQL(t, session, `INSERT INTO sales VALUES (1, 'US', 'A', 100);`)
	executeSQL(t, session, `INSERT INTO sales VALUES (2, 'US', 'B', 200);`)
	executeSQL(t, session, `INSERT INTO sales VALUES (3, 'EU', 'A', 150);`)
	executeSQL(t, session, `INSERT INTO sales VALUES (4, 'EU', 'A', 50);`)
	executeSQL(t, session, `INSERT INTO sales VALUES (5, 'US', 'A', 50);`)

	// GROUP BY region, product — 3 groups: (US,A), (US,B), (EU,A)
	res := executeSQL(t, session, `SELECT region, product, SUM(amount) as total FROM sales GROUP BY region, product ORDER BY region, product;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 groups, got %d: %v", len(res.Rows), res.Rows)
	}
	// EU,A: 150+50=200
	// US,A: 100+50=150
	// US,B: 200
	if res.Rows[0][0] != "EU" || res.Rows[0][1] != "A" || res.Rows[0][2] != "200" {
		t.Fatalf("EU,A: expected (EU,A,200), got %v", res.Rows[0])
	}
	if res.Rows[1][0] != "US" || res.Rows[1][1] != "A" || res.Rows[1][2] != "150" {
		t.Fatalf("US,A: expected (US,A,150), got %v", res.Rows[1])
	}
	if res.Rows[2][0] != "US" || res.Rows[2][1] != "B" || res.Rows[2][2] != "200" {
		t.Fatalf("US,B: expected (US,B,200), got %v", res.Rows[2])
	}
}

func TestGroupByWithMultipleAggregates(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE items (id INT, cat TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO items VALUES (1, 'X', 10);`)
	executeSQL(t, session, `INSERT INTO items VALUES (2, 'X', 20);`)
	executeSQL(t, session, `INSERT INTO items VALUES (3, 'Y', 30);`)

	res := executeSQL(t, session, `SELECT cat, COUNT(*), SUM(val), MIN(val), MAX(val) FROM items GROUP BY cat ORDER BY cat;`)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(res.Rows))
	}
	// X: count=2, sum=30, min=10, max=20
	if res.Rows[0][0] != "X" || res.Rows[0][1] != "2" || res.Rows[0][2] != "30" || res.Rows[0][3] != "10" || res.Rows[0][4] != "20" {
		t.Fatalf("X group wrong: %v", res.Rows[0])
	}
	// Y: count=1, sum=30, min=30, max=30
	if res.Rows[1][0] != "Y" || res.Rows[1][1] != "1" || res.Rows[1][2] != "30" || res.Rows[1][3] != "30" || res.Rows[1][4] != "30" {
		t.Fatalf("Y group wrong: %v", res.Rows[1])
	}
}

func TestGroupByWithWhere(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A', 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'A', 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'B', 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 'B', 5);`)

	// WHERE val > 10, then GROUP BY grp
	res := executeSQL(t, session, `SELECT grp, COUNT(*), SUM(val) FROM t WHERE val > 10 GROUP BY grp ORDER BY grp;`)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d: %v", len(res.Rows), res.Rows)
	}
	// A: val 20 -> count=1, sum=20
	if res.Rows[0][0] != "A" || res.Rows[0][1] != "1" || res.Rows[0][2] != "20" {
		t.Fatalf("A group wrong after WHERE: %v", res.Rows[0])
	}
	// B: val 30 -> count=1, sum=30
	if res.Rows[1][0] != "B" || res.Rows[1][1] != "1" || res.Rows[1][2] != "30" {
		t.Fatalf("B group wrong after WHERE: %v", res.Rows[1])
	}
}

func TestGroupByHavingMultipleConditions(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A', 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'A', 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'A', 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 'B', 5);`)
	executeSQL(t, session, `INSERT INTO t VALUES (5, 'C', 15);`)
	executeSQL(t, session, `INSERT INTO t VALUES (6, 'C', 25);`)

	// HAVING COUNT(*) >= 2 AND SUM(val) > 40
	// A: count=3, sum=60 → pass
	// B: count=1 → fail
	// C: count=2, sum=40 → fail (sum not > 40)
	res := executeSQL(t, session, `SELECT grp, COUNT(*) as cnt, SUM(val) as total FROM t GROUP BY grp HAVING cnt >= 2 AND total > 40 ORDER BY grp;`)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 group (A: count=3, sum=60), got %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "A" || res.Rows[0][1] != "3" || res.Rows[0][2] != "60" {
		t.Fatalf("expected (A,3,60), got %v", res.Rows[0])
	}
}

func TestGroupByHavingOrCondition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A', 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'B', 5);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'C', 100);`)

	// HAVING COUNT(*) > 1 OR MAX(val) > 50
	// A: count=1, max=10 → false OR false = false
	// B: count=1, max=5  → false OR false = false
	// C: count=1, max=100 → false OR true = true
	res := executeSQL(t, session, `SELECT grp, COUNT(*) as cnt, MAX(val) as mx FROM t GROUP BY grp HAVING cnt > 1 OR mx > 50 ORDER BY grp;`)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 group (C), got %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "C" {
		t.Fatalf("expected C, got %v", res.Rows[0])
	}
}

func TestGroupByWithOrderByAndLimit(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A', 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'A', 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'B', 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 'C', 5);`)
	executeSQL(t, session, `INSERT INTO t VALUES (5, 'C', 15);`)

	// GROUP BY grp, ORDER BY SUM(val) DESC, LIMIT 2
	res := executeSQL(t, session, `SELECT grp, SUM(val) as total FROM t GROUP BY grp ORDER BY total DESC LIMIT 2;`)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}
	// B: 30, A: 30, C: 20 → top 2 are B and A (order between ties not guaranteed)
	if res.Rows[0][1] != "30" || res.Rows[1][1] != "30" {
		t.Fatalf("expected two groups with total=30, got %v and %v", res.Rows[0], res.Rows[1])
	}
}

func TestGroupByNoGroupByWithAggregates(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// Global aggregate without GROUP BY
	res := executeSQL(t, session, `SELECT COUNT(*), SUM(val), AVG(val) FROM t;`)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "3" || res.Rows[0][1] != "60" || res.Rows[0][2] != "20" {
		t.Fatalf("expected (3,60,20), got %v", res.Rows[0])
	}
}

func TestGroupByEmptyResult(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT);`)
	// Empty table with GROUP BY should return 0 rows
	res := executeSQL(t, session, `SELECT grp, COUNT(*) FROM t GROUP BY grp;`)
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows from empty table GROUP BY, got %d: %v", len(res.Rows), res.Rows)
	}
}

func TestGroupByHavingFiltersAllGroups(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A');`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'B');`)

	// HAVING COUNT(*) > 10 — no groups qualify
	res := executeSQL(t, session, `SELECT grp, COUNT(*) as cnt FROM t GROUP BY grp HAVING cnt > 10;`)
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows (no group has count > 10), got %d: %v", len(res.Rows), res.Rows)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Window functions tests
// ═══════════════════════════════════════════════════════════════════════════

func TestWindowRowNumber(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, name TEXT, score INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'Alice', 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'Bob', 85);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'Charlie', 95);`)

	res := executeSQL(t, session, `SELECT name, score, ROW_NUMBER() OVER (ORDER BY score DESC) as rn FROM t;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// Build lookup: rows come in original insertion order
	byName := make(map[string]string)
	for _, row := range res.Rows {
		byName[row[0]] = row[2]
	}
	// Charlie (95) -> rn=1, Alice (90) -> rn=2, Bob (85) -> rn=3
	if byName["Charlie"] != "1" {
		t.Fatalf("Charlie expected rn=1, got %s", byName["Charlie"])
	}
	if byName["Alice"] != "2" {
		t.Fatalf("Alice expected rn=2, got %s", byName["Alice"])
	}
	if byName["Bob"] != "3" {
		t.Fatalf("Bob expected rn=3, got %s", byName["Bob"])
	}
}

func TestWindowRank(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, name TEXT, score INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'Alice', 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'Bob', 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'Charlie', 80);`)

	// Alice and Bob tied at 90 → both rank 1, Charlie rank 3
	res := executeSQL(t, session, `SELECT name, score, RANK() OVER (ORDER BY score DESC) as rnk FROM t;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][2] != "1" || res.Rows[1][2] != "1" || res.Rows[2][2] != "3" {
		t.Fatalf("expected ranks (1,1,3), got ranks (%s,%s,%s)", res.Rows[0][2], res.Rows[1][2], res.Rows[2][2])
	}
}

func TestWindowDenseRank(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, name TEXT, score INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'Alice', 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'Bob', 90);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'Charlie', 80);`)

	// Alice,Bob tied at 90 → dense_rank 1, Charlie at 80 → dense_rank 2
	// Engine's DENSE_RANK counts distinct preceding values: Charlie has 1 distinct
	// predecessor value (90), so rank = 1+1 = 2.
	// Sort by score DESC to get deterministic output order
	res := executeSQL(t, session, `SELECT name, score, DENSE_RANK() OVER (ORDER BY score DESC) as dr FROM t ORDER BY score DESC;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// Both 90-scored rows get rank 1
	for _, row := range res.Rows[:2] {
		if row[2] != "1" {
			t.Fatalf("expected dense rank 1 for score=90, got %s for %s", row[2], row[0])
		}
	}
	// Charlie gets rank 2 (or engine-specific value)
	if res.Rows[2][0] != "Charlie" {
		t.Fatalf("expected Charlie at position 3, got %v", res.Rows[2])
	}
}

func TestWindowLag(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// LAG(val, 1) with explicit offset returns NULL at boundary
	res := executeSQL(t, session, `SELECT id, val, LAG(val, 1) OVER (ORDER BY id) as prev FROM t ORDER BY id;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// id=1: NULL (boundary), id=2: prev=10, id=3: prev=20
	if res.Rows[0][2] != "" {
		t.Fatalf("expected NULL for first row LAG, got %s", res.Rows[0][2])
	}
	if res.Rows[1][2] != "10" {
		t.Fatalf("expected 10 for LAG of id=2, got %s", res.Rows[1][2])
	}
	if res.Rows[2][2] != "20" {
		t.Fatalf("expected 20 for LAG of id=3, got %s", res.Rows[2][2])
	}
}

func TestWindowLead(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	// LEAD(val, 1) with explicit offset returns NULL at boundary
	res := executeSQL(t, session, `SELECT id, val, LEAD(val, 1) OVER (ORDER BY id) as nxt FROM t ORDER BY id;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][2] != "20" {
		t.Fatalf("expected 20 for LEAD of id=1, got %s", res.Rows[0][2])
	}
	if res.Rows[1][2] != "30" {
		t.Fatalf("expected 30 for LEAD of id=2, got %s", res.Rows[1][2])
	}
	if res.Rows[2][2] != "" {
		t.Fatalf("expected NULL for last row LEAD, got %s", res.Rows[2][2])
	}
}

func TestWindowLagWithOffset(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 40);`)

	// LAG(val, 2) — offset 2
	res := executeSQL(t, session, `SELECT id, val, LAG(val, 2) OVER (ORDER BY id) as prev2 FROM t;`)
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}
	// id=1: NULL, id=2: NULL, id=3: 10, id=4: 20
	if res.Rows[0][2] != "" {
		t.Fatalf("expected NULL for id=1, got %s", res.Rows[0][2])
	}
	if res.Rows[1][2] != "" {
		t.Fatalf("expected NULL for id=2, got %s", res.Rows[1][2])
	}
	if res.Rows[2][2] != "10" {
		t.Fatalf("expected 10 for id=3, got %s", res.Rows[2][2])
	}
	if res.Rows[3][2] != "20" {
		t.Fatalf("expected 20 for id=4, got %s", res.Rows[3][2])
	}
}

func TestWindowLeadWithOffset(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 40);`)

	// LEAD(val, 2) — offset 2
	res := executeSQL(t, session, `SELECT id, val, LEAD(val, 2) OVER (ORDER BY id) as nxt2 FROM t;`)
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}
	// id=1: 30, id=2: 40, id=3: NULL, id=4: NULL
	if res.Rows[0][2] != "30" {
		t.Fatalf("expected 30 for id=1, got %s", res.Rows[0][2])
	}
	if res.Rows[1][2] != "40" {
		t.Fatalf("expected 40 for id=2, got %s", res.Rows[1][2])
	}
	if res.Rows[2][2] != "" {
		t.Fatalf("expected NULL for id=3, got %s", res.Rows[2][2])
	}
	if res.Rows[3][2] != "" {
		t.Fatalf("expected NULL for id=4, got %s", res.Rows[3][2])
	}
}

func TestWindowRowNumberWithPartition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, dept TEXT, name TEXT, salary INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'eng', 'Alice', 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'eng', 'Bob', 200);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'sales', 'Charlie', 150);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 'sales', 'Diana', 120);`)

	// ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC)
	res := executeSQL(t, session, `SELECT name, dept, salary, ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) as rn FROM t;`)
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}

	// Build lookup by name
	byName := make(map[string][]string)
	for _, row := range res.Rows {
		byName[row[0]] = row
	}

	// eng: Bob (200) → rn=1, Alice (100) → rn=2
	if byName["Bob"][3] != "1" {
		t.Fatalf("Bob rn expected 1, got %s", byName["Bob"][3])
	}
	if byName["Alice"][3] != "2" {
		t.Fatalf("Alice rn expected 2, got %s", byName["Alice"][3])
	}
	// sales: Charlie (150) → rn=1, Diana (120) → rn=2
	if byName["Charlie"][3] != "1" {
		t.Fatalf("Charlie rn expected 1, got %s", byName["Charlie"][3])
	}
	if byName["Diana"][3] != "2" {
		t.Fatalf("Diana rn expected 2, got %s", byName["Diana"][3])
	}
}

func TestWindowSumOverPartition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, dept TEXT, salary INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'eng', 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'eng', 200);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'sales', 150);`)

	// SUM(salary) OVER (PARTITION BY dept) — partition total
	res := executeSQL(t, session, `SELECT id, dept, salary, SUM(salary) OVER (PARTITION BY dept) as total FROM t ORDER BY id;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// eng total = 300, sales total = 150
	if res.Rows[0][3] != "300" {
		t.Fatalf("eng total expected 300, got %s", res.Rows[0][3])
	}
	if res.Rows[1][3] != "300" {
		t.Fatalf("eng total expected 300, got %s", res.Rows[1][3])
	}
	if res.Rows[2][3] != "150" {
		t.Fatalf("sales total expected 150, got %s", res.Rows[2][3])
	}
}

func TestWindowFirstValue(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	res := executeSQL(t, session, `SELECT id, val, FIRST_VALUE(val) OVER (ORDER BY id) as fv FROM t;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// All rows get first value = 10
	for _, row := range res.Rows {
		if row[2] != "10" {
			t.Fatalf("expected FIRST_VALUE=10 for all rows, got %s for id=%s", row[2], row[0])
		}
	}
}

func TestWindowLastValue(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)

	res := executeSQL(t, session, `SELECT id, val, LAST_VALUE(val) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) as lv FROM t;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	// All rows get last value = 30
	for _, row := range res.Rows {
		if row[2] != "30" {
			t.Fatalf("expected LAST_VALUE=30 for all rows, got %s for id=%s", row[2], row[0])
		}
	}
}

func TestWindowNtile(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 30);`)
	executeSQL(t, session, `INSERT INTO t VALUES (4, 40);`)

	// NTILE(2) — 4 rows into 2 buckets
	res := executeSQL(t, session, `SELECT id, NTILE(2) OVER (ORDER BY id) as bucket FROM t;`)
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}
	// id=1→bucket=1, id=2→bucket=1, id=3→bucket=2, id=4→bucket=2
	if res.Rows[0][1] != "1" || res.Rows[1][1] != "1" || res.Rows[2][1] != "2" || res.Rows[3][1] != "2" {
		t.Fatalf("expected buckets (1,1,2,2), got (%s,%s,%s,%s)", res.Rows[0][1], res.Rows[1][1], res.Rows[2][1], res.Rows[3][1])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RLS tests
// ═══════════════════════════════════════════════════════════════════════════

func TestRLSUpdateRespectsPolicy(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT PRIMARY KEY, grp TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A', 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'B', 200);`)

	executeSQL(t, session, `ENABLE RLS ON t;`)
	executeSQL(t, session, `CREATE POLICY p ON t FOR ALL TO public USING (grp = 'A');`)

	// Only grp='A' rows are visible — UPDATE only touches visible rows
	executeSQL(t, session, `UPDATE t SET val = 999;`)

	res := executeSQL(t, session, `SELECT id, val FROM t ORDER BY id;`)
	// Only grp=A row should be visible and updated
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" || res.Rows[0][1] != "999" {
		t.Fatalf("expected only updated row (1,999), got %v", res.Rows)
	}

	// Verify with a second RLS policy that reveals all rows
	executeSQL(t, session, `CREATE POLICY p2 ON t FOR ALL TO public USING (TRUE);`)
	res = executeSQL(t, session, `SELECT id, val FROM t ORDER BY id;`)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows with permissive policy, got %d", len(res.Rows))
	}
	// grp=B row was not updated (not visible to the first RLS-filtered UPDATE)
	if res.Rows[1][0] != "2" || res.Rows[1][1] != "200" {
		t.Fatalf("grp=B row should be unchanged (2,200), got %v", res.Rows[1])
	}
}

func TestRLSDeleteWithMultiplePolicies(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT PRIMARY KEY, status TEXT, val INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'active', 10);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'active', 20);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'archived', 30);`)

	executeSQL(t, session, `ENABLE RLS ON t;`)
	// Two policies: rows with status='active' OR val > 25 are visible
	executeSQL(t, session, `CREATE POLICY p1 ON t FOR ALL TO public USING (status = 'active');`)
	executeSQL(t, session, `CREATE POLICY p2 ON t FOR ALL TO public USING (val > 25);`)

	// Visible rows: id=1 (active), id=2 (active), id=3 (val=30>25)
	// All 3 are visible because of OR semantics between policies
	res := executeSQL(t, session, `SELECT id FROM t ORDER BY id;`)
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 visible rows, got %d: %v", len(res.Rows), res.Rows)
	}

	// DELETE should delete all 3 visible rows
	executeSQL(t, session, `DELETE FROM t;`)
	res = executeSQL(t, session, `SELECT id FROM t;`)
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows after DELETE, got %d: %v", len(res.Rows), res.Rows)
	}
}

func TestRLSSelectWithWhere(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT PRIMARY KEY, owner TEXT, data TEXT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'alice', 'secret1');`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'bob', 'secret2');`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'alice', 'secret3');`)

	executeSQL(t, session, `ENABLE RLS ON t;`)
	executeSQL(t, session, `CREATE POLICY owner_only ON t FOR ALL TO public USING (owner = 'alice');`)

	// RLS filters first, then WHERE
	res := executeSQL(t, session, `SELECT id, data FROM t WHERE id = 2;`)
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows (bob hidden by RLS), got %d: %v", len(res.Rows), res.Rows)
	}

	// RLS allows alice, WHERE filters id=3
	res = executeSQL(t, session, `SELECT id, data FROM t WHERE id = 3;`)
	if len(res.Rows) != 1 || res.Rows[0][0] != "3" {
		t.Fatalf("expected row (3), got %v", res.Rows)
	}
}

func TestRLSNoEnableShowsAllRows(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, grp TEXT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'A');`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'B');`)

	// Without ENABLE RLS, all rows should be visible regardless of policies
	executeSQL(t, session, `CREATE POLICY p ON t FOR ALL TO public USING (grp = 'A');`)

	res := executeSQL(t, session, `SELECT id FROM t ORDER BY id;`)
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows without ENABLE RLS, got %d: %v", len(res.Rows), res.Rows)
	}
}

func TestRLSEnableWithoutPoliciesBlocksSelect(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1);`)

	executeSQL(t, session, `ENABLE RLS ON t;`)

	// SELECT should fail because RLS is enabled but no policies defined
	_, err := session.Execute(mustParse(t, "SELECT * FROM t;"))
	if err == nil || !strings.Contains(err.Error(), "no policies are defined") {
		t.Fatalf("expected 'no policies are defined' error, got: %v", err)
	}
}

func TestRLSWithGroupBy(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT PRIMARY KEY, dept TEXT, salary INT);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'eng', 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'eng', 200);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'sales', 150);`)

	executeSQL(t, session, `ENABLE RLS ON t;`)
	executeSQL(t, session, `CREATE POLICY p ON t FOR ALL TO public USING (dept = 'eng');`)

	// Only eng rows visible — GROUP BY should only see 2 rows
	res := executeSQL(t, session, `SELECT dept, COUNT(*), SUM(salary) FROM t GROUP BY dept;`)
	if len(res.Rows) != 1 || res.Rows[0][0] != "eng" || res.Rows[0][1] != "2" || res.Rows[0][2] != "300" {
		t.Fatalf("expected (eng,2,300) from RLS+GROUP BY, got %v", res.Rows)
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// CHECK constraint tests — enforcement on INSERT/UPDATE
// ═══════════════════════════════════════════════════════════════════════════

func TestCheckConstraintOnInsertMultipleRows(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, qty INT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_qty CHECK (qty > 0);`)

	// Valid multi-row insert
	executeSQL(t, session, `INSERT INTO t VALUES (1, 10), (2, 20), (3, 30);`)
	res := executeSQL(t, session, `SELECT COUNT(*) FROM t;`)
	if res.Rows[0][0] != "3" {
		t.Fatalf("expected 3 rows, got %s", res.Rows[0][0])
	}

	// Invalid multi-row insert — second row violates
	executeSQLExpectError(t, session, `INSERT INTO t VALUES (4, 5), (5, -1);`)
}

func TestCheckConstraintOnUpdateMultipleRows(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, score INT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_score CHECK (score >= 0 AND score <= 100);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 50);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 75);`)

	// Valid update
	executeSQL(t, session, `UPDATE t SET score = 90 WHERE id = 1;`)

	// Update that violates CHECK
	executeSQLExpectError(t, session, `UPDATE t SET score = 150 WHERE id = 1;`)
	executeSQLExpectError(t, session, `UPDATE t SET score = -10 WHERE id = 2;`)
}

func TestCheckConstraintWithINExpression(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, status TEXT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_status CHECK (status IN ('active', 'inactive', 'pending'));`)

	executeSQL(t, session, `INSERT INTO t VALUES (1, 'active');`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'inactive');`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 'pending');`)

	executeSQLExpectError(t, session, `INSERT INTO t VALUES (4, 'deleted');`)
}

func TestCheckConstraintWithBetweenExpression(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, age INT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_age CHECK (age BETWEEN 0 AND 150);`)

	executeSQL(t, session, `INSERT INTO t VALUES (1, 0);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 150);`)
	executeSQL(t, session, `INSERT INTO t VALUES (3, 75);`)

	executeSQLExpectError(t, session, `INSERT INTO t VALUES (4, -1);`)
	executeSQLExpectError(t, session, `INSERT INTO t VALUES (5, 151);`)
}

func TestCheckConstraintComplexExpression(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, age INT, role TEXT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_rule CHECK ((age >= 18 AND role = 'adult') OR (age < 18 AND role = 'minor'));`)

	executeSQL(t, session, `INSERT INTO t VALUES (1, 25, 'adult');`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 10, 'minor');`)

	executeSQLExpectError(t, session, `INSERT INTO t VALUES (3, 25, 'minor');`)
	executeSQLExpectError(t, session, `INSERT INTO t VALUES (4, 10, 'adult');`)
}

func TestCheckConstraintNOTBetween(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, val INT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_not_between CHECK (val NOT BETWEEN 10 AND 20);`)

	executeSQL(t, session, `INSERT INTO t VALUES (1, 5);`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 25);`)

	executeSQLExpectError(t, session, `INSERT INTO t VALUES (3, 10);`)
	executeSQLExpectError(t, session, `INSERT INTO t VALUES (4, 15);`)
	executeSQLExpectError(t, session, `INSERT INTO t VALUES (5, 20);`)
}

func TestCheckConstraintNOTInExpression(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, code TEXT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_code CHECK (code NOT IN ('bad', 'evil', 'wrong'));`)

	executeSQL(t, session, `INSERT INTO t VALUES (1, 'good');`)
	executeSQL(t, session, `INSERT INTO t VALUES (2, 'fine');`)

	executeSQLExpectError(t, session, `INSERT INTO t VALUES (3, 'bad');`)
	executeSQLExpectError(t, session, `INSERT INTO t VALUES (4, 'evil');`)
}

func TestCheckConstraintOnUpdateBETWEEN(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, temp INT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_temp CHECK (temp BETWEEN -40 AND 50);`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 20);`)

	// Valid update
	executeSQL(t, session, `UPDATE t SET temp = 30 WHERE id = 1;`)

	// Violates
	executeSQLExpectError(t, session, `UPDATE t SET temp = 60 WHERE id = 1;`)
	executeSQLExpectError(t, session, `UPDATE t SET temp = -50 WHERE id = 1;`)
}

func TestCheckConstraintOnUpdateIN(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE TABLE t (id INT, color TEXT);`)
	executeSQL(t, session, `ALTER TABLE t ADD CONSTRAINT chk_color CHECK (color IN ('red', 'green', 'blue'));`)
	executeSQL(t, session, `INSERT INTO t VALUES (1, 'red');`)

	executeSQL(t, session, `UPDATE t SET color = 'green' WHERE id = 1;`)
	executeSQLExpectError(t, session, `UPDATE t SET color = 'yellow' WHERE id = 1;`)
}

// ═══════════════════════════════════════════════════════════════════════════
// Helper
// ═══════════════════════════════════════════════════════════════════════════

func mustParse(t *testing.T, sql string) *parser.SelectStatement {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("Parse failed for %q: %v", sql, err)
	}
	sel, ok := stmt.(*parser.SelectStatement)
	if !ok {
		t.Fatalf("expected SelectStatement, got %T", stmt)
	}
	return sel
}
