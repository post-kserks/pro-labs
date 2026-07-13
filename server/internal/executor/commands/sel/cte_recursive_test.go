package sel_test

import (
	"testing"

	"vaultdb/internal/executor"
)

func TestRecursiveCTEGenerateNumbers(t *testing.T) {
	session := executor.SetupSession(t)

	result := executor.ExecuteSQL(t, session, `
		WITH RECURSIVE numbers(n) AS (
			SELECT 1
			UNION ALL
			SELECT n + 1 FROM numbers WHERE n < 5
		)
		SELECT * FROM numbers;
	`)

	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(result.Rows))
	}

	expected := []string{"1", "2", "3", "4", "5"}
	for i, row := range result.Rows {
		if row[0] != expected[i] {
			t.Errorf("row %d: expected %s, got %s", i, expected[i], row[0])
		}
	}
}

func TestRecursiveCTEFibonacci(t *testing.T) {
	session := executor.SetupSession(t)

	result := executor.ExecuteSQL(t, session, `
		WITH RECURSIVE fib(a, b) AS (
			SELECT 0, 1
			UNION ALL
			SELECT b, a + b FROM fib WHERE b < 21
		)
		SELECT a FROM fib;
	`)

	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}

	expected := []string{"0", "1", "1", "2", "3", "5", "8", "13"}
	if len(result.Rows) != len(expected) {
		t.Fatalf("expected %d rows, got %d", len(expected), len(result.Rows))
	}
	for i, row := range result.Rows {
		if row[0] != expected[i] {
			t.Errorf("row %d: expected %s, got %s", i, expected[i], row[0])
		}
	}
}

func TestRecursiveCTEDepthLimit(t *testing.T) {
	session := executor.SetupSession(t)

	result := executor.ExecuteSQL(t, session, `
		WITH RECURSIVE countdown(n) AS (
			SELECT 10
			UNION ALL
			SELECT n - 1 FROM countdown WHERE n > 1
		)
		SELECT * FROM countdown;
	`)

	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 10 {
		t.Fatalf("expected 10 rows, got %d", len(result.Rows))
	}
}
