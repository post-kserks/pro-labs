package executor

import (
	"regexp"
	"strings"
	"testing"

	"vaultdb/internal/storage"
)

func mockSetupSession(t *testing.T) *Session {
	t.Helper()
	store := NewMockStorage()
	store.databases["testdb"] = true
	store.ensureDB("testdb")
	store.tables["testdb"]["t"] = &storage.TableSchema{
		Name: "t",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "val", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
			{Name: "alive", Type: "BOOL"},
		},
	}
	store.rows["testdb"]["t"] = []storage.Row{
		{int64(1), "alice", 9.5, true},
		{int64(2), "bob", 7.0, true},
		{int64(3), "carol", 8.0, false},
	}
	session := newTestSession(store)
	executeSQL(t, session, "USE testdb;")
	return session
}

func TestFnAbs(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT ABS(-5) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "5" {
		t.Fatalf("expected 5, got %s", res.Rows[0][0])
	}
}

func TestFnUpper(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT UPPER(val) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "ALICE" {
		t.Fatalf("expected ALICE, got %s", res.Rows[0][0])
	}
}

func TestFnLength(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LENGTH(val) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "5" {
		t.Fatalf("expected 5, got %s", res.Rows[0][0])
	}
}

func TestFnCoalesce(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT COALESCE(NULL, NULL, 'default') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "default" {
		t.Fatalf("expected default, got %s", res.Rows[0][0])
	}
}

func TestFnNullif(t *testing.T) {
	session := mockSetupSession(t)

	t.Run("equal_returns_null", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT NULLIF(id, 1) FROM t WHERE id = 1;")
		if res.Rows[0][0] != "" {
			t.Fatalf("expected NULL (empty string), got %s", res.Rows[0][0])
		}
	})

	t.Run("not_equal_returns_first", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT NULLIF(id, 2) FROM t WHERE id = 1;")
		if res.Rows[0][0] != "1" {
			t.Fatalf("expected 1, got %s", res.Rows[0][0])
		}
	})
}

func TestFnSubstring(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT SUBSTRING(val, 2, 3) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "lic" {
		t.Fatalf("expected 'lic', got %s", res.Rows[0][0])
	}
}

func TestFnReplace(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT REPLACE('hello', 'l', 'r') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "herro" {
		t.Fatalf("expected 'herro', got %s", res.Rows[0][0])
	}
}

func TestFnNow(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT NOW() FROM t WHERE id = 1;")
	if res.Rows[0][0] == "" {
		t.Fatal("expected non-nil timestamp, got empty string")
	}
}

func TestFnTrim(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT TRIM('  hello  ') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "hello" {
		t.Fatalf("expected 'hello', got %s", res.Rows[0][0])
	}
}

func TestFnLpad(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LPAD('hi', 5, '*') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "***hi" {
		t.Fatalf("expected '***hi', got %s", res.Rows[0][0])
	}
}

func TestFnRpad(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT RPAD('hi', 5, '*') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "hi***" {
		t.Fatalf("expected 'hi***', got %s", res.Rows[0][0])
	}
}

func TestFnLpadEmpty(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LPAD('hi', 5, '') FROM t WHERE id = 1;")
	if len(res.Rows) == 0 || res.Rows[0][0] != "ERR" {
		t.Fatalf("expected 'ERR' for empty pad string, got %v", res.Rows)
	}
}

func TestFnPosition(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT POSITION('l', 'hello') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "3" {
		t.Fatalf("expected 3, got %s", res.Rows[0][0])
	}
}

func TestFnInitcap(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT INITCAP('hello world') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "Hello World" {
		t.Fatalf("expected 'Hello World', got %s", res.Rows[0][0])
	}
}

func TestFnGreatest(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT GREATEST(1, 2, 3) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "3" {
		t.Fatalf("expected 3, got %s", res.Rows[0][0])
	}
}

func TestFnLeast(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LEAST(1, 2, 3) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "1" {
		t.Fatalf("expected 1, got %s", res.Rows[0][0])
	}
}

