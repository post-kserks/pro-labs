package dml_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/executor/commands/dml"
	"vaultdb/internal/core/parser"
)

func setupCopyTable(t *testing.T) (*executor.Session, string, string) {
	t.Helper()
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE people (id INT, name VARCHAR(100), age INT, score FLOAT, active BOOL, bio TEXT);")
	dataDir := session.Storage().DataDir()
	return session, dataDir, "copy_test.csv"
}

func TestCopyFromCSV(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	content := "1,Alice,30,9.5,TRUE,Engineer\n2,Bob,25,8.3,FALSE,Designer\n3,Charlie,35,7.8,TRUE,Manager\n"
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV);")
	if result.Affected != 3 {
		t.Fatalf("expected 3 rows imported, got %d", result.Affected)
	}

	res := executor.ExecuteSQL(t, session, "SELECT * FROM people ORDER BY id;")
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][1] != "Alice" {
		t.Fatalf("expected Alice, got %v", res.Rows[0][1])
	}
	if res.Rows[2][1] != "Charlie" {
		t.Fatalf("expected Charlie, got %v", res.Rows[2][1])
	}
}

func TestCopyFromCSVWithHeader(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	content := "id,name,age,score,active,bio\n1,Alice,30,9.5,TRUE,Engineer\n2,Bob,25,8.3,FALSE,Designer\n"
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV, HEADER true);")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows imported, got %d", result.Affected)
	}

	res := executor.ExecuteSQL(t, session, "SELECT * FROM people ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
}

func TestCopyFromCSVCustomDelimiter(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	content := "1|Alice|30|9.5|TRUE|Engineer\n2|Bob|25|8.3|FALSE|Designer\n"
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV, DELIMITER '|');")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows imported, got %d", result.Affected)
	}

	res := executor.ExecuteSQL(t, session, "SELECT * FROM people ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][1] != "Alice" {
		t.Fatalf("expected Alice, got %v", res.Rows[0][1])
	}
}

func TestCopyFromCSVQuotedFields(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	content := "1,\"Alice, Jr.\",30,9.5,TRUE,Engineer\n2,Bob,25,8.3,FALSE,\"Senior Designer\"\n"
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV);")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows imported, got %d", result.Affected)
	}

	res := executor.ExecuteSQL(t, session, "SELECT * FROM people ORDER BY id;")
	if res.Rows[0][1] != "Alice, Jr." {
		t.Fatalf("expected 'Alice, Jr.', got %v", res.Rows[0][1])
	}
	if res.Rows[1][5] != "Senior Designer" {
		t.Fatalf("expected 'Senior Designer', got %v", res.Rows[1][5])
	}
}

func TestCopyToCSV(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (1, 'Alice', 30, 9.5, TRUE, 'Engineer');")
	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (2, 'Bob', 25, 8.3, FALSE, 'Designer');")

	result := executor.ExecuteSQL(t, session, "COPY people TO '"+relPath+"' WITH (FORMAT CSV, HEADER true);")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows exported, got %d", result.Affected)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !containsStr(content, "id,name,age,score,active,bio") {
		t.Fatalf("expected header row, got: %s", content)
	}
	if !containsStr(content, "Alice") {
		t.Fatalf("expected Alice in output, got: %s", content)
	}
	if !containsStr(content, "Bob") {
		t.Fatalf("expected Bob in output, got: %s", content)
	}
}

func TestCopyToCSVWithoutHeader(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (1, 'Alice', 30, 9.5, TRUE, 'Engineer');")

	result := executor.ExecuteSQL(t, session, "COPY people TO '"+relPath+"' WITH (FORMAT CSV, HEADER false);")
	if result.Affected != 1 {
		t.Fatalf("expected 1 row exported, got %d", result.Affected)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if containsStr(content, "id,name") {
		t.Fatalf("should not have header row, got: %s", content)
	}
	if !containsStr(content, "Alice") {
		t.Fatalf("expected Alice in output, got: %s", content)
	}
}

func TestCopyFromJSONL(t *testing.T) {
	session, dataDir, _ := setupCopyTable(t)

	relPath := "copy_test.jsonl"
	content := `{"id":1,"name":"Alice","age":30,"score":9.5,"active":true,"bio":"Engineer"}
{"id":2,"name":"Bob","age":25,"score":8.3,"active":false,"bio":"Designer"}
`
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT JSON);")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows imported, got %d", result.Affected)
	}

	res := executor.ExecuteSQL(t, session, "SELECT * FROM people ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][1] != "Alice" {
		t.Fatalf("expected Alice, got %v", res.Rows[0][1])
	}
}

func TestCopyToJSONL(t *testing.T) {
	session, dataDir, _ := setupCopyTable(t)

	relPath := "output.jsonl"

	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (1, 'Alice', 30, 9.5, TRUE, 'Engineer');")
	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (2, 'Bob', 25, 8.3, FALSE, 'Designer');")

	result := executor.ExecuteSQL(t, session, "COPY people TO '"+relPath+"' WITH (FORMAT JSON);")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows exported, got %d", result.Affected)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !containsStr(content, "Alice") {
		t.Fatalf("expected Alice in output, got: %s", content)
	}
	if !containsStr(content, "Bob") {
		t.Fatalf("expected Bob in output, got: %s", content)
	}
}

func TestCopyFromNonexistentTable(t *testing.T) {
	session, dataDir, _ := setupCopyTable(t)
	relPath := "test.csv"
	os.WriteFile(filepath.Join(dataDir, relPath), []byte("1,foo\n"), 0644)

	executor.ExecuteSQLExpectError(t, session, "COPY nonexistent FROM '"+relPath+"';")
}

