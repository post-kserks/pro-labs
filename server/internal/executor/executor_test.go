package executor

import (
	"testing"

	"pixeldb/internal/parser"
	"pixeldb/internal/storage"
)

func executeSQL(t *testing.T, session *Session, sql string) *Result {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("Parse failed for %q: %v", sql, err)
	}
	result, err := session.Execute(stmt)
	if err != nil {
		t.Fatalf("Execute failed for %q: %v", sql, err)
	}
	return result
}

func setupSession(t *testing.T) *Session {
	t.Helper()
	store := storage.NewFileStorageEngine(t.TempDir())
	session := NewSession(store)

	executeSQL(t, session, "CREATE DATABASE mydb;")
	executeSQL(t, session, "USE mydb;")
	executeSQL(t, session, "CREATE TABLE heroes (id INT, name VARCHAR(100), level INT, alive BOOL, score FLOAT, bio TEXT);")
	return session
}

func seedHeroes(t *testing.T, session *Session) {
	t.Helper()
	executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King of Gondor');")
	executeSQL(t, session, "INSERT INTO heroes VALUES (2, 'Legolas', 9, TRUE, 9.5, 'Elven archer');")
	executeSQL(t, session, "INSERT INTO heroes VALUES (3, 'Gimli', 8, TRUE, 8.2, 'Dwarf warrior');")
	executeSQL(t, session, "INSERT INTO heroes VALUES (4, 'Boromir', 5, FALSE, 7.1, 'Captain of Gondor');")
}

func TestSelectWithoutWhere(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT * FROM heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}
}

func TestSelectLimitAndCount(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	limited := executeSQL(t, session, "SELECT * FROM heroes LIMIT 2;")
	if len(limited.Rows) != 2 {
		t.Fatalf("expected 2 limited rows, got %d", len(limited.Rows))
	}

	counted := executeSQL(t, session, "SELECT COUNT(*) FROM heroes WHERE alive = TRUE;")
	if len(counted.Rows) != 1 || counted.Rows[0][0] != "3" {
		t.Fatalf("expected count 3, got %#v", counted.Rows)
	}
}

func TestMetadataCommands(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	databases := executeSQL(t, session, "SHOW DATABASES;")
	if len(databases.Rows) != 1 || databases.Rows[0][0] != "mydb" {
		t.Fatalf("unexpected databases: %#v", databases.Rows)
	}

	tables := executeSQL(t, session, "SHOW TABLES FROM mydb;")
	if len(tables.Rows) != 1 || tables.Rows[0][0] != "heroes" || tables.Rows[0][1] != "4" {
		t.Fatalf("unexpected tables: %#v", tables.Rows)
	}

	schema := executeSQL(t, session, "DESCRIBE heroes FROM mydb;")
	if len(schema.Rows) != 6 || schema.Rows[1][0] != "name" || schema.Rows[1][1] != "VARCHAR(100)" {
		t.Fatalf("unexpected schema: %#v", schema.Rows)
	}
}

func TestSelectWithWhereAndOrNot(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT id, name FROM heroes WHERE NOT (level < 9) OR alive = FALSE;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
}

func TestInsertOneRow(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King');")
	if result.Affected != 1 {
		t.Fatalf("expected 1 affected row, got %d", result.Affected)
	}
}

func TestInsertMultipleRows(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King'), (2, 'Legolas', 9, TRUE, 9.5, 'Elf');")
	if result.Affected != 2 {
		t.Fatalf("expected 2 affected rows, got %d", result.Affected)
	}
}

func TestInsertWithExplicitColumns(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes (id, name) VALUES (1, 'Aragorn'), (2, 'Legolas');")
	if result.Affected != 2 {
		t.Fatalf("expected 2 affected rows, got %d", result.Affected)
	}

	selected := executeSQL(t, session, "SELECT level, alive FROM heroes WHERE id = 1;")
	if len(selected.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(selected.Rows))
	}
	if selected.Rows[0][0] != "NULL" || selected.Rows[0][1] != "NULL" {
		t.Fatalf("expected NULL values for omitted columns, got %#v", selected.Rows[0])
	}
}

func TestUpdateWithWhere(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "UPDATE heroes SET level = 11 WHERE id = 1;")
	if result.Affected != 1 {
		t.Fatalf("expected 1 affected row, got %d", result.Affected)
	}

	selected := executeSQL(t, session, "SELECT level FROM heroes WHERE id = 1;")
	if selected.Rows[0][0] != "11" {
		t.Fatalf("expected level=11, got %#v", selected.Rows[0][0])
	}
}

func TestDeleteWithWhere(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "DELETE FROM heroes WHERE alive = FALSE;")
	if result.Affected != 1 {
		t.Fatalf("expected 1 affected row, got %d", result.Affected)
	}

	selected := executeSQL(t, session, "SELECT * FROM heroes;")
	if len(selected.Rows) != 3 {
		t.Fatalf("expected 3 rows after delete, got %d", len(selected.Rows))
	}
}

func TestSelectFromMissingTable(t *testing.T) {
	store := storage.NewFileStorageEngine(t.TempDir())
	session := NewSession(store)
	executeSQL(t, session, "CREATE DATABASE mydb;")
	executeSQL(t, session, "USE mydb;")

	stmt, err := parser.Parse("SELECT * FROM ghosts;")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_, err = session.Execute(stmt)
	if err == nil {
		t.Fatal("expected error when selecting from missing table")
	}
}

func TestInsertWrongValuesCount(t *testing.T) {
	session := setupSession(t)
	stmt, err := parser.Parse("INSERT INTO heroes VALUES (1, 'Aragorn');")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_, err = session.Execute(stmt)
	if err == nil {
		t.Fatal("expected insert error for wrong values count")
	}
}
