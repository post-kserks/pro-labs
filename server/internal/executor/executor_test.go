package executor

import (
	"strings"
	"testing"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
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

func executeSQLExpectError(t *testing.T, session *Session, sql string) {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("Parse failed for %q: %v", sql, err)
	}
	_, err = session.Execute(stmt)
	if err == nil {
		t.Fatalf("Expected error for %q, but got none", sql)
	}
}

func setupSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

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

func TestAggregateFunctions(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// SUM, AVG, MIN, MAX
	res := executeSQL(t, session, "SELECT SUM(level), AVG(score), MIN(level), MAX(level) FROM heroes;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	// Aragorn (10), Legolas (9), Gimli (8), Boromir (5) -> SUM = 32, AVG = (9.8+9.5+8.2+7.1)/4 = 34.6/4 = 8.65, MIN = 5, MAX = 10
	if res.Rows[0][0] != "32" {
		t.Fatalf("expected SUM=32, got %s", res.Rows[0][0])
	}
	if res.Rows[0][1] != "8.65" {
		t.Fatalf("expected AVG=8.65, got %s", res.Rows[0][1])
	}
	if res.Rows[0][2] != "5" {
		t.Fatalf("expected MIN=5, got %s", res.Rows[0][2])
	}
	if res.Rows[0][3] != "10" {
		t.Fatalf("expected MAX=10, got %s", res.Rows[0][3])
	}
}

func TestArithmeticProjection(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// SELECT name, level * 2 as double_level FROM heroes WHERE id = 1;
	res := executeSQL(t, session, "SELECT name, level * 2 as double_level FROM heroes WHERE id = 1;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Rows[0][1] != "20" {
		t.Fatalf("expected double_level=20, got %s", res.Rows[0][1])
	}
	if res.Columns[1] != "double_level" {
		t.Fatalf("expected column name double_level, got %s", res.Columns[1])
	}
}

func TestGroupBy(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)
	executeSQL(t, session, "INSERT INTO heroes (id, name, level, alive) VALUES (5, 'Faramir', 5, TRUE);")

	// GROUP BY level
	res := executeSQL(t, session, "SELECT level, COUNT(*) as cnt FROM heroes GROUP BY level ORDER BY level DESC;")
	// level 10: 1 (Aragorn)
	// level 9: 1 (Legolas)
	// level 8: 1 (Gimli)
	// level 5: 2 (Boromir, Faramir)
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 groups, got %d", len(res.Rows))
	}
	if res.Rows[3][0] != "5" || res.Rows[3][1] != "2" {
		t.Fatalf("expected level 5 group to have count 2, got level=%s cnt=%s", res.Rows[3][0], res.Rows[3][1])
	}
}
func TestHaving(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)
	executeSQL(t, session, "INSERT INTO heroes (id, name, level, alive) VALUES (5, 'Faramir', 5, TRUE);")

	// GROUP BY level HAVING COUNT(*) > 1
	res := executeSQL(t, session, "SELECT level, COUNT(*) as cnt FROM heroes GROUP BY level HAVING cnt > 1;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 group, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "5" || res.Rows[0][1] != "2" {
		t.Fatalf("expected level 5 group with count 2, got level=%s cnt=%s", res.Rows[0][0], res.Rows[0][1])
	}
}
func TestJoin(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)
	executeSQL(t, session, "CREATE TABLE weapons (hero_id INT, model VARCHAR(50));")
	executeSQL(t, session, "INSERT INTO weapons VALUES (1, 'Anduril'), (2, 'Galadhrim Bow'), (3, 'Axe of Durin');")

	// INNER JOIN
	res := executeSQL(t, session, "SELECT heroes.name, weapons.model FROM heroes JOIN weapons ON heroes.id = weapons.hero_id;")
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 joined rows, got %d", len(res.Rows))
	}
	// Aragorn - Anduril
	// Legolas - Galadhrim Bow
	// Gimli - Axe of Durin
	found := false
	for _, row := range res.Rows {
		if row[0] == "Aragorn" && row[1] == "Anduril" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Aragorn with Anduril not found in join results: %#v", res.Rows)
	}
}

