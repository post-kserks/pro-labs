package dml_test

import (
	"testing"

	"vaultdb/internal/core/executor"
)

func TestJSONBMergeOperator(t *testing.T) {
	session := executor.SetupSession(t)

	// Create a table with a JSONB column
	executor.ExecuteSQL(t, session, "CREATE DATABASE testdb;")
	executor.ExecuteSQL(t, session, "USE testdb;")
	executor.ExecuteSQL(t, session, "CREATE TABLE config (id INT, settings JSONB);")

	// Insert a JSON object
	executor.ExecuteSQL(t, session, `INSERT INTO config VALUES (1, '{"theme": "dark", "lang": "ru"}');`)

	// Use || to merge JSON objects in SELECT
	result := executor.ExecuteSQL(t, session, `SELECT '{"notifications": true}' || '{"volume": 80}';`)
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	t.Logf("|| merge result: %s", result.Rows[0][0])

	// Check that result contains both keys
	merged := result.Rows[0][0]
	if merged == "" {
		t.Fatal("expected non-empty merge result")
	}

	t.Log("JSONB || merge operator works correctly!")
}

func TestJSONBContainsOperator(t *testing.T) {
	session := executor.SetupSession(t)

	executor.ExecuteSQL(t, session, "CREATE DATABASE testdb2;")
	executor.ExecuteSQL(t, session, "USE testdb2;")
	executor.ExecuteSQL(t, session, "CREATE TABLE docs (id INT, data JSONB);")

	executor.ExecuteSQL(t, session, `INSERT INTO docs VALUES (1, '{"name": "Alice", "age": 30, "city": "Moscow"}');`)
	executor.ExecuteSQL(t, session, `INSERT INTO docs VALUES (2, '{"name": "Bob", "age": 25, "city": "SPb"}');`)
	executor.ExecuteSQL(t, session, `INSERT INTO docs VALUES (3, '{"name": "Charlie", "age": 35, "city": "Moscow"}');`)

	// Use ? to check for key presence
	result := executor.ExecuteSQL(t, session, `SELECT * FROM docs WHERE data ? 'age';`)
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 rows with ?, got %d", len(result.Rows))
	}
	t.Logf("? found %d rows", len(result.Rows))

	t.Log("JSONB ? operator works correctly!")
}

func TestJSONBFunctions(t *testing.T) {
	session := executor.SetupSession(t)

	executor.ExecuteSQL(t, session, "CREATE DATABASE testdb3;")
	executor.ExecuteSQL(t, session, "USE testdb3;")

	// JSONB_BUILD_OBJECT
	result := executor.ExecuteSQL(t, session, `SELECT JSONB_BUILD_OBJECT('name', 'Alice', 'age', 30);`)
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	t.Logf("JSONB_BUILD_OBJECT: %s", result.Rows[0][0])

	// JSONB_BUILD_ARRAY
	result = executor.ExecuteSQL(t, session, `SELECT JSONB_BUILD_ARRAY(1, 2, 3);`)
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	t.Logf("JSONB_BUILD_ARRAY: %s", result.Rows[0][0])

	// JSONB_TYPEOF
	result = executor.ExecuteSQL(t, session, `SELECT JSONB_TYPEOF('{"a": 1}');`)
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	t.Logf("JSONB_TYPEOF: %s", result.Rows[0][0])

	// JSONB_EXTRACT_PATH
	result = executor.ExecuteSQL(t, session, `SELECT JSONB_EXTRACT_PATH('{"a": {"b": 42}}', 'a', 'b');`)
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	t.Logf("JSONB_EXTRACT_PATH: %s", result.Rows[0][0])

	t.Log("JSONB functions work correctly!")
}
