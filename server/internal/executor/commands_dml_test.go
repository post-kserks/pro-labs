package executor

import (
	"testing"
)

func TestInsertReturningUsesPreMutationData(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Frodo', 15, TRUE, 9.0, 'Ring bearer') RETURNING *;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(result.Rows))
	}
	if result.Rows[0][1] != "Frodo" {
		t.Fatalf("expected name 'Frodo', got %q", result.Rows[0][1])
	}
	if result.Rows[0][2] != "15" {
		t.Fatalf("expected level '15', got %q", result.Rows[0][2])
	}
}

func TestUpdateReturningUsesPreMutationData(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "UPDATE heroes SET level = 99 WHERE name = 'Aragorn' RETURNING name, level;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Aragorn" {
		t.Fatalf("expected name 'Aragorn', got %q", result.Rows[0][0])
	}
	if result.Rows[0][1] != "10" {
		t.Fatalf("expected pre-mutation level '10', got %q", result.Rows[0][1])
	}

	// Verify the mutation actually happened
	verify := executeSQL(t, session, "SELECT level FROM heroes WHERE name = 'Aragorn';")
	if len(verify.Rows) != 1 || verify.Rows[0][0] != "99" {
		t.Fatalf("expected post-mutation level '99', got %q", verify.Rows[0][0])
	}
}

func TestDeleteReturningUsesPreMutationData(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "DELETE FROM heroes WHERE name = 'Boromir' RETURNING *;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(result.Rows))
	}
	if result.Rows[0][1] != "Boromir" {
		t.Fatalf("expected name 'Boromir', got %q", result.Rows[0][1])
	}
	if result.Rows[0][2] != "5" {
		t.Fatalf("expected pre-deletion level '5', got %q", result.Rows[0][2])
	}
	if result.Rows[0][3] != "false" {
		t.Fatalf("expected pre-deletion alive 'false', got %q", result.Rows[0][3])
	}

	// Verify the row was actually deleted
	verify := executeSQL(t, session, "SELECT COUNT(*) FROM heroes WHERE name = 'Boromir';")
	if len(verify.Rows) != 1 || verify.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows after delete, got %q", verify.Rows[0][0])
	}
}

func TestInsertReturningMultipleRows(t *testing.T) {
	session := setupSession(t)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (20, 'Pippin', 3, TRUE, 5.0, 'Hobbit'), (21, 'Merry', 4, TRUE, 5.5, 'Hobbit') RETURNING id, name;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 returned rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "20" || result.Rows[0][1] != "Pippin" {
		t.Fatalf("expected [20, Pippin], got %v", result.Rows[0])
	}
	if result.Rows[1][0] != "21" || result.Rows[1][1] != "Merry" {
		t.Fatalf("expected [21, Merry], got %v", result.Rows[1])
	}
}

func TestUpdateReturningMultipleRows(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "UPDATE heroes SET score = 10.0 WHERE alive = TRUE RETURNING name, score;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("expected 3 returned rows (Aragorn, Legolas, Gimli), got %d", len(result.Rows))
	}

	for _, row := range result.Rows {
		if row[1] != "9.8" && row[1] != "9.5" && row[1] != "8.2" {
			t.Fatalf("expected pre-mutation score, got %q for %q", row[1], row[0])
		}
	}
}

func TestMergeWhenNotMatchedValidation(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE target (id INT, name VARCHAR(100));")
	executeSQL(t, session, "CREATE TABLE source (id INT, val VARCHAR(100));")
	executeSQL(t, session, "INSERT INTO source VALUES (1, 'hello');")

	// More columns than values
	executeSQLExpectError(t, session, `
		MERGE INTO target
		USING source AS s
		ON target.id = s.id
		WHEN MATCHED THEN UPDATE SET name = s.val
		WHEN NOT MATCHED THEN
			INSERT (id, name) VALUES (s.id);
	`)

	// More values than columns
	executeSQLExpectError(t, session, `
		MERGE INTO target
		USING source AS s
		ON target.id = s.id
		WHEN MATCHED THEN UPDATE SET name = s.val
		WHEN NOT MATCHED THEN
			INSERT (id) VALUES (s.id, s.val);
	`)

	// Correct count should work
	executeSQL(t, session, `
		MERGE INTO target
		USING source AS s
		ON target.id = s.id
		WHEN MATCHED THEN UPDATE SET name = s.val
		WHEN NOT MATCHED THEN
			INSERT (id, name) VALUES (s.id, s.val);
	`)
}

func TestDeleteReturningMultipleRows(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "DELETE FROM heroes WHERE alive = FALSE RETURNING name;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Boromir" {
		t.Fatalf("expected 'Boromir', got %q", result.Rows[0][0])
	}
}