func TestSetOperations(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// UNION: heroes with level 10 + heroes with level 9
	res := executeSQL(t, session, "SELECT name FROM heroes WHERE level = 10 UNION SELECT name FROM heroes WHERE level = 9;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows for UNION, got %d", len(res.Rows))
	}

	// INTERSECT: heroes with level > 8 and heroes with level < 10
	res = executeSQL(t, session, "SELECT name FROM heroes WHERE level > 8 INTERSECT SELECT name FROM heroes WHERE level < 10;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "Legolas" {
		t.Fatalf("expected Legolas for INTERSECT, got %#v", res.Rows)
	}

	// EXCEPT: all heroes EXCEPT those with level > 8
	res = executeSQL(t, session, "SELECT name FROM heroes EXCEPT SELECT name FROM heroes WHERE level > 8;")
	if len(res.Rows) != 2 { // Gimli (8), Boromir (5)
		t.Fatalf("expected 2 rows for EXCEPT, got %d", len(res.Rows))
	}
}

func TestSubqueries(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Scalar subquery: SELECT name FROM heroes WHERE level > (SELECT AVG(level) FROM heroes);
	// Levels: 10, 9, 8, 5 -> AVG = 32/4 = 8.0
	// Greater than 8.0: Aragorn (10), Legolas (9)
	res := executeSQL(t, session, "SELECT name FROM heroes WHERE level > (SELECT AVG(level) FROM heroes) ORDER BY level DESC;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows for scalar subquery, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "Aragorn" || res.Rows[1][0] != "Legolas" {
		t.Fatalf("unexpected scalar subquery results: %#v", res.Rows)
	}

	// IN subquery: SELECT name FROM heroes WHERE id IN (SELECT id FROM heroes WHERE level >= 9);
	// id IN (1, 2) -> Aragorn, Legolas
	res = executeSQL(t, session, "SELECT name FROM heroes WHERE id IN (SELECT id FROM heroes WHERE level >= 9) ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows for IN subquery, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "Aragorn" || res.Rows[1][0] != "Legolas" {
		t.Fatalf("unexpected IN subquery results: %#v", res.Rows)
	}
}

func TestWindowFunctions(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// ROW_NUMBER() OVER (ORDER BY level DESC)
	res := executeSQL(t, session, "SELECT name, level, ROW_NUMBER() OVER (ORDER BY level DESC) as rn FROM heroes;")
	if len(res.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(res.Rows))
	}
	// Aragorn (10) -> 1
	// Legolas (9) -> 2
	// Gimli (8) -> 3
	// Boromir (5) -> 4
	if res.Rows[0][2] != "1" || res.Rows[3][2] != "4" {
		t.Fatalf("unexpected row numbers: %#v", res.Rows)
	}

	// SUM(level) OVER (ORDER BY level ASC) -- Running total
	// Boromir (5) -> 5
	// Gimli (8) -> 13
	// Legolas (9) -> 22
	// Aragorn (10) -> 32
	res = executeSQL(t, session, "SELECT name, SUM(level) OVER (ORDER BY level ASC) as total FROM heroes;")
	found := false
	for _, row := range res.Rows {
		if row[0] == "Gimli" && row[1] == "13" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("running total for Gimli (13) not found: %#v", res.Rows)
	}
}

func TestAlterTable(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE t_alter (id INT, val TEXT);")
	executeSQL(t, session, "INSERT INTO t_alter VALUES (1, 'old');")

	// ADD COLUMN
	executeSQL(t, session, "ALTER TABLE t_alter ADD COLUMN score FLOAT DEFAULT 0.0;")
	res := executeSQL(t, session, "SELECT * FROM t_alter;")
	if len(res.Columns) != 3 || res.Rows[0][2] != "0" {
		t.Fatalf("expected 3 columns and default value 0.0, got cols=%d val=%s", len(res.Columns), res.Rows[0][2])
	}

	// RENAME COLUMN
	executeSQL(t, session, "ALTER TABLE t_alter RENAME COLUMN val TO description;")
	res = executeSQL(t, session, "SELECT description FROM t_alter;")
	if res.Columns[0] != "description" {
		t.Fatalf("expected column name description, got %s", res.Columns[0])
	}

	// DROP COLUMN
	executeSQL(t, session, "ALTER TABLE t_alter DROP COLUMN score;")
	res = executeSQL(t, session, "SELECT * FROM t_alter;")
	if len(res.Columns) != 2 {
		t.Fatalf("expected 2 columns after drop, got %d", len(res.Columns))
	}

	// RENAME TABLE
	executeSQL(t, session, "ALTER TABLE t_alter RENAME TO t_new;")
	res = executeSQL(t, session, "SELECT * FROM t_new;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row from renamed table, got %d", len(res.Rows))
	}
}

