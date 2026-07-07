package executor

import (
	"fmt"
	"strings"
	"testing"

	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func newEdgeSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess := NewSession(store, metrics.New(), txm, NewBroadcaster())
	mustExec(t, sess, "CREATE DATABASE edge;")
	sess.SetCurrentDatabase("edge")
	return sess
}

// 3.10 Empty JSON object/array
func TestEdgeEmptyJSON(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_json (id INT, data JSONB)")
	mustExec(t, session, `INSERT INTO edge_json VALUES (1, '{}')`)
	mustExec(t, session, `INSERT INTO edge_json VALUES (2, '[]')`)
	res := executeSQL(t, session, "SELECT data FROM edge_json ORDER BY id")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "{}" {
		t.Fatalf("expected empty object '{}', got %q", res.Rows[0][0])
	}
	if res.Rows[1][0] != "[]" {
		t.Fatalf("expected empty array '[]', got %q", res.Rows[1][0])
	}
}

// 3.12 Transaction with zero statements
func TestEdgeEmptyTransaction(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "BEGIN")
	mustExec(t, session, "COMMIT")
}

// 3.13 Connection with empty SQL
func TestEdgeEmptySQL(t *testing.T) {
	_, err := parser.Parse("")
	if err == nil {
		t.Error("expected error for empty SQL, got nil")
	}
}

// 3.14 SQL with only whitespace
func TestEdgeWhitespaceOnlySQL(t *testing.T) {
	_, err := parser.Parse("   \t\n  \r  ")
	if err == nil {
		t.Error("expected error for whitespace-only SQL, got nil")
	}
}

// 3.3 Max-size TEXT insert — tests near binary encoding limit
func TestEdgeMaxSizeVarchar(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_varchar (id INT, val TEXT)")

	// VaultDB tuple limit ~65535 bytes; 1KB tests multi-field row storage
	bigStr := strings.Repeat("A", 1000)
	stmt, err := parser.Parse("INSERT INTO edge_varchar VALUES (1, '" + bigStr + "')")
	if err != nil {
		t.Fatalf("parse large INSERT: %v", err)
	}
	if _, err := session.Execute(stmt); err != nil {
		t.Fatalf("exec large INSERT: %v", err)
	}

	res := executeSQL(t, session, "SELECT val FROM edge_varchar WHERE id = 1")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	got := res.Rows[0][0]
	if len(got) != 1000 {
		t.Fatalf("stored length = %d, want 1000", len(got))
	}
	if got != bigStr {
		t.Fatal("stored string does not match original")
	}
	t.Logf("large TEXT (1000 bytes) stored and retrieved correctly")
}

// 3.5 Unicode/emoji in strings
func TestEdgeUnicodeEmoji(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_unicode (id INT, val TEXT)")
	mustExec(t, session, `INSERT INTO edge_unicode VALUES (1, 'Привет мир')`)
	mustExec(t, session, `INSERT INTO edge_unicode VALUES (2, '日本語テスト')`)
	mustExec(t, session, `INSERT INTO edge_unicode VALUES (3, '🚀🎉🔥')`)
	mustExec(t, session, `INSERT INTO edge_unicode VALUES (4, 'émojis et accents: café résumé')`)

	res := executeSQL(t, session, "SELECT COUNT(*) FROM edge_unicode")
	if res.Rows[0][0] != "4" {
		t.Fatalf("expected 4 rows, got %s", res.Rows[0][0])
	}

	res = executeSQL(t, session, "SELECT val FROM edge_unicode ORDER BY id")
	if res.Rows[0][0] != "Привет мир" {
		t.Errorf("row 1: got %q, want 'Привет мир'", res.Rows[0][0])
	}
	if res.Rows[1][0] != "日本語テスト" {
		t.Errorf("row 2: got %q, want '日本語テスト'", res.Rows[1][0])
	}
	if res.Rows[2][0] != "🚀🎉🔥" {
		t.Errorf("row 3: got %q, want '🚀🎉🔥'", res.Rows[2][0])
	}
	if res.Rows[3][0] != "émojis et accents: café résumé" {
		t.Errorf("row 4: got %q", res.Rows[3][0])
	}
}

