package executor

import (
	"testing"

	"vaultdb/internal/parser"
)

// --- H4: DROP TABLE/DROP INDEX must be rejected in migrations ---

func TestMigrationRejectsDropTable(t *testing.T) {
	cases := []string{
		"DROP TABLE people;",
		"DROP TABLE IF EXISTS people;",
	}
	for _, sql := range cases {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		if isMigrationSafe(stmt) {
			t.Errorf("expected unsafe but accepted: %s", sql)
		}
	}
}

func TestMigrationRejectsDropIndex(t *testing.T) {
	stmt, err := parser.Parse("DROP INDEX idx;")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if isMigrationSafe(stmt) {
		t.Fatal("expected unsafe but accepted DROP INDEX")
	}
}

// --- M5: Only ADD COLUMN and ADD CONSTRAINT pass isAlterTableSafe ---

func TestMigrationRejectsDestructiveAlter(t *testing.T) {
	unsafe := []string{
		"ALTER TABLE people DROP COLUMN email;",
		"ALTER TABLE people RENAME COLUMN name TO x;",
		"ALTER TABLE people RENAME TO new_name;",
	}
	for _, sql := range unsafe {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		if isMigrationSafe(stmt) {
			t.Errorf("expected unsafe but accepted: %s", sql)
		}
	}

	safe := []string{
		"ALTER TABLE people ADD COLUMN email TEXT;",
		"ALTER TABLE people ADD CONSTRAINT uniq_email UNIQUE (email);",
	}
	for _, sql := range safe {
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatalf("parse %q: %v", sql, err)
		}
		if !isMigrationSafe(stmt) {
			t.Errorf("expected safe but rejected: %s", sql)
		}
	}
}

func TestIsAlterTableSafe(t *testing.T) {
	tests := []struct {
		sql  string
		safe bool
	}{
		{"ALTER TABLE t ADD COLUMN c INT;", true},
		{"ALTER TABLE t DROP COLUMN c;", false},
		{"ALTER TABLE t RENAME COLUMN a TO b;", false},
		{"ALTER TABLE t RENAME TO new_t;", false},
	}
	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			stmt, err := parser.Parse(tt.sql)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.sql, err)
			}
			alt, ok := stmt.(*parser.AlterTableStatement)
			if !ok {
				t.Fatalf("expected *AlterTableStatement for %q, got %T", tt.sql, stmt)
			}
			got := isAlterTableSafe(alt)
			if got != tt.safe {
				t.Errorf("isAlterTableSafe(%q) = %v, want %v", tt.sql, got, tt.safe)
			}
		})
	}
}

// --- H5: splitSQLStatements respects string literal boundaries ---

func TestSplitSQLStatements(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"simple", "SELECT 1;", 1},
		{"two statements", "INSERT INTO t VALUES (1); INSERT INTO t VALUES (2);", 2},
		{"single-quoted semicolon", "INSERT INTO t VALUES ('hello;world');", 1},
		{"double-quoted semicolon", `INSERT INTO t VALUES ("hello;world");`, 1},
		{"escaped backslash semicolon", `INSERT INTO t VALUES ('hello\;world');`, 1},
		{"mixed", "INSERT INTO t VALUES ('a;b'); SELECT * FROM t WHERE name='c;d';", 2},
		{"empty", "", 0},
		{"trailing content after last semicolon", "SELECT 1; SELECT 2;", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parts := splitSQLStatements(tt.input)
			if len(parts) != tt.expected {
				t.Errorf("got %d parts, want %d: %v", len(parts), tt.expected, parts)
			}
		})
	}
}

func TestSplitSQLPreservesStringContent(t *testing.T) {
	input := "INSERT INTO t VALUES ('hello;world'); SELECT 1;"
	parts := splitSQLStatements(input)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "INSERT INTO t VALUES ('hello;world')" {
		t.Errorf("first part wrong: %q", parts[0])
	}
}

// --- H5: Procedure with semicolons in body parses correctly ---