func TestBuiltInFunctions(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// UPPER, LOWER, CONCAT
	res := executeSQL(t, session, "SELECT UPPER(name), LOWER(name), CONCAT(name, ' (', level, ')') FROM heroes WHERE id = 1;")
	if res.Rows[0][0] != "ARAGORN" || res.Rows[0][1] != "aragorn" || res.Rows[0][2] != "Aragorn (10)" {
		t.Fatalf("unexpected string function results: %#v", res.Rows[0])
	}

	// ABS, ROUND
	res = executeSQL(t, session, "SELECT ABS(-10.5), ROUND(8.654, 1);")
	if res.Rows[0][0] != "10.5" || res.Rows[0][1] != "8.7" {
		t.Fatalf("unexpected math function results: %#v", res.Rows[0])
	}

	// COALESCE
	res = executeSQL(t, session, "SELECT COALESCE(NULL, 'fallback');")
	if res.Rows[0][0] != "fallback" {
		t.Fatalf("expected fallback, got %s", res.Rows[0][0])
	}
}

func TestCaseAndCast(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// CAST
	res := executeSQL(t, session, "SELECT CAST(id AS TEXT), CAST('123' AS INT) + 1 FROM heroes WHERE id = 1;")
	if res.Rows[0][0] != "1" || res.Rows[0][1] != "124" {
		t.Fatalf("unexpected CAST results: %#v", res.Rows[0])
	}

	// CASE searched
	res = executeSQL(t, session, "SELECT name, CASE WHEN level >= 10 THEN 'legend' WHEN level >= 8 THEN 'veteran' ELSE 'rookie' END as rank FROM heroes ORDER BY level DESC;")
	// Aragorn (10) -> legend
	// Legolas (9) -> veteran
	// Gimli (8) -> veteran
	// Boromir (5) -> rookie
	if res.Rows[0][1] != "legend" || res.Rows[1][1] != "veteran" || res.Rows[3][1] != "rookie" {
		t.Fatalf("unexpected CASE results: %#v", res.Rows)
	}

	// CASE simple
	res = executeSQL(t, session, "SELECT name, CASE id WHEN 1 THEN 'one' WHEN 2 THEN 'two' ELSE 'other' END FROM heroes ORDER BY id;")
	if res.Rows[0][1] != "one" || res.Rows[1][1] != "two" || res.Rows[2][1] != "other" {
		t.Fatalf("unexpected simple CASE results: %#v", res.Rows)
	}
}

func TestSemanticSearch(t *testing.T) {
	session := setupSession(t)
	// Tests use a deterministic mock-embedder; in production without
	// configured AI, SEMANTIC_MATCH returns an error (see NoopEmbedder).
	session.SetEmbedder(ai.MockEmbedder{})
	executeSQL(t, session, "CREATE TABLE docs (id INT, content TEXT, v VECTOR(8));")

	// Use AI_EMBED to generate vectors during INSERT
	executeSQL(t, session, "INSERT INTO docs (id, content, v) VALUES (1, 'database systems', AI_EMBED('database systems'));")
	executeSQL(t, session, "INSERT INTO docs (id, content, v) VALUES (2, 'artificial intelligence', AI_EMBED('artificial intelligence'));")

	// Semantic search using content SEMANTIC_MATCH 'query'
	res := executeSQL(t, session, "SELECT content FROM docs WHERE content SEMANTIC_MATCH 'sql storage';")
	if len(res.Rows) != 1 || res.Rows[0][0] != "database systems" {
		t.Fatalf("expected semantic match for 'database systems', got %#v", res.Rows)
	}

	res = executeSQL(t, session, "SELECT content FROM docs WHERE content SEMANTIC_MATCH 'neural networks';")
	if len(res.Rows) != 1 || res.Rows[0][0] != "artificial intelligence" {
		t.Fatalf("expected semantic match for 'artificial intelligence', got %#v", res.Rows)
	}
}

func TestSchemaFreeMode(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE dynamic INFER SCHEMA;")

	// First insert infers schema: id=INT, data=FLEXIBLE
	executeSQL(t, session, "INSERT INTO dynamic (id, data) VALUES (1, '{\"name\": \"VaultDB\", \"tags\": [\"sql\", \"ai\"]}');")

	res := executeSQL(t, session, "SELECT * FROM dynamic;")
	if len(res.Columns) != 2 || res.Columns[1] != "data" {
		t.Fatalf("expected inferred columns [id, data], got %v", res.Columns)
	}

	// Query using JSON path
	res = executeSQL(t, session, "SELECT data->>'name' FROM dynamic WHERE id = 1;")
	if res.Rows[0][0] != "VaultDB" {
		t.Fatalf("expected JSON path result 'VaultDB', got %s", res.Rows[0][0])
	}
}

