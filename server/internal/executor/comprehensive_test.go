package executor

import (
	"testing"
)

// ═══════════════════════════════════════════════════════════════════════════
// Comprehensive Tests for all features
// ═══════════════════════════════════════════════════════════════════════════

// --- GROUP A: Data Integrity & Reliability ---

func TestTransactionAtomicityComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// BEGIN
	executeSQL(t, session, "BEGIN;")

	// INSERT within transaction
	executeSQL(t, session, "INSERT INTO heroes VALUES (5, 'Frodo', 7, TRUE, 8.5, 'Hobbit');")
	executeSQL(t, session, "INSERT INTO heroes VALUES (6, 'Sam', 6, TRUE, 7.8, 'Loyal friend');")

	// COMMIT
	executeSQL(t, session, "COMMIT;")

	// Verify after commit
	count := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if count.Rows[0][0] != "6" {
		t.Fatalf("expected 6 rows after commit, got %s", count.Rows[0][0])
	}
}

func TestTransactionRollbackComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// BEGIN
	executeSQL(t, session, "BEGIN;")

	// INSERT within transaction
	executeSQL(t, session, "INSERT INTO heroes VALUES (5, 'Frodo', 7, TRUE, 8.5, 'Hobbit');")

	// ROLLBACK
	executeSQL(t, session, "ROLLBACK;")

	// Verify rollback
	count := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if count.Rows[0][0] != "4" {
		t.Fatalf("expected 4 rows after rollback, got %s", count.Rows[0][0])
	}
}

// --- GROUP B: Performance ---

func TestQueryOptimizerComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// EXPLAIN should show optimized plan
	result := executeSQL(t, session, "EXPLAIN SELECT * FROM heroes WHERE level > 5;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}
	if len(result.Message) == 0 {
		t.Fatal("expected non-empty EXPLAIN output")
	}
}

func TestBTreeIndexLookupComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Create index
	executeSQL(t, session, "CREATE INDEX idx_level ON heroes (level);")

	// Query should use index
	result := executeSQL(t, session, "SELECT * FROM heroes WHERE level = 10;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row with level=10, got %d", len(result.Rows))
	}
}

// --- GROUP C: SQL Completeness ---

func TestDISTINCTComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// All heroes have different levels
	result := executeSQL(t, session, "SELECT DISTINCT level FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 distinct levels, got %d", len(result.Rows))
	}

	// Insert duplicate level
	executeSQL(t, session, "INSERT INTO heroes VALUES (5, 'Frodo', 10, TRUE, 8.5, 'Hobbit');")

	// Should still be 4 distinct levels
	result = executeSQL(t, session, "SELECT DISTINCT level FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 distinct levels after insert, got %d", len(result.Rows))
	}
}

func TestBETWEENComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT * FROM heroes WHERE level BETWEEN 8 AND 10;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 heroes with level between 8 and 10, got %d", len(result.Rows))
	}
}

func TestNULLIFComprehensive(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE t (a INT, b INT);")
	executeSQL(t, session, "INSERT INTO t VALUES (1, 1);")
	executeSQL(t, session, "INSERT INTO t VALUES (2, 3);")

	result := executeSQL(t, session, "SELECT NULLIF(a, b) FROM t;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	// First row: NULLIF(1, 1) = NULL
	if result.Rows[0][0] != "" {
		t.Fatalf("expected NULL for first row, got %s", result.Rows[0][0])
	}
	// Second row: NULLIF(2, 3) = 2
	if result.Rows[1][0] != "2" {
		t.Fatalf("expected 2 for second row, got %s", result.Rows[1][0])
	}
}

func TestLEFTJOINComprehensive(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE t1 (id INT, name TEXT);")
	executeSQL(t, session, "CREATE TABLE t2 (id INT, value INT);")
	executeSQL(t, session, "INSERT INTO t1 VALUES (1, 'a');")
	executeSQL(t, session, "INSERT INTO t1 VALUES (2, 'b');")
	executeSQL(t, session, "INSERT INTO t2 VALUES (1, 100);")

	// LEFT JOIN: t1 rows 1 and 2 should appear, row 2 has NULL for t2
	result := executeSQL(t, session, "SELECT t1.name, t2.value FROM t1 LEFT JOIN t2 ON t1.id = t2.id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows from LEFT JOIN, got %d", len(result.Rows))
	}

	// Find row with NULL value
	found := false
	for _, row := range result.Rows {
		if row[0] == "b" && row[1] == "" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected row with NULL value for unmatched LEFT JOIN")
	}
}

