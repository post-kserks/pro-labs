package executor

import (
	"testing"
)

func TestAutoIncrement(t *testing.T) {
	resetSequenceCounters()

	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE items (id INT PRIMARY KEY AUTO_INCREMENT, name TEXT);")

	executeSQL(t, session, "INSERT INTO items (name) VALUES ('first');")
	executeSQL(t, session, "INSERT INTO items (name) VALUES ('second');")
	executeSQL(t, session, "INSERT INTO items (name) VALUES ('third');")

	result := executeSQL(t, session, "SELECT id, name FROM items ORDER BY id;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "1" || result.Rows[0][1] != "first" {
		t.Fatalf("expected (1, 'first'), got (%s, '%s')", result.Rows[0][0], result.Rows[0][1])
	}
	if result.Rows[1][0] != "2" || result.Rows[1][1] != "second" {
		t.Fatalf("expected (2, 'second'), got (%s, '%s')", result.Rows[1][0], result.Rows[1][1])
	}
	if result.Rows[2][0] != "3" || result.Rows[2][1] != "third" {
		t.Fatalf("expected (3, 'third'), got (%s, '%s')", result.Rows[2][0], result.Rows[2][1])
	}
}

func TestAutoIncrementExplicitValue(t *testing.T) {
	resetSequenceCounters()

	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE items2 (id INT PRIMARY KEY AUTO_INCREMENT, name TEXT);")

	executeSQL(t, session, "INSERT INTO items2 (id, name) VALUES (10, 'tenth');")

	result := executeSQL(t, session, "SELECT id, name FROM items2;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "10" {
		t.Fatalf("expected (10, 'tenth'), got %#v", result.Rows[0])
	}

	executeSQL(t, session, "INSERT INTO items2 (name) VALUES ('next');")

	result = executeSQL(t, session, "SELECT id, name FROM items2 ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "10" || result.Rows[0][1] != "tenth" {
		t.Fatalf("expected (10, 'tenth'), got (%s, '%s')", result.Rows[0][0], result.Rows[0][1])
	}
	if result.Rows[1][0] != "11" || result.Rows[1][1] != "next" {
		t.Fatalf("expected (11, 'next'), got (%s, '%s')", result.Rows[1][0], result.Rows[1][1])
	}
}
