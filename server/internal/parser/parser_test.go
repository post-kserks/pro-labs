package parser

import (
	"strings"
	"testing"
)

func TestParseValidStatements(t *testing.T) {
	queries := []string{
		"CREATE DATABASE mydb;",
		"CREATE DATABASE IF NOT EXISTS mydb;",
		"DROP DATABASE mydb;",
		"DROP DATABASE IF EXISTS mydb;",
		"USE mydb;",
		"CREATE TABLE heroes (id INT, name VARCHAR(100), alive BOOL);",
		"DROP TABLE heroes;",
		"SHOW DATABASES;",
		"SHOW TABLES;",
		"SHOW TABLES FROM mydb;",
		"SHOW ENCRYPTION STATUS;",
		"DESCRIBE heroes;",
		"DESCRIBE heroes FROM mydb;",
		"SELECT * FROM heroes;",
		"SELECT * FROM heroes LIMIT 10;",
		"SELECT COUNT(*) FROM heroes;",
		"SELECT id, name FROM heroes WHERE level > 5;",
		"SELECT * FROM heroes WHERE alive = TRUE AND level >= 3;",
		"SELECT * FROM heroes WHERE NOT (level < 2) OR name = 'Gimli';",
		"SELECT * FROM heroes VERSION 5;",
		"SELECT * FROM heroes AS OF TIMESTAMP '2025-08-01 12:00:00';",
		"EXPLAIN SELECT * FROM heroes;",
		"EXPLAIN ANALYZE SELECT * FROM heroes WHERE level > 5;",
		"HISTORY heroes KEY 1;",
		"INSERT INTO heroes VALUES (1, 'Aragorn', 10);",
		"INSERT INTO heroes (id, name) VALUES (1, 'test'), (2, 'test2');",
		"UPDATE heroes SET level = 11 WHERE id = 1;",
		"DELETE FROM heroes WHERE alive = FALSE;",
		"ALTER TABLE heroes ADD COLUMN age INT;",
		"ALTER TABLE heroes DROP COLUMN alive;",
		"ALTER TABLE heroes RENAME COLUMN level TO exp;",
	}

	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			if _, err := Parse(query); err != nil {
				t.Fatalf("Parse(%q) returned error: %v", query, err)
			}
		})
	}
}

func TestParseSelectShape(t *testing.T) {
	stmt, err := Parse("SELECT id, name FROM heroes WHERE level > 5;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	sel, ok := stmt.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
	if len(sel.Columns) != 2 {
		t.Fatalf("unexpected number of columns: %d", len(sel.Columns))
	}
	col1, ok1 := sel.Columns[0].Expr.(*ColumnRef)
	col2, ok2 := sel.Columns[1].Expr.(*ColumnRef)
	if !ok1 || !ok2 || col1.Name != "id" || col2.Name != "name" {
		t.Fatalf("unexpected columns: %#v", sel.Columns)
	}
	if sel.TableName != "heroes" {
		t.Fatalf("unexpected table name: %s", sel.TableName)
	}
	if sel.Where == nil {
		t.Fatal("expected WHERE expression")
	}
}

func TestParseTimeTravelShape(t *testing.T) {
	stmt, err := Parse("SELECT * FROM heroes VERSION 42;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	sel, ok := stmt.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
	if sel.AsOf == nil || !sel.AsOf.UseVersion || sel.AsOf.Version != 42 {
		t.Fatalf("unexpected as_of clause: %#v", sel.AsOf)
	}
}

func TestParseVersionAsColumnName(t *testing.T) {
	t.Run("CREATE TABLE with version column", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE deals (id INT PRIMARY KEY, version INT);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(create.Columns))
		}
		if create.Columns[1].Name != "version" {
			t.Fatalf("expected column name 'version', got %q", create.Columns[1].Name)
		}
	})

	t.Run("SELECT with version in WHERE clause", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM deals WHERE version = 1;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel, ok := stmt.(*SelectStatement)
		if !ok {
			t.Fatalf("expected *SelectStatement, got %T", stmt)
		}
		if sel.Where == nil {
			t.Fatal("expected WHERE clause")
		}
	})

	t.Run("INSERT with version column", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO deals (id, version) VALUES (1, 1);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		insert, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if len(insert.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(insert.Columns))
		}
		if insert.Columns[1] != "version" {
			t.Fatalf("expected column name 'version', got %q", insert.Columns[1])
		}
	})

	t.Run("Time travel with VERSION still works", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t VERSION 5;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel, ok := stmt.(*SelectStatement)
		if !ok {
			t.Fatalf("expected *SelectStatement, got %T", stmt)
		}
		if sel.AsOf == nil || !sel.AsOf.UseVersion || sel.AsOf.Version != 5 {
			t.Fatalf("unexpected as_of clause: %#v", sel.AsOf)
		}
	})
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"CREATE TABLE heroes (id DOUBLE);",
		"INSERT INTO heroes VALUES ();",
	}

	for _, query := range cases {
		if _, err := Parse(query); err == nil {
			t.Fatalf("expected parsing error for %q", query)
		}
	}
}

func TestParseDerivedTable(t *testing.T) {
	queries := []string{
		"SELECT * FROM (SELECT id, name FROM users) AS t;",
		"SELECT t.id, t.name FROM (SELECT id, name FROM users WHERE age > 18) t;",
		"SELECT * FROM (SELECT id, name, level FROM heroes WHERE level > 5) AS high_level;",
		"SELECT * FROM (SELECT id FROM users) AS a;",
	}
	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("failed to parse derived table: %q: %v", q, err)
		}
		sel, ok := stmt.(*SelectStatement)
		if !ok {
			t.Fatalf("expected SelectStatement for %q", q)
		}
		if sel.FromSubquery == nil {
			t.Fatalf("expected FromSubquery for %q", q)
		}
	}
}

func TestParseAlterTable(t *testing.T) {
	t.Run("ADD COLUMN", func(t *testing.T) {
		stmt, err := Parse("ALTER TABLE t ADD COLUMN age INT;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		alt, ok := stmt.(*AlterTableStatement)
		if !ok {
			t.Fatalf("expected *AlterTableStatement, got %T", stmt)
		}
		if alt.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", alt.TableName)
		}
		add, ok := alt.Action.(*AlterAddColumn)
		if !ok {
			t.Fatalf("expected *AlterAddColumn, got %T", alt.Action)
		}
		if add.Column.Name != "age" || add.Column.DataType != "INT" {
			t.Fatalf("unexpected column: %+v", add.Column)
		}
	})

	t.Run("DROP COLUMN", func(t *testing.T) {
		stmt, err := Parse("ALTER TABLE t DROP COLUMN age;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		alt, ok := stmt.(*AlterTableStatement)
		if !ok {
			t.Fatalf("expected *AlterTableStatement, got %T", stmt)
		}
		drop, ok := alt.Action.(*AlterDropColumn)
		if !ok {
			t.Fatalf("expected *AlterDropColumn, got %T", alt.Action)
		}
		if drop.ColumnName != "age" {
			t.Fatalf("expected column name 'age', got %q", drop.ColumnName)
		}
	})

	t.Run("RENAME COLUMN", func(t *testing.T) {
		stmt, err := Parse("ALTER TABLE t RENAME COLUMN old_name TO new_name;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		alt, ok := stmt.(*AlterTableStatement)
		if !ok {
			t.Fatalf("expected *AlterTableStatement, got %T", stmt)
		}
		ren, ok := alt.Action.(*AlterRenameColumn)
		if !ok {
			t.Fatalf("expected *AlterRenameColumn, got %T", alt.Action)
		}
		if ren.OldName != "old_name" || ren.NewName != "new_name" {
			t.Fatalf("unexpected rename: old=%q new=%q", ren.OldName, ren.NewName)
		}
	})

	t.Run("RENAME TABLE", func(t *testing.T) {
		stmt, err := Parse("ALTER TABLE t RENAME TO new_table;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		alt, ok := stmt.(*AlterTableStatement)
		if !ok {
			t.Fatalf("expected *AlterTableStatement, got %T", stmt)
		}
		ren, ok := alt.Action.(*AlterRenameTable)
		if !ok {
			t.Fatalf("expected *AlterRenameTable, got %T", alt.Action)
		}
		if ren.NewName != "new_table" {
			t.Fatalf("expected new name 'new_table', got %q", ren.NewName)
		}
	})
}

func TestParseInsertWithConflict(t *testing.T) {
	t.Run("DO NOTHING", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t (id, name) VALUES (1, 'a') ON CONFLICT DO NOTHING;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if ins.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", ins.TableName)
		}
		if ins.OnConflict == nil {
			t.Fatal("expected OnConflict clause")
		}
		if ins.OnConflict.Action != "NOTHING" {
			t.Fatalf("expected action 'NOTHING', got %q", ins.OnConflict.Action)
		}
	})

	t.Run("DO UPDATE SET", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t (id, name) VALUES (1, 'a') ON CONFLICT DO UPDATE SET name = 'b';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if ins.OnConflict == nil {
			t.Fatal("expected OnConflict clause")
		}
		if ins.OnConflict.Action != "UPDATE" {
			t.Fatalf("expected action 'UPDATE', got %q", ins.OnConflict.Action)
		}
		if len(ins.OnConflict.Assignments) != 1 {
			t.Fatalf("expected 1 assignment, got %d", len(ins.OnConflict.Assignments))
		}
		if ins.OnConflict.Assignments[0].Column != "name" {
			t.Fatalf("expected assignment column 'name', got %q", ins.OnConflict.Assignments[0].Column)
		}
	})

	t.Run("conflict target columns DO UPDATE", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t (id, name) VALUES (1, 'test') ON CONFLICT (id) DO UPDATE SET name = 'updated';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if ins.OnConflict == nil {
			t.Fatal("expected OnConflict clause")
		}
		if len(ins.OnConflict.Columns) != 1 || ins.OnConflict.Columns[0] != "id" {
			t.Fatalf("expected conflict columns [id], got %v", ins.OnConflict.Columns)
		}
		if ins.OnConflict.Action != "UPDATE" {
			t.Fatalf("expected action 'UPDATE', got %q", ins.OnConflict.Action)
		}
	})

	t.Run("conflict target columns DO NOTHING", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t (id, name) VALUES (1, 'a') ON CONFLICT (id, name) DO NOTHING;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if ins.OnConflict == nil {
			t.Fatal("expected OnConflict clause")
		}
		if len(ins.OnConflict.Columns) != 2 || ins.OnConflict.Columns[0] != "id" || ins.OnConflict.Columns[1] != "name" {
			t.Fatalf("expected conflict columns [id name], got %v", ins.OnConflict.Columns)
		}
		if ins.OnConflict.Action != "NOTHING" {
			t.Fatalf("expected action 'NOTHING', got %q", ins.OnConflict.Action)
		}
	})
}

func TestParseInsertWithReturning(t *testing.T) {
	t.Run("RETURNING star", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t (id, name) VALUES (1, 'a') RETURNING *;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if ins.Returning == nil {
			t.Fatal("expected Returning clause")
		}
		if len(ins.Returning) != 0 {
			t.Fatalf("expected 0 columns for RETURNING *, got %d", len(ins.Returning))
		}
	})

	t.Run("RETURNING columns", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t (id, name) VALUES (1, 'a') RETURNING id, name;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if len(ins.Returning) != 2 {
			t.Fatalf("expected 2 returning columns, got %d", len(ins.Returning))
		}
	})
}

func TestParseUpdateWithReturning(t *testing.T) {
	stmt, err := Parse("UPDATE t SET name = 'b' WHERE id = 1 RETURNING *;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	upd, ok := stmt.(*UpdateStatement)
	if !ok {
		t.Fatalf("expected *UpdateStatement, got %T", stmt)
	}
	if upd.TableName != "t" {
		t.Fatalf("expected table name 't', got %q", upd.TableName)
	}
	if len(upd.Assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(upd.Assignments))
	}
	if upd.Where == nil {
		t.Fatal("expected WHERE expression")
	}
	if upd.Returning == nil {
		t.Fatal("expected Returning clause")
	}
	if len(upd.Returning) != 0 {
		t.Fatalf("expected 0 returning columns for RETURNING *, got %d", len(upd.Returning))
	}
}