func TestProcedureMultiStatementSplit(t *testing.T) {
	sess := newSmokeSession(t)
	mustExec(t, sess, `CREATE TABLE proc_split_test (val INT);`)
	mustExec(t, sess,
		"CREATE PROCEDURE multi_insert () AS 'INSERT INTO proc_split_test VALUES (1); INSERT INTO proc_split_test VALUES (2)' LANGUAGE SQL;")
	mustExec(t, sess, "CALL multi_insert();")

	res := mustExec(t, sess, "SELECT COUNT(*) FROM proc_split_test;")
	if res.Rows[0][0] != "2" {
		t.Fatalf("expected 2 rows, got %s", res.Rows[0][0])
	}
}

// --- H6: containsSubqueryDML catches DML in subqueries ---

func TestContainsSubqueryDML_DetectsInsert(t *testing.T) {
	inner, err := parser.Parse("INSERT INTO t VALUES (1);")
	if err != nil {
		t.Fatal(err)
	}

	// SubqueryExpr wrapping INSERT
	sel1 := &parser.SelectStatement{
		Columns:  []parser.SelectColumn{{Expr: &parser.SubqueryExpr{Query: inner}}},
		TableName: "t",
	}
	if !containsSubqueryDML(sel1) {
		t.Error("SubqueryExpr with INSERT not detected")
	}

	// ExistsExpr wrapping INSERT
	sel2 := &parser.SelectStatement{
		Columns:  []parser.SelectColumn{{Expr: &parser.ExistsExpr{Select: inner}}},
		TableName: "t",
	}
	if !containsSubqueryDML(sel2) {
		t.Error("ExistsExpr with INSERT not detected")
	}

	// ComparisonSubqueryExpr wrapping INSERT
	sel3 := &parser.SelectStatement{
		Columns:  []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "id"}}},
		TableName: "t",
		Where: &parser.ComparisonSubqueryExpr{
			Left:     &parser.ColumnRef{Name: "id"},
			Operator: "=",
			Subquery: inner,
		},
	}
	if !containsSubqueryDML(sel3) {
		t.Error("ComparisonSubqueryExpr with INSERT not detected")
	}
}

func TestContainsSubqueryDML_SafeSelects(t *testing.T) {
	stmt1, _ := parser.Parse("SELECT * FROM t")
	if containsSubqueryDML(stmt1.(*parser.SelectStatement)) {
		t.Error("pure SELECT should be safe")
	}

	stmt2, _ := parser.Parse("SELECT * FROM t WHERE id IN (SELECT id FROM t2)")
	if containsSubqueryDML(stmt2.(*parser.SelectStatement)) {
		t.Error("SELECT subquery should be safe")
	}

	stmt3, _ := parser.Parse("SELECT * FROM t WHERE EXISTS (SELECT 1 FROM t2)")
	if containsSubqueryDML(stmt3.(*parser.SelectStatement)) {
		t.Error("EXISTS(SELECT) should be safe")
	}
}

// --- H6: Safe functions with subqueries are accepted ---

func TestFunctionWithSafeSubquery(t *testing.T) {
	sess := newSmokeSession(t)
	mustExec(t, sess, `CREATE FUNCTION safe_func () RETURNS INT AS 'SELECT (SELECT COUNT(*) FROM people)' LANGUAGE SQL;`)
}

func TestFunctionWithCTE(t *testing.T) {
	sess := newSmokeSession(t)
	stmt, err := parser.Parse(`CREATE FUNCTION cte_func () RETURNS INT AS 'WITH cte AS (SELECT 1) SELECT 1' LANGUAGE SQL;`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = sess.Execute(stmt)
	if err != nil {
		t.Fatalf("CTE with SELECT should be allowed: %v", err)
	}
}

// --- H6: Function with non-SELECT body is rejected ---

func TestFunctionRejectsNonSelect(t *testing.T) {
	sess := newSmokeSession(t)
	stmt, err := parser.Parse(`CREATE FUNCTION bad_func () RETURNS INT AS 'INSERT INTO people VALUES (1, 2, 3, 4)' LANGUAGE SQL;`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = sess.Execute(stmt)
	if err == nil {
		t.Fatal("expected error for function with INSERT body")
	}
}
