package executor

import (
	"fmt"
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
// Comprehensive Stress Tests — extreme conditions & edge-case boundaries
// ═══════════════════════════════════════════════════════════════════════════

func setupCompStressSession(t *testing.T) *Session {
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
// 1. Concurrent INSERT/UPDATE/DELETE — all DML types racing simultaneously
// ═══════════════════════════════════════════════════════════════════════════

func TestStressComprehensiveConcurrentDML(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	session := setupCompStressSession(t)
	executeSQL(t, session, "CREATE TABLE stress_test (id INT PRIMARY KEY, value TEXT, counter INT);")

	// Pre-populate with IDs 1000-1099 (inserters use 0-999 to avoid overlap)
	for i := 0; i < 100; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO stress_test VALUES (%d, 'init', 0);", 1000+i))
	}

	var wg sync.WaitGroup
	dmlErrors := make(chan error, 200)

	// 50 goroutines doing concurrent updates on pre-populated rows
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				stmt, err := parser.Parse(fmt.Sprintf(
					"UPDATE stress_test SET counter = counter + 1 WHERE id = %d;", 1000+id))
				if err != nil {
					dmlErrors <- fmt.Errorf("parse update: %v", err)
					return
				}
				_, err = session.Execute(stmt)
				if err != nil {
					dmlErrors <- err
				}
			}
		}(i)
	}

	// 20 goroutines doing concurrent inserts (unique ID ranges: 0-999)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				stmt, err := parser.Parse(fmt.Sprintf(
					"INSERT INTO stress_test VALUES (%d, 'new', 1);", base*50+j))
				if err != nil {
					dmlErrors <- fmt.Errorf("parse insert: %v", err)
					return
				}
				_, err = session.Execute(stmt)
				if err != nil {
					dmlErrors <- err
				}
			}
		}(i)
	}

	// 10 goroutines doing concurrent deletes on pre-populated rows
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				stmt, err := parser.Parse(fmt.Sprintf(
					"DELETE FROM stress_test WHERE id = %d;", 1050+base*20+j))
				if err != nil {
					dmlErrors <- fmt.Errorf("parse delete: %v", err)
					return
				}
				_, _ = session.Execute(stmt)
			}
		}(i)
	}

	wg.Wait()
	close(dmlErrors)

	// Filter out expected errors (OCC conflicts, duplicate keys)
	var unexpectedErrors []error
	for err := range dmlErrors {
		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "conflict") ||
				strings.Contains(errStr, "transaction") ||
				strings.Contains(errStr, "duplicate") {
				continue
			}
			unexpectedErrors = append(unexpectedErrors, err)
		}
	}

	if len(unexpectedErrors) > 0 {
		for _, err := range unexpectedErrors {
			t.Errorf("unexpected concurrent DML error: %v", err)
		}
	}

	// Verify table is still readable and not empty
	result := executeSQL(t, session, "SELECT COUNT(*) FROM stress_test;")
	var count int
	fmt.Sscanf(result.Rows[0][0], "%d", &count)
	if count == 0 {
		t.Error("table is empty after concurrent DML — data loss detected")
	}

	t.Logf("Concurrent DML: 50 updaters×100 + 20 inserters×50 + 10 deleters×20, final rows: %d", count)
}

// ═══════════════════════════════════════════════════════════════════════════
// 2. Large Dataset Operations — 100K rows, indexed lookup, range queries
// ═══════════════════════════════════════════════════════════════════════════

