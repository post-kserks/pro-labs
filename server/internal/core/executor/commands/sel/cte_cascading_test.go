package sel_test

import (
	"testing"

	"vaultdb/internal/core/executor"
)

func TestCascadingCTE(t *testing.T) {
	session := executor.SetupSession(t)

	// cte2 references cte1
	result := executor.ExecuteSQL(t, session, `
		WITH
			cte1 AS (SELECT 1 AS val),
			cte2 AS (SELECT val + 1 AS doubled FROM cte1)
		SELECT * FROM cte2;
	`)

	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "2" {
		t.Errorf("expected doubled=2, got %s", result.Rows[0][0])
	}
}

func TestCascadingCTEThreeLevels(t *testing.T) {
	session := executor.SetupSession(t)

	// cte3 references cte2, which references cte1
	result := executor.ExecuteSQL(t, session, `
		WITH
			cte1 AS (SELECT 1 AS val),
			cte2 AS (SELECT val * 2 AS val FROM cte1),
			cte3 AS (SELECT val + 10 AS val FROM cte2)
		SELECT * FROM cte3;
	`)

	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "12" {
		t.Errorf("expected val=12, got %s", result.Rows[0][0])
	}
}