func TestStringFunctionsComprehensive(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE t (s TEXT);")
	executeSQL(t, session, "INSERT INTO t VALUES ('Hello World');")

	// LENGTH
	result := executeSQL(t, session, "SELECT LENGTH(s) FROM t;")
	if result.Rows[0][0] != "11" {
		t.Fatalf("expected LENGTH=11, got %s", result.Rows[0][0])
	}

	// UPPER
	result = executeSQL(t, session, "SELECT UPPER(s) FROM t;")
	if result.Rows[0][0] != "HELLO WORLD" {
		t.Fatalf("expected UPPER='HELLO WORLD', got %s", result.Rows[0][0])
	}

	// LOWER
	result = executeSQL(t, session, "SELECT LOWER(s) FROM t;")
	if result.Rows[0][0] != "hello world" {
		t.Fatalf("expected LOWER='hello world', got %s", result.Rows[0][0])
	}

	// SUBSTRING
	result = executeSQL(t, session, "SELECT SUBSTRING(s, 1, 5) FROM t;")
	if result.Rows[0][0] != "Hello" {
		t.Fatalf("expected SUBSTRING='Hello', got %s", result.Rows[0][0])
	}

	// REPLACE
	result = executeSQL(t, session, "SELECT REPLACE(s, 'World', 'SQL') FROM t WHERE s = 'Hello World';")
	if result.Rows[0][0] != "Hello SQL" {
		t.Fatalf("expected REPLACE='Hello SQL', got %s", result.Rows[0][0])
	}
}

func TestNumericFunctionsComprehensive(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE t (n INT);")
	executeSQL(t, session, "INSERT INTO t VALUES (10);")

	// MOD
	result := executeSQL(t, session, "SELECT MOD(10, 3) FROM t;")
	if result.Rows[0][0] != "1" {
		t.Fatalf("expected MOD(10,3)=1, got %s", result.Rows[0][0])
	}

	// POWER
	result = executeSQL(t, session, "SELECT POWER(2, 3) FROM t;")
	if result.Rows[0][0] != "8" {
		t.Fatalf("expected POWER(2,3)=8, got %s", result.Rows[0][0])
	}

	// SQRT
	result = executeSQL(t, session, "SELECT SQRT(9) FROM t;")
	if result.Rows[0][0] != "3" {
		t.Fatalf("expected SQRT(9)=3, got %s", result.Rows[0][0])
	}

	// SIGN
	result = executeSQL(t, session, "SELECT SIGN(-5) FROM t;")
	if result.Rows[0][0] != "-1" {
		t.Fatalf("expected SIGN(-5)=-1, got %s", result.Rows[0][0])
	}

	// GREATEST
	result = executeSQL(t, session, "SELECT GREATEST(1, 5, 3) FROM t;")
	if result.Rows[0][0] != "5" {
		t.Fatalf("expected GREATEST(1,5,3)=5, got %s", result.Rows[0][0])
	}

	// LEAST
	result = executeSQL(t, session, "SELECT LEAST(1, 5, 3) FROM t;")
	if result.Rows[0][0] != "1" {
		t.Fatalf("expected LEAST(1,5,3)=1, got %s", result.Rows[0][0])
	}
}

