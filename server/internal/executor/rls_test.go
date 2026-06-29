package executor

import (
	"strings"
	"testing"

	"vaultdb/internal/parser"
)

func TestRLSBasics(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE salaries (id INT PRIMARY KEY, name TEXT, dept TEXT, amount INT);")
	executeSQL(t, session, "INSERT INTO salaries VALUES (1, 'Alice', 'engineering', 100);")
	executeSQL(t, session, "INSERT INTO salaries VALUES (2, 'Bob', 'engineering', 200);")
	executeSQL(t, session, "INSERT INTO salaries VALUES (3, 'Charlie', 'sales', 150);")
	executeSQL(t, session, "INSERT INTO salaries VALUES (4, 'Diana', 'sales', 120);")

	// Without RLS, all 4 rows are visible
	result := executeSQL(t, session, "SELECT * FROM salaries;")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows without RLS, got %d", len(result.Rows))
	}

	// Enable RLS — no policies yet, so SELECT is blocked
	executeSQL(t, session, "ENABLE RLS ON salaries;")
	stmt, _ := parser.Parse("SELECT * FROM salaries;")
	_, err := session.Execute(stmt)
	if err == nil || !strings.Contains(err.Error(), "no policies are defined") {
		t.Fatalf("expected 'no policies are defined' error, got: %v", err)
	}

	// Create policy: only engineering dept
	executeSQL(t, session, "CREATE POLICY eng_only ON salaries FOR ALL TO public USING (dept = 'engineering');")

	// SELECT — only engineering rows
	result = executeSQL(t, session, "SELECT id, name FROM salaries ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows (engineering), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][1] != "Alice" || result.Rows[1][1] != "Bob" {
		t.Fatalf("expected Alice and Bob, got: %v", result.Rows)
	}

	// Sales rows are invisible to SELECT
	result = executeSQL(t, session, "SELECT id, name FROM salaries WHERE dept = 'sales';")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 sales rows (hidden by RLS), got %d", len(result.Rows))
	}

	// UPDATE affects only visible rows
	executeSQL(t, session, "UPDATE salaries SET amount = 300 WHERE dept = 'engineering';")
	result = executeSQL(t, session, "SELECT amount FROM salaries ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows after UPDATE, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "300" || result.Rows[1][0] != "300" {
		t.Fatalf("expected amounts to be 300, got: %v", result.Rows)
	}
}

func TestRLSAndCondition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE items (id INT PRIMARY KEY, name TEXT, price INT, category TEXT);")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'Widget', 100, 'A');")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'Gadget', 50, 'B');")
	executeSQL(t, session, "INSERT INTO items VALUES (3, 'Doohickey', 30, 'A');")
	executeSQL(t, session, "INSERT INTO items VALUES (4, 'Thing', 25, 'B');")

	executeSQL(t, session, "ENABLE RLS ON items;")
	executeSQL(t, session, "CREATE POLICY p1 ON items FOR ALL TO public USING (category = 'A' AND price > 50);")

	result := executeSQL(t, session, "SELECT id, name FROM items ORDER BY id;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row (category=A AND price>50), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][1] != "Widget" {
		t.Fatalf("expected Widget, got: %v", result.Rows)
	}
}

func TestRLSOrCondition(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE t (id INT PRIMARY KEY, val INT);")
	executeSQL(t, session, "INSERT INTO t VALUES (1, 10);")
	executeSQL(t, session, "INSERT INTO t VALUES (2, 20);")
	executeSQL(t, session, "INSERT INTO t VALUES (3, 30);")

	executeSQL(t, session, "ENABLE RLS ON t;")
	executeSQL(t, session, "CREATE POLICY p ON t FOR ALL TO public USING (val < 15 OR val > 25);")

	result := executeSQL(t, session, "SELECT id FROM t ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows (val<15 or val>25), got %d: %v", len(result.Rows), result.Rows)
	}
	if result.Rows[0][0] != "1" || result.Rows[1][0] != "3" {
		t.Fatalf("expected id=1 and id=3, got: %v", result.Rows)
	}
}

func TestRLSDeleteRespectsPolicy(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE t (id INT PRIMARY KEY, grp TEXT);")
	executeSQL(t, session, "INSERT INTO t VALUES (1, 'A');")
	executeSQL(t, session, "INSERT INTO t VALUES (2, 'B');")

	executeSQL(t, session, "ENABLE RLS ON t;")
	executeSQL(t, session, "CREATE POLICY p ON t FOR ALL TO public USING (grp = 'A');")

	// Only row with grp='A' is visible
	result := executeSQL(t, session, "SELECT id FROM t;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "1" {
		t.Fatalf("expected only id=1, got: %v", result.Rows)
	}

	// Delete all visible rows
	executeSQL(t, session, "DELETE FROM t;")
	result = executeSQL(t, session, "SELECT id FROM t;")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows after DELETE (all visible were deleted), got %d", len(result.Rows))
	}
}