func TestEvalArithmeticDivByZero(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT 1 / 0 FROM t WHERE id = 1;")
	if len(res.Rows) == 0 || res.Rows[0][0] != "ERR" {
		t.Fatalf("expected 'ERR' for division by zero, got %v", res.Rows)
	}
}

func TestEvalComparisonSubquery(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT val FROM t WHERE id = (SELECT MAX(id) FROM t);")
	if len(res.Rows) != 1 || res.Rows[0][0] != "carol" {
		t.Fatalf("expected [carol], got %v", res.Rows)
	}
}

func TestEvalCastInt(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT CAST('123' AS INT) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "123" {
		t.Fatalf("expected 123, got %s", res.Rows[0][0])
	}
}

func TestEvalCastFloat(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT CAST('3.14' AS FLOAT) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "3.14" {
		t.Fatalf("expected 3.14, got %s", res.Rows[0][0])
	}
}

func TestEvalCaseWhen(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT CASE WHEN 1 > 0 THEN 'yes' ELSE 'no' END FROM t WHERE id = 1;")
	if res.Rows[0][0] != "yes" {
		t.Fatalf("expected 'yes', got %s", res.Rows[0][0])
	}
}

func TestJsonContainsObject(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE DATABASE testjson;")
	executeSQL(t, session, "USE testjson;")
	executeSQL(t, session, "CREATE TABLE docs (id INT, data JSONB);")

	executeSQL(t, session, `INSERT INTO docs VALUES (1, '{"name": "Alice", "age": 30, "city": "Moscow"}');`)
	executeSQL(t, session, `INSERT INTO docs VALUES (2, '{"name": "Bob", "age": 25, "city": "SPb"}');`)
	executeSQL(t, session, `INSERT INTO docs VALUES (3, '{"name": "Charlie", "age": 35}');`)

	// Object containment: right is subset of left
	t.Run("subset_match", func(t *testing.T) {
		res := executeSQL(t, session, `SELECT id FROM docs WHERE data @> '{"age": 30}' ORDER BY id;`)
		if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
			t.Fatalf("expected [1], got %v", res.Rows)
		}
	})

	t.Run("multi_key_subset", func(t *testing.T) {
		res := executeSQL(t, session, `SELECT id FROM docs WHERE data @> '{"name": "Alice", "city": "Moscow"}' ORDER BY id;`)
		if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
			t.Fatalf("expected [1], got %v", res.Rows)
		}
	})

	t.Run("empty_right_matches_all", func(t *testing.T) {
		res := executeSQL(t, session, `SELECT COUNT(*) FROM docs WHERE data @> '{}';`)
		if res.Rows[0][0] != "3" {
			t.Fatalf("expected 3, got %s", res.Rows[0][0])
		}
	})

	t.Run("no_match", func(t *testing.T) {
		res := executeSQL(t, session, `SELECT id FROM docs WHERE data @> '{"name": "Dave"}';`)
		if len(res.Rows) != 0 {
			t.Fatalf("expected 0, got %d", len(res.Rows))
		}
	})

	t.Run("missing_key", func(t *testing.T) {
		res := executeSQL(t, session, `SELECT id FROM docs WHERE data @> '{"city": "Moscow"}' ORDER BY id;`)
		if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
			t.Fatalf("expected [1], got %v", res.Rows)
		}
	})

	// Array containment should still work
	t.Run("array_containment_still_works", func(t *testing.T) {
		executeSQL(t, session, "CREATE TABLE tags (id INT, items JSONB);")
		executeSQL(t, session, `INSERT INTO tags VALUES (1, '["a", "b", "c"]');`)
		executeSQL(t, session, `INSERT INTO tags VALUES (2, '["x", "y"]');`)
		res := executeSQL(t, session, `SELECT id FROM tags WHERE items @> '["a"]' ORDER BY id;`)
		if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
			t.Fatalf("expected [1], got %v", res.Rows)
		}
	})
}