func TestDateTimeFunctionsComprehensive(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE t (n INT);")
	executeSQL(t, session, "INSERT INTO t VALUES (1);")

	// CURRENT_DATE
	result := executeSQL(t, session, "SELECT CURRENT_DATE FROM t;")
	if len(result.Rows[0][0]) != 10 {
		t.Fatalf("expected CURRENT_DATE format YYYY-MM-DD, got %s", result.Rows[0][0])
	}

	// CURRENT_TIMESTAMP
	result = executeSQL(t, session, "SELECT CURRENT_TIMESTAMP FROM t;")
	if len(result.Rows[0][0]) < 10 {
		t.Fatalf("expected CURRENT_TIMESTAMP, got %s", result.Rows[0][0])
	}

	// TO_CHAR
	result = executeSQL(t, session, "SELECT TO_CHAR(CURRENT_TIMESTAMP, '2006-01-02') FROM t;")
	if len(result.Rows[0][0]) != 10 {
		t.Fatalf("expected TO_CHAR date format, got %s", result.Rows[0][0])
	}
}

func TestAggregatesComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Multiple aggregates in one query
	result := executeSQL(t, session, "SELECT COUNT(*), SUM(level), AVG(level), MIN(level), MAX(level) FROM heroes;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "4" {
		t.Fatalf("expected COUNT=4, got %s", result.Rows[0][0])
	}
}

func TestWindowFunctionsComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// ROW_NUMBER
	result := executeSQL(t, session, "SELECT ROW_NUMBER() OVER (ORDER BY level) FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}

	// RANK
	result = executeSQL(t, session, "SELECT RANK() OVER (ORDER BY level) FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}

	// DENSE_RANK
	result = executeSQL(t, session, "SELECT DENSE_RANK() OVER (ORDER BY level) FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}

	// LAG
	result = executeSQL(t, session, "SELECT LAG(level) OVER (ORDER BY level) FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}

	// LEAD
	result = executeSQL(t, session, "SELECT LEAD(level) OVER (ORDER BY level) FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}
}

func TestEXISTSComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// EXISTS with matching row
	result := executeSQL(t, session, "SELECT * FROM heroes WHERE EXISTS (SELECT 1 FROM heroes WHERE level = 10);")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 heroes with EXISTS, got %d", len(result.Rows))
	}

	// NOT EXISTS
	result = executeSQL(t, session, "SELECT * FROM heroes WHERE NOT EXISTS (SELECT 1 FROM heroes WHERE level = 100);")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 heroes with NOT EXISTS, got %d", len(result.Rows))
	}
}

func TestSetOperationsComprehensive(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE t1 (id INT);")
	executeSQL(t, session, "CREATE TABLE t2 (id INT);")
	executeSQL(t, session, "INSERT INTO t1 VALUES (1);")
	executeSQL(t, session, "INSERT INTO t1 VALUES (2);")
	executeSQL(t, session, "INSERT INTO t2 VALUES (2);")
	executeSQL(t, session, "INSERT INTO t2 VALUES (3);")

	// UNION
	result := executeSQL(t, session, "SELECT * FROM t1 UNION SELECT * FROM t2;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows from UNION, got %d", len(result.Rows))
	}

	// INTERSECT
	result = executeSQL(t, session, "SELECT * FROM t1 INTERSECT SELECT * FROM t2;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row from INTERSECT, got %d", len(result.Rows))
	}

	// EXCEPT
	result = executeSQL(t, session, "SELECT * FROM t1 EXCEPT SELECT * FROM t2;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row from EXCEPT, got %d", len(result.Rows))
	}
}

func TestSubqueriesComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Scalar subquery
	result := executeSQL(t, session, "SELECT * FROM heroes WHERE level = (SELECT MAX(level) FROM heroes);")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row with max level, got %d", len(result.Rows))
	}

	// IN subquery
	result = executeSQL(t, session, "SELECT * FROM heroes WHERE id IN (SELECT id FROM heroes WHERE alive = TRUE);")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 alive heroes, got %d", len(result.Rows))
	}
}

func TestCASEExpressionComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT CASE WHEN level >= 9 THEN 'high' ELSE 'low' END FROM heroes;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}

	// Aragorn level=10, Legolas level=9 -> high
	// Gimli level=8, Boromir level=5 -> low
	highCount := 0
	for _, row := range result.Rows {
		if row[0] == "high" {
			highCount++
		}
	}
	if highCount != 2 {
		t.Fatalf("expected 2 high-level heroes, got %d", highCount)
	}
}

func TestCTEComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// CTE query
	result := executeSQL(t, session, "WITH high_level AS (SELECT * FROM heroes WHERE level >= 9) SELECT * FROM high_level;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows with level >= 9, got %d", len(result.Rows))
	}
}

func TestMERGEComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Create a source table
	executeSQL(t, session, "CREATE TABLE updates (id INT, new_level INT, new_alive BOOL);")
	executeSQL(t, session, "INSERT INTO updates VALUES (1, 11, TRUE);") // Match Aragorn
	executeSQL(t, session, "INSERT INTO updates VALUES (5, 7, TRUE);")  // No match, should insert Frodo

	// MERGE
	// Use qualified names in ON, UPDATE and INSERT
	executeSQL(t, session, `
		MERGE INTO heroes 
		USING updates AS u 
		ON heroes.id = u.id 
		WHEN MATCHED THEN 
			UPDATE SET level = u.new_level, alive = u.new_alive 
		WHEN NOT MATCHED THEN 
			INSERT (id, name, level, alive) VALUES (u.id, 'New Hero', u.new_level, u.new_alive);
	`)

	// Verify update
	res := executeSQL(t, session, "SELECT level FROM heroes WHERE id = 1;")
	if res.Rows[0][0] != "11" {
		t.Fatalf("expected level 11 for Aragorn, got %s", res.Rows[0][0])
	}

	// Verify insert
	res = executeSQL(t, session, "SELECT name, level FROM heroes WHERE id = 5;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "New Hero" || res.Rows[0][1] != "7" {
		t.Fatalf("expected New Hero with level 7, got %#v", res.Rows)
	}
}

func TestTRUNCATEComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// TRUNCATE
	executeSQL(t, session, "TRUNCATE TABLE heroes;")

	// Verify table is empty
	count := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if count.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows after TRUNCATE, got %s", count.Rows[0][0])
	}
}

func TestSAVEPOINTComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// BEGIN
	executeSQL(t, session, "BEGIN;")

	// SAVEPOINT теперь реализован: устанавливается без ошибки.
	executeSQL(t, session, "SAVEPOINT sp1;")

	// ROLLBACK TO SAVEPOINT известного маркера — без ошибки.
	executeSQL(t, session, "ROLLBACK TO SAVEPOINT sp1;")

	// RELEASE SAVEPOINT известного маркера — без ошибки.
	executeSQL(t, session, "RELEASE SAVEPOINT sp1;")

	// После RELEASE маркер неизвестен — ROLLBACK TO должен вернуть ошибку.
	executeSQLExpectError(t, session, "ROLLBACK TO SAVEPOINT sp1;")

	// COMMIT
	executeSQL(t, session, "COMMIT;")
}

func TestConcurrentAccessComprehensive(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			executeSQL(t, session, "SELECT * FROM heroes;")
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestConnectionPoolComprehensive(t *testing.T) {
	// Test that connection pool works correctly
	// This is more of an integration test
	session := setupSession(t)
	seedHeroes(t, session)

	// Multiple concurrent operations
	done := make(chan bool, 20)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			executeSQL(t, session, "SELECT * FROM heroes;")
		}()
	}

	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			executeSQL(t, session, "INSERT INTO heroes VALUES (100, 'user', 5, TRUE, 5.0, 'test');")
		}(i)
	}

	for i := 0; i < 20; i++ {
		<-done
	}
}