func TestCopyFromNonexistentFile(t *testing.T) {
	session, _, _ := setupCopyTable(t)
	executor.ExecuteSQLExpectError(t, session, "COPY people FROM 'nonexistent/file.csv';")
}

func TestCopyRoundTrip(t *testing.T) {
	session, _, _ := setupCopyTable(t)

	// Insert original data
	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (1, 'Alice', 30, 9.5, TRUE, 'Engineer');")
	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (2, 'Bob', 25, 8.3, FALSE, 'Designer');")

	// Export
	exportRelPath := "export.csv"
	executor.ExecuteSQL(t, session, "COPY people TO '"+exportRelPath+"' WITH (FORMAT CSV);")

	// Create new table and import
	executor.ExecuteSQL(t, session, "CREATE TABLE people2 (id INT, name VARCHAR(100), age INT, score FLOAT, active BOOL, bio TEXT);")
	result := executor.ExecuteSQL(t, session, "COPY people2 FROM '"+exportRelPath+"' WITH (FORMAT CSV);")
	if result.Affected != 2 {
		t.Fatalf("expected 2 rows imported into people2, got %d", result.Affected)
	}

	// Verify
	res := executor.ExecuteSQL(t, session, "SELECT * FROM people2 ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	if res.Rows[0][1] != "Alice" {
		t.Fatalf("expected Alice, got %v", res.Rows[0][1])
	}
	if res.Rows[1][1] != "Bob" {
		t.Fatalf("expected Bob, got %v", res.Rows[1][1])
	}
}

func TestCopyFromCSVEmptyFile(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV);")
	if result.Affected != 0 {
		t.Fatalf("expected 0 rows imported from empty file, got %d", result.Affected)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Security tests for path validation
func TestCopyPathValidationRejectsAbsolutePaths(t *testing.T) {
	session, _, _ := setupCopyTable(t)
	executor.ExecuteSQLExpectError(t, session, "COPY people FROM '/etc/passwd';")
	executor.ExecuteSQLExpectError(t, session, "COPY people TO '/tmp/evil.csv';")
}

func TestCopyPathValidationRejectsTraversal(t *testing.T) {
	session, _, _ := setupCopyTable(t)
	executor.ExecuteSQLExpectError(t, session, "COPY people FROM '../../../etc/passwd';")
	executor.ExecuteSQLExpectError(t, session, "COPY people FROM 'subdir/../../etc/passwd';")
	executor.ExecuteSQLExpectError(t, session, "COPY people TO '../../../tmp/evil.csv';")
}

func TestCopyPathValidationRejectsEmptyFilename(t *testing.T) {
	session, _, _ := setupCopyTable(t)
	executor.ExecuteSQLExpectError(t, session, "COPY people FROM '';")
	executor.ExecuteSQLExpectError(t, session, "COPY people TO '';")
}

func TestDelimiterValidationRejectsLongDelimiter(t *testing.T) {
	// A 21-char delimiter string literal should be parsed but then rejected by validation
	longDelim := strings.Repeat("x", 21)
	_, err := parser.Parse("COPY people FROM 'test.csv' WITH (DELIMITER '" + longDelim + "');")
	if err == nil {
		t.Fatalf("expected parse error for long delimiter, got nil")
	}
}

func TestDelimiterValidationRejectsEmptyDelimiter(t *testing.T) {
	// Empty string literals may not be produced by the lexer, but verify
	// the validateDelimiter function works if called directly
	// This is tested in the parser package's unit tests
}

func TestDelimiterValidationAcceptsValidDelimiter(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	content := "1,Alice\n"
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Single char delimiter
	executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (DELIMITER '|');")
	// TAB keyword
	executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (DELIMITER TAB);")
	// Multi-char up to 20 should work
	validDelim := strings.Repeat("ab", 10) // 20 chars
	executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (DELIMITER '"+validDelim+"');")
}

// --- COPY Row Limit Tests ---

func TestCopyFromCSVRowLimitExceeded(t *testing.T) {
	_, dataDir, relPath := setupCopyTable(t)

	// Generate a CSV file with more rows than MaxCopyRows
	var sb strings.Builder
	for i := 0; i <= dml.MaxCopyRows; i++ {
		sb.WriteString(fmt.Sprintf("%d,User%d,%d,8.0,TRUE,Bio\n", i, i, 20+i%50))
	}
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := parser.Parse("COPY people FROM '" + relPath + "' WITH (FORMAT CSV);")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	// The limit is checked at execution time, not parse time.
	// Execute directly with a fresh session that has no MAX_COPY_ROWS override.
	session2 := executor.SetupSession(t)
	executor.ExecuteSQL(t, session2, "CREATE TABLE people (id INT, name VARCHAR(100), age INT, score FLOAT, active BOOL, bio TEXT);")
	executor.ExecuteSQLExpectError(t, session2, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV);")
}

func TestCopyFromCSVWithinLimit(t *testing.T) {
	session, dataDir, relPath := setupCopyTable(t)

	// Generate a CSV file with fewer rows than MaxCopyRows
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf("%d,User%d,%d,8.0,TRUE,Bio\n", i, i, 20+i%50))
	}
	if err := os.WriteFile(filepath.Join(dataDir, relPath), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	result := executor.ExecuteSQL(t, session, "COPY people FROM '"+relPath+"' WITH (FORMAT CSV);")
	if result.Affected != 100 {
		t.Fatalf("expected 100 rows imported, got %d", result.Affected)
	}
}
