package parser

import (
	"testing"
)

func TestCoverStatementTypeMethods(t *testing.T) {
	tests := []struct {
		stmt Statement
		want string
	}{
		{&MergeStatement{}, "MERGE"},
		{&TruncateStatement{}, "TRUNCATE"},
		{&SavepointStatement{}, "SAVEPOINT"},
		{&RollbackToSavepointStatement{}, "ROLLBACK_TO_SAVEPOINT"},
		{&ReleaseSavepointStatement{}, "RELEASE_SAVEPOINT"},
	}
	for _, tt := range tests {
		if got := tt.stmt.StatementType(); got != tt.want {
			t.Errorf("%T.StatementType() = %q, want %q", tt.stmt, got, tt.want)
		}
	}
}

func TestCoverMergeStatement(t *testing.T) {
	queries := []string{
		"MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET name = source.name;",
		"MERGE INTO target USING source ON target.id = source.id WHEN NOT MATCHED THEN INSERT (id, name) VALUES (source.id, source.name);",
		"MERGE INTO target t USING source s ON t.id = s.id WHEN MATCHED AND s.active = TRUE THEN UPDATE SET t.name = s.name;",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
		if _, ok := stmt.(*MergeStatement); !ok {
			t.Fatalf("expected *MergeStatement, got %T", stmt)
		}
	}
}

func TestCoverTruncateWithOptionalTable(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"TRUNCATE TABLE users;", "users"},
		{"TRUNCATE users;", "users"},
	}
	for _, tt := range tests {
		stmt, err := Parse(tt.input)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", tt.input, err)
		}
		tr, ok := stmt.(*TruncateStatement)
		if !ok {
			t.Fatalf("expected *TruncateStatement, got %T", stmt)
		}
		if tr.TableName != tt.want {
			t.Errorf("TableName = %q, want %q", tr.TableName, tt.want)
		}
	}
}

func TestCoverSavepointStatement(t *testing.T) {
	stmt, err := Parse("SAVEPOINT sp1;")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	sp, ok := stmt.(*SavepointStatement)
	if !ok {
		t.Fatalf("expected *SavepointStatement, got %T", stmt)
	}
	if sp.Name != "sp1" {
		t.Errorf("Name = %q, want %q", sp.Name, "sp1")
	}
}

func TestCoverRollbackToSavepointStatement(t *testing.T) {
	stmt, err := Parse("ROLLBACK TO SAVEPOINT sp1;")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	rsp, ok := stmt.(*RollbackToSavepointStatement)
	if !ok {
		t.Fatalf("expected *RollbackToSavepointStatement, got %T", stmt)
	}
	if rsp.Name != "sp1" {
		t.Errorf("Name = %q, want %q", rsp.Name, "sp1")
	}
}

func TestCoverReleaseSavepointStatement(t *testing.T) {
	stmt, err := Parse("RELEASE SAVEPOINT sp1;")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	rsp, ok := stmt.(*ReleaseSavepointStatement)
	if !ok {
		t.Fatalf("expected *ReleaseSavepointStatement, got %T", stmt)
	}
	if rsp.Name != "sp1" {
		t.Errorf("Name = %q, want %q", rsp.Name, "sp1")
	}
}

func TestCoverCTEWithRecursive(t *testing.T) {
	queries := []string{
		"WITH cte AS (SELECT id FROM users) SELECT * FROM cte;",
		"WITH RECURSIVE cte AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM cte WHERE n < 10) SELECT * FROM cte;",
		"WITH t1 AS (SELECT id FROM a), t2 AS (SELECT id FROM b) SELECT * FROM t1 JOIN t2 ON t1.id = t2.id;",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
		cte, ok := stmt.(*CTEStatement)
		if !ok {
			t.Fatalf("expected *CTEStatement for %q, got %T", q, stmt)
		}
		if len(cte.CTEs) == 0 {
			t.Errorf("expected at least one CTE in %q", q)
		}
	}
}

