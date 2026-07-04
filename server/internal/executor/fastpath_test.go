package executor

import (
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func TestFastPathInsert(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Frodo', 15, TRUE, 9.0, 'Ring bearer');")
	if result.Type != "affected" {
		t.Fatalf("expected 'affected' result, got %s", result.Type)
	}
	if result.Affected != 1 {
		t.Fatalf("expected 1 affected row, got %d", result.Affected)
	}

	sel := executeSQL(t, session, "SELECT * FROM heroes WHERE id = 10;")
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}
	if sel.Rows[0][1] != "Frodo" {
		t.Fatalf("expected name 'Frodo', got %q", sel.Rows[0][1])
	}
}

func TestFastPathInsertWithColumnList(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes (id, name, level) VALUES (20, 'Bilbo', 5);")
	if result.Type != "affected" {
		t.Fatalf("expected 'affected' result, got %s", result.Type)
	}
	if result.Affected != 1 {
		t.Fatalf("expected 1 affected row, got %d", result.Affected)
	}

	sel := executeSQL(t, session, "SELECT name, level FROM heroes WHERE id = 20;")
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}
	if sel.Rows[0][0] != "Bilbo" {
		t.Fatalf("expected name 'Bilbo', got %q", sel.Rows[0][0])
	}
}

func TestSlowPathInsertWithReturning(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (30, 'Sam', 3, TRUE, 7.0, 'Loyal friend') RETURNING name;")
	if result.Type != "rows" {
		t.Fatalf("expected 'rows' result for RETURNING, got %s", result.Type)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] != "Sam" {
		t.Fatalf("expected row with 'Sam', got %v", result.Rows)
	}
}

func TestSlowPathInsertWithOnConflict(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King of Gondor');")
	result := executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Strider', 12, TRUE, 9.9, 'Ranger') ON CONFLICT DO UPDATE SET name = 'Aragorn';")
	if result.Type != "affected" {
		t.Fatalf("expected 'affected' result, got %s", result.Type)
	}
}

func TestSlowPathInsertWithMultipleRows(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (50, 'Gandalf', 20, TRUE, 10.0, 'Wizard'), (51, 'Saruman', 18, FALSE, 9.5, 'White wizard');")
	if result.Type != "affected" {
		t.Fatalf("expected 'affected' result, got %s", result.Type)
	}
	if result.Affected != 2 {
		t.Fatalf("expected 2 affected rows, got %d", result.Affected)
	}
}

func TestFastPathSelect(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT id, name FROM heroes WHERE level > 5 ORDER BY level;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows (level > 5), got %d", len(result.Rows))
	}
	if result.Rows[0][1] != "Gimli" {
		t.Fatalf("expected first row 'Gimli', got %q", result.Rows[0][1])
	}
}

func TestFastPathSelectAll(t *testing.T) {
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

func TestFastPathSelectWithLimit(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT id, name FROM heroes ORDER BY id LIMIT 2;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
}

func TestSlowPathSelectWithJoin(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE guilds (id INT, hero_id INT, name VARCHAR(50));")
	executeSQL(t, session, "INSERT INTO guilds VALUES (1, 1, 'Fellowship');")
	executeSQL(t, session, "INSERT INTO guilds VALUES (2, 2, 'Rangers');")
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT heroes.name, guilds.name FROM heroes JOIN guilds ON heroes.id = guilds.hero_id;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows from JOIN, got %d", len(result.Rows))
	}
}

func TestSlowPathSelectWithGroupBy(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT alive, COUNT(*) FROM heroes GROUP BY alive;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(result.Rows))
	}
}

func TestSlowPathSelectWithAggregate(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
}

func TestSlowPathSelectWithCTE(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "WITH strong AS (SELECT * FROM heroes WHERE level > 8) SELECT name FROM strong;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows from CTE, got %d", len(result.Rows))
	}
}

func TestSlowPathSelectWithDistinct(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE tags (id INT, tag VARCHAR(20));")
	executeSQL(t, session, "INSERT INTO tags VALUES (1, 'hero');")
	executeSQL(t, session, "INSERT INTO tags VALUES (2, 'hero');")
	executeSQL(t, session, "INSERT INTO tags VALUES (3, 'villain');")

	result := executeSQL(t, session, "SELECT DISTINCT tag FROM tags;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 distinct tags, got %d", len(result.Rows))
	}
}

func TestFastPathInsertIntoNonexistentTable(t *testing.T) {
	session := setupSession(t)

	executeSQLExpectError(t, session, "INSERT INTO nonexistent VALUES (1, 'test');")
}

func TestFastPathSelectFromNonexistentTable(t *testing.T) {
	session := setupSession(t)

	executeSQLExpectError(t, session, "SELECT * FROM nonexistent;")
}

func TestSlowPathInsertInTransaction(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

	executeSQL(t, session, "CREATE DATABASE txdb;")
	executeSQL(t, session, "USE txdb;")
	executeSQL(t, session, "CREATE TABLE t1 (id INT, val TEXT);")

	executeSQL(t, session, "BEGIN;")
	result := executeSQL(t, session, "INSERT INTO t1 VALUES (1, 'buffered');")
	if result.Type != "message" {
		t.Fatalf("expected buffered message in tx, got %s: %v", result.Type, result.Message)
	}
	executeSQL(t, session, "COMMIT;")

	sel := executeSQL(t, session, "SELECT * FROM t1;")
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(sel.Rows))
	}
}

func TestFastPathSelectWithOffset(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT id, name FROM heroes ORDER BY id OFFSET 1;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows after offset 1, got %d", len(result.Rows))
	}
}

func TestFastPathInsertWrongColumnCount(t *testing.T) {
	session := setupSession(t)

	executeSQLExpectError(t, session, "INSERT INTO heroes VALUES (1, 'test');")
}

func TestFastPathSelectWithWhere(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT * FROM heroes WHERE id = 1;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
}

// Benchmarks

func benchSetup(b *testing.B) *Session {
	b.Helper()
	dir := b.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)
	return session
}

func benchExec(b *testing.B, session *Session, sql string) {
	b.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		b.Fatal(err)
	}
	_, err = session.Execute(stmt)
	if err != nil {
		b.Fatal(err)
	}
}

func BenchmarkFastPathInsert(b *testing.B) {
	session := benchSetup(b)
	benchExec(b, session, "CREATE DATABASE benchdb;")
	benchExec(b, session, "USE benchdb;")
	benchExec(b, session, "CREATE TABLE bench (id INT, name TEXT, val INT);")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchExec(b, session, "INSERT INTO bench VALUES (1, 'test', 42);")
	}
}

func BenchmarkFastPathSelect(b *testing.B) {
	session := benchSetup(b)
	benchExec(b, session, "CREATE DATABASE benchdb;")
	benchExec(b, session, "USE benchdb;")
	benchExec(b, session, "CREATE TABLE bench (id INT, name TEXT, val INT);")
	for i := 0; i < 100; i++ {
		benchExec(b, session, "INSERT INTO bench VALUES (1, 'test', 42);")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchExec(b, session, "SELECT * FROM bench WHERE val > 0;")
	}
}
