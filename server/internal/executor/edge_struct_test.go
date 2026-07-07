package executor

import (
	"fmt"
	"strings"
	"testing"

	"vaultdb/internal/parser"
)

func TestEdgeManyColumns(t *testing.T) {
	session := setupSession(t)

	const numCols = 100
	colDefs := make([]string, numCols)
	for i := 0; i < numCols; i++ {
		colDefs[i] = fmt.Sprintf("c%d INT", i)
	}
	executeSQL(t, session, fmt.Sprintf("CREATE TABLE wide_table (%s);", strings.Join(colDefs, ", ")))

	vals := make([]string, numCols)
	for i := 0; i < numCols; i++ {
		vals[i] = fmt.Sprintf("%d", i*10)
	}
	executeSQL(t, session, fmt.Sprintf("INSERT INTO wide_table VALUES (%s);", strings.Join(vals, ", ")))

	res := executeSQL(t, session, "SELECT * FROM wide_table;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if len(res.Rows[0]) != numCols {
		t.Fatalf("expected %d columns, got %d", numCols, len(res.Rows[0]))
	}
	for i, val := range res.Rows[0] {
		expected := fmt.Sprintf("%d", i*10)
		if val != expected {
			t.Fatalf("column c%d: expected %q, got %q", i, expected, val)
		}
	}

	res = executeSQL(t, session, "SELECT c0, c50, c99 FROM wide_table;")
	if res.Rows[0][0] != "0" || res.Rows[0][1] != "500" || res.Rows[0][2] != "990" {
		t.Fatalf("expected [0 500 990], got %v", res.Rows[0])
	}
}

func TestEdgeManyIndexes(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, `CREATE TABLE multi_idx (
		id INT PRIMARY KEY,
		name TEXT,
		age INT,
		score FLOAT,
		active BOOL
	);`)
	executeSQL(t, session, "CREATE INDEX idx_name ON multi_idx(name);")
	executeSQL(t, session, "CREATE INDEX idx_age ON multi_idx(age);")
	executeSQL(t, session, "CREATE INDEX idx_score ON multi_idx(score);")
	executeSQL(t, session, "CREATE INDEX idx_active ON multi_idx(active);")

	for i := 0; i < 20; i++ {
		executeSQL(t, session, fmt.Sprintf(
			"INSERT INTO multi_idx VALUES (%d, 'user%d', %d, %.1f, %v);",
			i, i, 20+i, float64(i)*1.5, i%2 == 0,
		))
	}

	res := executeSQL(t, session, "SELECT COUNT(*) FROM multi_idx;")
	if res.Rows[0][0] != "20" {
		t.Fatalf("expected 20 rows, got %s", res.Rows[0][0])
	}

	res = executeSQL(t, session, "SELECT name FROM multi_idx WHERE age = 25;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "user5" {
		t.Fatalf("expected [user5], got %v", res.Rows)
	}

	res = executeSQL(t, session, "SELECT name FROM multi_idx WHERE active = TRUE ORDER BY id;")
	if len(res.Rows) != 10 {
		t.Fatalf("expected 10 active rows, got %d", len(res.Rows))
	}

	res = executeSQL(t, session, "SELECT name FROM multi_idx WHERE score > 10.0 ORDER BY score;")
	if len(res.Rows) == 0 {
		t.Fatal("expected rows with score > 10, got none")
	}
}

func TestEdgeLongNames(t *testing.T) {
	session := setupSession(t)

	longTable := strings.Repeat("a", 128)
	longCol := strings.Repeat("b", 128)

	executeSQL(t, session, fmt.Sprintf(
		"CREATE TABLE %s (%s INT, name TEXT);", longTable, longCol,
	))
	executeSQL(t, session, fmt.Sprintf(
		"INSERT INTO %s (%s, name) VALUES (42, 'test');", longTable, longCol,
	))

	res := executeSQL(t, session, fmt.Sprintf(
		"SELECT %s, name FROM %s;", longCol, longTable,
	))
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "42" || res.Rows[0][1] != "test" {
		t.Fatalf("expected [42 test], got %v", res.Rows[0])
	}

	executeSQLExpectError(t, session, fmt.Sprintf(
		"SELECT %s FROM nonexistent_table_xyz;", longCol,
	))
}

