package dml_test

import (
	"strings"
	"testing"

	"vaultdb/internal/core/executor"
)

func TestUniqueConstraintOnInsert(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE users_unique (id INT, name TEXT UNIQUE);")
	executor.ExecuteSQL(t, session, "INSERT INTO users_unique VALUES (1, 'Alice');")
	executor.ExecuteSQLExpectError(t, session, "INSERT INTO users_unique VALUES (2, 'Alice');")
}

func TestUniqueConstraintOnInsertMultipleColumns(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE multi_unique (id INT UNIQUE, name TEXT UNIQUE);")
	executor.ExecuteSQL(t, session, "INSERT INTO multi_unique VALUES (1, 'Alice');")
	// Duplicate id
	executor.ExecuteSQLExpectError(t, session, "INSERT INTO multi_unique VALUES (1, 'Bob');")
	// Duplicate name
	executor.ExecuteSQLExpectError(t, session, "INSERT INTO multi_unique VALUES (2, 'Alice');")
}

func TestUniqueConstraintOnInsertSuccess(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE unique_success (id INT, email TEXT UNIQUE);")
	executor.ExecuteSQL(t, session, "INSERT INTO unique_success VALUES (1, 'a@test.com');")
	executor.ExecuteSQL(t, session, "INSERT INTO unique_success VALUES (2, 'b@test.com');")
	executor.ExecuteSQL(t, session, "INSERT INTO unique_success VALUES (3, 'c@test.com');")

	result := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM unique_success;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "3" {
		t.Errorf("expected 3 rows, got %v", result.Rows)
	}
}

func TestUniqueIndexOnInsert(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE idx_unique (id INT, name TEXT);")
	executor.ExecuteSQL(t, session, "CREATE UNIQUE INDEX idx_uname ON idx_unique(name);")
	executor.ExecuteSQL(t, session, "INSERT INTO idx_unique VALUES (1, 'Alice');")
	executor.ExecuteSQLExpectError(t, session, "INSERT INTO idx_unique VALUES (2, 'Alice');")
}

func TestUniqueIndexOnInsertSuccess(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE idx_unique_ok (id INT, name TEXT);")
	executor.ExecuteSQL(t, session, "CREATE UNIQUE INDEX idx_name_ok ON idx_unique_ok(name);")
	executor.ExecuteSQL(t, session, "INSERT INTO idx_unique_ok VALUES (1, 'Alice');")
	executor.ExecuteSQL(t, session, "INSERT INTO idx_unique_ok VALUES (2, 'Bob');")
	executor.ExecuteSQL(t, session, "INSERT INTO idx_unique_ok VALUES (3, 'Charlie');")

	result := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM idx_unique_ok;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "3" {
		t.Errorf("expected 3 rows, got %v", result.Rows)
	}
}

func TestUniqueConstraintNullAllowed(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE unique_null (id INT, email TEXT UNIQUE);")
	executor.ExecuteSQL(t, session, "INSERT INTO unique_null VALUES (1, NULL);")
	executor.ExecuteSQL(t, session, "INSERT INTO unique_null VALUES (2, NULL);")

	result := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM unique_null;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "2" {
		t.Errorf("expected 2 rows with NULLs allowed, got %v", result.Rows)
	}
}

func TestUniqueIndexNullAllowed(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE idx_null (id INT, email TEXT);")
	executor.ExecuteSQL(t, session, "CREATE UNIQUE INDEX idx_email_null ON idx_null(email);")
	executor.ExecuteSQL(t, session, "INSERT INTO idx_null VALUES (1, NULL);")
	executor.ExecuteSQL(t, session, "INSERT INTO idx_null VALUES (2, NULL);")

	result := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM idx_null;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "2" {
		t.Errorf("expected 2 rows with NULLs allowed in unique index, got %v", result.Rows)
	}
}

func TestParserCreateUniqueIndex(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE parse_test (id INT, name TEXT);")
	// This should parse and execute without error
	executor.ExecuteSQL(t, session, "CREATE UNIQUE INDEX idx_parse ON parse_test(name);")

	// Verify the index exists
	res := executor.ExecuteSQL(t, session, "SHOW INDEXES FROM parse_test;")
	found := false
	for _, row := range res.Rows {
		if len(row) > 0 && strings.Contains(row[0], "idx_parse") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find unique index 'idx_parse' in SHOW INDEXES output")
	}
}

func TestUniqueConstraintOnUpdate(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE upd_unique (id INT, code TEXT UNIQUE);")
	executor.ExecuteSQL(t, session, "INSERT INTO upd_unique VALUES (1, 'ABC');")
	executor.ExecuteSQL(t, session, "INSERT INTO upd_unique VALUES (2, 'DEF');")
	// Try to update to an existing value
	executor.ExecuteSQLExpectError(t, session, "UPDATE upd_unique SET code = 'ABC' WHERE id = 2;")
	// Update to a new value should succeed
	executor.ExecuteSQL(t, session, "UPDATE upd_unique SET code = 'GHI' WHERE id = 2;")

	result := executor.ExecuteSQL(t, session, "SELECT code FROM upd_unique ORDER BY id;")
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[1][0] != "GHI" {
		t.Errorf("expected 'GHI', got %v", result.Rows[1][0])
	}
}