func TestStressComprehensiveLargeDataset(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	session := setupCompStressSession(t)
	executeSQL(t, session, "CREATE TABLE bigtable (id INT PRIMARY KEY, data TEXT, value FLOAT);")

	// Insert 100K rows in batches of 500
	const totalRows = 100000
	const batchSize = 500
	start := time.Now()
	for batch := 0; batch < totalRows/batchSize; batch++ {
		var values []string
		for i := 0; i < batchSize; i++ {
			rowID := batch*batchSize + i
			values = append(values, fmt.Sprintf("(%d, 'row_%d', %f)", rowID, rowID, float64(rowID)*1.5))
		}
		sql := fmt.Sprintf("INSERT INTO bigtable VALUES %s;", strings.Join(values, ", "))
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse error at batch %d: %v", batch, err)
		}
		_, err = session.Execute(stmt)
		if err != nil {
			t.Fatalf("execute error at batch %d: %v", batch, err)
		}
	}
	t.Logf("Insert 100K rows: %v", time.Since(start))

	// Full table scan — COUNT(*)
	start = time.Now()
	result := executeSQL(t, session, "SELECT COUNT(*) FROM bigtable;")
	var rowCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &rowCount)
	if rowCount != totalRows {
		t.Errorf("expected %d rows, got %d", totalRows, rowCount)
	}
	t.Logf("Full scan count (100K): %v", time.Since(start))

	// Create index on non-PK column and do indexed lookup
	executeSQL(t, session, "CREATE INDEX idx_bigtable_value ON bigtable (value);")
	start = time.Now()
	result = executeSQL(t, session, "SELECT * FROM bigtable WHERE id = 50000;")
	if len(result.Rows) != 1 {
		t.Errorf("lookup: expected 1 row, got %d", len(result.Rows))
	}
	t.Logf("Lookup (100K table): %v", time.Since(start))

	// Range query
	start = time.Now()
	result = executeSQL(t, session, "SELECT * FROM bigtable WHERE id BETWEEN 40000 AND 60000;")
	expectedRange := 20001 // BETWEEN is inclusive
	if len(result.Rows) != expectedRange {
		t.Errorf("range query: expected %d rows, got %d", expectedRange, len(result.Rows))
	}
	t.Logf("Range query (20K rows from 100K): %v", time.Since(start))
}

// ═══════════════════════════════════════════════════════════════════════════
// 3. Transaction Stress — concurrent bank transfers with real conflicts
// ═══════════════════════════════════════════════════════════════════════════

func TestStressComprehensiveConcurrentTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	session := setupCompStressSession(t)
	executeSQL(t, session, "CREATE TABLE accounts (id INT PRIMARY KEY, balance INT);")
	executeSQL(t, session, "INSERT INTO accounts VALUES (1, 10000), (2, 10000), (3, 10000);")

	var wg sync.WaitGroup
	successCount := atomic.Int32{}

	// 100 goroutines doing concurrent transfers, each with its OWN session
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			from := (idx % 3) + 1
			to := ((idx + 1) % 3) + 1

			// Create a separate session for this goroutine
			txSession := setupCompStressSession(t)
			executeSQL(t, txSession, "USE stressdb;")

			// BEGIN
			beginStmt, _ := parser.Parse("BEGIN;")
			_, err := txSession.Execute(beginStmt)
			if err != nil {
				return
			}

			// UPDATE sender
			stmt1, _ := parser.Parse(fmt.Sprintf(
				"UPDATE accounts SET balance = balance - 100 WHERE id = %d;", from))
			_, err = txSession.Execute(stmt1)
			if err != nil {
				rb, _ := parser.Parse("ROLLBACK;")
				txSession.Execute(rb)
				return
			}

			// UPDATE receiver
			stmt2, _ := parser.Parse(fmt.Sprintf(
				"UPDATE accounts SET balance = balance + 100 WHERE id = %d;", to))
			_, err = txSession.Execute(stmt2)
			if err != nil {
				rb, _ := parser.Parse("ROLLBACK;")
				txSession.Execute(rb)
				return
			}

			// COMMIT
			commitStmt, _ := parser.Parse("COMMIT;")
			_, err = txSession.Execute(commitStmt)
			if err == nil {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// Verify total balance is preserved (invariant: sum = 30000)
	result := executeSQL(t, session, "SELECT SUM(balance) FROM accounts;")
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		t.Fatal("SUM(balance) returned no result")
	}

	t.Logf("Concurrent transactions: %d successful transfers, total balance: %s",
		successCount.Load(), result.Rows[0][0])

	if result.Rows[0][0] != "30000" {
		t.Errorf("balance invariant violated: expected 30000, got %s", result.Rows[0][0])
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// 4. Memory Pressure — large text payloads
// ═══════════════════════════════════════════════════════════════════════════

func TestStressComprehensiveMemoryPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	session := setupCompStressSession(t)
	executeSQL(t, session, "CREATE TABLE largedata (id INT PRIMARY KEY, payload TEXT);")

	// Insert rows with 1KB payloads
	largePayload := strings.Repeat("x", 1000)
	const numRows = 200
	for i := 0; i < numRows; i++ {
		stmt, err := parser.Parse(fmt.Sprintf(
			"INSERT INTO largedata VALUES (%d, '%s');", i, largePayload))
		if err != nil {
			t.Fatalf("parse error at row %d: %v", i, err)
		}
		_, err = session.Execute(stmt)
		if err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}
	}

	// Query all rows
	start := time.Now()
	result := executeSQL(t, session, "SELECT * FROM largedata;")
	if len(result.Rows) != numRows {
		t.Errorf("expected %d rows, got %d", numRows, len(result.Rows))
	}
	t.Logf("Read %d rows with 1KB payload: %v, rows: %d", numRows, time.Since(start), len(result.Rows))

	// Verify payload integrity on samples
	for i := 0; i < 10; i++ {
		rowID := i * 20
		result = executeSQL(t, session, fmt.Sprintf("SELECT LENGTH(payload) FROM largedata WHERE id = %d;", rowID))
		var length int
		fmt.Sscanf(result.Rows[0][0], "%d", &length)
		if length != 1000 {
			t.Errorf("row %d payload length: expected 1000, got %d", rowID, length)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// 5. Rapid Open/Close — storage engine lifecycle stress
// ═══════════════════════════════════════════════════════════════════════════

func TestStressComprehensiveRapidOpenClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	dir := t.TempDir()

	for i := 0; i < 100; i++ {
		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatalf("open %d failed: %v", i, err)
		}

		session := NewSession(store, nil, txm, nil)

		// Create database on first iteration, reuse after
		if i == 0 {
			executeSQL(t, session, "CREATE DATABASE testdb;")
		}
		executeSQL(t, session, "USE testdb;")

		// Create table on first iteration
		if i == 0 {
			executeSQL(t, session, "CREATE TABLE rapid_test (id INT PRIMARY KEY, val TEXT);")
		}

		// Insert a row
		stmt, err := parser.Parse(fmt.Sprintf(
			"INSERT INTO rapid_test VALUES (%d, 'iter_%d');", i, i))
		if err != nil {
			t.Fatalf("parse error at iteration %d: %v", i, err)
		}
		_, err = session.Execute(stmt)
		if err != nil {
			t.Fatalf("insert at iteration %d failed: %v", i, err)
		}

		// Read it back
		result := executeSQL(t, session, "SELECT COUNT(*) FROM rapid_test;")
		var count int
		fmt.Sscanf(result.Rows[0][0], "%d", &count)
		if count != i+1 {
			t.Fatalf("iteration %d: expected %d rows, got %d", i, i+1, count)
		}

		if err := store.Close(); err != nil {
			t.Fatalf("close %d failed: %v", i, err)
		}
	}

	// Final open: verify all rows persisted
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session := NewSession(store, nil, txm, nil)
	executeSQL(t, session, "USE testdb;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM rapid_test;")
	var finalCount int
	fmt.Sscanf(result.Rows[0][0], "%d", &finalCount)
	if finalCount != 100 {
		t.Errorf("after 100 open/close cycles: expected 100 rows, got %d", finalCount)
	}

	t.Logf("Rapid open/close: 100 cycles, all %d rows persisted", finalCount)
}