func TestParseDeleteWithReturning(t *testing.T) {
	stmt, err := Parse("DELETE FROM t WHERE id = 1 RETURNING *;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	del, ok := stmt.(*DeleteStatement)
	if !ok {
		t.Fatalf("expected *DeleteStatement, got %T", stmt)
	}
	if del.TableName != "t" {
		t.Fatalf("expected table name 't', got %q", del.TableName)
	}
	if del.Where == nil {
		t.Fatal("expected WHERE expression")
	}
	if del.Returning == nil {
		t.Fatal("expected Returning clause")
	}
	if len(del.Returning) != 0 {
		t.Fatalf("expected 0 returning columns for RETURNING *, got %d", len(del.Returning))
	}
}

func TestParseMerge(t *testing.T) {
	stmt, err := Parse("MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET name = source.name WHEN NOT MATCHED THEN INSERT (id, name) VALUES (source.id, source.name);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	merge, ok := stmt.(*MergeStatement)
	if !ok {
		t.Fatalf("expected *MergeStatement, got %T", stmt)
	}
	if merge.TargetTable != "target" {
		t.Fatalf("expected target table 'target', got %q", merge.TargetTable)
	}
	if merge.SourceTable != "source" {
		t.Fatalf("expected source table 'source', got %q", merge.SourceTable)
	}
	if merge.OnCondition == nil {
		t.Fatal("expected ON condition")
	}
	if merge.WhenMatched == nil {
		t.Fatal("expected WHEN MATCHED clause")
	}
	if merge.WhenMatched.Action != "UPDATE" {
		t.Fatalf("expected WHEN MATCHED action 'UPDATE', got %q", merge.WhenMatched.Action)
	}
	if merge.WhenNotMatched == nil {
		t.Fatal("expected WHEN NOT MATCHED clause")
	}
	if merge.WhenNotMatched.Action != "INSERT" {
		t.Fatalf("expected WHEN NOT MATCHED action 'INSERT', got %q", merge.WhenNotMatched.Action)
	}
	if len(merge.WhenNotMatched.Columns) != 2 {
		t.Fatalf("expected 2 insert columns, got %d", len(merge.WhenNotMatched.Columns))
	}
	if len(merge.WhenNotMatched.Values) != 1 {
		t.Fatalf("expected 1 insert values row, got %d", len(merge.WhenNotMatched.Values))
	}
}

func TestParseTruncate(t *testing.T) {
	t.Run("with TABLE keyword", func(t *testing.T) {
		stmt, err := Parse("TRUNCATE TABLE t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		trunc, ok := stmt.(*TruncateStatement)
		if !ok {
			t.Fatalf("expected *TruncateStatement, got %T", stmt)
		}
		if trunc.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", trunc.TableName)
		}
	})

	t.Run("without TABLE keyword", func(t *testing.T) {
		stmt, err := Parse("TRUNCATE t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		trunc, ok := stmt.(*TruncateStatement)
		if !ok {
			t.Fatalf("expected *TruncateStatement, got %T", stmt)
		}
		if trunc.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", trunc.TableName)
		}
	})
}

func TestParseCTE(t *testing.T) {
	t.Run("simple CTE", func(t *testing.T) {
		stmt, err := Parse("WITH cte AS (SELECT id FROM users) SELECT * FROM cte;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		cteStmt, ok := stmt.(*CTEStatement)
		if !ok {
			t.Fatalf("expected *CTEStatement, got %T", stmt)
		}
		if len(cteStmt.CTEs) != 1 {
			t.Fatalf("expected 1 CTE, got %d", len(cteStmt.CTEs))
		}
		if cteStmt.CTEs[0].Name != "cte" {
			t.Fatalf("expected CTE name 'cte', got %q", cteStmt.CTEs[0].Name)
		}
		if cteStmt.CTEs[0].Query == nil {
			t.Fatal("expected CTE query")
		}
		if cteStmt.Recursive {
			t.Fatal("expected non-recursive CTE")
		}
		body, ok := cteStmt.Body.(*SelectStatement)
		if !ok {
			t.Fatalf("expected body *SelectStatement, got %T", cteStmt.Body)
		}
		if body.TableName != "cte" {
			t.Fatalf("expected body table name 'cte', got %q", body.TableName)
		}
	})

	t.Run("recursive CTE", func(t *testing.T) {
		stmt, err := Parse("WITH RECURSIVE cte AS (SELECT 1 AS n) SELECT * FROM cte;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		cteStmt, ok := stmt.(*CTEStatement)
		if !ok {
			t.Fatalf("expected *CTEStatement, got %T", stmt)
		}
		if !cteStmt.Recursive {
			t.Fatal("expected recursive CTE")
		}
		if len(cteStmt.CTEs) != 1 {
			t.Fatalf("expected 1 CTE, got %d", len(cteStmt.CTEs))
		}
		if cteStmt.CTEs[0].Name != "cte" {
			t.Fatalf("expected CTE name 'cte', got %q", cteStmt.CTEs[0].Name)
		}
	})

	t.Run("CTE with column aliases", func(t *testing.T) {
		stmt, err := Parse("WITH cte (a, b) AS (SELECT id, name FROM users) SELECT * FROM cte;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		cteStmt, ok := stmt.(*CTEStatement)
		if !ok {
			t.Fatalf("expected *CTEStatement, got %T", stmt)
		}
		if len(cteStmt.CTEs[0].Columns) != 2 {
			t.Fatalf("expected 2 CTE columns, got %d", len(cteStmt.CTEs[0].Columns))
		}
		if cteStmt.CTEs[0].Columns[0] != "a" || cteStmt.CTEs[0].Columns[1] != "b" {
			t.Fatalf("unexpected CTE columns: %v", cteStmt.CTEs[0].Columns)
		}
	})

	t.Run("multiple CTEs", func(t *testing.T) {
		stmt, err := Parse("WITH cte1 AS (SELECT 1), cte2 AS (SELECT 2) SELECT * FROM cte1;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		cteStmt, ok := stmt.(*CTEStatement)
		if !ok {
			t.Fatalf("expected *CTEStatement, got %T", stmt)
		}
		if len(cteStmt.CTEs) != 2 {
			t.Fatalf("expected 2 CTEs, got %d", len(cteStmt.CTEs))
		}
		if cteStmt.CTEs[0].Name != "cte1" || cteStmt.CTEs[1].Name != "cte2" {
			t.Fatalf("unexpected CTE names: %q, %q", cteStmt.CTEs[0].Name, cteStmt.CTEs[1].Name)
		}
	})
}

func TestParseCreateView(t *testing.T) {
	stmt, err := Parse("CREATE VIEW v AS SELECT id, name FROM users;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	view, ok := stmt.(*CreateViewStatement)
	if !ok {
		t.Fatalf("expected *CreateViewStatement, got %T", stmt)
	}
	if view.Name != "v" {
		t.Fatalf("expected view name 'v', got %q", view.Name)
	}
	if view.Query == nil {
		t.Fatal("expected view query")
	}
	if view.Query.TableName != "users" {
		t.Fatalf("expected view query table 'users', got %q", view.Query.TableName)
	}
	if view.OrReplace {
		t.Fatal("expected OrReplace false")
	}
}

func TestParseCreateTrigger(t *testing.T) {
	stmt, err := Parse("CREATE TRIGGER my_trigger BEFORE INSERT ON my_table BEGIN SELECT 1; END;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	trig, ok := stmt.(*CreateTriggerStatement)
	if !ok {
		t.Fatalf("expected *CreateTriggerStatement, got %T", stmt)
	}
	if trig.Name != "my_trigger" {
		t.Fatalf("expected trigger name 'my_trigger', got %q", trig.Name)
	}
	if trig.TableName != "my_table" {
		t.Fatalf("expected table name 'my_table', got %q", trig.TableName)
	}
	if trig.Timing != "BEFORE" {
		t.Fatalf("expected timing 'BEFORE', got %q", trig.Timing)
	}
	if trig.Event != "INSERT" {
		t.Fatalf("expected event 'INSERT', got %q", trig.Event)
	}
	if trig.Body == "" {
		t.Fatal("expected non-empty trigger body")
	}
}

func TestParseCreateFunction(t *testing.T) {
	stmt, err := Parse("CREATE FUNCTION my_func(a, b) RETURNS INT AS 'RETURN a + b' LANGUAGE plpgsql;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	fn, ok := stmt.(*CreateFunctionStatement)
	if !ok {
		t.Fatalf("expected *CreateFunctionStatement, got %T", stmt)
	}
	if fn.Name != "my_func" {
		t.Fatalf("expected function name 'my_func', got %q", fn.Name)
	}
	if len(fn.Params) != 2 || fn.Params[0] != "a" || fn.Params[1] != "b" {
		t.Fatalf("expected params [a, b], got %v", fn.Params)
	}
	if fn.ReturnType != "INT" {
		t.Fatalf("expected return type 'INT', got %q", fn.ReturnType)
	}
	if fn.Body != "RETURN a + b" {
		t.Fatalf("expected body 'RETURN a + b', got %q", fn.Body)
	}
	if fn.Language != "PLPGSQL" {
		t.Fatalf("expected language 'PLPGSQL', got %q", fn.Language)
	}
}

func TestParseCreateFunctionWASM(t *testing.T) {
	t.Run("LANGUAGE WASM before AS", func(t *testing.T) {
		stmt, err := Parse("CREATE FUNCTION hash_pii(value) RETURNS TEXT LANGUAGE WASM AS 'file:///plugins/hash_pii.wasm';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		fn, ok := stmt.(*CreateFunctionStatement)
		if !ok {
			t.Fatalf("expected *CreateFunctionStatement, got %T", stmt)
		}
		if fn.Name != "hash_pii" {
			t.Fatalf("expected function name 'hash_pii', got %q", fn.Name)
		}
		if fn.Language != "WASM" {
			t.Fatalf("expected language 'WASM', got %q", fn.Language)
		}
		if fn.Body != "file:///plugins/hash_pii.wasm" {
			t.Fatalf("expected body 'file:///plugins/hash_pii.wasm', got %q", fn.Body)
		}
		if fn.Options != nil {
			t.Fatalf("expected nil options, got %v", fn.Options)
		}
	})

	t.Run("LANGUAGE WASM with WITH options", func(t *testing.T) {
		stmt, err := Parse("CREATE FUNCTION hash_pii(value) RETURNS TEXT LANGUAGE WASM AS 'file:///plugins/hash_pii.wasm' WITH (memory_limit = '16MB', timeout = '100ms');")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		fn, ok := stmt.(*CreateFunctionStatement)
		if !ok {
			t.Fatalf("expected *CreateFunctionStatement, got %T", stmt)
		}
		if fn.Language != "WASM" {
			t.Fatalf("expected language 'WASM', got %q", fn.Language)
		}
		if fn.Options == nil {
			t.Fatal("expected non-nil options")
		}
		if fn.Options["memory_limit"] != "16MB" {
			t.Fatalf("expected memory_limit '16MB', got %q", fn.Options["memory_limit"])
		}
		if fn.Options["timeout"] != "100ms" {
			t.Fatalf("expected timeout '100ms', got %q", fn.Options["timeout"])
		}
	})

	t.Run("existing SQL function still works", func(t *testing.T) {
		stmt, err := Parse("CREATE FUNCTION calc_price(qty, price) RETURNS FLOAT LANGUAGE SQL AS 'SELECT qty * price';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		fn, ok := stmt.(*CreateFunctionStatement)
		if !ok {
			t.Fatalf("expected *CreateFunctionStatement, got %T", stmt)
		}
		if fn.Language != "SQL" {
			t.Fatalf("expected language 'SQL', got %q", fn.Language)
		}
		if fn.Body != "SELECT qty * price" {
			t.Fatalf("expected body 'SELECT qty * price', got %q", fn.Body)
		}
	})
}

func TestParseCreateProcedureWASM(t *testing.T) {
	t.Run("LANGUAGE WASM before AS", func(t *testing.T) {
		stmt, err := Parse("CREATE PROCEDURE run_wasm() LANGUAGE WASM AS 'file:///plugins/runner.wasm';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		proc, ok := stmt.(*CreateProcedureStatement)
		if !ok {
			t.Fatalf("expected *CreateProcedureStatement, got %T", stmt)
		}
		if proc.Name != "run_wasm" {
			t.Fatalf("expected procedure name 'run_wasm', got %q", proc.Name)
		}
		if proc.Language != "WASM" {
			t.Fatalf("expected language 'WASM', got %q", proc.Language)
		}
		if proc.Body != "file:///plugins/runner.wasm" {
			t.Fatalf("expected body 'file:///plugins/runner.wasm', got %q", proc.Body)
		}
	})

	t.Run("LANGUAGE WASM with WITH options", func(t *testing.T) {
		stmt, err := Parse("CREATE PROCEDURE run_wasm() LANGUAGE WASM AS 'file:///plugins/runner.wasm' WITH (timeout = '500ms');")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		proc, ok := stmt.(*CreateProcedureStatement)
		if !ok {
			t.Fatalf("expected *CreateProcedureStatement, got %T", stmt)
		}
		if proc.Options == nil {
			t.Fatal("expected non-nil options")
		}
		if proc.Options["timeout"] != "500ms" {
			t.Fatalf("expected timeout '500ms', got %q", proc.Options["timeout"])
		}
	})
}

func TestParseCreateIndex(t *testing.T) {
	t.Run("simple index", func(t *testing.T) {
		stmt, err := Parse("CREATE INDEX idx ON t(col);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		idx, ok := stmt.(*CreateIndexStatement)
		if !ok {
			t.Fatalf("expected *CreateIndexStatement, got %T", stmt)
		}
		if idx.IndexName != "idx" {
			t.Fatalf("expected index name 'idx', got %q", idx.IndexName)
		}
		if idx.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", idx.TableName)
		}
		if idx.Column != "col" {
			t.Fatalf("expected column 'col', got %q", idx.Column)
		}
	})
}

func TestParsePrepare(t *testing.T) {
	t.Run("PREPARE", func(t *testing.T) {
		stmt, err := Parse("PREPARE my_query AS SELECT id FROM users WHERE id = $1;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		prep, ok := stmt.(*PrepareStatement)
		if !ok {
			t.Fatalf("expected *PrepareStatement, got %T", stmt)
		}
		if prep.Name != "my_query" {
			t.Fatalf("expected name 'my_query', got %q", prep.Name)
		}
		if prep.Query == nil {
			t.Fatal("expected non-nil query")
		}
		if _, ok := prep.Query.(*SelectStatement); !ok {
			t.Fatalf("expected query to be *SelectStatement, got %T", prep.Query)
		}
	})

	t.Run("EXECUTE", func(t *testing.T) {
		stmt, err := Parse("EXECUTE my_query;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		exec, ok := stmt.(*ExecuteStatement)
		if !ok {
			t.Fatalf("expected *ExecuteStatement, got %T", stmt)
		}
		if exec.Name != "my_query" {
			t.Fatalf("expected name 'my_query', got %q", exec.Name)
		}
		if len(exec.Params) != 0 {
			t.Fatalf("expected 0 params, got %d", len(exec.Params))
		}
	})

	t.Run("DEALLOCATE", func(t *testing.T) {
		stmt, err := Parse("DEALLOCATE my_query;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		dealloc, ok := stmt.(*DeallocateStatement)
		if !ok {
			t.Fatalf("expected *DeallocateStatement, got %T", stmt)
		}
		if dealloc.Name != "my_query" {
			t.Fatalf("expected name 'my_query', got %q", dealloc.Name)
		}
	})
}

func TestParseExplain(t *testing.T) {
	t.Run("EXPLAIN", func(t *testing.T) {
		stmt, err := Parse("EXPLAIN SELECT * FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		expl, ok := stmt.(*ExplainStatement)
		if !ok {
			t.Fatalf("expected *ExplainStatement, got %T", stmt)
		}
		if expl.Inner == nil {
			t.Fatal("expected non-nil Inner")
		}
		if expl.Analyze {
			t.Fatal("expected Analyze false")
		}
	})

	t.Run("EXPLAIN ANALYZE", func(t *testing.T) {
		stmt, err := Parse("EXPLAIN ANALYZE SELECT * FROM t WHERE id > 5;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		expl, ok := stmt.(*ExplainStatement)
		if !ok {
			t.Fatalf("expected *ExplainStatement, got %T", stmt)
		}
		if !expl.Analyze {
			t.Fatal("expected Analyze true")
		}
		if expl.Inner == nil {
			t.Fatal("expected non-nil Inner")
		}
		if expl.Inner.Where == nil {
			t.Fatal("expected WHERE in inner select")
		}
	})
}

func TestParseVacuum(t *testing.T) {
	t.Run("VACUUM table", func(t *testing.T) {
		stmt, err := Parse("VACUUM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		vac, ok := stmt.(*VacuumStatement)
		if !ok {
			t.Fatalf("expected *VacuumStatement, got %T", stmt)
		}
		if vac.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", vac.TableName)
		}
		if vac.Analyze {
			t.Fatal("expected Analyze false")
		}
	})

	t.Run("VACUUM ANALYZE table", func(t *testing.T) {
		stmt, err := Parse("VACUUM ANALYZE t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		vac, ok := stmt.(*VacuumStatement)
		if !ok {
			t.Fatalf("expected *VacuumStatement, got %T", stmt)
		}
		if vac.TableName != "t" {
			t.Fatalf("expected table name 't', got %q", vac.TableName)
		}
		if !vac.Analyze {
			t.Fatal("expected Analyze true")
		}
	})

	t.Run("VACUUM without table", func(t *testing.T) {
		stmt, err := Parse("VACUUM;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		vac, ok := stmt.(*VacuumStatement)
		if !ok {
			t.Fatalf("expected *VacuumStatement, got %T", stmt)
		}
		if vac.TableName != "" {
			t.Fatalf("expected empty table name, got %q", vac.TableName)
		}
	})
}

func TestParseExpressionIn(t *testing.T) {
	t.Run("IN list", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col IN (1, 2, 3);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		inExpr, ok := sel.Where.(*InExpr)
		if !ok {
			t.Fatalf("expected *InExpr, got %T", sel.Where)
		}
		if inExpr.Not {
			t.Fatal("expected Not=false")
		}
		if len(inExpr.Right) != 3 {
			t.Fatalf("expected 3 values, got %d", len(inExpr.Right))
		}
	})

	t.Run("IN subquery", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col IN (SELECT id FROM t2);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		inExpr, ok := sel.Where.(*InExpr)
		if !ok {
			t.Fatalf("expected *InExpr, got %T", sel.Where)
		}
		if len(inExpr.Right) != 1 {
			t.Fatalf("expected 1 subquery, got %d", len(inExpr.Right))
		}
		sub, ok := inExpr.Right[0].(*SubqueryExpr)
		if !ok {
			t.Fatalf("expected *SubqueryExpr, got %T", inExpr.Right[0])
		}
		if sub.Query == nil {
			t.Fatal("expected non-nil subquery")
		}
	})
}

func TestParseExpressionExists(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	exists, ok := sel.Where.(*ExistsExpr)
	if !ok {
		t.Fatalf("expected *ExistsExpr, got %T", sel.Where)
	}
	if exists.Not {
		t.Fatal("expected Not=false")
	}
	if exists.Select == nil {
		t.Fatal("expected non-nil Select")
	}
	selStmt, ok := exists.Select.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", exists.Select)
	}
	if selStmt.TableName != "t2" {
		t.Fatalf("expected subquery table 't2', got %q", selStmt.TableName)
	}
}

func TestParseExpressionBetween(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col BETWEEN 1 AND 10;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	between, ok := sel.Where.(*BetweenExpr)
	if !ok {
		t.Fatalf("expected *BetweenExpr, got %T", sel.Where)
	}
	if between.Not {
		t.Fatal("expected Not=false")
	}
	left, ok := between.Expr.(*ColumnRef)
	if !ok {
		t.Fatalf("expected *ColumnRef on Expr, got %T", between.Expr)
	}
	if left.Name != "col" {
		t.Fatalf("expected column 'col', got %q", left.Name)
	}
}

func TestParseExpressionSubquery(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col = (SELECT MAX(id) FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	bin, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	if bin.Operator != "=" {
		t.Fatalf("expected operator '=', got %q", bin.Operator)
	}
	sub, ok := bin.Right.(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr on right, got %T", bin.Right)
	}
	if sub.Query == nil {
		t.Fatal("expected non-nil subquery")
	}
}

func TestParseExpressionWindow(t *testing.T) {
	stmt, err := Parse("SELECT ROW_NUMBER() OVER (ORDER BY col) FROM t;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(sel.Columns))
	}
	win, ok := sel.Columns[0].Expr.(*WindowFunctionExpr)
	if !ok {
		t.Fatalf("expected *WindowFunctionExpr, got %T", sel.Columns[0].Expr)
	}
	if win.FuncName != "ROW_NUMBER" {
		t.Fatalf("expected func name 'ROW_NUMBER', got %q", win.FuncName)
	}
	if len(win.Over.OrderBy) != 1 {
		t.Fatalf("expected 1 ORDER BY item, got %d", len(win.Over.OrderBy))
	}
	if win.Over.OrderBy[0].Direction != "ASC" {
		t.Fatalf("expected direction 'ASC', got %q", win.Over.OrderBy[0].Direction)
	}
}

func TestParseExpressionCast(t *testing.T) {
	stmt, err := Parse("SELECT CAST(col AS INT) FROM t;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(sel.Columns))
	}
	cast, ok := sel.Columns[0].Expr.(*CastExpr)
	if !ok {
		t.Fatalf("expected *CastExpr, got %T", sel.Columns[0].Expr)
	}
	if cast.TargetType != "INT" {
		t.Fatalf("expected target type 'INT', got %q", cast.TargetType)
	}
	col, ok := cast.Expr.(*ColumnRef)
	if !ok {
		t.Fatalf("expected *ColumnRef, got %T", cast.Expr)
	}
	if col.Name != "col" {
		t.Fatalf("expected column 'col', got %q", col.Name)
	}
}

func TestParseExpressionCase(t *testing.T) {
	stmt, err := Parse("SELECT CASE WHEN col > 0 THEN 'pos' ELSE 'neg' END FROM t;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if len(sel.Columns) != 1 {
		t.Fatalf("expected 1 column, got %d", len(sel.Columns))
	}
	caseExpr, ok := sel.Columns[0].Expr.(*CaseExpr)
	if !ok {
		t.Fatalf("expected *CaseExpr, got %T", sel.Columns[0].Expr)
	}
	if caseExpr.Base != nil {
		t.Fatal("expected nil Base")
	}
	if len(caseExpr.Whens) != 1 {
		t.Fatalf("expected 1 WHEN clause, got %d", len(caseExpr.Whens))
	}
	if caseExpr.Else == nil {
		t.Fatal("expected ELSE expression")
	}
	when := caseExpr.Whens[0]
	bin, ok := when.Condition.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr in WHEN condition, got %T", when.Condition)
	}
	if bin.Operator != ">" {
		t.Fatalf("expected operator '>', got %q", bin.Operator)
	}
	elseVal, ok := caseExpr.Else.(Value)
	if !ok {
		t.Fatalf("expected Value for ELSE, got %T", caseExpr.Else)
	}
	if elseVal.StrVal != "neg" {
		t.Fatalf("expected ELSE value 'neg', got %q", elseVal.StrVal)
	}
}

func TestParseMultipleStatements(t *testing.T) {
	_, err := Parse("SELECT 1; SELECT 2; SELECT 3;")
	if err == nil {
		t.Fatal("expected error for multiple statements")
	}
}

func TestParserErrorSanitization(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty query", ""},
		{"illegal token", "SELECT @@invalid FROM t;"},
		{"bad syntax", "SELECT * FROM heroes WHERE;"},
		{"unexpected token after semicolon", "SELECT 1; SELECT 2;"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.input)
			if err == nil {
				t.Fatalf("expected error for %q", tc.input)
			}
			if err.Error() != "invalid query syntax" {
				t.Fatalf("expected generic error message, got: %q", err.Error())
			}
		})
	}
}

func TestParseSetOperation(t *testing.T) {
	t.Run("UNION ALL", func(t *testing.T) {
		stmt, err := Parse("SELECT 1 UNION ALL SELECT 2;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		setOp, ok := stmt.(*SetOperationStatement)
		if !ok {
			t.Fatalf("expected *SetOperationStatement, got %T", stmt)
		}
		if setOp.Op != "UNION ALL" {
			t.Fatalf("expected 'UNION ALL', got %q", setOp.Op)
		}
		if _, ok := setOp.Left.(*SelectStatement); !ok {
			t.Fatalf("expected left *SelectStatement, got %T", setOp.Left)
		}
		if _, ok := setOp.Right.(*SelectStatement); !ok {
			t.Fatalf("expected right *SelectStatement, got %T", setOp.Right)
		}
	})

	t.Run("UNION", func(t *testing.T) {
		stmt, err := Parse("SELECT 1 UNION SELECT 2;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		setOp, ok := stmt.(*SetOperationStatement)
		if !ok {
			t.Fatalf("expected *SetOperationStatement, got %T", stmt)
		}
		if setOp.Op != "UNION" {
			t.Fatalf("expected 'UNION', got %q", setOp.Op)
		}
	})

	t.Run("INTERSECT", func(t *testing.T) {
		stmt, err := Parse("SELECT 1 INTERSECT SELECT 2;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		setOp, ok := stmt.(*SetOperationStatement)
		if !ok {
			t.Fatalf("expected *SetOperationStatement, got %T", stmt)
		}
		if setOp.Op != "INTERSECT" {
			t.Fatalf("expected 'INTERSECT', got %q", setOp.Op)
		}
	})

	t.Run("EXCEPT", func(t *testing.T) {
		stmt, err := Parse("SELECT 1 EXCEPT SELECT 2;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		setOp, ok := stmt.(*SetOperationStatement)
		if !ok {
			t.Fatalf("expected *SetOperationStatement, got %T", stmt)
		}
		if setOp.Op != "EXCEPT" {
			t.Fatalf("expected 'EXCEPT', got %q", setOp.Op)
		}
	})
}

func TestSubqueryWithUnion(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col = (SELECT id FROM t1 UNION SELECT id FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	bin, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	sub, ok := bin.Right.(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr on right, got %T", bin.Right)
	}
	setOp, ok := sub.Query.(*SetOperationStatement)
	if !ok {
		t.Fatalf("expected *SetOperationStatement, got %T", sub.Query)
	}
	if setOp.Op != "UNION" {
		t.Fatalf("expected UNION, got %q", setOp.Op)
	}
}

func TestExistsWithUnion(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE EXISTS (SELECT id FROM t1 UNION SELECT id FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	exists, ok := sel.Where.(*ExistsExpr)
	if !ok {
		t.Fatalf("expected *ExistsExpr, got %T", sel.Where)
	}
	setOp, ok := exists.Select.(*SetOperationStatement)
	if !ok {
		t.Fatalf("expected *SetOperationStatement, got %T", exists.Select)
	}
	if setOp.Op != "UNION" {
		t.Fatalf("expected UNION, got %q", setOp.Op)
	}
}

func TestSubqueryWithIntersect(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col IN (SELECT id FROM t1 INTERSECT SELECT id FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	inExpr, ok := sel.Where.(*InExpr)
	if !ok {
		t.Fatalf("expected *InExpr, got %T", sel.Where)
	}
	sub, ok := inExpr.Right[0].(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr, got %T", inExpr.Right[0])
	}
	setOp, ok := sub.Query.(*SetOperationStatement)
	if !ok {
		t.Fatalf("expected *SetOperationStatement, got %T", sub.Query)
	}
	if setOp.Op != "INTERSECT" {
		t.Fatalf("expected INTERSECT, got %q", setOp.Op)
	}
}

func TestSubqueryWithExcept(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col = (SELECT id FROM t1 EXCEPT SELECT id FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	bin, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	sub, ok := bin.Right.(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr on right, got %T", bin.Right)
	}
	setOp, ok := sub.Query.(*SetOperationStatement)
	if !ok {
		t.Fatalf("expected *SetOperationStatement, got %T", sub.Query)
	}
	if setOp.Op != "EXCEPT" {
		t.Fatalf("expected EXCEPT, got %q", setOp.Op)
	}
}

func TestNotInSubquery(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col NOT IN (SELECT id FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	inExpr, ok := sel.Where.(*InExpr)
	if !ok {
		t.Fatalf("expected *InExpr, got %T", sel.Where)
	}
	if !inExpr.Not {
		t.Fatal("expected Not=true")
	}
	sub, ok := inExpr.Right[0].(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr, got %T", inExpr.Right[0])
	}
	selStmt, ok := sub.Query.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", sub.Query)
	}
	if selStmt.TableName != "t2" {
		t.Fatalf("expected table 't2', got %q", selStmt.TableName)
	}
}

func TestNotInSubqueryWithUnion(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col NOT IN (SELECT id FROM t1 UNION SELECT id FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	inExpr, ok := sel.Where.(*InExpr)
	if !ok {
		t.Fatalf("expected *InExpr, got %T", sel.Where)
	}
	if !inExpr.Not {
		t.Fatal("expected Not=true")
	}
	sub, ok := inExpr.Right[0].(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr, got %T", inExpr.Right[0])
	}
	setOp, ok := sub.Query.(*SetOperationStatement)
	if !ok {
		t.Fatalf("expected *SetOperationStatement, got %T", sub.Query)
	}
	if setOp.Op != "UNION" {
		t.Fatalf("expected UNION, got %q", setOp.Op)
	}
}

func TestParseInsertSelect(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t1 SELECT * FROM t2;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if ins.TableName != "t1" {
			t.Fatalf("expected table 't1', got %q", ins.TableName)
		}
		if ins.SelectQuery == nil {
			t.Fatal("expected SelectQuery to be set")
		}
		if len(ins.Rows) != 0 {
			t.Fatalf("expected no VALUES rows, got %d", len(ins.Rows))
		}
	})

	t.Run("with columns", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t1 (a, b) SELECT x, y FROM t2;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins, ok := stmt.(*InsertStatement)
		if !ok {
			t.Fatalf("expected *InsertStatement, got %T", stmt)
		}
		if len(ins.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(ins.Columns))
		}
		if ins.Columns[0] != "a" || ins.Columns[1] != "b" {
			t.Fatalf("expected columns [a, b], got %v", ins.Columns)
		}
		if ins.SelectQuery == nil {
			t.Fatal("expected SelectQuery to be set")
		}
	})

	t.Run("with WHERE", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t1 SELECT * FROM t2 WHERE id > 5;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins := stmt.(*InsertStatement)
		if ins.SelectQuery == nil {
			t.Fatal("expected SelectQuery to be set")
		}
		sel, ok := ins.SelectQuery.(*SelectStatement)
		if !ok {
			t.Fatalf("expected *SelectStatement, got %T", ins.SelectQuery)
		}
		if sel.Where == nil {
			t.Fatal("expected WHERE clause")
		}
	})

	t.Run("with RETURNING", func(t *testing.T) {
		stmt, err := Parse("INSERT INTO t1 SELECT * FROM t2 RETURNING *;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		ins := stmt.(*InsertStatement)
		if ins.SelectQuery == nil {
			t.Fatal("expected SelectQuery to be set")
		}
		if ins.Returning == nil {
			t.Fatal("expected Returning to be non-nil (RETURNING clause present)")
		}
	})
}

func TestParseUpdateFromSubquery(t *testing.T) {
	queries := []struct {
		sql      string
		hasSub   bool
		hasTable bool
		alias    string
	}{
		{
			sql:    "UPDATE t1 SET col = s.val FROM (SELECT id, val FROM t2) AS s WHERE t1.id = s.id;",
			hasSub: true, alias: "s",
		},
		{
			sql:    "UPDATE t1 SET col = s.val FROM (SELECT id, val FROM t2) s WHERE t1.id = s.id;",
			hasSub: true,
			alias:  "s",
		},
		{
			sql:      "UPDATE t1 SET col = t2.val FROM t2 WHERE t1.id = t2.id;",
			hasTable: true,
		},
	}
	for _, q := range queries {
		stmt, err := Parse(q.sql)
		if err != nil {
			t.Fatalf("failed to parse: %q: %v", q.sql, err)
		}
		u, ok := stmt.(*UpdateStatement)
		if !ok {
			t.Fatalf("expected *UpdateStatement for %q", q.sql)
		}
		if q.hasSub && u.FromSubquery == nil {
			t.Fatalf("expected FromSubquery for %q", q.sql)
		}
		if q.hasTable && u.FromTable == "" {
			t.Fatalf("expected FromTable for %q", q.sql)
		}
		if q.alias != "" && u.FromAlias != q.alias {
			t.Fatalf("expected FromAlias %q, got %q for %s", q.alias, u.FromAlias, q.sql)
		}
	}
}

// --- T43: Complex expression tests ---

func TestParseMergeVariants(t *testing.T) {
	cases := []struct {
		name              string
		query             string
		targetTable       string
		hasSourceTable    bool
		sourceTable       string
		hasSourceQuery    bool
		alias             string
		hasWhenMatched    bool
		matchedAction     string
		hasWhenNotMatched bool
		notMatchedAction  string
		insertCols        int
		insertVals        int
		hasReturning      bool
	}{
		{
			name:              "basic MERGE with table source",
			query:             "MERGE INTO target USING source ON target.id = source.id WHEN MATCHED THEN UPDATE SET name = source.name WHEN NOT MATCHED THEN INSERT (id, name) VALUES (source.id, source.name);",
			targetTable:       "target",
			hasSourceTable:    true,
			sourceTable:       "source",
			hasWhenMatched:    true,
			matchedAction:     "UPDATE",
			hasWhenNotMatched: true,
			notMatchedAction:  "INSERT",
			insertCols:        2,
			insertVals:        1,
		},
		{
			name:           "MERGE with subquery source",
			query:          "MERGE INTO target USING (SELECT id, name FROM source WHERE active = TRUE) AS s ON target.id = s.id WHEN MATCHED THEN UPDATE SET name = s.name;",
			targetTable:    "target",
			hasSourceQuery: true,
			alias:          "s",
			hasWhenMatched: true,
			matchedAction:  "UPDATE",
		},
		{
			name:              "MERGE with SELECT in NOT MATCHED",
			query:             "MERGE INTO target USING source ON target.id = source.id WHEN NOT MATCHED THEN INSERT (id, name) SELECT id, name FROM source WHERE id > 10;",
			targetTable:       "target",
			hasSourceTable:    true,
			sourceTable:       "source",
			hasWhenNotMatched: true,
			notMatchedAction:  "INSERT",
			insertCols:        2,
		},
		{
			name:           "MERGE with alias on source table",
			query:          "MERGE INTO t1 USING t2 AS s ON t1.id = s.id WHEN MATCHED THEN UPDATE SET val = s.val;",
			targetTable:    "t1",
			hasSourceTable: true,
			sourceTable:    "t2",
			alias:          "s",
			hasWhenMatched: true,
			matchedAction:  "UPDATE",
		},
		{
			name:              "MERGE with multiple SET assignments",
			query:             "MERGE INTO t1 USING t2 ON t1.id = t2.id WHEN MATCHED THEN UPDATE SET name = t2.name, val = t2.val WHEN NOT MATCHED THEN INSERT (id, name, val) VALUES (t2.id, t2.name, t2.val);",
			targetTable:       "t1",
			hasSourceTable:    true,
			sourceTable:       "t2",
			hasWhenMatched:    true,
			matchedAction:     "UPDATE",
			hasWhenNotMatched: true,
			notMatchedAction:  "INSERT",
			insertCols:        3,
			insertVals:        1,
		},
		{
			name:           "MERGE with RETURNING *",
			query:          "MERGE INTO t1 USING t2 ON t1.id = t2.id WHEN MATCHED THEN UPDATE SET val = t2.val RETURNING *;",
			targetTable:    "t1",
			hasSourceTable: true,
			sourceTable:    "t2",
			hasWhenMatched: true,
			matchedAction:  "UPDATE",
			hasReturning:   true,
		},
		{
			name:           "MERGE with RETURNING columns",
			query:          "MERGE INTO t1 USING t2 ON t1.id = t2.id WHEN MATCHED THEN UPDATE SET val = t2.val RETURNING id, val;",
			targetTable:    "t1",
			hasSourceTable: true,
			sourceTable:    "t2",
			hasWhenMatched: true,
			matchedAction:  "UPDATE",
			hasReturning:   true,
		},
		{
			name:              "MERGE only WHEN NOT MATCHED",
			query:             "MERGE INTO t1 USING t2 ON t1.id = t2.id WHEN NOT MATCHED THEN INSERT (id, name) VALUES (t2.id, t2.name);",
			targetTable:       "t1",
			hasSourceTable:    true,
			sourceTable:       "t2",
			hasWhenNotMatched: true,
			notMatchedAction:  "INSERT",
			insertCols:        2,
			insertVals:        1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			merge, ok := stmt.(*MergeStatement)
			if !ok {
				t.Fatalf("expected *MergeStatement, got %T", stmt)
			}
			if merge.TargetTable != tc.targetTable {
				t.Fatalf("expected target table %q, got %q", tc.targetTable, merge.TargetTable)
			}
			if tc.hasSourceTable && merge.SourceTable != tc.sourceTable {
				t.Fatalf("expected source table %q, got %q", tc.sourceTable, merge.SourceTable)
			}
			if tc.hasSourceQuery && merge.SourceQuery == nil {
				t.Fatal("expected SourceQuery to be set")
			}
			if tc.alias != "" && merge.Alias != tc.alias {
				t.Fatalf("expected alias %q, got %q", tc.alias, merge.Alias)
			}
			if tc.hasWhenMatched {
				if merge.WhenMatched == nil {
					t.Fatal("expected WhenMatched to be set")
				}
				if merge.WhenMatched.Action != tc.matchedAction {
					t.Fatalf("expected matched action %q, got %q", tc.matchedAction, merge.WhenMatched.Action)
				}
			}
			if tc.hasWhenNotMatched {
				if merge.WhenNotMatched == nil {
					t.Fatal("expected WhenNotMatched to be set")
				}
				if merge.WhenNotMatched.Action != tc.notMatchedAction {
					t.Fatalf("expected not matched action %q, got %q", tc.notMatchedAction, merge.WhenNotMatched.Action)
				}
				if tc.insertCols > 0 && len(merge.WhenNotMatched.Columns) != tc.insertCols {
					t.Fatalf("expected %d insert columns, got %d", tc.insertCols, len(merge.WhenNotMatched.Columns))
				}
				if tc.insertVals > 0 && len(merge.WhenNotMatched.Values) != tc.insertVals {
					t.Fatalf("expected %d insert values rows, got %d", tc.insertVals, len(merge.WhenNotMatched.Values))
				}
			}
			if tc.hasReturning && merge.Returning == nil {
				t.Fatal("expected Returning to be set")
			}
		})
	}
}

func TestParseMergeErrors(t *testing.T) {
	cases := []string{
		"MERGE INTO t USING s;",
		"MERGE INTO t USING s ON;",
		"MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN;",
		"MERGE INTO t USING s ON t.id = s.id WHEN NOT MATCHED THEN INSERT;",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Fatalf("expected error for %q", q)
		}
	}
}

func TestParseWindowFunctions(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		funcName  string
		partCols  int
		orderCols int
	}{
		{
			name:      "ROW_NUMBER OVER ORDER BY",
			query:     "SELECT ROW_NUMBER() OVER (ORDER BY id) FROM t;",
			funcName:  "ROW_NUMBER",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "ROW_NUMBER OVER PARTITION BY ORDER BY",
			query:     "SELECT ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary) FROM t;",
			funcName:  "ROW_NUMBER",
			partCols:  1,
			orderCols: 1,
		},
		{
			name:      "RANK OVER ORDER BY",
			query:     "SELECT RANK() OVER (ORDER BY score DESC) FROM t;",
			funcName:  "RANK",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "SUM OVER PARTITION BY",
			query:     "SELECT SUM(amount) OVER (PARTITION BY dept) FROM t;",
			funcName:  "SUM",
			partCols:  1,
			orderCols: 0,
		},
		{
			name:      "COUNT OVER PARTITION BY ORDER BY",
			query:     "SELECT COUNT(*) OVER (PARTITION BY dept ORDER BY id) FROM t;",
			funcName:  "COUNT",
			partCols:  1,
			orderCols: 1,
		},
		{
			name:      "AVG OVER with multiple partition cols",
			query:     "SELECT AVG(salary) OVER (PARTITION BY dept, region ORDER BY hire_date) FROM t;",
			funcName:  "AVG",
			partCols:  2,
			orderCols: 1,
		},
		{
			name:      "MAX OVER ORDER BY",
			query:     "SELECT MAX(val) OVER (ORDER BY created_at) FROM t;",
			funcName:  "MAX",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "MIN OVER PARTITION BY ORDER BY",
			query:     "SELECT MIN(score) OVER (PARTITION BY group_id ORDER BY ts DESC) FROM t;",
			funcName:  "MIN",
			partCols:  1,
			orderCols: 1,
		},
		{
			name:      "window with ROWS BETWEEN",
			query:     "SELECT SUM(amount) OVER (ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t;",
			funcName:  "SUM",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "window with RANGE BETWEEN",
			query:     "SELECT SUM(amount) OVER (ORDER BY id RANGE BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t;",
			funcName:  "SUM",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "window with ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING",
			query:     "SELECT SUM(amount) OVER (PARTITION BY dept ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t;",
			funcName:  "SUM",
			partCols:  1,
			orderCols: 0,
		},
		{
			name:      "window with ROWS BETWEEN N PRECEDING AND N FOLLOWING",
			query:     "SELECT AVG(val) OVER (ORDER BY id ROWS BETWEEN 3 PRECEDING AND 3 FOLLOWING) FROM t;",
			funcName:  "AVG",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "window with ROWS CURRENT ROW",
			query:     "SELECT SUM(val) OVER (ORDER BY id ROWS CURRENT ROW) FROM t;",
			funcName:  "SUM",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "window with ROWS UNBOUNDED PRECEDING (single bound)",
			query:     "SELECT SUM(val) OVER (ORDER BY id ROWS UNBOUNDED PRECEDING) FROM t;",
			funcName:  "SUM",
			partCols:  0,
			orderCols: 1,
		},
		{
			name:      "multiple window functions",
			query:     "SELECT ROW_NUMBER() OVER (ORDER BY id), RANK() OVER (ORDER BY score) FROM t;",
			funcName:  "ROW_NUMBER",
			partCols:  0,
			orderCols: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected *SelectStatement, got %T", stmt)
			}
			if len(sel.Columns) == 0 {
				t.Fatal("expected at least 1 column")
			}
			win, ok := sel.Columns[0].Expr.(*WindowFunctionExpr)
			if !ok {
				t.Fatalf("expected *WindowFunctionExpr, got %T", sel.Columns[0].Expr)
			}
			if win.FuncName != tc.funcName {
				t.Fatalf("expected func name %q, got %q", tc.funcName, win.FuncName)
			}
			if len(win.Over.PartitionBy) != tc.partCols {
				t.Fatalf("expected %d partition columns, got %d", tc.partCols, len(win.Over.PartitionBy))
			}
			if len(win.Over.OrderBy) != tc.orderCols {
				t.Fatalf("expected %d order columns, got %d", tc.orderCols, len(win.Over.OrderBy))
			}
		})
	}
}

func TestParseWindowFunctionsFrameBounds(t *testing.T) {
	t.Run("RANGE BETWEEN", func(t *testing.T) {
		stmt, err := Parse("SELECT SUM(val) OVER (ORDER BY id RANGE BETWEEN 5 PRECEDING AND 5 FOLLOWING) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		win := sel.Columns[0].Expr.(*WindowFunctionExpr)
		if win.Over.Frame == nil {
			t.Fatal("expected Frame to be set")
		}
		if win.Over.Frame.Mode != "RANGE" {
			t.Fatalf("expected mode RANGE, got %q", win.Over.Frame.Mode)
		}
		if win.Over.Frame.StartType != "PRECEDING" || win.Over.Frame.StartN != 5 {
			t.Fatalf("expected start 5 PRECEDING, got %q N=%d", win.Over.Frame.StartType, win.Over.Frame.StartN)
		}
		if win.Over.Frame.EndType != "FOLLOWING" || win.Over.Frame.EndN != 5 {
			t.Fatalf("expected end 5 FOLLOWING, got %q N=%d", win.Over.Frame.EndType, win.Over.Frame.EndN)
		}
	})

	t.Run("ROWS BETWEEN UNBOUNDED AND UNBOUNDED", func(t *testing.T) {
		stmt, err := Parse("SELECT COUNT(*) OVER (PARTITION BY grp ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		win := sel.Columns[0].Expr.(*WindowFunctionExpr)
		if win.Over.Frame == nil {
			t.Fatal("expected Frame to be set")
		}
		if win.Over.Frame.StartType != "UNBOUNDED PRECEDING" {
			t.Fatalf("expected start UNBOUNDED PRECEDING, got %q", win.Over.Frame.StartType)
		}
		if win.Over.Frame.EndType != "UNBOUNDED FOLLOWING" {
			t.Fatalf("expected end UNBOUNDED FOLLOWING, got %q", win.Over.Frame.EndType)
		}
	})

	t.Run("ROWS BETWEEN N PRECEDING AND CURRENT ROW", func(t *testing.T) {
		stmt, err := Parse("SELECT AVG(val) OVER (ORDER BY id ROWS BETWEEN 2 PRECEDING AND CURRENT ROW) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		win := sel.Columns[0].Expr.(*WindowFunctionExpr)
		if win.Over.Frame == nil {
			t.Fatal("expected Frame to be set")
		}
		if win.Over.Frame.StartType != "PRECEDING" || win.Over.Frame.StartN != 2 {
			t.Fatalf("expected start 2 PRECEDING, got %q N=%d", win.Over.Frame.StartType, win.Over.Frame.StartN)
		}
		if win.Over.Frame.EndType != "CURRENT ROW" {
			t.Fatalf("expected end CURRENT ROW, got %q", win.Over.Frame.EndType)
		}
	})

	t.Run("ROWS single bound = BETWEEN bound AND CURRENT ROW", func(t *testing.T) {
		stmt, err := Parse("SELECT SUM(val) OVER (ORDER BY id ROWS 3 PRECEDING) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		win := sel.Columns[0].Expr.(*WindowFunctionExpr)
		if win.Over.Frame == nil {
			t.Fatal("expected Frame to be set")
		}
		if win.Over.Frame.StartType != "PRECEDING" || win.Over.Frame.StartN != 3 {
			t.Fatalf("expected start 3 PRECEDING, got %q N=%d", win.Over.Frame.StartType, win.Over.Frame.StartN)
		}
		if win.Over.Frame.EndType != "CURRENT ROW" {
			t.Fatalf("expected end CURRENT ROW (implicit), got %q", win.Over.Frame.EndType)
		}
	})
}

func TestParseWindowFunctionsWithAlias(t *testing.T) {
	stmt, err := Parse("SELECT ROW_NUMBER() OVER (ORDER BY id) AS rn FROM t;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	if sel.Columns[0].Alias != "rn" {
		t.Fatalf("expected alias 'rn', got %q", sel.Columns[0].Alias)
	}
}

func TestParseJsonbOperators(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		exprType string
		op       string
		path     string
	}{
		{
			name:     "arrow operator ->",
			query:    "SELECT data->'name' FROM t;",
			exprType: "JsonPathExpr",
			op:       "->",
			path:     "name",
		},
		{
			name:     "double arrow operator ->>",
			query:    "SELECT data->>'name' FROM t;",
			exprType: "JsonPathExpr",
			op:       "->>",
			path:     "name",
		},
		{
			name:     "chained arrows",
			query:    "SELECT data->'address'->'city' FROM t;",
			exprType: "JsonPathExpr",
			op:       "->",
			path:     "city",
		},
		{
			name:     "arrow in WHERE clause",
			query:    "SELECT * FROM t WHERE data->'active' = TRUE;",
			exprType: "JsonPathExpr",
			op:       "->",
			path:     "active",
		},
		{
			name:     "double arrow in WHERE clause",
			query:    "SELECT * FROM t WHERE data->>'name' = 'test';",
			exprType: "JsonPathExpr",
			op:       "->>",
			path:     "name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected *SelectStatement, got %T", stmt)
			}
			// For WHERE tests, check the WHERE expression
			if sel.Where != nil {
				bin, ok := sel.Where.(*BinaryExpr)
				if !ok {
					t.Fatalf("expected *BinaryExpr in WHERE, got %T", sel.Where)
				}
				jp, ok := bin.Left.(*JsonPathExpr)
				if !ok {
					t.Fatalf("expected *JsonPathExpr on left, got %T", bin.Left)
				}
				if jp.Op != tc.op {
					t.Fatalf("expected op %q, got %q", tc.op, jp.Op)
				}
				if jp.Path != tc.path {
					t.Fatalf("expected path %q, got %q", tc.path, jp.Path)
				}
				return
			}
			// For SELECT tests
			col := sel.Columns[0].Expr
			// For chained arrows, outer is JsonPathExpr wrapping another
			jp, ok := col.(*JsonPathExpr)
			if !ok {
				t.Fatalf("expected *JsonPathExpr, got %T", col)
			}
			if jp.Op != tc.op {
				t.Fatalf("expected op %q, got %q", tc.op, jp.Op)
			}
			if jp.Path != tc.path {
				t.Fatalf("expected path %q, got %q", tc.path, jp.Path)
			}
		})
	}
}

func TestParseJsonbComparisonOperators(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		op       string
		checkCol bool   // check in column expression instead of WHERE
		exprType string // "BinaryExpr" or "JSONAccess"
	}{
		{
			name:     "jsonb contains @>",
			query:    "SELECT * FROM t WHERE data @> 'val';",
			op:       "@>",
			exprType: "JSONAccess",
		},
		{
			name:     "jsonb contained by <@",
			query:    "SELECT * FROM t WHERE data <@ 'val';",
			op:       "<@",
			exprType: "BinaryExpr",
		},
		{
			name:     "jsonb has key ?",
			query:    "SELECT * FROM t WHERE data ? 'name';",
			op:       "?",
			exprType: "JSONAccess",
		},
		{
			name:     "jsonb merge ||",
			query:    "SELECT data || other FROM t;",
			op:       "||",
			checkCol: true,
			exprType: "BinaryExpr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected *SelectStatement, got %T", stmt)
			}
			var expr Expression
			if tc.checkCol {
				expr = sel.Columns[0].Expr
			} else {
				if sel.Where == nil {
					t.Fatal("expected WHERE clause")
				}
				expr = sel.Where
			}
			switch tc.exprType {
			case "BinaryExpr":
				bin, ok := expr.(*BinaryExpr)
				if !ok {
					t.Fatalf("expected *BinaryExpr, got %T", expr)
				}
				if bin.Operator != tc.op {
					t.Fatalf("expected operator %q, got %q", tc.op, bin.Operator)
				}
			case "JSONAccess":
				ja, ok := expr.(*JSONAccess)
				if !ok {
					t.Fatalf("expected *JSONAccess, got %T", expr)
				}
				if ja.Operator != tc.op {
					t.Fatalf("expected operator %q, got %q", tc.op, ja.Operator)
				}
			}
		})
	}
}

func TestParseSubqueryScalar(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t WHERE col = (SELECT MAX(id) FROM t2);")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	sel := stmt.(*SelectStatement)
	bin, ok := sel.Where.(*BinaryExpr)
	if !ok {
		t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
	}
	if bin.Operator != "=" {
		t.Fatalf("expected operator '=', got %q", bin.Operator)
	}
	sub, ok := bin.Right.(*SubqueryExpr)
	if !ok {
		t.Fatalf("expected *SubqueryExpr on right, got %T", bin.Right)
	}
	if sub.Query == nil {
		t.Fatal("expected non-nil subquery")
	}
	if _, ok := sub.Query.(*SelectStatement); !ok {
		t.Fatalf("expected *SelectStatement in subquery, got %T", sub.Query)
	}
}

func TestParseSubqueryExists(t *testing.T) {
	t.Run("EXISTS", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		exists, ok := sel.Where.(*ExistsExpr)
		if !ok {
			t.Fatalf("expected *ExistsExpr, got %T", sel.Where)
		}
		if exists.Not {
			t.Fatal("expected Not=false")
		}
		if exists.Select == nil {
			t.Fatal("expected non-nil Select")
		}
	})

	t.Run("NOT EXISTS", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE NOT EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		notExpr, ok := sel.Where.(*NotExpr)
		if !ok {
			t.Fatalf("expected *NotExpr, got %T", sel.Where)
		}
		exists, ok := notExpr.Expr.(*ExistsExpr)
		if !ok {
			t.Fatalf("expected *ExistsExpr inside NotExpr, got %T", notExpr.Expr)
		}
		if exists.Not {
			t.Fatal("expected inner ExistsExpr.Not=false (negation is on outer NotExpr)")
		}
		if exists.Select == nil {
			t.Fatal("expected non-nil Select")
		}
	})
}

func TestParseSubqueryIn(t *testing.T) {
	t.Run("IN list", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col IN (1, 2, 3);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		inExpr, ok := sel.Where.(*InExpr)
		if !ok {
			t.Fatalf("expected *InExpr, got %T", sel.Where)
		}
		if inExpr.Not {
			t.Fatal("expected Not=false")
		}
		if len(inExpr.Right) != 3 {
			t.Fatalf("expected 3 values, got %d", len(inExpr.Right))
		}
	})

	t.Run("IN subquery", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col IN (SELECT id FROM t2);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		inExpr, ok := sel.Where.(*InExpr)
		if !ok {
			t.Fatalf("expected *InExpr, got %T", sel.Where)
		}
		if len(inExpr.Right) != 1 {
			t.Fatalf("expected 1 subquery, got %d", len(inExpr.Right))
		}
		sub, ok := inExpr.Right[0].(*SubqueryExpr)
		if !ok {
			t.Fatalf("expected *SubqueryExpr, got %T", inExpr.Right[0])
		}
		if sub.Query == nil {
			t.Fatal("expected non-nil subquery")
		}
	})

	t.Run("NOT IN list", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col NOT IN (1, 2);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		inExpr, ok := sel.Where.(*InExpr)
		if !ok {
			t.Fatalf("expected *InExpr, got %T", sel.Where)
		}
		if !inExpr.Not {
			t.Fatal("expected Not=true")
		}
	})

	t.Run("NOT IN subquery", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col NOT IN (SELECT id FROM t2);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		inExpr, ok := sel.Where.(*InExpr)
		if !ok {
			t.Fatalf("expected *InExpr, got %T", sel.Where)
		}
		if !inExpr.Not {
			t.Fatal("expected Not=true")
		}
	})
}

func TestParseSubqueryAll(t *testing.T) {
	cases := []struct {
		name  string
		query string
		op    string
		quant string
	}{
		{
			name:  "= ALL subquery",
			query: "SELECT * FROM t WHERE col = ALL (SELECT id FROM t2);",
			op:    "=",
			quant: "ALL",
		},
		{
			name:  "> ALL subquery",
			query: "SELECT * FROM t WHERE col > ALL (SELECT val FROM t2);",
			op:    ">",
			quant: "ALL",
		},
		{
			name:  ">= ALL subquery",
			query: "SELECT * FROM t WHERE col >= ALL (SELECT val FROM t2);",
			op:    ">=",
			quant: "ALL",
		},
		{
			name:  "< ALL subquery",
			query: "SELECT * FROM t WHERE col < ALL (SELECT val FROM t2);",
			op:    "<",
			quant: "ALL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel := stmt.(*SelectStatement)
			csq, ok := sel.Where.(*ComparisonSubqueryExpr)
			if !ok {
				t.Fatalf("expected *ComparisonSubqueryExpr, got %T", sel.Where)
			}
			if csq.Operator != tc.op {
				t.Fatalf("expected operator %q, got %q", tc.op, csq.Operator)
			}
			if csq.Quantifier != tc.quant {
				t.Fatalf("expected quantifier %q, got %q", tc.quant, csq.Quantifier)
			}
			if csq.Subquery == nil {
				t.Fatal("expected non-nil Subquery")
			}
		})
	}
}

func TestParseSubqueryAny(t *testing.T) {
	cases := []struct {
		name  string
		query string
		op    string
		quant string
	}{
		{
			name:  "= ANY subquery",
			query: "SELECT * FROM t WHERE col = ANY (SELECT id FROM t2);",
			op:    "=",
			quant: "ANY",
		},
		{
			name:  "> ANY subquery",
			query: "SELECT * FROM t WHERE col > ANY (SELECT val FROM t2);",
			op:    ">",
			quant: "ANY",
		},
		{
			name:  "!= ANY subquery",
			query: "SELECT * FROM t WHERE col != ANY (SELECT val FROM t2);",
			op:    "!=",
			quant: "ANY",
		},
		{
			name:  "<= ANY subquery",
			query: "SELECT * FROM t WHERE col <= ANY (SELECT val FROM t2);",
			op:    "<=",
			quant: "ANY",
		},
		{
			name:  "= SOME subquery",
			query: "SELECT * FROM t WHERE col = SOME (SELECT id FROM t2);",
			op:    "=",
			quant: "SOME",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel := stmt.(*SelectStatement)
			csq, ok := sel.Where.(*ComparisonSubqueryExpr)
			if !ok {
				t.Fatalf("expected *ComparisonSubqueryExpr, got %T", sel.Where)
			}
			if csq.Operator != tc.op {
				t.Fatalf("expected operator %q, got %q", tc.op, csq.Operator)
			}
			if csq.Quantifier != tc.quant {
				t.Fatalf("expected quantifier %q, got %q", tc.quant, csq.Quantifier)
			}
			if csq.Subquery == nil {
				t.Fatal("expected non-nil Subquery")
			}
		})
	}
}

func TestParseNestedCase(t *testing.T) {
	t.Run("nested CASE in CASE", func(t *testing.T) {
		query := `SELECT CASE WHEN status = 'active' THEN CASE WHEN level > 5 THEN 'senior' ELSE 'junior' END ELSE 'inactive' END FROM t;`
		stmt, err := Parse(query)
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		outer, ok := sel.Columns[0].Expr.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr, got %T", sel.Columns[0].Expr)
		}
		if len(outer.Whens) != 1 {
			t.Fatalf("expected 1 outer WHEN, got %d", len(outer.Whens))
		}
		if outer.Else == nil {
			t.Fatal("expected outer ELSE")
		}
		inner, ok := outer.Whens[0].Result.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr in WHEN result, got %T", outer.Whens[0].Result)
		}
		if len(inner.Whens) != 1 {
			t.Fatalf("expected 1 inner WHEN, got %d", len(inner.Whens))
		}
		if inner.Else == nil {
			t.Fatal("expected inner ELSE")
		}
	})

	t.Run("CASE with base expression", func(t *testing.T) {
		stmt, err := Parse("SELECT CASE col WHEN 1 THEN 'one' WHEN 2 THEN 'two' ELSE 'other' END FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		caseExpr, ok := sel.Columns[0].Expr.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr, got %T", sel.Columns[0].Expr)
		}
		if caseExpr.Base == nil {
			t.Fatal("expected non-nil Base")
		}
		if len(caseExpr.Whens) != 2 {
			t.Fatalf("expected 2 WHEN clauses, got %d", len(caseExpr.Whens))
		}
	})

	t.Run("CASE without ELSE", func(t *testing.T) {
		stmt, err := Parse("SELECT CASE WHEN col > 0 THEN 'pos' END FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		caseExpr, ok := sel.Columns[0].Expr.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr, got %T", sel.Columns[0].Expr)
		}
		if caseExpr.Else != nil {
			t.Fatal("expected nil ELSE")
		}
	})

	t.Run("CASE with multiple WHEN", func(t *testing.T) {
		stmt, err := Parse("SELECT CASE WHEN a = 1 THEN 'one' WHEN a = 2 THEN 'two' WHEN a = 3 THEN 'three' ELSE 'other' END FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		caseExpr, ok := sel.Columns[0].Expr.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr, got %T", sel.Columns[0].Expr)
		}
		if len(caseExpr.Whens) != 3 {
			t.Fatalf("expected 3 WHEN clauses, got %d", len(caseExpr.Whens))
		}
	})
}