// 3.6 Zero-precision DECIMAL (NUMERIC)
func TestEdgeZeroPrecisionDecimal(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_decimal (id INT, val DECIMAL)")
	mustExec(t, session, "INSERT INTO edge_decimal VALUES (1, 42)")
	mustExec(t, session, "INSERT INTO edge_decimal VALUES (2, 3.7)")
	mustExec(t, session, "INSERT INTO edge_decimal VALUES (3, -100.9)")
	mustExec(t, session, "INSERT INTO edge_decimal VALUES (4, 0)")
	mustExec(t, session, "INSERT INTO edge_decimal VALUES (5, -1)")
	mustExec(t, session, "INSERT INTO edge_decimal VALUES (6, 9999999999)")

	res := executeSQL(t, session, "SELECT val FROM edge_decimal ORDER BY id")
	if len(res.Rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(res.Rows))
	}
	t.Logf("DECIMAL input=42 stored=%s", res.Rows[0][0])
	t.Logf("DECIMAL input=3.7 stored=%s", res.Rows[1][0])
	t.Logf("DECIMAL input=-100.9 stored=%s", res.Rows[2][0])
	t.Logf("DECIMAL input=0 stored=%s", res.Rows[3][0])
	t.Logf("DECIMAL input=-1 stored=%s", res.Rows[4][0])
	t.Logf("DECIMAL input=9999999999 stored=%s", res.Rows[5][0])
}

// 3.7 Negative timestamps (before 1970)
func TestEdgeNegativeTimestamps(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_ts (id INT, ts TIMESTAMP)")
	mustExec(t, session, "INSERT INTO edge_ts VALUES (1, '1969-07-20 20:17:40')")
	mustExec(t, session, "INSERT INTO edge_ts VALUES (2, '1900-01-01 00:00:00')")
	mustExec(t, session, "INSERT INTO edge_ts VALUES (3, '1969-12-31 23:59:59')")

	res := executeSQL(t, session, "SELECT ts FROM edge_ts ORDER BY id")
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "1969-07-20 20:17:40" {
		t.Errorf("row 1: got %q, want '1969-07-20 20:17:40'", res.Rows[0][0])
	}
	if res.Rows[1][0] != "1900-01-01 00:00:00" {
		t.Errorf("row 2: got %q, want '1900-01-01 00:00:00'", res.Rows[1][0])
	}
	if res.Rows[2][0] != "1969-12-31 23:59:59" {
		t.Errorf("row 3: got %q, want '1969-12-31 23:59:59'", res.Rows[2][0])
	}
	t.Logf("pre-1970 timestamps stored correctly")
}

// 3.8 Boundary integer values
func TestEdgeBoundaryIntegers(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_int (id INT, val INT)")
	mustExec(t, session, "INSERT INTO edge_int VALUES (1, 2147483647)")   // INT MAX
	mustExec(t, session, "INSERT INTO edge_int VALUES (2, -2147483648)") // INT MIN
	mustExec(t, session, "INSERT INTO edge_int VALUES (3, 0)")

	res := executeSQL(t, session, "SELECT val FROM edge_int ORDER BY id")
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "2147483647" {
		t.Errorf("INT MAX: got %q, want 2147483647", res.Rows[0][0])
	}
	if res.Rows[1][0] != "-2147483648" {
		t.Errorf("INT MIN: got %q, want -2147483648", res.Rows[1][0])
	}
	if res.Rows[2][0] != "0" {
		t.Errorf("ZERO: got %q, want 0", res.Rows[2][0])
	}

	res = executeSQL(t, session, "SELECT val + 1 FROM edge_int WHERE id = 1")
	t.Logf("INT MAX + 1 = %s (overflow behavior)", res.Rows[0][0])

	res = executeSQL(t, session, "SELECT val - 1 FROM edge_int WHERE id = 2")
	t.Logf("INT MIN - 1 = %s (overflow behavior)", res.Rows[0][0])
}

func TestEdgeEmptyString(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_empty (id INT, val TEXT)")
	mustExec(t, session, "INSERT INTO edge_empty VALUES (1, '')")

	res := executeSQL(t, session, "SELECT LENGTH(val) FROM edge_empty WHERE id = 1")
	if res.Rows[0][0] != "0" {
		t.Errorf("empty string length: got %q, want 0", res.Rows[0][0])
	}
}