func TestCoverNestedSubqueries(t *testing.T) {
	queries := []string{
		"SELECT * FROM (SELECT * FROM (SELECT id FROM users) sub1) sub2;",
		"SELECT * FROM users WHERE id IN (SELECT id FROM active_users);",
		"SELECT * FROM users WHERE EXISTS (SELECT 1 FROM orders WHERE orders.user_id = users.id);",
		"SELECT * FROM users WHERE id = (SELECT MAX(id) FROM users);",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverParseErrorPaths(t *testing.T) {
	errorQueries := []string{
		"SELECT FROM;",
		"SELECT * FROM;",
		"INSERT;",
		"UPDATE SET;",
		"DELETE;",
		"ALTER TABLE;",
		"DROP;",
		"CREATE;",
		"WITH;",
		"SELECT * FROM t WHERE x =;",
		"SELECT * FROM t WHERE = 1;",
		"SELECT * FROM t WHERE x IN;",
	}
	for _, q := range errorQueries {
		_, err := Parse(q)
		if err == nil {
			t.Errorf("expected error for %q, got none", q)
		}
	}
}

func TestCoverFormatExpressionNil(t *testing.T) {
	got := FormatExpression(nil)
	if got != "" {
		t.Errorf("FormatExpression(nil) = %q, want empty", got)
	}
}

func TestCoverFormatExpressionComplex(t *testing.T) {
	exprs := []Expression{
		&InExpr{
			Left: &ColumnRef{Name: "id"},
			Right: []Expression{
				&Value{Type: "int", IntVal: 1},
				&Value{Type: "int", IntVal: 2},
			},
			Not: true,
		},
		&BetweenExpr{
			Expr:  &ColumnRef{Name: "age"},
			Lower: &Value{Type: "int", IntVal: 18},
			Upper: &Value{Type: "int", IntVal: 65},
		},
		&BinaryExpr{
			Left:     &ColumnRef{Name: "name"},
			Operator: "LIKE",
			Right:    &Value{Type: "string", StrVal: "%test%"},
		},
		&CaseExpr{
			Base: nil,
			Whens: []CaseWhen{
				{
					Condition: &BinaryExpr{
						Left:     &ColumnRef{Name: "x"},
						Operator: ">",
						Right:    &Value{Type: "int", IntVal: 0},
					},
					Result: &Value{Type: "string", StrVal: "positive"},
				},
			},
			Else: &Value{Type: "string", StrVal: "zero"},
		},
		&CastExpr{
			Expr:       &ColumnRef{Name: "val"},
			TargetType: "INT",
		},
		&FunctionCall{
			Name: "COALESCE",
			Args: []Expression{
				&ColumnRef{Name: "a"},
				&Value{Type: "int", IntVal: 0},
			},
		},
		&AggregateExpr{
			Name: "SUM",
			Args: []Expression{&ColumnRef{Name: "amount"}},
		},
	}
	for _, expr := range exprs {
		got := FormatExpression(expr)
		if got == "" {
			t.Errorf("FormatExpression(%T) returned empty", expr)
		}
	}
}

func TestCoverWindowFunctions(t *testing.T) {
	queries := []string{
		"SELECT ROW_NUMBER() OVER (ORDER BY id) FROM users;",
		"SELECT SUM(amount) OVER (PARTITION BY dept ORDER BY date) FROM sales;",
		"SELECT RANK() OVER (PARTITION BY dept ORDER BY score DESC) FROM scores;",
		"SELECT LAG(val, 1) OVER (ORDER BY id) FROM t;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverSetOperations(t *testing.T) {
	queries := []string{
		"SELECT id FROM t1 UNION SELECT id FROM t2;",
		"SELECT id FROM t1 INTERSECT SELECT id FROM t2;",
		"SELECT id FROM t1 EXCEPT SELECT id FROM t2;",
		"SELECT id FROM t1 UNION ALL SELECT id FROM t2;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreatePolicy(t *testing.T) {
	queries := []string{
		"CREATE POLICY user_policy ON users FOR ALL USING (user_id = current_user());",
		"CREATE POLICY insert_policy ON users FOR INSERT WITH CHECK (role = 'admin');",
		"CREATE POLICY update_policy ON users FOR UPDATE USING (id = current_user()) WITH CHECK (role = 'admin');",
		"CREATE POLICY delete_policy ON users FOR DELETE USING (role = 'admin');",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverEnableRls(t *testing.T) {
	queries := []string{
		"ALTER TABLE users ENABLE ROW LEVEL SECURITY;",
		"ALTER TABLE users DISABLE ROW LEVEL SECURITY;",
		"ALTER TABLE users FORCE ROW LEVEL SECURITY;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverAlterTableConstraint(t *testing.T) {
	queries := []string{
		"ALTER TABLE users ADD CONSTRAINT uq_email UNIQUE (email);",
		"ALTER TABLE users ADD CONSTRAINT chk_age CHECK (age > 0);",
		"ALTER TABLE orders ADD CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id);",
		"ALTER TABLE orders ADD CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropPolicy(t *testing.T) {
	queries := []string{
		"DROP POLICY user_policy ON users;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateTriggerStatement(t *testing.T) {
	queries := []string{
		"CREATE TRIGGER audit_insert AFTER INSERT ON users FOR EACH ROW EXECUTE PROCEDURE audit_log();",
		"CREATE TRIGGER check_update BEFORE UPDATE ON orders FOR EACH ROW EXECUTE PROCEDURE validate_order();",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropTriggerStatement(t *testing.T) {
	queries := []string{
		"DROP TRIGGER audit_insert ON users;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateProcedureStatement(t *testing.T) {
	queries := []string{
		"CREATE PROCEDURE test_proc() LANGUAGE SQL AS $$SELECT 1;$$;",
		"CREATE PROCEDURE test_proc() LANGUAGE SQL AS $$INSERT INTO t VALUES (1);$$;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateFunctionStatement(t *testing.T) {
	queries := []string{
		"CREATE FUNCTION test_fn() RETURNS INT LANGUAGE SQL AS $$SELECT 1;$$;",
		"CREATE FUNCTION add(a INT, b INT) RETURNS INT LANGUAGE SQL AS $$SELECT a + b;$$;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropFunctionStatement(t *testing.T) {
	queries := []string{
		"DROP FUNCTION test_fn;",
		"DROP PROCEDURE test_proc;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateIndexStatement(t *testing.T) {
	queries := []string{
		"CREATE INDEX idx_name ON users (name);",
		"CREATE INDEX idx_composite ON orders (user_id, created_at);",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropIndexStatement(t *testing.T) {
	queries := []string{
		"DROP INDEX idx_name;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCallStatement(t *testing.T) {
	queries := []string{
		"CALL my_proc();",
		"CALL my_proc(1, 'test');",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverExplainStatement(t *testing.T) {
	queries := []string{
		"EXPLAIN SELECT * FROM users;",
		"EXPLAIN ANALYZE SELECT * FROM users WHERE id = 1;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverShowStatements(t *testing.T) {
	queries := []string{
		"SHOW DATABASES;",
		"SHOW TABLES;",
		"SHOW TABLES FROM mydb;",
		"SHOW ENCRYPTION STATUS;",
		"SHOW INDEXES FROM users;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDescribeStatement(t *testing.T) {
	queries := []string{
		"DESCRIBE users;",
		"DESCRIBE users FROM mydb;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateViewStatement(t *testing.T) {
	queries := []string{
		"CREATE VIEW active_users AS SELECT * FROM users WHERE active = TRUE;",
		"CREATE OR REPLACE VIEW user_counts AS SELECT user_id, COUNT(*) AS cnt FROM orders GROUP BY user_id;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropViewStatement(t *testing.T) {
	queries := []string{
		"DROP VIEW active_users;",
		"DROP VIEW IF EXISTS active_users;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateTypeStatement(t *testing.T) {
	queries := []string{
		"CREATE TYPE mood AS ENUM ('happy', 'sad', 'neutral');",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropTypeStatement(t *testing.T) {
	queries := []string{
		"DROP TYPE mood;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverColumnDefVariants(t *testing.T) {
	queries := []string{
		"CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(100) NOT NULL, active BOOL DEFAULT TRUE, score FLOAT, bio TEXT, tags TEXT[]);",
		"CREATE TABLE t (id INT UNIQUE, val INT DEFAULT 0);",
		"CREATE TABLE t (id INT AUTO_INCREMENT);",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverColumnTypeVariants(t *testing.T) {
	queries := []string{
		"CREATE TABLE t (a INT, b FLOAT, c BOOL, d TEXT, e VARCHAR(255), f BLOB, g TIMESTAMP, h DATE, i TIME, j JSON, k VECTOR(128), l ENUM('a','b','c'));",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDropStatements(t *testing.T) {
	queries := []string{
		"DROP TABLE users;",
		"DROP TABLE IF EXISTS users;",
		"DROP DATABASE mydb;",
		"DROP DATABASE IF EXISTS mydb;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverAlterTableDropRename(t *testing.T) {
	queries := []string{
		"ALTER TABLE users DROP COLUMN email;",
		"ALTER TABLE users RENAME COLUMN old_name TO new_name;",
		"ALTER TABLE users RENAME TO people;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverAlterTableAlterColumn(t *testing.T) {
	queries := []string{
		"ALTER TABLE users ALTER COLUMN name TYPE VARCHAR(200);",
		"ALTER TABLE users ALTER COLUMN name SET DEFAULT 'unknown';",
		"ALTER TABLE users ALTER COLUMN name DROP DEFAULT;",
		"ALTER TABLE users ALTER COLUMN name SET NOT NULL;",
		"ALTER TABLE users ALTER COLUMN name DROP NOT NULL;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverVacuumStatements(t *testing.T) {
	queries := []string{
		"VACUUM;",
		"VACUUM ANALYZE;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverArchiveAuditLogStatement(t *testing.T) {
	queries := []string{
		"ARCHIVE AUDIT LOG;",
		"ARCHIVE AUDIT LOG TO '/tmp/audit.json';",
		"ARCHIVE AUDIT LOG KEEP 100;",
		"ARCHIVE AUDIT LOG TO '/tmp/audit.json' KEEP 100;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverMigrationStatement(t *testing.T) {
	queries := []string{
		"APPLY MIGRATION add_users;",
		"DROP MIGRATION add_users;",
		"LIST MIGRATIONS;",
		"MIGRATION STATUS;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverVerifyAuditLogStatement(t *testing.T) {
	queries := []string{
		"VERIFY AUDIT LOG;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverParseExpressionErrorPaths(t *testing.T) {
	_, err := ParseExpression("")
	if err == nil {
		t.Fatal("expected error for empty expression")
	}

	_, err = ParseExpression("!!!")
	if err == nil {
		t.Fatal("expected error for illegal expression")
	}
}

func TestCoverComplexWhereClauses(t *testing.T) {
	queries := []string{
		"SELECT * FROM t WHERE a > 1 AND b < 2 OR c = 3;",
		"SELECT * FROM t WHERE NOT (a = 1);",
		"SELECT * FROM t WHERE a BETWEEN 1 AND 10;",
		"SELECT * FROM t WHERE a LIKE '%test%';",
		"SELECT * FROM t WHERE a IS NULL;",
		"SELECT * FROM t WHERE a IS NOT NULL;",
		"SELECT * FROM t WHERE a >= 1 AND (b <= 2 OR c != 3);",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverInsertVariants(t *testing.T) {
	queries := []string{
		"INSERT INTO t VALUES (1, 'a', TRUE);",
		"INSERT INTO t (a, b) VALUES (1, 'a');",
		"INSERT INTO t VALUES (1, 'a'), (2, 'b'), (3, 'c');",
		"INSERT INTO t SELECT * FROM other;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverUpdateVariants(t *testing.T) {
	queries := []string{
		"UPDATE t SET a = 1;",
		"UPDATE t SET a = 1 WHERE b = 2;",
		"UPDATE t SET a = 1, b = 'test' WHERE c > 0;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDeleteVariants(t *testing.T) {
	queries := []string{
		"DELETE FROM t;",
		"DELETE FROM t WHERE id = 1;",
		"DELETE FROM t WHERE a = 1 AND b = 'test';",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverGroupByHaving(t *testing.T) {
	queries := []string{
		"SELECT dept, COUNT(*) FROM emp GROUP BY dept;",
		"SELECT dept, COUNT(*) FROM emp GROUP BY dept HAVING COUNT(*) > 5;",
		"SELECT a, b, SUM(c) FROM t GROUP BY a, b HAVING SUM(c) > 100;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverOrderByDirection(t *testing.T) {
	queries := []string{
		"SELECT * FROM t ORDER BY a;",
		"SELECT * FROM t ORDER BY a ASC;",
		"SELECT * FROM t ORDER BY a DESC;",
		"SELECT * FROM t ORDER BY a DESC, b ASC;",
		"SELECT * FROM t ORDER BY 1;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverLimitOffsetVariants(t *testing.T) {
	queries := []string{
		"SELECT * FROM t LIMIT 10;",
		"SELECT * FROM t LIMIT 10 OFFSET 5;",
		"SELECT * FROM t OFFSET 5;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverDistinctVariants(t *testing.T) {
	queries := []string{
		"SELECT DISTINCT name FROM users;",
		"SELECT DISTINCT ON (dept) name FROM users;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverJoinVariants(t *testing.T) {
	queries := []string{
		"SELECT * FROM a INNER JOIN b ON a.id = b.id;",
		"SELECT * FROM a LEFT JOIN b ON a.id = b.id;",
		"SELECT * FROM a RIGHT JOIN b ON a.id = b.id;",
		"SELECT * FROM a FULL JOIN b ON a.id = b.id;",
		"SELECT * FROM a CROSS JOIN b;",
		"SELECT * FROM a JOIN b ON a.id = b.id;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverFromSubquery(t *testing.T) {
	queries := []string{
		"SELECT * FROM (SELECT id, name FROM users) sub;",
		"SELECT * FROM (SELECT id FROM users WHERE active = TRUE) sub WHERE sub.id > 5;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCaseExpression(t *testing.T) {
	queries := []string{
		"SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t;",
		"SELECT CASE a WHEN 1 THEN 'one' WHEN 2 THEN 'two' ELSE 'other' END FROM t;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverAggregateFunctions(t *testing.T) {
	queries := []string{
		"SELECT COUNT(*) FROM t;",
		"SELECT COUNT(DISTINCT name) FROM t;",
		"SELECT SUM(amount) FROM orders;",
		"SELECT AVG(score) FROM grades;",
		"SELECT MIN(price), MAX(price) FROM products;",
		"SELECT STRING_AGG(name, ',') FROM users;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverCreateTablePartitioned(t *testing.T) {
	queries := []string{
		"CREATE TABLE logs (id INT, hash INT) PARTITION BY HASH (hash) PARTITIONS 4;",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
		ct, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if ct.PartitionBy == nil {
			t.Fatal("expected PartitionBy to be set")
		}
	}
}

func TestCoverExpressionAdvanced(t *testing.T) {
	exprs := []string{
		"a = 1",
		"a > 1 AND b < 2",
		"NOT a",
		"a IN (1, 2, 3)",
		"a BETWEEN 1 AND 10",
		"a LIKE '%test%'",
		"EXISTS (SELECT 1 FROM t)",
		"a > (SELECT MAX(id) FROM t)",
		"COALESCE(a, 0)",
		"NULLIF(a, 0)",
		"CAST(a AS INT)",
	}
	for _, e := range exprs {
		_, err := ParseExpression(e)
		if err != nil {
			t.Fatalf("ParseExpression(%q) error: %v", e, err)
		}
	}
}

func TestCoverCallWithArgs(t *testing.T) {
	queries := []string{
		"CALL my_proc(1, 2.5, 'hello', TRUE, NULL);",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverSelectWithAlias(t *testing.T) {
	queries := []string{
		"SELECT id AS user_id, name AS username FROM users;",
		"SELECT t.id, t.name FROM users t;",
		"SELECT u.id, o.total FROM users u INNER JOIN orders o ON u.id = o.user_id;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverInsertErrorPaths(t *testing.T) {
	errorQueries := []string{
		"INSERT;",
		"INSERT INTO;",
		"INSERT INTO t;",
		"INSERT INTO t VALUES;",
	}
	for _, q := range errorQueries {
		_, err := Parse(q)
		if err == nil {
			t.Errorf("expected error for %q, got none", q)
		}
	}
}

func TestCoverUpdateErrorPaths(t *testing.T) {
	errorQueries := []string{
		"UPDATE;",
		"UPDATE t;",
		"UPDATE t SET;",
	}
	for _, q := range errorQueries {
		_, err := Parse(q)
		if err == nil {
			t.Errorf("expected error for %q, got none", q)
		}
	}
}

func TestCoverDeleteErrorPaths(t *testing.T) {
	errorQueries := []string{
		"DELETE;",
		"DELETE FROM;",
	}
	for _, q := range errorQueries {
		_, err := Parse(q)
		if err == nil {
			t.Errorf("expected error for %q, got none", q)
		}
	}
}

func TestCoverAlterTableAddColumnVariants(t *testing.T) {
	queries := []string{
		"ALTER TABLE t ADD COLUMN a INT;",
		"ALTER TABLE t ADD a INT;",
		"ALTER TABLE t ADD COLUMN a INT DEFAULT 0;",
		"ALTER TABLE t ADD COLUMN a INT NOT NULL;",
		"ALTER TABLE t ADD COLUMN a INT PRIMARY KEY;",
		"ALTER TABLE t ADD COLUMN a INT UNIQUE;",
		"ALTER TABLE t ADD COLUMN a INT AUTO_INCREMENT;",
		"ALTER TABLE t ADD COLUMN a INT ENCRYPTED;",
	}
	for _, q := range queries {
		_, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", q, err)
		}
	}
}

func TestCoverParseExpressionCacheWith(t *testing.T) {
	stmt, err := ParseCachedWith("SELECT 1;", NewStatementCache(10))
	if err != nil {
		t.Fatalf("ParseCachedWith error: %v", err)
	}
	if stmt == nil {
		t.Fatal("expected non-nil statement")
	}

	// Second call should hit cache
	stmt2, err := ParseCachedWith("SELECT 1;", NewStatementCache(10))
	if err != nil {
		t.Fatalf("ParseCachedWith second call error: %v", err)
	}
	if stmt2 == nil {
		t.Fatal("expected non-nil statement from cache")
	}
}

func TestCoverParseExpressionIllegalToken(t *testing.T) {
	_, err := ParseExpression("1 @ 2")
	if err == nil {
		t.Fatal("expected error for illegal token")
	}
}