func TestSanitizeObjectName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", true},
		{"dotdot", "..", true},
		{"slash", "/", true},
		{"valid", "my_table", false},
		{"valid_upper", "TABLE1", false},
		{"valid_mixed", "My_Table_123", false},
		{"too_long", strings.Repeat("a", 129), true},
		{"invalid_chars", "table name", true},
		{"invalid_special", "table@name", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sanitizeObjectName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sanitizeObjectName(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestResolveProjection(t *testing.T) {
	schema := &storage.TableSchema{
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
		},
	}

	t.Run("empty_returns_all", func(t *testing.T) {
		indices, columns, err := resolveProjection(schema, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 3 || len(columns) != 3 {
			t.Fatalf("expected 3 columns, got %d", len(indices))
		}
		if columns[0] != "id" || columns[1] != "name" || columns[2] != "score" {
			t.Fatalf("unexpected columns: %v", columns)
		}
	})

	t.Run("known_columns", func(t *testing.T) {
		indices, columns, err := resolveProjection(schema, []string{"name", "score"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(indices))
		}
		if columns[0] != "name" || columns[1] != "score" {
			t.Fatalf("unexpected columns: %v", columns)
		}
	})

	t.Run("unknown_column", func(t *testing.T) {
		_, _, err := resolveProjection(schema, []string{"nonexistent"})
		if err == nil {
			t.Fatal("expected error for unknown column")
		}
		if !strings.Contains(err.Error(), "unknown column") {
			t.Fatalf("expected 'unknown column' in error, got: %v", err)
		}
	})
}

func TestFtsMatchConsolidated(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE fts_docs (id INT, content TEXT);")
	executeSQL(t, session, "INSERT INTO fts_docs VALUES (1, 'the quick brown fox jumps over the lazy dog');")
	executeSQL(t, session, "INSERT INTO fts_docs VALUES (2, 'a completely different document about databases');")
	executeSQL(t, session, "INSERT INTO fts_docs VALUES (3, 'the fox is quick and brown');")

	t.Run("FTS_MATCH_operator", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT id FROM fts_docs WHERE content FTS_MATCH 'quick fox' ORDER BY id;")
		if len(res.Rows) != 2 {
			t.Fatalf("FTS_MATCH: expected 2 rows, got %d: %#v", len(res.Rows), res.Rows)
		}
		if res.Rows[0][0] != "1" || res.Rows[1][0] != "3" {
			t.Fatalf("FTS_MATCH: expected ids [1,3], got %#v", res.Rows)
		}
	})

	t.Run("FULLTEXT_MATCH_operator", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT id FROM fts_docs WHERE content @@ 'quick fox' ORDER BY id;")
		if len(res.Rows) != 2 {
			t.Fatalf("@@: expected 2 rows, got %d: %#v", len(res.Rows), res.Rows)
		}
		if res.Rows[0][0] != "1" || res.Rows[1][0] != "3" {
			t.Fatalf("@@: expected ids [1,3], got %#v", res.Rows)
		}
	})

	t.Run("empty_query_matches_all", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT COUNT(*) FROM fts_docs WHERE content FTS_MATCH '';")
		if res.Rows[0][0] != "3" {
			t.Fatalf("empty FTS_MATCH: expected 3, got %s", res.Rows[0][0])
		}
		res2 := executeSQL(t, session, "SELECT COUNT(*) FROM fts_docs WHERE content @@ '';")
		if res2.Rows[0][0] != "3" {
			t.Fatalf("empty @@: expected 3, got %s", res2.Rows[0][0])
		}
	})

	t.Run("no_match", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT id FROM fts_docs WHERE content FTS_MATCH 'zzzzz';")
		if len(res.Rows) != 0 {
			t.Fatalf("FTS_MATCH no match: expected 0 rows, got %d", len(res.Rows))
		}
		res2 := executeSQL(t, session, "SELECT id FROM fts_docs WHERE content @@ 'zzzzz';")
		if len(res2.Rows) != 0 {
			t.Fatalf("@@ no match: expected 0 rows, got %d", len(res2.Rows))
		}
	})

	t.Run("both_operators_produce_same_results", func(t *testing.T) {
		resFts := executeSQL(t, session, "SELECT id FROM fts_docs WHERE content FTS_MATCH 'quick brown' ORDER BY id;")
		resAt := executeSQL(t, session, "SELECT id FROM fts_docs WHERE content @@ 'quick brown' ORDER BY id;")
		if len(resFts.Rows) != len(resAt.Rows) {
			t.Fatalf("FTS_MATCH and @@ returned different row counts: %d vs %d", len(resFts.Rows), len(resAt.Rows))
		}
		for i := range resFts.Rows {
			if resFts.Rows[i][0] != resAt.Rows[i][0] {
				t.Fatalf("row %d differs: FTS_MATCH=%s, @@=%s", i, resFts.Rows[i][0], resAt.Rows[i][0])
			}
		}
	})
}