func TestParseNestedCaseErrors(t *testing.T) {
	cases := []string{
		"SELECT CASE WHEN END FROM t;",
		"SELECT CASE END FROM t;",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Fatalf("expected error for %q", q)
		}
	}
}

func TestParseCastExpressions(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		targetType string
	}{
		{
			name:       "CAST as INT",
			query:      "SELECT CAST(col AS INT) FROM t;",
			targetType: "INT",
		},
		{
			name:       "CAST as VARCHAR",
			query:      "SELECT CAST(col AS VARCHAR) FROM t;",
			targetType: "VARCHAR",
		},
		{
			name:       "CAST as FLOAT",
			query:      "SELECT CAST(col AS FLOAT) FROM t;",
			targetType: "FLOAT",
		},
		{
			name:       "CAST as BOOL",
			query:      "SELECT CAST(col AS BOOL) FROM t;",
			targetType: "BOOL",
		},
		{
			name:       "CAST as TEXT",
			query:      "SELECT CAST(col AS TEXT) FROM t;",
			targetType: "TEXT",
		},
		{
			name:       "CAST with expression",
			query:      "SELECT CAST(a + b AS FLOAT) FROM t;",
			targetType: "FLOAT",
		},
		{
			name:       "CAST as expression with comparison",
			query:      "SELECT CAST(col AS INT) > 5 FROM t;",
			targetType: "INT",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel := stmt.(*SelectStatement)
			var expr Expression
			if sel.Where != nil {
				expr = sel.Where
			} else {
				expr = sel.Columns[0].Expr
			}
			// Unwrap BinaryExpr if needed (e.g. "CAST(x AS INT) > 5")
			if bin, ok := expr.(*BinaryExpr); ok {
				expr = bin.Left
			}
			cast, ok := expr.(*CastExpr)
			if !ok {
				t.Fatalf("expected *CastExpr, got %T", expr)
			}
			if cast.TargetType != tc.targetType {
				t.Fatalf("expected target type %q, got %q", tc.targetType, cast.TargetType)
			}
			if cast.Expr == nil {
				t.Fatal("expected non-nil Expr")
			}
		})
	}
}

