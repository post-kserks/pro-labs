package executor

import (
	"testing"
)

func TestVacuum(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// Delete some heroes to create dead versions
	executeSQL(t, session, "DELETE FROM heroes WHERE level < 9;") // Gimli and Boromir

	result := executeSQL(t, session, "VACUUM heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	// Check columns: table, rows_before, rows_after, reclaimed, size_before_kb, size_after_kb, duration_ms
	found := false
	for _, row := range result.Rows {
		if row[0] == "heroes" {
			found = true
			if row[3] == "0" {
				t.Fatalf("expected reclaimed rows > 0, got %s", row[3])
			}
		}
	}
	if !found {
		t.Fatal("heroes table not found in vacuum results")
	}
}

func TestIndex(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	executeSQL(t, session, "CREATE INDEX idx_name ON heroes(name);")

	// Test index lookup
	result := executeSQL(t, session, "SELECT * FROM heroes WHERE name = 'Aragorn';")
	if len(result.Rows) != 1 || result.Rows[0][1] != "Aragorn" {
		t.Fatalf("expected Aragorn, got %#v", result.Rows)
	}

	executeSQL(t, session, "DROP INDEX idx_name;")
}

func TestTransactions(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (5, 'Gandalf', 20, TRUE, 10.0, 'Wizard');")
	
	// Should not be visible to other sessions or even this session before commit?
	// Actually in our implementation it's buffered, so it shouldn't be visible in SelectRows.
	res := executeSQL(t, session, "SELECT * FROM heroes WHERE id = 5;")
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows before commit, got %d", len(res.Rows))
	}

	executeSQL(t, session, "COMMIT;")

	res = executeSQL(t, session, "SELECT * FROM heroes WHERE id = 5;")
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(res.Rows))
	}
}

func TestRollback(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (6, 'Sauron', 50, TRUE, 0.0, 'Dark Lord');")
	executeSQL(t, session, "ROLLBACK;")

	res := executeSQL(t, session, "SELECT * FROM heroes WHERE id = 6;")
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 rows after rollback, got %d", len(res.Rows))
	}
}

func TestPreparedStatements(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	executeSQL(t, session, "PREPARE get_hero AS SELECT name FROM heroes WHERE id = $1;")
	
	result := executeSQL(t, session, "EXECUTE get_hero(1);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "Aragorn" {
		t.Fatalf("expected Aragorn, got %#v", result.Rows)
	}

	executeSQL(t, session, "DEALLOCATE get_hero;")
}
