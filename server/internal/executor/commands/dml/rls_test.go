package dml_test

import (
	"strings"
	"testing"

	"vaultdb/internal/executor"
	"vaultdb/internal/parser"
)

func TestRLSBasics(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE salaries (id INT PRIMARY KEY, name TEXT, dept TEXT, amount INT);")
	executor.ExecuteSQL(t, session, "INSERT INTO salaries VALUES (1, 'Alice', 'engineering', 100);")
	executor.ExecuteSQL(t, session, "INSERT INTO salaries VALUES (2, 'Bob', 'engineering', 200);")
	executor.ExecuteSQL(t, session, "INSERT INTO salaries VALUES (3, 'Charlie', 'sales', 150);")
	executor.ExecuteSQL(t, session, "INSERT INTO salaries VALUES (4, 'Diana', 'sales', 120);")

	// Without RLS, all 4 rows are visible
	result := executor.ExecuteSQL(t, session, "SELECT * FROM salaries;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows without RLS, got %d", len(result.Rows))
	}

	// Enable RLS — no policies yet, so SELECT is blocked
	executor.ExecuteSQL(t, session, "ENABLE RLS ON salaries;")
	stmt, _ := parser.Parse("SELECT * FROM salaries;")
	_, err := session.Execute(stmt)
	if err == nil || !strings.Contains(err.Error(), "no policies are defined") {
		t.Fatalf("expected 'no policies are defined' error, got: %v", err)
	}

	// Create policy: only engineering dept
	executor.ExecuteSQL(t, session, "CREATE POLICY eng_only ON salaries FOR ALL TO public USING (dept = 'engineering');")

	// SELECT — only engineering rows
	result = executor.ExecuteSQL(t, session, "SELECT id, name FROM salaries ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows (engineering), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][1] != "Alice" || result.Rows[1][1] != "Bob" {
		t.Fatalf("expected Alice and Bob, got: %v", result.Rows)
	}

	// Sales rows are invisible to SELECT
	result = executor.ExecuteSQL(t, session, "SELECT id, name FROM salaries WHERE dept = 'sales';")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 sales rows (hidden by RLS), got %d", len(result.Rows))
	}

	// UPDATE affects only visible rows
	executor.ExecuteSQL(t, session, "UPDATE salaries SET amount = 300 WHERE dept = 'engineering';")
	result = executor.ExecuteSQL(t, session, "SELECT amount FROM salaries ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after UPDATE, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "300" || result.Rows[1][0] != "300" {
		t.Fatalf("expected amounts to be 300, got: %v", result.Rows)
	}
}

func TestRLSAndCondition(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE items (id INT PRIMARY KEY, name TEXT, price INT, category TEXT);")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (1, 'Widget', 100, 'A');")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (2, 'Gadget', 50, 'B');")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (3, 'Doohickey', 30, 'A');")
	executor.ExecuteSQL(t, session, "INSERT INTO items VALUES (4, 'Thing', 25, 'B');")

	executor.ExecuteSQL(t, session, "ENABLE RLS ON items;")
	executor.ExecuteSQL(t, session, "CREATE POLICY p1 ON items FOR ALL TO public USING (category = 'A' AND price > 50);")

	result := executor.ExecuteSQL(t, session, "SELECT id, name FROM items ORDER BY id;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row (category=A AND price>50), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][1] != "Widget" {
		t.Fatalf("expected Widget, got: %v", result.Rows)
	}
}

func TestRLSOrCondition(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE t (id INT PRIMARY KEY, val INT);")
	executor.ExecuteSQL(t, session, "INSERT INTO t VALUES (1, 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO t VALUES (2, 20);")
	executor.ExecuteSQL(t, session, "INSERT INTO t VALUES (3, 30);")

	executor.ExecuteSQL(t, session, "ENABLE RLS ON t;")
	executor.ExecuteSQL(t, session, "CREATE POLICY p ON t FOR ALL TO public USING (val < 15 OR val > 25);")

	result := executor.ExecuteSQL(t, session, "SELECT id FROM t ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows (val<15 or val>25), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "1" || result.Rows[1][0] != "3" {
		t.Fatalf("expected id=1 and id=3, got: %v", result.Rows)
	}
}

