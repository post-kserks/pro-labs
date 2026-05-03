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
		"INSERT INTO heroes VALUES (1, 'Aragorn', 10);",
		"INSERT INTO heroes (id, name) VALUES (1, 'test'), (2, 'test2');",
		"UPDATE heroes SET level = 11 WHERE id = 1;",
		"DELETE FROM heroes WHERE alive = FALSE;",
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
	if len(sel.Columns) != 2 || sel.Columns[0] != "id" || sel.Columns[1] != "name" {
		t.Fatalf("unexpected columns: %#v", sel.Columns)
	}
	if sel.TableName != "heroes" {
		t.Fatalf("unexpected table name: %s", sel.TableName)
	}
	if sel.Where == nil {
		t.Fatal("expected WHERE expression")
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