func TestSelectOrderBy(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Sort by level ASC
	asc := executeSQL(t, session, "SELECT name FROM heroes ORDER BY level ASC;")
	expectedAsc := []string{"Boromir", "Gimli", "Legolas", "Aragorn"}
	for i, name := range expectedAsc {
		if asc.Rows[i][0] != name {
			t.Fatalf("expected %s at index %d, got %s", name, i, asc.Rows[i][0])
		}
	}

	// Sort by level DESC
	desc := executeSQL(t, session, "SELECT name FROM heroes ORDER BY level DESC;")
	expectedDesc := []string{"Aragorn", "Legolas", "Gimli", "Boromir"}
	for i, name := range expectedDesc {
		if desc.Rows[i][0] != name {
			t.Fatalf("expected %s at index %d, got %s", name, i, desc.Rows[i][0])
		}
	}

	// Multi-column sort: score DESC, name ASC (not needed here but good to test)
}

func TestSelectOffset(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// ORDER BY level DESC LIMIT 2 OFFSET 1
	// Full: Aragorn (10), Legolas (9), Gimli (8), Boromir (5)
	// Offset 1: Legolas (9), Gimli (8), Boromir (5)
	// Limit 2: Legolas (9), Gimli (8)
	res := executeSQL(t, session, "SELECT name FROM heroes ORDER BY level DESC LIMIT 2 OFFSET 1;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "Legolas" || res.Rows[1][0] != "Gimli" {
		t.Fatalf("unexpected rows: %#v", res.Rows)
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
	if selected.Rows[0][0] != "" || selected.Rows[0][1] != "" {
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

func TestUpdateSetQualifiedLHS(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "UPDATE heroes SET heroes.name = 'Strider' WHERE heroes.id = 1;")
	if result.Affected != 1 {
		t.Fatalf("expected 1 affected row, got %d", result.Affected)
	}

	selected := executeSQL(t, session, "SELECT name FROM heroes WHERE id = 1;")
	if selected.Rows[0][0] != "Strider" {
		t.Fatalf("expected name='Strider', got %#v", selected.Rows[0][0])
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
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := NewSession(store, nil, txm, nil)
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

func TestExplainAnalyze(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "EXPLAIN ANALYZE SELECT * FROM heroes WHERE alive = TRUE;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}
	if !strings.Contains(result.Message, "OPTIMIZED QUERY PLAN") {
		t.Fatalf("expected query plan output, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "Actual Rows") {
		t.Fatalf("expected stats in explain analyze output, got: %s", result.Message)
	}
}

func TestTimeTravelAsOf(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King');")

	time.Sleep(10 * time.Millisecond)
	marker := time.Now().UTC().Format(time.RFC3339Nano)
	time.Sleep(10 * time.Millisecond)

	executeSQL(t, session, "UPDATE heroes SET level = 11 WHERE id = 1;")

	current := executeSQL(t, session, "SELECT level FROM heroes WHERE id = 1;")
	if len(current.Rows) != 1 || current.Rows[0][0] != "11" {
		t.Fatalf("expected current level=11, got %#v", current.Rows)
	}

	historical := executeSQL(t, session, "SELECT level FROM heroes AS OF TIMESTAMP '"+marker+"' WHERE id = 1;")
	if len(historical.Rows) != 1 || historical.Rows[0][0] != "10" {
		t.Fatalf("expected historical level=10, got %#v", historical.Rows)
	}
	if historical.AsOfNote == "" {
		t.Fatalf("expected as_of_note in result")
	}
}

func TestHistoryCommand(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King');")
	executeSQL(t, session, "UPDATE heroes SET level = 11 WHERE id = 1;")

	history := executeSQL(t, session, "HISTORY heroes KEY 1;")
	if history.Type != "rows" {
		t.Fatalf("expected rows result, got %s", history.Type)
	}
	if len(history.Rows) < 2 {
		t.Fatalf("expected at least 2 history rows, got %d", len(history.Rows))
	}
}

func TestSemanticMatchWithoutAIConfigured(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE docs2 (id INT, content TEXT);")
	executeSQL(t, session, "INSERT INTO docs2 VALUES (1, 'database systems');")

	stmt, err := parser.Parse("SELECT content FROM docs2 WHERE content SEMANTIC_MATCH 'sql';")
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(stmt)
	if err == nil {
		t.Fatal("SEMANTIC_MATCH without configured AI must return an error, not a mock result")
	}
	if !strings.Contains(err.Error(), "AI embedding is not configured") {
		t.Fatalf("error must explain how to configure AI, got: %v", err)
	}
}

func TestExplainContainsPlannerNote(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "EXPLAIN SELECT * FROM heroes;")
	if !strings.Contains(result.Message, "OPTIMIZED QUERY PLAN") {
		t.Fatalf("EXPLAIN output must contain optimized plan, got:\n%s", result.Message)
	}
	if !strings.Contains(result.Message, "Estimated Cost") {
		t.Fatalf("EXPLAIN output must contain cost estimate, got:\n%s", result.Message)
	}
}

func TestInsertSelectBasic(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	executeSQL(t, session, "CREATE TABLE heroes_copy (id INT, name VARCHAR(100), level INT, alive BOOL, score FLOAT, bio TEXT);")

	result := executeSQL(t, session, "INSERT INTO heroes_copy SELECT * FROM heroes;")
	if result.Affected != 4 {
		t.Fatalf("expected 4 affected rows, got %d", result.Affected)
	}

	sel := executeSQL(t, session, "SELECT COUNT(*) FROM heroes_copy;")
	if sel.Rows[0][0] != "4" {
		t.Fatalf("expected 4 rows in heroes_copy, got %s", sel.Rows[0][0])
	}

	sel = executeSQL(t, session, "SELECT name FROM heroes_copy WHERE id = 1;")
	if sel.Rows[0][0] != "Aragorn" {
		t.Fatalf("expected 'Aragorn', got %s", sel.Rows[0][0])
	}
}

func TestInsertSelectWithWhere(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	executeSQL(t, session, "CREATE TABLE alive_heroes (id INT, name VARCHAR(100), level INT);")

	result := executeSQL(t, session, "INSERT INTO alive_heroes (id, name, level) SELECT id, name, level FROM heroes WHERE alive = TRUE;")
	if result.Affected != 3 {
		t.Fatalf("expected 3 affected rows, got %d", result.Affected)
	}

	sel := executeSQL(t, session, "SELECT name FROM alive_heroes ORDER BY id;")
	expected := []string{"Aragorn", "Legolas", "Gimli"}
	if len(sel.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(sel.Rows))
	}
	for i, row := range sel.Rows {
		if row[0] != expected[i] {
			t.Fatalf("row %d: expected %s, got %s", i, expected[i], row[0])
		}
	}
}

func TestInsertSelectCTE(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	executeSQL(t, session, "CREATE TABLE dst (id INT, name VARCHAR(100));")

	result := executeSQL(t, session, "WITH cte AS (INSERT INTO dst SELECT id, name FROM heroes RETURNING id, name) SELECT * FROM cte;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result type, got %s", result.Type)
	}
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows from CTE SELECT, got %d", len(result.Rows))
	}

	sel := executeSQL(t, session, "SELECT COUNT(*) FROM dst;")
	if sel.Rows[0][0] != "4" {
		t.Fatalf("expected 4 rows in dst, got %s", sel.Rows[0][0])
	}
}

func TestUpdateFromSubquery(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Create a source table with bonus levels
	executeSQL(t, session, "CREATE TABLE bonuses (hero_id INT, bonus INT);")
	executeSQL(t, session, "INSERT INTO bonuses VALUES (1, 5);")
	executeSQL(t, session, "INSERT INTO bonuses VALUES (2, 3);")
	executeSQL(t, session, "INSERT INTO bonuses VALUES (3, 2);")

	// UPDATE using FROM subquery
	result := executeSQL(t, session,
		"UPDATE heroes SET level = level + b.bonus FROM (SELECT hero_id, bonus FROM bonuses) AS b WHERE heroes.id = b.hero_id RETURNING name, level;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 returned rows, got %d", len(result.Rows))
	}

	// Verify: Aragorn 10+5=15, Legolas 9+3=12, Gimli 8+2=10
	expected := map[string]string{
		"Aragorn": "15",
		"Legolas": "12",
		"Gimli":   "10",
	}
	for _, row := range result.Rows {
		name, level := row[0], row[1]
		if exp, ok := expected[name]; ok {
			if level != exp {
				t.Fatalf("expected level %s for %s, got %s", exp, name, level)
			}
		} else {
			t.Fatalf("unexpected row: %v", row)
		}
	}

	// Boromir should remain unchanged (no bonus row for id=4)
	verify := executeSQL(t, session, "SELECT level FROM heroes WHERE name = 'Boromir';")
	if len(verify.Rows) != 1 || verify.Rows[0][0] != "5" {
		t.Fatalf("expected Boromir level 5, got %v", verify.Rows[0])
	}
}