func TestParseCastErrors(t *testing.T) {
	cases := []string{
		"SELECT CAST(col) FROM t;",
		"SELECT CAST(col AS) FROM t;",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Fatalf("expected error for %q", q)
		}
	}
}

func TestParseCoalesce(t *testing.T) {
	cases := []struct {
		name  string
		query string
		args  int
	}{
		{
			name:  "COALESCE two args",
			query: "SELECT COALESCE(a, b) FROM t;",
			args:  2,
		},
		{
			name:  "COALESCE three args",
			query: "SELECT COALESCE(a, b, c) FROM t;",
			args:  3,
		},
		{
			name:  "COALESCE with literal",
			query: "SELECT COALESCE(name, 'unknown') FROM t;",
			args:  2,
		},
		{
			name:  "COALESCE with column ref in arg",
			query: "SELECT COALESCE(a, b) FROM t;",
			args:  2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel := stmt.(*SelectStatement)
			var expr Expression
			if sel.Where != nil {
				expr = sel.Where
			} else {
				expr = sel.Columns[0].Expr
			}
			fn, ok := expr.(*FunctionCall)
			if !ok {
				t.Fatalf("expected *FunctionCall, got %T", expr)
			}
			if fn.Name != "COALESCE" {
				t.Fatalf("expected name 'COALESCE', got %q", fn.Name)
			}
			if len(fn.Args) != tc.args {
				t.Fatalf("expected %d args, got %d", tc.args, len(fn.Args))
			}
		})
	}
}