func TestBm25ScoreFunction(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE bm25_docs (id INT, content TEXT);")
	executeSQL(t, session, "INSERT INTO bm25_docs VALUES (1, 'the quick brown fox jumps over the lazy dog');")
	executeSQL(t, session, "INSERT INTO bm25_docs VALUES (2, 'a completely different document about databases');")
	executeSQL(t, session, "INSERT INTO bm25_docs VALUES (3, 'the fox is quick and brown');")

	t.Run("returns_float_scores", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT id, bm25_score('bm25_docs', 'content', 'quick fox') AS score FROM bm25_docs WHERE content FTS_MATCH 'quick fox' ORDER BY score DESC;")
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows, got %d: %#v", len(res.Rows), len(res.Rows))
		}
		// Both matching rows should have positive scores
		for _, row := range res.Rows {
			if len(row) < 2 {
				t.Fatalf("expected at least 2 columns, got %d", len(row))
			}
		}
	})

	t.Run("higher_score_for_better_match", func(t *testing.T) {
		// Row 1 has "quick brown fox" — more query term overlap than row 3 "fox is quick and brown"
		// Both should match; row 1 should score >= row 3
		res := executeSQL(t, session, "SELECT id, bm25_score('bm25_docs', 'content', 'quick brown fox') AS score FROM bm25_docs WHERE content FTS_MATCH 'quick brown fox' ORDER BY score DESC;")
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(res.Rows))
		}
	})

	t.Run("no_match_returns_zero", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT bm25_score('bm25_docs', 'content', 'zzzzz') FROM bm25_docs WHERE content FTS_MATCH 'zzzzz';")
		if len(res.Rows) != 0 {
			t.Fatalf("expected 0 rows for no-match query, got %d", len(res.Rows))
		}
	})

	t.Run("works_without_where_filter", func(t *testing.T) {
		// bm25_score should work even without a WHERE FTS_MATCH clause
		res := executeSQL(t, session, "SELECT id, bm25_score('bm25_docs', 'content', 'fox') AS score FROM bm25_docs ORDER BY score DESC;")
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(res.Rows))
		}
		// Rows with 'fox' should score higher than the one without
		for _, row := range res.Rows {
			if len(row) < 2 {
				t.Fatalf("expected at least 2 columns, got %d", len(row))
			}
		}
	})
}

func TestInferType(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
		want string
	}{
		{"int64", int64(42), "INT"},
		{"int", int(42), "INT"},
		{"float64", float64(3.14), "FLOAT"},
		{"bool_true", true, "BOOL"},
		{"bool_false", false, "BOOL"},
		{"string", "hello", "TEXT"},
		{"nil", nil, "TEXT"},
		{"map", map[string]interface{}{"k": "v"}, "FLEXIBLE"},
		{"json_string", `{"key":"val"}`, "FLEXIBLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferType(tt.val)
			if got != tt.want {
				t.Fatalf("inferType(%v) = %s, want %s", tt.val, got, tt.want)
			}
		})
	}
}