func TestRLSDeleteRespectsPolicy(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE t (id INT PRIMARY KEY, grp TEXT);")
	executor.ExecuteSQL(t, session, "INSERT INTO t VALUES (1, 'A');")
	executor.ExecuteSQL(t, session, "INSERT INTO t VALUES (2, 'B');")

	executor.ExecuteSQL(t, session, "ENABLE RLS ON t;")
	executor.ExecuteSQL(t, session, "CREATE POLICY p ON t FOR ALL TO public USING (grp = 'A');")

	// Only row with grp='A' is visible
	result := executor.ExecuteSQL(t, session, "SELECT id FROM t;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "1" {
		t.Fatalf("expected only id=1, got: %v", result.Rows)
	}

	// Delete all visible rows
	executor.ExecuteSQL(t, session, "DELETE FROM t;")
	result = executor.ExecuteSQL(t, session, "SELECT id FROM t;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows after DELETE (all visible were deleted), got %d", len(result.Rows))
	}
}

func TestRLSViewInheritsTableRLS(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE products (id INT PRIMARY KEY, name TEXT, dept TEXT, price INT);")
	executor.ExecuteSQL(t, session, "INSERT INTO products VALUES (1, 'Laptop', 'eng', 1000);")
	executor.ExecuteSQL(t, session, "INSERT INTO products VALUES (2, 'Phone', 'eng', 500);")
	executor.ExecuteSQL(t, session, "INSERT INTO products VALUES (3, 'Desk', 'sales', 300);")
	executor.ExecuteSQL(t, session, "INSERT INTO products VALUES (4, 'Chair', 'sales', 200);")

	// Enable RLS on base table: only engineering dept visible
	executor.ExecuteSQL(t, session, "ENABLE RLS ON products;")
	executor.ExecuteSQL(t, session, "CREATE POLICY eng_only ON products FOR ALL TO public USING (dept = 'eng');")

	// Without a view, only 2 rows
	result := executor.ExecuteSQL(t, session, "SELECT id, name FROM products ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows on table, got %d: %v", len(result.Rows), result.Rows)
	}

	// Create view on the table
	executor.ExecuteSQL(t, session, "CREATE VIEW all_products AS SELECT id, name, price FROM products;")

	// Query through view: base table RLS still applies
	result = executor.ExecuteSQL(t, session, "SELECT id, name FROM all_products ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows through view (base table RLS), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][1] != "Laptop" || result.Rows[1][1] != "Phone" {
		t.Fatalf("expected Laptop and Phone, got: %v", result.Rows)
	}
}

func TestRLSViewOwnPolicy(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE orders (id INT PRIMARY KEY, customer TEXT, region TEXT, amount INT);")
	executor.ExecuteSQL(t, session, "INSERT INTO orders VALUES (1, 'Alice', 'us', 100);")
	executor.ExecuteSQL(t, session, "INSERT INTO orders VALUES (2, 'Bob', 'eu', 200);")
	executor.ExecuteSQL(t, session, "INSERT INTO orders VALUES (3, 'Charlie', 'us', 300);")
	executor.ExecuteSQL(t, session, "INSERT INTO orders VALUES (4, 'Diana', 'eu', 40);")

	// Create view with no RLS on base table — all 4 rows visible
	executor.ExecuteSQL(t, session, "CREATE VIEW eu_orders AS SELECT id, customer, amount FROM orders WHERE region = 'eu';")
	result := executor.ExecuteSQL(t, session, "SELECT id, customer FROM eu_orders ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 eu rows, got %d: %v", len(result.Rows), result.Rows)
	}

	// Enable RLS on the view itself
	executor.ExecuteSQL(t, session, "ENABLE RLS ON eu_orders;")
	// No policies yet — should error
	stmt, _ := parser.Parse("SELECT id FROM eu_orders;")
	_, err := session.Execute(stmt)
	if err == nil || !strings.Contains(err.Error(), "no policies are defined") {
		t.Fatalf("expected 'no policies are defined' error, got: %v", err)
	}

	// Add policy: only orders > 50
	executor.ExecuteSQL(t, session, "CREATE POLICY high_value ON eu_orders FOR ALL TO public USING (amount > 50);")

	// Only Bob (200) should be visible through the view
	result = executor.ExecuteSQL(t, session, "SELECT id, customer FROM eu_orders ORDER BY id;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row (high-value eu order), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "2" || result.Rows[0][1] != "Bob" {
		t.Fatalf("expected Bob, got: %v", result.Rows)
	}

	// Base table is unaffected — still all 4 rows
	result = executor.ExecuteSQL(t, session, "SELECT id FROM orders ORDER BY id;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows on base table, got %d: %v", len(result.Rows), result.Rows)
	}
}