func TestParseComplexExpressionsCombined(t *testing.T) {
	t.Run("nested CAST in CASE", func(t *testing.T) {
		stmt, err := Parse("SELECT CASE WHEN col > 0 THEN CAST(col AS FLOAT) ELSE CAST(0 AS FLOAT) END FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		caseExpr, ok := sel.Columns[0].Expr.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr, got %T", sel.Columns[0].Expr)
		}
		if len(caseExpr.Whens) != 1 {
			t.Fatalf("expected 1 WHEN, got %d", len(caseExpr.Whens))
		}
		// Check THEN is CAST
		thenCast, ok := caseExpr.Whens[0].Result.(*CastExpr)
		if !ok {
			t.Fatalf("expected *CastExpr in THEN, got %T", caseExpr.Whens[0].Result)
		}
		if thenCast.TargetType != "FLOAT" {
			t.Fatalf("expected FLOAT, got %q", thenCast.TargetType)
		}
		// Check ELSE is CAST
		elseCast, ok := caseExpr.Else.(*CastExpr)
		if !ok {
			t.Fatalf("expected *CastExpr in ELSE, got %T", caseExpr.Else)
		}
		if elseCast.TargetType != "FLOAT" {
			t.Fatalf("expected FLOAT, got %q", elseCast.TargetType)
		}
	})

	t.Run("COALESCE with CASE inside", func(t *testing.T) {
		stmt, err := Parse("SELECT COALESCE(CASE WHEN a > 0 THEN a ELSE 0 END, b) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		fn, ok := sel.Columns[0].Expr.(*FunctionCall)
		if !ok {
			t.Fatalf("expected *FunctionCall, got %T", sel.Columns[0].Expr)
		}
		if fn.Name != "COALESCE" {
			t.Fatalf("expected COALESCE, got %q", fn.Name)
		}
		if len(fn.Args) != 2 {
			t.Fatalf("expected 2 args, got %d", len(fn.Args))
		}
		innerCase, ok := fn.Args[0].(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr as first arg, got %T", fn.Args[0])
		}
		if len(innerCase.Whens) != 1 {
			t.Fatalf("expected 1 WHEN in inner CASE, got %d", len(innerCase.Whens))
		}
	})

	t.Run("window function with CASE in expression", func(t *testing.T) {
		stmt, err := Parse("SELECT SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) OVER (ORDER BY id) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		win, ok := sel.Columns[0].Expr.(*WindowFunctionExpr)
		if !ok {
			t.Fatalf("expected *WindowFunctionExpr, got %T", sel.Columns[0].Expr)
		}
		if win.FuncName != "SUM" {
			t.Fatalf("expected SUM, got %q", win.FuncName)
		}
		if len(win.Args) != 1 {
			t.Fatalf("expected 1 arg, got %d", len(win.Args))
		}
		innerCase, ok := win.Args[0].(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr as arg, got %T", win.Args[0])
		}
		if len(innerCase.Whens) != 1 {
			t.Fatalf("expected 1 WHEN, got %d", len(innerCase.Whens))
		}
	})

	t.Run("JSONB with CAST", func(t *testing.T) {
		stmt, err := Parse("SELECT CAST(data->>'count' AS INT) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		cast, ok := sel.Columns[0].Expr.(*CastExpr)
		if !ok {
			t.Fatalf("expected *CastExpr, got %T", sel.Columns[0].Expr)
		}
		if cast.TargetType != "INT" {
			t.Fatalf("expected INT, got %q", cast.TargetType)
		}
		jp, ok := cast.Expr.(*JsonPathExpr)
		if !ok {
			t.Fatalf("expected *JsonPathExpr, got %T", cast.Expr)
		}
		if jp.Op != "->>" {
			t.Fatalf("expected ->>, got %q", jp.Op)
		}
	})

	t.Run("scalar subquery with CASE", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE col = (SELECT CASE WHEN a > 0 THEN a ELSE 0 END FROM t2);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		bin, ok := sel.Where.(*BinaryExpr)
		if !ok {
			t.Fatalf("expected *BinaryExpr, got %T", sel.Where)
		}
		sub, ok := bin.Right.(*SubqueryExpr)
		if !ok {
			t.Fatalf("expected *SubqueryExpr, got %T", bin.Right)
		}
		selStmt, ok := sub.Query.(*SelectStatement)
		if !ok {
			t.Fatalf("expected *SelectStatement, got %T", sub.Query)
		}
		if len(selStmt.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(selStmt.Columns))
		}
		caseExpr, ok := selStmt.Columns[0].Expr.(*CaseExpr)
		if !ok {
			t.Fatalf("expected *CaseExpr in subquery, got %T", selStmt.Columns[0].Expr)
		}
		if len(caseExpr.Whens) != 1 {
			t.Fatalf("expected 1 WHEN, got %d", len(caseExpr.Whens))
		}
	})

	t.Run("window function with PARTITION BY multiple cols and frame", func(t *testing.T) {
		stmt, err := Parse("SELECT SUM(val) OVER (PARTITION BY dept, region ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		win, ok := sel.Columns[0].Expr.(*WindowFunctionExpr)
		if !ok {
			t.Fatalf("expected *WindowFunctionExpr, got %T", sel.Columns[0].Expr)
		}
		if len(win.Over.PartitionBy) != 2 {
			t.Fatalf("expected 2 partition cols, got %d", len(win.Over.PartitionBy))
		}
		if len(win.Over.OrderBy) != 1 {
			t.Fatalf("expected 1 order col, got %d", len(win.Over.OrderBy))
		}
		if win.Over.Frame == nil {
			t.Fatal("expected Frame to be set")
		}
		if win.Over.Frame.Mode != "ROWS" {
			t.Fatalf("expected mode ROWS, got %q", win.Over.Frame.Mode)
		}
	})

	t.Run("EXISTS with UNION subquery", func(t *testing.T) {
		stmt, err := Parse("SELECT * FROM t WHERE EXISTS (SELECT id FROM t1 WHERE a > 1 UNION SELECT id FROM t2 WHERE b > 2);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		sel := stmt.(*SelectStatement)
		exists, ok := sel.Where.(*ExistsExpr)
		if !ok {
			t.Fatalf("expected *ExistsExpr, got %T", sel.Where)
		}
		setOp, ok := exists.Select.(*SetOperationStatement)
		if !ok {
			t.Fatalf("expected *SetOperationStatement, got %T", exists.Select)
		}
		if setOp.Op != "UNION" {
			t.Fatalf("expected UNION, got %q", setOp.Op)
		}
	})
}

func TestParseWindowFunctionErrors(t *testing.T) {
	cases := []string{
		"SELECT ROW_NUMBER() OVER FROM t;",
		"SELECT ROW_NUMBER() OVER (PARTITION FROM t;",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Fatalf("expected error for %q", q)
		}
	}
}

func TestParseComparisonSubqueryErrors(t *testing.T) {
	cases := []string{
		"SELECT * FROM t WHERE col > ALL;",
		"SELECT * FROM t WHERE col = ANY;",
	}
	for _, q := range cases {
		if _, err := Parse(q); err == nil {
			t.Fatalf("expected error for %q", q)
		}
	}
}

func TestLimitWithParamRef(t *testing.T) {
	stmt, err := Parse("SELECT * FROM t LIMIT $1 OFFSET $2;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	sel, ok := stmt.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
	if !sel.HasLimit {
		t.Fatal("expected HasLimit to be true")
	}
	if sel.LimitExpr == nil {
		t.Fatal("expected LimitExpr to be set")
	}
	param1, ok := sel.LimitExpr.(*ParamRef)
	if !ok {
		t.Fatalf("expected *ParamRef for LimitExpr, got %T", sel.LimitExpr)
	}
	if param1.Index != 1 {
		t.Fatalf("expected param index 1 for limit, got %d", param1.Index)
	}
	if !sel.HasOffset {
		t.Fatal("expected HasOffset to be true")
	}
	if sel.OffsetExpr == nil {
		t.Fatal("expected OffsetExpr to be set")
	}
	param2, ok := sel.OffsetExpr.(*ParamRef)
	if !ok {
		t.Fatalf("expected *ParamRef for OffsetExpr, got %T", sel.OffsetExpr)
	}
	if param2.Index != 2 {
		t.Fatalf("expected param index 2 for offset, got %d", param2.Index)
	}
}

func TestParseWithoutSemicolon(t *testing.T) {
	queries := []string{
		"SELECT 1",
		"SELECT * FROM heroes",
		"INSERT INTO heroes VALUES (1, 'test')",
		"UPDATE heroes SET level = 10 WHERE id = 1",
		"DELETE FROM heroes WHERE id = 1",
		"CREATE TABLE test (id INT)",
		"DROP TABLE test",
		"SHOW DATABASES",
	}

	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			stmt, err := Parse(query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", query, err)
			}
			if stmt == nil {
				t.Fatal("expected non-nil statement")
			}
		})
	}
}

