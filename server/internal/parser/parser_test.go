package parser

import "testing"

func TestParseValidStatements(t *testing.T) {
	queries := []string{
		"CREATE DATABASE mydb;",
		"DROP DATABASE mydb;",
		"USE mydb;",
		"CREATE TABLE heroes (id INT, name VARCHAR(100), alive BOOL);",
		"DROP TABLE heroes;",
		"SHOW DATABASES;",
		"SHOW TABLES;",
		"SHOW TABLES FROM mydb;",
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

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",
		"SELECT * FROM heroes",
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
		{"missing semicolon", "SELECT * FROM heroes"},
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