func TestParamLimitOffset(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Prepare a statement with parameterised LIMIT and OFFSET
	executeSQL(t, session, "PREPARE page AS SELECT name FROM heroes ORDER BY id LIMIT $1 OFFSET $2;")

	// OFFSET 0, LIMIT 2 → first two rows
	result := executeSQL(t, session, "EXECUTE page (2, 0);")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Aragorn" || result.Rows[1][0] != "Legolas" {
		t.Fatalf("expected [Aragorn, Legolas], got %v", result.Rows)
	}

	// OFFSET 2, LIMIT 2 → rows 3 and 4
	result = executeSQL(t, session, "EXECUTE page (2, 2);")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Gimli" || result.Rows[1][0] != "Boromir" {
		t.Fatalf("expected [Gimli, Boromir], got %v", result.Rows)
	}

	// OFFSET 1, LIMIT 2 → rows 2 and 3
	result = executeSQL(t, session, "EXECUTE page (2, 1);")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Legolas" || result.Rows[1][0] != "Gimli" {
		t.Fatalf("expected [Legolas, Gimli], got %v", result.Rows)
	}

	// LIMIT larger than total rows
	result = executeSQL(t, session, "EXECUTE page (100, 0);")
	if len(result.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(result.Rows))
	}

	// OFFSET beyond total rows
	result = executeSQL(t, session, "EXECUTE page (10, 100);")
	if len(result.Rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(result.Rows))
	}

	// Only LIMIT param, no OFFSET
	executeSQL(t, session, "PREPARE limited AS SELECT name FROM heroes ORDER BY id LIMIT $1;")
	result = executeSQL(t, session, "EXECUTE limited (1);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "Aragorn" {
		t.Fatalf("expected [Aragorn], got %v", result.Rows)
	}
}