func TestParseMultilineSQL(t *testing.T) {
	query := "SELECT\n\tid,\n\tname\nFROM\n\theroes\nWHERE\n\tlevel > 5\n;"
	stmt, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", query, err)
	}
	sel, ok := stmt.(*SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
	if sel.TableName != "heroes" {
		t.Fatalf("unexpected table name: %s", sel.TableName)
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"SELECT\nFROM\nheroes;", "SELECT FROM heroes;"},
		{"SELECT\t*\tFROM\theroes;", "SELECT * FROM heroes;"},
		{"SELECT  *   FROM\n\theroes;", "SELECT * FROM heroes;"},
	}
	for _, tt := range tests {
		got := normalizeWhitespace(tt.input)
		if got != tt.want {
			t.Errorf("normalizeWhitespace(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseCreateTableWithNotNull(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		wantNN []bool // expected NotNull per column
	}{
		{
			name:   "single NOT NULL column",
			sql:    "CREATE TABLE t (id INT NOT NULL, name VARCHAR(100));",
			wantNN: []bool{true, false},
		},
		{
			name:   "multiple NOT NULL columns",
			sql:    "CREATE TABLE t (id INT NOT NULL, name VARCHAR(100) NOT NULL, age INT);",
			wantNN: []bool{true, true, false},
		},
		{
			name:   "NOT NULL with DEFAULT",
			sql:    "CREATE TABLE t (id INT NOT NULL DEFAULT 1, name VARCHAR(100));",
			wantNN: []bool{true, false},
		},
		{
			name:   "NOT NULL with PRIMARY KEY",
			sql:    "CREATE TABLE t (id INT PRIMARY KEY NOT NULL, name VARCHAR(100));",
			wantNN: []bool{true, false},
		},
		{
			name:   "no NOT NULL",
			sql:    "CREATE TABLE t (id INT, name VARCHAR(100));",
			wantNN: []bool{false, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.sql, err)
			}
			create, ok := stmt.(*CreateTableStatement)
			if !ok {
				t.Fatalf("expected *CreateTableStatement, got %T", stmt)
			}
			if len(create.Columns) != len(tt.wantNN) {
				t.Fatalf("expected %d columns, got %d", len(tt.wantNN), len(create.Columns))
			}
			for i, want := range tt.wantNN {
				if create.Columns[i].NotNull != want {
					t.Errorf("column %d (%s): NotNull = %v, want %v", i, create.Columns[i].Name, create.Columns[i].NotNull, want)
				}
			}
		})
	}
}

