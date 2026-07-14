package executor

import (
	"fmt"
	"testing"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

// TestJoinBufferReuse verifies correctness of buffer-reused join paths
// using the established test infrastructure that all existing tests rely on.
func TestJoinBufferReuse(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)
	executeSQL(t, session, "CREATE TABLE weapons2 (hero_id INT, dmg INT);")
	for i := 1; i <= 5; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO weapons2 VALUES (%d, %d);", i, i*10))
	}

	// INNER JOIN: heroes 1-4 match weapons 1-4 → 4 rows
	res := executeSQL(t, session,
		"SELECT COUNT(*) FROM heroes INNER JOIN weapons2 ON heroes.id = weapons2.hero_id;")
	var cnt int
	fmt.Sscanf(res.Rows[0][0], "%d", &cnt)
	if cnt != 4 {
		t.Errorf("INNER JOIN count: expected 4, got %d", cnt)
	}

	// LEFT JOIN: all 4 heroes remain → 4 rows
	res = executeSQL(t, session,
		"SELECT COUNT(*) FROM heroes LEFT JOIN weapons2 ON heroes.id = weapons2.hero_id;")
	fmt.Sscanf(res.Rows[0][0], "%d", &cnt)
	if cnt != 4 {
		t.Errorf("LEFT JOIN count: expected 4, got %d", cnt)
	}

	// Verify row content — specific joined pair must be correct
	res = executeSQL(t, session,
		"SELECT heroes.name, weapons2.dmg FROM heroes JOIN weapons2 ON heroes.id = weapons2.hero_id ORDER BY heroes.id;")
	if len(res.Rows) != 4 {
		t.Fatalf("INNER content: expected 4 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "Aragorn" || res.Rows[0][1] != "10" {
		t.Errorf("unexpected first row: %v", res.Rows[0])
	}
	if res.Rows[1][0] != "Legolas" || res.Rows[1][1] != "20" {
		t.Errorf("unexpected second row: %v", res.Rows[1])
	}
}

// BenchmarkJoinBufferReuse benchmarks INNER join with condition filtering
// where buffer reuse eliminates allocations for non-matching cross-product rows.
func BenchmarkJoinBufferReuse(b *testing.B) {
	session := setupBenchSession(b)
	executeSQLBench(b, session, "CREATE TABLE bjl (id INT, v TEXT);")
	executeSQLBench(b, session, "CREATE TABLE bjr (id INT, v TEXT);")
	for i := 0; i < 200; i++ {
		executeSQLBench(b, session, fmt.Sprintf("INSERT INTO bjl VALUES (%d, 'a%d');", i, i))
	}
	// Sparse: only 10% of left ids match right
	for i := 0; i < 20; i++ {
		executeSQLBench(b, session, fmt.Sprintf("INSERT INTO bjr VALUES (%d, 'b%d');", i*10, i*10))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stmt, _ := parser.Parse("SELECT COUNT(*) FROM bjl JOIN bjr ON bjl.id = bjr.id;")
		session.Execute(stmt)
	}
}

func setupBenchSession(b *testing.B) *Session {
	b.Helper()
	dir := b.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)
	executeSQLBench(b, session, "CREATE DATABASE benchdb;")
	executeSQLBench(b, session, "USE benchdb;")
	return session
}

func executeSQLBench(b testing.TB, session *Session, sql string) *Result {
	b.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		b.Fatalf("Parse failed for %q: %v", sql, err)
	}
	result, err := session.Execute(stmt)
	if err != nil {
		b.Fatalf("Execute failed for %q: %v", sql, err)
	}
	return result
}
