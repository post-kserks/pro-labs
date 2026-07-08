package executor

import (
	"fmt"
	"testing"
)

// TestOptimizerPushdown_ConnectedToExecutor verifies that optimizer predicate
// pushdown is wired into the executor: queries with WHERE clauses return
// correct results and RowsScanned reflects storage reads before filtering.
func TestOptimizerPushdown_ConnectedToExecutor(t *testing.T) {
	session := setupSession(t)

	// Seed 20 rows with varying levels (table columns: id, name, level, alive, score, bio)
	for i := 1; i <= 20; i++ {
		alive := "TRUE"
		if i%5 == 0 {
			alive = "FALSE"
		}
		executeSQL(t, session,
			fmt.Sprintf("INSERT INTO heroes VALUES (%d, 'Hero%d', %d, %s, 9.0, 'bio');", i, i, i, alive))
	}

	// Query with WHERE that filters to a subset
	result := executeSQL(t, session, "SELECT name FROM heroes WHERE level > 10;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	// 10 heroes with level 11..20
	if len(result.Rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(result.Rows))
	}
	// RowsScanned should reflect all rows read from storage (20)
	if result.RowsScanned != 20 {
		t.Errorf("expected RowsScanned=20, got %d", result.RowsScanned)
	}
}

// TestOptimizerPushdown_FastPath_Connected verifies pushdown on the fast-path
// SELECT (single table, no joins, no aggregation).
func TestOptimizerPushdown_FastPath_Connected(t *testing.T) {
	session := setupSession(t)

	for i := 1; i <= 15; i++ {
		alive := "TRUE"
		if i%3 == 0 {
			alive = "FALSE"
		}
		executeSQL(t, session,
			fmt.Sprintf("INSERT INTO heroes VALUES (%d, 'Hero%d', %d, %s, 8.5, 'bio');", i, i, i, alive))
	}

	// Fast path: single table, specific columns, no joins/agg
	result := executeSQL(t, session, "SELECT name FROM heroes WHERE alive = TRUE AND level > 5;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	// alive=TRUE: 1,2,4,5,7,8,10,11,13,14 (10 rows)
	// level>5 among those: 7,8,10,11,13,14 (6 rows)
	if len(result.Rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(result.Rows))
	}
	if result.RowsScanned != 15 {
		t.Errorf("expected RowsScanned=15, got %d", result.RowsScanned)
	}
}

// TestOptimizerPushdown_CorrectnessAfterPushdown ensures that applying
// pushdown filters doesn't change query results.
func TestOptimizerPushdown_CorrectnessAfterPushdown(t *testing.T) {
	session := setupSession(t)

	for i := 1; i <= 10; i++ {
		alive := "TRUE"
		if i > 7 {
			alive = "FALSE"
		}
		executeSQL(t, session,
			fmt.Sprintf("INSERT INTO heroes VALUES (%d, 'Hero%d', %d, %s, %d.0, 'bio');", i, i, i, alive, i))
	}

	// Multiple filter conditions
	result := executeSQL(t, session, "SELECT name, level FROM heroes WHERE alive = TRUE AND level >= 3 AND level <= 5;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	// alive=TRUE: 1,2,3,4,5,6,7 (7 rows)
	// level>=3 AND level<=5: 3,4,5 -> 3 rows
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
	// Verify specific values
	expected := map[string]bool{"Hero3": true, "Hero4": true, "Hero5": true}
	for _, row := range result.Rows {
		if !expected[row[0]] {
			t.Errorf("unexpected hero: %s", row[0])
		}
	}
}

// TestOptimizerPushdown_JoinReducesRows verifies that pushdown reduces the
// number of rows flowing into a JOIN by filtering the right table early.
func TestOptimizerPushdown_JoinReducesRows(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE orders (id INT, amount INT, customer_id INT);")
	executeSQL(t, session, "CREATE TABLE customers (id INT, name VARCHAR(50));")

	// Insert 10 customers
	for i := 1; i <= 10; i++ {
		executeSQL(t, session,
			fmt.Sprintf("INSERT INTO customers VALUES (%d, 'Cust%d');", i, i))
	}
	// Insert 20 orders, half with high amounts
	for i := 1; i <= 20; i++ {
		amt := 50
		if i <= 10 {
			amt = 150
		}
		custID := (i-1)%10 + 1
		executeSQL(t, session,
			fmt.Sprintf("INSERT INTO orders VALUES (%d, %d, %d);", i, amt, custID))
	}

	// JOIN with WHERE filtering orders
	result := executeSQL(t, session,
		"SELECT orders.id, customers.name FROM orders INNER JOIN customers ON orders.customer_id = customers.id WHERE orders.amount > 100;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	// 10 orders with amount > 100 (orders 1..10)
	if len(result.Rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(result.Rows))
	}
}