func TestVarcharWithNotNull(t *testing.T) {
	tests := []struct {
		name     string
		sql      string
		wantCols []struct {
			name     string
			dataType string
			varchar  int
			notNull  bool
		}
	}{
		{
			name: "VARCHAR(100) NOT NULL",
			sql:  "CREATE TABLE t (name VARCHAR(100) NOT NULL);",
			wantCols: []struct {
				name     string
				dataType string
				varchar  int
				notNull  bool
			}{
				{name: "name", dataType: "VARCHAR", varchar: 100, notNull: true},
			},
		},
		{
			name: "VARCHAR(100) NOT NULL DEFAULT ''",
			sql:  "CREATE TABLE t (name VARCHAR(100) NOT NULL DEFAULT '');",
			wantCols: []struct {
				name     string
				dataType string
				varchar  int
				notNull  bool
			}{
				{name: "name", dataType: "VARCHAR", varchar: 100, notNull: true},
			},
		},
		{
			name: "mixed columns with VARCHAR constraints",
			sql:  "CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(100) NOT NULL, email VARCHAR(255) UNIQUE);",
			wantCols: []struct {
				name     string
				dataType string
				varchar  int
				notNull  bool
			}{
				{name: "id", dataType: "INT", varchar: 0, notNull: false},
				{name: "name", dataType: "VARCHAR", varchar: 100, notNull: true},
				{name: "email", dataType: "VARCHAR", varchar: 255, notNull: false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.sql, err)
			}
			create, ok := stmt.(*CreateTableStatement)
			if !ok {
				t.Fatalf("expected *CreateTableStatement, got %T", stmt)
			}
			if len(create.Columns) != len(tt.wantCols) {
				t.Fatalf("expected %d columns, got %d", len(tt.wantCols), len(create.Columns))
			}
			for i, wc := range tt.wantCols {
				col := create.Columns[i]
				if col.Name != wc.name {
					t.Errorf("column %d: Name = %q, want %q", i, col.Name, wc.name)
				}
				if col.DataType != wc.dataType {
					t.Errorf("column %d (%s): DataType = %q, want %q", i, col.Name, col.DataType, wc.dataType)
				}
				if col.VarcharLen != wc.varchar {
					t.Errorf("column %d (%s): VarcharLen = %d, want %d", i, col.Name, col.VarcharLen, wc.varchar)
				}
				if col.NotNull != wc.notNull {
					t.Errorf("column %d (%s): NotNull = %v, want %v", i, col.Name, col.NotNull, wc.notNull)
				}
			}
		})
	}
}

func TestParseCreateTableWithBigIntNumericTimestampz(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantTypes []string
	}{
		{
			name:      "BIGINT type",
			sql:       "CREATE TABLE t (id BIGINT);",
			wantTypes: []string{"INT"},
		},
		{
			name:      "NUMERIC type",
			sql:       "CREATE TABLE t (amount NUMERIC);",
			wantTypes: []string{"FLOAT"},
		},
		{
			name:      "TIMESTAMPTZ type",
			sql:       "CREATE TABLE t (created_at TIMESTAMPTZ);",
			wantTypes: []string{"TIMESTAMP"},
		},
		{
			name:      "all new types together",
			sql:       "CREATE TABLE t (id BIGINT, amount NUMERIC, ts TIMESTAMPTZ);",
			wantTypes: []string{"INT", "FLOAT", "TIMESTAMP"},
		},
		{
			name:      "new types mixed with existing",
			sql:       "CREATE TABLE t (id INT, big_id BIGINT, ts TIMESTAMPTZ, name TEXT);",
			wantTypes: []string{"INT", "INT", "TIMESTAMP", "TEXT"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.sql, err)
			}
			create, ok := stmt.(*CreateTableStatement)
			if !ok {
				t.Fatalf("expected *CreateTableStatement, got %T", stmt)
			}
			if len(create.Columns) != len(tt.wantTypes) {
				t.Fatalf("expected %d columns, got %d", len(tt.wantTypes), len(create.Columns))
			}
			for i, want := range tt.wantTypes {
				if create.Columns[i].DataType != want {
					t.Errorf("column %d (%s): DataType = %q, want %q", i, create.Columns[i].Name, create.Columns[i].DataType, want)
				}
			}
		})
	}
}

func TestParseCreateTableIfNotExists(t *testing.T) {
	t.Run("IF NOT EXISTS", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE IF NOT EXISTS heroes (id INT, name TEXT);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if !create.IfNotExists {
			t.Fatal("expected IfNotExists to be true")
		}
		if create.TableName != "heroes" {
			t.Fatalf("expected table name 'heroes', got %q", create.TableName)
		}
		if len(create.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(create.Columns))
		}
	})

	t.Run("without IF NOT EXISTS", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE heroes (id INT);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if create.IfNotExists {
			t.Fatal("expected IfNotExists to be false")
		}
	})

	t.Run("IF NOT EXISTS with INFER SCHEMA", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE IF NOT EXISTS heroes INFER SCHEMA;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if !create.IfNotExists {
			t.Fatal("expected IfNotExists to be true")
		}
		if !create.InferSchema {
			t.Fatal("expected InferSchema to be true")
		}
	})
}

func TestParseDropTableIfExists(t *testing.T) {
	t.Run("IF EXISTS", func(t *testing.T) {
		stmt, err := Parse("DROP TABLE IF EXISTS heroes;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		drop, ok := stmt.(*DropTableStatement)
		if !ok {
			t.Fatalf("expected *DropTableStatement, got %T", stmt)
		}
		if !drop.IfExists {
			t.Fatal("expected IfExists to be true")
		}
		if drop.TableName != "heroes" {
			t.Fatalf("expected table name 'heroes', got %q", drop.TableName)
		}
	})

	t.Run("without IF EXISTS", func(t *testing.T) {
		stmt, err := Parse("DROP TABLE heroes;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		drop, ok := stmt.(*DropTableStatement)
		if !ok {
			t.Fatalf("expected *DropTableStatement, got %T", stmt)
		}
		if drop.IfExists {
			t.Fatal("expected IfExists to be false")
		}
	})
}

func TestParseCreateDatabaseIfNotExists(t *testing.T) {
	t.Run("IF NOT EXISTS", func(t *testing.T) {
		stmt, err := Parse("CREATE DATABASE IF NOT EXISTS mydb;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateDatabaseStatement)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStatement, got %T", stmt)
		}
		if !create.IfNotExists {
			t.Fatal("expected IfNotExists to be true")
		}
		if create.DatabaseName != "mydb" {
			t.Fatalf("expected database name 'mydb', got %q", create.DatabaseName)
		}
	})

	t.Run("without IF NOT EXISTS", func(t *testing.T) {
		stmt, err := Parse("CREATE DATABASE mydb;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateDatabaseStatement)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStatement, got %T", stmt)
		}
		if create.IfNotExists {
			t.Fatal("expected IfNotExists to be false")
		}
	})
}

func TestParseDropDatabaseIfExists(t *testing.T) {
	t.Run("IF EXISTS", func(t *testing.T) {
		stmt, err := Parse("DROP DATABASE IF EXISTS mydb;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		drop, ok := stmt.(*DropDatabaseStatement)
		if !ok {
			t.Fatalf("expected *DropDatabaseStatement, got %T", stmt)
		}
		if !drop.IfExists {
			t.Fatal("expected IfExists to be true")
		}
		if drop.DatabaseName != "mydb" {
			t.Fatalf("expected database name 'mydb', got %q", drop.DatabaseName)
		}
	})

	t.Run("without IF EXISTS", func(t *testing.T) {
		stmt, err := Parse("DROP DATABASE mydb;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		drop, ok := stmt.(*DropDatabaseStatement)
		if !ok {
			t.Fatalf("expected *DropDatabaseStatement, got %T", stmt)
		}
		if drop.IfExists {
			t.Fatal("expected IfExists to be false")
		}
	})
}

func TestParseCreateTableWithSerial(t *testing.T) {
	t.Run("SERIAL PRIMARY KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id SERIAL PRIMARY KEY);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(create.Columns))
		}
		col := create.Columns[0]
		if col.Name != "id" {
			t.Errorf("column name = %q, want %q", col.Name, "id")
		}
		if col.DataType != "INT" {
			t.Errorf("DataType = %q, want %q", col.DataType, "INT")
		}
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
		if !col.PrimaryKey {
			t.Error("expected PrimaryKey to be true")
		}
	})

	t.Run("SERIAL without PRIMARY KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id SERIAL);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		col := create.Columns[0]
		if col.DataType != "INT" {
			t.Errorf("DataType = %q, want %q", col.DataType, "INT")
		}
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
	})

	t.Run("SERIAL with other columns", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id SERIAL PRIMARY KEY, name TEXT);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(create.Columns))
		}
		if create.Columns[0].DataType != "INT" || !create.Columns[0].AutoIncrement {
			t.Errorf("first column: DataType=%q AutoIncrement=%v", create.Columns[0].DataType, create.Columns[0].AutoIncrement)
		}
		if create.Columns[1].DataType != "TEXT" {
			t.Errorf("second column: DataType=%q, want TEXT", create.Columns[1].DataType)
		}
	})
}

func TestParseCreateTableAutoIncrementBeforePrimaryKey(t *testing.T) {
	t.Run("INT AUTO_INCREMENT PRIMARY KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE deals (id INT AUTO_INCREMENT PRIMARY KEY);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(create.Columns))
		}
		col := create.Columns[0]
		if col.Name != "id" {
			t.Errorf("column name = %q, want %q", col.Name, "id")
		}
		if col.DataType != "INT" {
			t.Errorf("DataType = %q, want %q", col.DataType, "INT")
		}
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
		if !col.PrimaryKey {
			t.Error("expected PrimaryKey to be true")
		}
	})

	t.Run("INT PRIMARY KEY AUTO_INCREMENT (existing order)", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE deals (id INT PRIMARY KEY AUTO_INCREMENT);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		col := create.Columns[0]
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
		if !col.PrimaryKey {
			t.Error("expected PrimaryKey to be true")
		}
	})

	t.Run("deals table with multiple columns", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE deals (id INT AUTO_INCREMENT PRIMARY KEY, name TEXT NOT NULL);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(create.Columns))
		}
		col := create.Columns[0]
		if !col.AutoIncrement || !col.PrimaryKey {
			t.Errorf("first column: AutoIncrement=%v PrimaryKey=%v", col.AutoIncrement, col.PrimaryKey)
		}
		if create.Columns[1].DataType != "TEXT" || !create.Columns[1].NotNull {
			t.Errorf("second column: DataType=%q NotNull=%v", create.Columns[1].DataType, create.Columns[1].NotNull)
		}
	})
}

func TestParseCreateTableWithGeneratedIdentity(t *testing.T) {
	t.Run("GENERATED ALWAYS AS IDENTITY PRIMARY KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(create.Columns))
		}
		col := create.Columns[0]
		if col.Name != "id" {
			t.Errorf("column name = %q, want %q", col.Name, "id")
		}
		if col.DataType != "INT" {
			t.Errorf("DataType = %q, want %q", col.DataType, "INT")
		}
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
		if !col.PrimaryKey {
			t.Error("expected PrimaryKey to be true")
		}
	})

	t.Run("GENERATED BY DEFAULT AS IDENTITY", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id INT GENERATED BY DEFAULT AS IDENTITY);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		col := create.Columns[0]
		if col.Name != "id" {
			t.Errorf("column name = %q, want %q", col.Name, "id")
		}
		if col.DataType != "INT" {
			t.Errorf("DataType = %q, want %q", col.DataType, "INT")
		}
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
	})

	t.Run("GENERATED ALWAYS AS IDENTITY without PRIMARY KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id INT GENERATED ALWAYS AS IDENTITY);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		col := create.Columns[0]
		if !col.AutoIncrement {
			t.Error("expected AutoIncrement to be true")
		}
		if col.PrimaryKey {
			t.Error("expected PrimaryKey to be false")
		}
	})
}

func TestParseCreateTableWithBlob(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantTypes []string
	}{
		{
			name:      "BLOB type",
			sql:       "CREATE TABLE t (data BLOB);",
			wantTypes: []string{"BLOB"},
		},
		{
			name:      "BLOB with other types",
			sql:       "CREATE TABLE t (id INT, name TEXT, data BLOB);",
			wantTypes: []string{"INT", "TEXT", "BLOB"},
		},
		{
			name:      "multiple BLOB columns",
			sql:       "CREATE TABLE t (enc_data BLOB, image BLOB);",
			wantTypes: []string{"BLOB", "BLOB"},
		},
		{
			name:      "BLOB with NOT NULL",
			sql:       "CREATE TABLE t (data BLOB NOT NULL);",
			wantTypes: []string{"BLOB"},
		},
		{
			name:      "BLOB with DEFAULT",
			sql:       "CREATE TABLE t (data BLOB DEFAULT '')",
			wantTypes: []string{"BLOB"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt, err := Parse(tt.sql)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.sql, err)
			}
			create, ok := stmt.(*CreateTableStatement)
			if !ok {
				t.Fatalf("expected *CreateTableStatement, got %T", stmt)
			}
			if len(create.Columns) != len(tt.wantTypes) {
				t.Fatalf("expected %d columns, got %d", len(tt.wantTypes), len(create.Columns))
			}
			for i, want := range tt.wantTypes {
				if create.Columns[i].DataType != want {
					t.Errorf("column %d (%s): DataType = %q, want %q", i, create.Columns[i].Name, create.Columns[i].DataType, want)
				}
			}
		})
	}
}