func TestCreateDatabaseIfNotExists(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

	// Create database normally
	executeSQL(t, session, "CREATE DATABASE testdb;")

	// Create same database with IF NOT EXISTS — should succeed without error
	res := executeSQL(t, session, "CREATE DATABASE IF NOT EXISTS testdb;")
	if res.Message != "Database 'testdb' already exists, skipping." {
		t.Fatalf("unexpected message: %s", res.Message)
	}

	// Create new database with IF NOT EXISTS — should succeed
	executeSQL(t, session, "CREATE DATABASE IF NOT EXISTS newdb;")

	// Verify newdb was created
	executeSQL(t, session, "USE newdb;")
}

func TestDropDatabaseIfExists(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

	// Create and then drop a database
	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "DROP DATABASE testdb;")

	// Drop non-existent database with IF EXISTS — should succeed without error
	res := executeSQL(t, session, "DROP DATABASE IF EXISTS testdb;")
	if res.Message != "Database 'testdb' does not exist, skipping." {
		t.Fatalf("unexpected message: %s", res.Message)
	}

	// Create and drop with IF EXISTS — should succeed
	executeSQL(t, session, "CREATE DATABASE anotherdb;")
	res = executeSQL(t, session, "DROP DATABASE IF EXISTS anotherdb;")
	if res.Message != "Database 'anotherdb' dropped successfully." {
		t.Fatalf("unexpected message: %s", res.Message)
	}
}

func TestShowEncryptionStatus(t *testing.T) {
	session := setupSession(t)

	// Check encryption status for mydb (not encrypted)
	result := executeSQL(t, session, "SHOW ENCRYPTION STATUS;")

	// Verify columns
	if len(result.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(result.Columns))
	}
	expectedCols := []string{"database", "encrypted", "algorithm", "key_source"}
	for i, col := range expectedCols {
		if result.Columns[i] != col {
			t.Fatalf("expected column %s at index %d, got %s", col, i, result.Columns[i])
		}
	}

	// Verify single row
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}

	// Verify not encrypted
	if result.Rows[0][0] != "mydb" {
		t.Errorf("expected database=mydb, got %s", result.Rows[0][0])
	}
	if result.Rows[0][1] != "no" {
		t.Errorf("expected encrypted=no, got %s", result.Rows[0][1])
	}
	if result.Rows[0][2] != "-" {
		t.Errorf("expected algorithm=-, got %s", result.Rows[0][2])
	}
	if result.Rows[0][3] != "-" {
		t.Errorf("expected key_source=-, got %s", result.Rows[0][3])
	}
}