func TestEdgeReservedWordAsIdentifier(t *testing.T) {
	session := setupSession(t)

	_, err := parser.Parse(`CREATE TABLE "user" ("id" INT, "select" TEXT);`)
	if err == nil {
		t.Fatal("expected parse error for double-quoted identifiers, got nil")
	}

	_, err = parser.Parse("CREATE TABLE select (id INT);")
	if err == nil {
		t.Fatal("expected parse error for 'select' as table name, got nil")
	}

	_, err = parser.Parse("CREATE TABLE order (id INT);")
	if err == nil {
		t.Fatal("expected parse error for 'order' as table name, got nil")
	}

	executeSQL(t, session, "CREATE TABLE user_data (uid INT, sel TEXT);")
	executeSQL(t, session, "INSERT INTO user_data VALUES (1, 'active');")

	res := executeSQL(t, session, "SELECT uid, sel FROM user_data;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "1" || res.Rows[0][1] != "active" {
		t.Fatalf("expected [1 active], got %v", res.Rows[0])
	}
}

func TestEdgeCrossDatabase(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE db1;")
	executeSQL(t, session, "CREATE DATABASE db2;")

	executeSQL(t, session, "USE db1;")
	executeSQL(t, session, "CREATE TABLE t1 (id INT, val TEXT);")
	executeSQL(t, session, "INSERT INTO t1 VALUES (1, 'from_db1');")

	executeSQL(t, session, "USE db2;")
	executeSQL(t, session, "CREATE TABLE t2 (id INT, val TEXT);")
	executeSQL(t, session, "INSERT INTO t2 VALUES (2, 'from_db2');")

	res := executeSQL(t, session, "SELECT * FROM t2;")
	if len(res.Rows) != 1 || res.Rows[0][1] != "from_db2" {
		t.Fatalf("expected [2 from_db2], got %v", res.Rows)
	}

	executeSQL(t, session, "USE db1;")
	res = executeSQL(t, session, "SELECT * FROM t1;")
	if len(res.Rows) != 1 || res.Rows[0][1] != "from_db1" {
		t.Fatalf("expected [1 from_db1], got %v", res.Rows)
	}

	executeSQL(t, session, "USE db2;")
	executeSQLExpectError(t, session, "SELECT * FROM t1;")
}

func TestEdgeSelfRefFK(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, `CREATE TABLE employees (
		id INT PRIMARY KEY,
		name TEXT,
		manager_id INT
	);`)
	executeSQL(t, session, `ALTER TABLE employees ADD CONSTRAINT fk_manager FOREIGN KEY (manager_id) REFERENCES employees(id);`)

	executeSQL(t, session, "INSERT INTO employees (id, name, manager_id) VALUES (1, 'CEO', NULL);")
	executeSQL(t, session, "INSERT INTO employees (id, name, manager_id) VALUES (2, 'VP', 1);")
	executeSQL(t, session, "INSERT INTO employees (id, name, manager_id) VALUES (3, 'Director', 2);")
	executeSQL(t, session, "INSERT INTO employees (id, name, manager_id) VALUES (4, 'Engineer', 3);")

	res := executeSQL(t, session, "SELECT COUNT(*) FROM employees;")
	if res.Rows[0][0] != "4" {
		t.Fatalf("expected 4 rows, got %s", res.Rows[0][0])
	}

	res = executeSQL(t, session, "SELECT name FROM employees WHERE manager_id = 1;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "VP" {
		t.Fatalf("expected [VP], got %v", res.Rows)
	}

	res = executeSQL(t, session, "SELECT name FROM employees WHERE manager_id IS NULL;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "CEO" {
		t.Fatalf("expected [CEO], got %v", res.Rows)
	}

	executeSQLExpectError(t, session, "INSERT INTO employees (id, name, manager_id) VALUES (5, 'Ghost', 999);")

	executeSQL(t, session, "UPDATE employees SET manager_id = 1 WHERE id = 4;")
	res = executeSQL(t, session, "SELECT name FROM employees WHERE manager_id = 1 ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 direct reports to CEO, got %d", len(res.Rows))
	}
}