func TestParseCreateDatabaseEncrypted(t *testing.T) {
	t.Run("ENCRYPTED WITH KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE DATABASE mydb ENCRYPTED WITH KEY 'secret123';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateDatabaseStatement)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStatement, got %T", stmt)
		}
		if !create.Encrypted {
			t.Fatal("expected Encrypted to be true")
		}
		if create.EncryptionKey != "secret123" {
			t.Fatalf("expected EncryptionKey 'secret123', got %q", create.EncryptionKey)
		}
	})

	t.Run("ENCRYPTED without KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE DATABASE mydb ENCRYPTED;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateDatabaseStatement)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStatement, got %T", stmt)
		}
		if !create.Encrypted {
			t.Fatal("expected Encrypted to be true")
		}
		if create.EncryptionKey != "" {
			t.Fatalf("expected empty EncryptionKey, got %q", create.EncryptionKey)
		}
	})

	t.Run("IF NOT EXISTS ENCRYPTED WITH KEY", func(t *testing.T) {
		stmt, err := Parse("CREATE DATABASE IF NOT EXISTS mydb ENCRYPTED WITH KEY 'mykey';")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateDatabaseStatement)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStatement, got %T", stmt)
		}
		if !create.IfNotExists {
			t.Fatal("expected IfNotExists to be true")
		}
		if !create.Encrypted {
			t.Fatal("expected Encrypted to be true")
		}
		if create.EncryptionKey != "mykey" {
			t.Fatalf("expected EncryptionKey 'mykey', got %q", create.EncryptionKey)
		}
	})

	t.Run("not encrypted", func(t *testing.T) {
		stmt, err := Parse("CREATE DATABASE mydb;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateDatabaseStatement)
		if !ok {
			t.Fatalf("expected *CreateDatabaseStatement, got %T", stmt)
		}
		if create.Encrypted {
			t.Fatal("expected Encrypted to be false")
		}
	})
}

func TestParseCreateTableEncrypted(t *testing.T) {
	t.Run("ENCRYPTED table", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id INT) ENCRYPTED;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if !create.Encrypted {
			t.Fatal("expected Encrypted to be true")
		}
	})

	t.Run("not encrypted table", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id INT);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if create.Encrypted {
			t.Fatal("expected Encrypted to be false")
		}
	})

	t.Run("IF NOT EXISTS ENCRYPTED", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE IF NOT EXISTS t (id INT) ENCRYPTED;")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if !create.IfNotExists {
			t.Fatal("expected IfNotExists to be true")
		}
		if !create.Encrypted {
			t.Fatal("expected Encrypted to be true")
		}
	})
}

func TestParseCreateTableWithEncryptedColumn(t *testing.T) {
	t.Run("single encrypted column", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (secret TEXT ENCRYPTED);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 1 {
			t.Fatalf("expected 1 column, got %d", len(create.Columns))
		}
		if !create.Columns[0].Encrypted {
			t.Fatal("expected column Encrypted to be true")
		}
	})

	t.Run("mixed encrypted and non-encrypted columns", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (id INT, secret TEXT ENCRYPTED, name VARCHAR(100));")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		if len(create.Columns) != 3 {
			t.Fatalf("expected 3 columns, got %d", len(create.Columns))
		}
		if create.Columns[0].Encrypted {
			t.Fatal("expected column 'id' Encrypted to be false")
		}
		if !create.Columns[1].Encrypted {
			t.Fatal("expected column 'secret' Encrypted to be true")
		}
		if create.Columns[2].Encrypted {
			t.Fatal("expected column 'name' Encrypted to be false")
		}
	})

	t.Run("encrypted column with other constraints", func(t *testing.T) {
		stmt, err := Parse("CREATE TABLE t (secret TEXT NOT NULL ENCRYPTED);")
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		create, ok := stmt.(*CreateTableStatement)
		if !ok {
			t.Fatalf("expected *CreateTableStatement, got %T", stmt)
		}
		col := create.Columns[0]
		if !col.NotNull {
			t.Fatal("expected NotNull to be true")
		}
		if !col.Encrypted {
			t.Fatal("expected Encrypted to be true")
		}
	})
}

func TestParseShowEncryptionStatus(t *testing.T) {
	stmt, err := Parse("SHOW ENCRYPTION STATUS;")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if _, ok := stmt.(*ShowEncryptionStatusStatement); !ok {
		t.Fatalf("expected *ShowEncryptionStatusStatement, got %T", stmt)
	}
	if stmt.StatementType() != "SHOW_ENCRYPTION_STATUS" {
		t.Fatalf("expected StatementType SHOW_ENCRYPTION_STATUS, got %s", stmt.StatementType())
	}
}

func TestParseInsertOrReplace(t *testing.T) {
	stmt, err := Parse("INSERT OR REPLACE INTO users (id, name) VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ins, ok := stmt.(*InsertStatement)
	if !ok {
		t.Fatalf("expected *InsertStatement, got %T", stmt)
	}
	if !ins.OrReplace {
		t.Error("expected OrReplace=true")
	}
	if ins.TableName != "users" {
		t.Errorf("expected table 'users', got '%s'", ins.TableName)
	}
	if len(ins.Columns) != 2 || ins.Columns[0] != "id" || ins.Columns[1] != "name" {
		t.Errorf("expected columns [id, name], got %v", ins.Columns)
	}
	if len(ins.Rows) != 1 || len(ins.Rows[0]) != 2 {
		t.Errorf("expected 1 row with 2 values, got %d rows with %d values", len(ins.Rows), len(ins.Rows[0]))
	}
}

func TestParseInsertWithoutOrReplace(t *testing.T) {
	stmt, err := Parse("INSERT INTO users (id, name) VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ins, ok := stmt.(*InsertStatement)
	if !ok {
		t.Fatalf("expected *InsertStatement, got %T", stmt)
	}
	if ins.OrReplace {
		t.Error("expected OrReplace=false")
	}
}

func TestParseInsertOrReplaceWithoutColumns(t *testing.T) {
	stmt, err := Parse("INSERT OR REPLACE INTO users VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	ins := stmt.(*InsertStatement)
	if !ins.OrReplace {
		t.Error("expected OrReplace=true")
	}
	if len(ins.Columns) != 0 {
		t.Errorf("expected no columns, got %v", ins.Columns)
	}
}

func TestParseInsertOrReplaceError(t *testing.T) {
	_, err := Parse("INSERT OR INTO users VALUES (1, 'Alice');")
	if err == nil {
		t.Fatal("expected error for 'INSERT OR INTO'")
	}
}

func TestParseDistinctOn(t *testing.T) {
	cases := []struct {
		name        string
		query       string
		distinctOn  int  // expected number of DISTINCT ON expressions
		distinct    bool // expected DISTINCT flag
	}{
		{
			name:       "DISTINCT ON single column",
			query:      "SELECT DISTINCT ON (name) * FROM t;",
			distinctOn: 1,
			distinct:   true,
		},
		{
			name:       "DISTINCT ON multiple columns",
			query:      "SELECT DISTINCT ON (name, age) * FROM t;",
			distinctOn: 2,
			distinct:   true,
		},
		{
			name:       "DISTINCT ON with expressions",
			query:      "SELECT DISTINCT ON (data->>'category') * FROM t;",
			distinctOn: 1,
			distinct:   true,
		},
		{
			name:       "DISTINCT without ON",
			query:      "SELECT DISTINCT * FROM t;",
			distinctOn: 0,
			distinct:   true,
		},
		{
			name:       "no DISTINCT",
			query:      "SELECT * FROM t;",
			distinctOn: 0,
			distinct:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected *SelectStatement, got %T", stmt)
			}
			if sel.Distinct != tc.distinct {
				t.Fatalf("expected Distinct=%v, got %v", tc.distinct, sel.Distinct)
			}
			if len(sel.DistinctOn) != tc.distinctOn {
				t.Fatalf("expected %d DISTINCT ON expressions, got %d", tc.distinctOn, len(sel.DistinctOn))
			}
		})
	}
}

func TestParseJSONAccessOperators(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		op       string
		inWhere  bool
	}{
		{
			name:    "JSONB contains @> in WHERE",
			query:   "SELECT * FROM t WHERE data @> '{\"a\": 1}';",
			op:      "@>",
			inWhere: true,
		},
		{
			name:    "JSONB has key ? in WHERE",
			query:   "SELECT * FROM t WHERE data ? 'name';",
			op:      "?",
			inWhere: true,
		},
		{
			name:    "JSONB contains @> in SELECT",
			query:   "SELECT data @> '{\"a\": 1}' FROM t;",
			op:      "@>",
			inWhere: false,
		},
		{
			name:    "JSONB has key ? in SELECT",
			query:   "SELECT data ? 'name' FROM t;",
			op:      "?",
			inWhere: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stmt, err := Parse(tc.query)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tc.name, err)
			}
			sel, ok := stmt.(*SelectStatement)
			if !ok {
				t.Fatalf("expected *SelectStatement, got %T", stmt)
			}
			var expr Expression
			if tc.inWhere {
				expr = sel.Where
			} else {
				expr = sel.Columns[0].Expr
			}
			ja, ok := expr.(*JSONAccess)
			if !ok {
				t.Fatalf("expected *JSONAccess, got %T", expr)
			}
			if ja.Operator != tc.op {
				t.Fatalf("expected operator %q, got %q", tc.op, ja.Operator)
			}
		})
	}
}

func TestParseCreateTablePartitionByRange(t *testing.T) {
	sql := `CREATE TABLE orders (
		id INT,
		order_date DATE,
		amount FLOAT
	) PARTITION BY RANGE (order_date) (
		PARTITION p2023 VALUES LESS THAN ('2024-01-01'),
		PARTITION p2024 VALUES LESS THAN ('2025-01-01'),
		PARTITION p2025 VALUES LESS THAN (MAXVALUE)
	);`
	stmt, err := Parse(sql)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	create, ok := stmt.(*CreateTableStatement)
	if !ok {
		t.Fatalf("expected *CreateTableStatement, got %T", stmt)
	}
	if create.PartitionBy == nil {
		t.Fatal("expected PartitionBy to be set")
	}
	if create.PartitionBy.Type != "RANGE" {
		t.Errorf("expected partition type RANGE, got %s", create.PartitionBy.Type)
	}
	if len(create.PartitionBy.Columns) != 1 || create.PartitionBy.Columns[0] != "order_date" {
		t.Errorf("expected partition columns [order_date], got %v", create.PartitionBy.Columns)
	}
	if len(create.PartitionBy.Partitions) != 3 {
		t.Fatalf("expected 3 partitions, got %d", len(create.PartitionBy.Partitions))
	}
	if create.PartitionBy.Partitions[0].Name != "p2023" {
		t.Errorf("expected partition name p2023, got %s", create.PartitionBy.Partitions[0].Name)
	}
	if create.PartitionBy.Partitions[2].Bound != nil {
		t.Error("expected last partition bound to be nil (MAXVALUE)")
	}
}

func TestParseCreateTablePartitionByHash(t *testing.T) {
	sql := `CREATE TABLE sessions (
		user_id INT,
		data TEXT
	) PARTITION BY HASH (user_id) PARTITIONS 4;`
	stmt, err := Parse(sql)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	create, ok := stmt.(*CreateTableStatement)
	if !ok {
		t.Fatalf("expected *CreateTableStatement, got %T", stmt)
	}
	if create.PartitionBy == nil {
		t.Fatal("expected PartitionBy to be set")
	}
	if create.PartitionBy.Type != "HASH" {
		t.Errorf("expected partition type HASH, got %s", create.PartitionBy.Type)
	}
	if len(create.PartitionBy.Columns) != 1 || create.PartitionBy.Columns[0] != "user_id" {
		t.Errorf("expected partition columns [user_id], got %v", create.PartitionBy.Columns)
	}
	if create.PartitionBy.NumParts != 4 {
		t.Errorf("expected 4 partitions, got %d", create.PartitionBy.NumParts)
	}
}

func TestParseCreateTableNoPartition(t *testing.T) {
	sql := `CREATE TABLE t (id INT, name TEXT);`
	stmt, err := Parse(sql)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	create, ok := stmt.(*CreateTableStatement)
	if !ok {
		t.Fatalf("expected *CreateTableStatement, got %T", stmt)
	}
	if create.PartitionBy != nil {
		t.Error("expected PartitionBy to be nil for non-partitioned table")
	}
}

// --- Parser Recursion Depth Limit Tests ---

func TestParserDepthLimitSubquery(t *testing.T) {
	// Build a deeply nested subquery: SELECT (SELECT (SELECT ... 1 ...))
	depth := defaultMaxParserDepth + 5
	query := ""
	for i := 0; i < depth; i++ {
		query += "SELECT ("
	}
	query += "1"
	for i := 0; i < depth; i++ {
		query += ")"
	}
	query += ";"

	_, err := parse(query)
	if err == nil {
		t.Fatalf("expected depth limit error for %d nested subqueries", depth)
	}
	if !strings.Contains(err.Error(), "too deeply nested") {
		t.Fatalf("expected 'too deeply nested' error, got: %v", err)
	}
}

func TestParserDepthLimitExists(t *testing.T) {
	// Build deeply nested EXISTS: SELECT * FROM t WHERE EXISTS (SELECT 1 WHERE EXISTS (SELECT 1 ...))
	depth := defaultMaxParserDepth + 5
	query := "SELECT * FROM t WHERE "
	for i := 0; i < depth; i++ {
		query += "EXISTS (SELECT 1 WHERE "
	}
	query += "1=1"
	for i := 0; i < depth; i++ {
		query += ")"
	}
	query += ";"

	_, err := parse(query)
	if err == nil {
		t.Fatalf("expected depth limit error for %d nested EXISTS", depth)
	}
	if !strings.Contains(err.Error(), "too deeply nested") {
		t.Fatalf("expected 'too deeply nested' error, got: %v", err)
	}
}

func TestParserDepthLimitWithinBounds(t *testing.T) {
	// Queries within the depth limit should parse fine
	query := "SELECT * FROM t WHERE x IN (SELECT id FROM t2 WHERE y IN (SELECT z FROM t3));"
	_, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
}

func TestParserDepthLimitDefault(t *testing.T) {
	// Verify the default max depth is 32
	if defaultMaxParserDepth != 32 {
		t.Fatalf("defaultMaxParserDepth = %d, want 32", defaultMaxParserDepth)
	}
}