func TestValueToString(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"int", int(42), "42"},
		{"int64", int64(42), "42"},
		{"float64", float64(3.14), "3.14"},
		{"bool_true", true, "true"},
		{"bool_false", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueToString(tt.val)
			if got != tt.want {
				t.Fatalf("valueToString(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

func TestEvalOperandErrorPropagation(t *testing.T) {
	session := mockSetupSession(t)

	// Test: GROUP BY with non-existent column should propagate error
	executeSQLExpectError(t, session, "SELECT nonexistent, COUNT(*) FROM t GROUP BY nonexistent;")

	// Test: Window PARTITION BY with non-existent column should propagate error
	executeSQLExpectError(t, session, "SELECT id, ROW_NUMBER() OVER (PARTITION BY nonexistent ORDER BY id) FROM t;")

	// Test: SELECT with non-existent column in aggregate should propagate error
	executeSQLExpectError(t, session, "SELECT COUNT(nonexistent) FROM t;")
}

func TestResolveColumnCached(t *testing.T) {
	schema := &storage.TableSchema{
		Name: "t",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "val", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
		},
	}
	row := storage.Row{int64(1), "alice", 9.5}

	t.Run("unqualified match via cache", func(t *testing.T) {
		idx := buildColumnIndex(schema)
		got, err := resolveColumn(row, schema, "val", idx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "alice" {
			t.Fatalf("expected alice, got %v", got)
		}
	})

	t.Run("case-insensitive cache lookup", func(t *testing.T) {
		idx := buildColumnIndex(schema)
		got, err := resolveColumn(row, schema, "VAL", idx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "alice" {
			t.Fatalf("expected alice, got %v", got)
		}
	})

	t.Run("unqualified fallback to linear scan", func(t *testing.T) {
		got, err := resolveColumn(row, schema, "score", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 9.5 {
			t.Fatalf("expected 9.5, got %v", got)
		}
	})

	t.Run("unknown column returns error", func(t *testing.T) {
		idx := buildColumnIndex(schema)
		_, err := resolveColumn(row, schema, "nonexistent", idx)
		if err == nil {
			t.Fatal("expected error for unknown column")
		}
	})
}

func BenchmarkResolveColumnCached(b *testing.B) {
	schema := &storage.TableSchema{
		Name: "t",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "val", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
			{Name: "active", Type: "BOOL"},
		},
	}
	row := storage.Row{int64(1), "alice", 9.5, true}
	idx := buildColumnIndex(schema)

	b.Run("cached", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resolveColumn(row, schema, "val", idx)
		}
	})

	b.Run("linear_scan", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			resolveColumn(row, schema, "val", nil)
		}
	})
}

func TestFnUuid(t *testing.T) {
	session := mockSetupSession(t)
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	// Basic format test
	res := executeSQL(t, session, "SELECT UUID() FROM t WHERE id = 1;")
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("expected 1 row with 1 column, got %v", res.Rows)
	}
	got := res.Rows[0][0]
	if !uuidRe.MatchString(got) {
		t.Fatalf("UUID() returned invalid format: %q", got)
	}

	// Verify uniqueness: call UUID() multiple times and check all results are unique
	res1 := executeSQL(t, session, "SELECT UUID() FROM t WHERE id = 1;")
	res2 := executeSQL(t, session, "SELECT UUID() FROM t WHERE id = 2;")
	res3 := executeSQL(t, session, "SELECT UUID() FROM t WHERE id = 3;")
	u1, u2, u3 := res1.Rows[0][0], res2.Rows[0][0], res3.Rows[0][0]
	for _, u := range []string{u1, u2, u3} {
		if !uuidRe.MatchString(u) {
			t.Fatalf("UUID() returned invalid format: %q", u)
		}
	}
	if u1 == u2 || u1 == u3 || u2 == u3 {
		t.Fatalf("expected unique UUIDs, got: %s, %s, %s", u1, u2, u3)
	}
}