func TestInsertOrReplace(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);")

	// Insert initial row
	executeSQL(t, session, "INSERT INTO users VALUES (1, 'Alice');")

	// INSERT OR REPLACE should update the existing row
	result := executeSQL(t, session, "INSERT OR REPLACE INTO users VALUES (1, 'Bob');")
	if result.Affected != 1 {
		t.Errorf("expected 1 affected row, got %d", result.Affected)
	}

	// Verify the row was updated
	result = executeSQL(t, session, "SELECT name FROM users WHERE id = 1;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Bob" {
		t.Errorf("expected name='Bob', got '%s'", result.Rows[0][0])
	}
}

func TestInsertOrReplaceNewRow(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);")

	// INSERT OR REPLACE with non-existing key should insert
	result := executeSQL(t, session, "INSERT OR REPLACE INTO users VALUES (1, 'Alice');")
	if result.Affected != 1 {
		t.Errorf("expected 1 affected row, got %d", result.Affected)
	}

	// Verify the row was inserted
	result = executeSQL(t, session, "SELECT name FROM users WHERE id = 1;")
	if result.Rows[0][0] != "Alice" {
		t.Errorf("expected name='Alice', got '%s'", result.Rows[0][0])
	}
}

func TestInsertOrReplaceWithColumnList(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE users (id INT PRIMARY KEY, name TEXT, age INT);")

	// Insert initial row
	executeSQL(t, session, "INSERT INTO users VALUES (1, 'Alice', 30);")

	// INSERT OR REPLACE with column list
	result := executeSQL(t, session, "INSERT OR REPLACE INTO users (id, name) VALUES (1, 'Bob');")
	if result.Affected != 1 {
		t.Errorf("expected 1 affected row, got %d", result.Affected)
	}

	// Verify the row was updated (name changed, age should be updated to default/nil)
	result = executeSQL(t, session, "SELECT name FROM users WHERE id = 1;")
	if result.Rows[0][0] != "Bob" {
		t.Errorf("expected name='Bob', got '%s'", result.Rows[0][0])
	}
}

func TestInsertOrReplaceMultipleRows(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE testdb;")
	executeSQL(t, session, "USE testdb;")
	executeSQL(t, session, "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);")

	// Insert initial rows
	executeSQL(t, session, "INSERT INTO users VALUES (1, 'Alice');")
	executeSQL(t, session, "INSERT INTO users VALUES (2, 'Bob');")

	// INSERT OR REPLACE with multiple rows (one new, one existing)
	result := executeSQL(t, session, "INSERT OR REPLACE INTO users VALUES (1, 'Charlie'), (3, 'Dave');")
	if result.Affected != 2 {
		t.Errorf("expected 2 affected rows, got %d", result.Affected)
	}

	// Verify both rows
	result = executeSQL(t, session, "SELECT id, name FROM users ORDER BY id;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][1] != "Charlie" {
		t.Errorf("expected name='Charlie' for id=1, got '%s'", result.Rows[0][1])
	}
	if result.Rows[2][1] != "Dave" {
		t.Errorf("expected name='Dave' for id=3, got '%s'", result.Rows[2][1])
	}
}