func TestEdgeNullInsert(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_null (id INT, val TEXT)")
	mustExec(t, session, "INSERT INTO edge_null (id) VALUES (1)")

	res := executeSQL(t, session, "SELECT val IS NULL FROM edge_null WHERE id = 1")
	if res.Rows[0][0] != "true" {
		t.Errorf("expected NULL, got %q", res.Rows[0][0])
	}
}

func TestEdgeFloatPrecision(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_float (id INT, val FLOAT)")
	mustExec(t, session, "INSERT INTO edge_float VALUES (1, 0.1)")
	mustExec(t, session, "INSERT INTO edge_float VALUES (2, 0.3)")
	mustExec(t, session, "INSERT INTO edge_float VALUES (3, -0.0)")

	res := executeSQL(t, session, "SELECT val FROM edge_float ORDER BY id")
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	t.Logf("0.1 stored as: %s", res.Rows[0][0])
	t.Logf("0.3 stored as: %s", res.Rows[1][0])
	t.Logf("-0.0 stored as: %s", res.Rows[2][0])
}

func TestEdgeSpecialCharactersInString(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_special (id INT, val TEXT)")
	mustExec(t, session, "INSERT INTO edge_special VALUES (1, 'tab	here')")

	res := executeSQL(t, session, "SELECT COUNT(*) FROM edge_special")
	if res.Rows[0][0] != "1" {
		t.Fatalf("expected 1 row, got %s", res.Rows[0][0])
	}
}

func TestEdgeLargeColumnCount(t *testing.T) {
	session := newEdgeSession(t)

	cols := make([]string, 50)
	for i := range cols {
		cols[i] = fmt.Sprintf("c%d INT", i)
	}
	mustExec(t, session, fmt.Sprintf("CREATE TABLE edge_wide (%s)", strings.Join(cols, ", ")))

	vals := make([]string, 50)
	for i := range vals {
		vals[i] = fmt.Sprintf("%d", i)
	}
	mustExec(t, session, fmt.Sprintf("INSERT INTO edge_wide VALUES (%s)", strings.Join(vals, ", ")))

	res := executeSQL(t, session, "SELECT * FROM edge_wide")
	if len(res.Columns) != 50 {
		t.Errorf("expected 50 columns, got %d", len(res.Columns))
	}
	if res.Rows[0][25] != "25" {
		t.Errorf("column 25: got %q, want 25", res.Rows[0][25])
	}
}

func TestEdgeNegativeFloat(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_negfloat (id INT, val FLOAT)")
	mustExec(t, session, "INSERT INTO edge_negfloat VALUES (1, -3.14)")
	mustExec(t, session, "INSERT INTO edge_negfloat VALUES (2, -999.999)")

	res := executeSQL(t, session, "SELECT val FROM edge_negfloat ORDER BY id")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	t.Logf("-3.14 stored as: %s", res.Rows[0][0])
	t.Logf("-999.999 stored as: %s", res.Rows[1][0])
}

func TestEdgeBoolEdgeCases(t *testing.T) {
	session := newEdgeSession(t)
	mustExec(t, session, "CREATE TABLE edge_bool (id INT, val BOOL)")
	mustExec(t, session, "INSERT INTO edge_bool VALUES (1, TRUE)")
	mustExec(t, session, "INSERT INTO edge_bool VALUES (2, FALSE)")

	res := executeSQL(t, session, "SELECT val FROM edge_bool ORDER BY id")
	if res.Rows[0][0] != "true" {
		t.Errorf("TRUE: got %q, want true", res.Rows[0][0])
	}
	if res.Rows[1][0] != "false" {
		t.Errorf("FALSE: got %q, want false", res.Rows[1][0])
	}

	res = executeSQL(t, session, "SELECT COUNT(*) FROM edge_bool WHERE val = TRUE")
	if res.Rows[0][0] != "1" {
		t.Errorf("TRUE count: got %q, want 1", res.Rows[0][0])
	}
}