func TestDistinctOn(t *testing.T) {
	session := setupSession(t)

	// Create table with duplicate names
	executeSQL(t, session, "CREATE TABLE people (id INT, name VARCHAR(50), city VARCHAR(50));")
	executeSQL(t, session, "INSERT INTO people VALUES (1, 'Alice', 'NYC');")
	executeSQL(t, session, "INSERT INTO people VALUES (2, 'Alice', 'LA');")
	executeSQL(t, session, "INSERT INTO people VALUES (3, 'Bob', 'NYC');")
	executeSQL(t, session, "INSERT INTO people VALUES (4, 'Bob', 'Chicago');")
	executeSQL(t, session, "INSERT INTO people VALUES (5, 'Charlie', 'NYC');")

	// DISTINCT ON (name) should return 3 rows (one per name)
	result := executeSQL(t, session, "SELECT DISTINCT ON (name) name, city FROM people ORDER BY name, id;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Alice" || result.Rows[0][1] != "NYC" {
		t.Errorf("expected (Alice, NYC), got (%s, %s)", result.Rows[0][0], result.Rows[0][1])
	}
	if result.Rows[1][0] != "Bob" || result.Rows[1][1] != "NYC" {
		t.Errorf("expected (Bob, NYC), got (%s, %s)", result.Rows[1][0], result.Rows[1][1])
	}
	if result.Rows[2][0] != "Charlie" || result.Rows[2][1] != "NYC" {
		t.Errorf("expected (Charlie, NYC), got (%s, %s)", result.Rows[2][0], result.Rows[2][1])
	}
}

func TestDistinctOnMultipleColumns(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE items (id INT, category VARCHAR(50), status VARCHAR(50));")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 'A', 'active');")
	executeSQL(t, session, "INSERT INTO items VALUES (2, 'A', 'active');")
	executeSQL(t, session, "INSERT INTO items VALUES (3, 'A', 'inactive');")
	executeSQL(t, session, "INSERT INTO items VALUES (4, 'B', 'active');")

	// DISTINCT ON (category, status) should return 3 rows
	result := executeSQL(t, session, "SELECT DISTINCT ON (category, status) category, status FROM items ORDER BY category, status;")
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result.Rows))
	}
}

func TestJSONBContainsOperatorInWhere(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE events (id INT, data JSONB);")
	executeSQL(t, session, `INSERT INTO events VALUES (1, '{"type": "click", "page": "home"}');`)
	executeSQL(t, session, `INSERT INTO events VALUES (2, '{"type": "scroll", "page": "home"}');`)
	executeSQL(t, session, `INSERT INTO events VALUES (3, '{"type": "click", "page": "about"}');`)

	// @> operator in WHERE
	result := executeSQL(t, session, `SELECT * FROM events WHERE data @> '{"type": "click"}';`)
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
}

func TestJSONBHasKeyOperatorInWhere(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE configs (id INT, settings JSONB);")
	executeSQL(t, session, `INSERT INTO configs VALUES (1, '{"theme": "dark", "lang": "en"}');`)
	executeSQL(t, session, `INSERT INTO configs VALUES (2, '{"lang": "fr"}');`)
	executeSQL(t, session, `INSERT INTO configs VALUES (3, '{"theme": "light"}');`)

	// ? operator in WHERE
	result := executeSQL(t, session, `SELECT * FROM configs WHERE settings ? 'theme';`)
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
}

func TestJSONBArrowOperators(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE docs (id INT, data JSONB);")
	executeSQL(t, session, `INSERT INTO docs VALUES (1, '{"name": "Alice", "age": 30}');`)
	executeSQL(t, session, `INSERT INTO docs VALUES (2, '{"name": "Bob", "age": 25}');`)

	// -> operator (returns JSON value)
	result := executeSQL(t, session, `SELECT data->'name' FROM docs ORDER BY id;`)
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}

	// ->> operator (returns text)
	result = executeSQL(t, session, `SELECT data->>'name' FROM docs ORDER BY id;`)
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Alice" {
		t.Errorf("expected 'Alice', got '%s'", result.Rows[0][0])
	}
	if result.Rows[1][0] != "Bob" {
		t.Errorf("expected 'Bob', got '%s'", result.Rows[1][0])
	}
}

func TestJSONBContainsInSelect(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE records (id INT, data JSONB);")
	executeSQL(t, session, `INSERT INTO records VALUES (1, '{"active": true}');`)
	executeSQL(t, session, `INSERT INTO records VALUES (2, '{"active": false}');`)

	// @> operator in SELECT expression
	result := executeSQL(t, session, `SELECT data @> '{"active": true}' FROM records ORDER BY id;`)
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "true" {
		t.Errorf("expected 'true', got '%s'", result.Rows[0][0])
	}
	if result.Rows[1][0] != "false" {
		t.Errorf("expected 'false', got '%s'", result.Rows[1][0])
	}
}

func TestJSONBHasKeyInSelect(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE records (id INT, data JSONB);")
	executeSQL(t, session, `INSERT INTO records VALUES (1, '{"name": "Alice"}');`)
	executeSQL(t, session, `INSERT INTO records VALUES (2, '{"age": 30}');`)

	// ? operator in SELECT expression
	result := executeSQL(t, session, `SELECT data ? 'name' FROM records ORDER BY id;`)
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "true" {
		t.Errorf("expected 'true', got '%s'", result.Rows[0][0])
	}
	if result.Rows[1][0] != "false" {
		t.Errorf("expected 'false', got '%s'", result.Rows[1][0])
	}
}
