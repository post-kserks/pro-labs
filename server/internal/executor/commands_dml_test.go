package executor

import (
	"fmt"
	"testing"

	"vaultdb/internal/parser"
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

func TestUpdateReturningUsesPostMutationData(t *testing.T) {
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
	if result.Rows[0][1] != "99" {
		t.Fatalf("expected post-mutation level '99', got %q", result.Rows[0][1])
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
		if row[1] != "10" {
			t.Fatalf("expected post-mutation score '10', got %q for %q", row[1], row[0])
		}
	}
}

func TestUpdateReturningOldNewSyntax(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "UPDATE heroes SET level = 99 WHERE name = 'Aragorn' RETURNING name, old.level AS old_level, new.level AS new_level;")
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
		t.Fatalf("expected old.level '10', got %q", result.Rows[0][1])
	}
	if result.Rows[0][2] != "99" {
		t.Fatalf("expected new.level '99', got %q", result.Rows[0][2])
	}
}

func TestDeleteReturningOldSyntax(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "DELETE FROM heroes WHERE name = 'Boromir' RETURNING old.name, old.level;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "Boromir" {
		t.Fatalf("expected old.name 'Boromir', got %q", result.Rows[0][0])
	}
	if result.Rows[0][1] != "5" {
		t.Fatalf("expected old.level '5', got %q", result.Rows[0][1])
	}
}

func TestInsertReturningNewSyntax(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Frodo', 15, TRUE, 9.0, 'Ring bearer') RETURNING new.id, new.name;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "10" {
		t.Fatalf("expected new.id '10', got %q", result.Rows[0][0])
	}
	if result.Rows[0][1] != "Frodo" {
		t.Fatalf("expected new.name 'Frodo', got %q", result.Rows[0][1])
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

func TestTruncateBasic(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "TRUNCATE heroes;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}

	count := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if count.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows after TRUNCATE, got %s", count.Rows[0][0])
	}
}

func TestTruncateInsideTransaction(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "TRUNCATE heroes;")

	// Read-your-own-writes (Bug #1): буферизованный TRUNCATE виден своей же
	// транзакции — таблица читается пустой через tx-overlay.
	count := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if count.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows within tx after buffered TRUNCATE, got %s", count.Rows[0][0])
	}

	executeSQL(t, session, "COMMIT;")

	count = executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if count.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows after COMMIT, got %s", count.Rows[0][0])
	}
}

func TestTruncateConcurrentInsertAtomicity(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	txm := session.TxManager
	exec := session.executor

	// Run TRUNCATE and concurrent INSERT in parallel.
	// The implicit transaction in TRUNCATE uses version-based conflict detection.
	// One of two outcomes is valid:
	//   1) TRUNCATE wins: table has 0 or 1 rows (the late insert arrived after TRUNCATE committed)
	//   2) INSERT wins: TRUNCATE fails with conflict, table has >= 4 original rows
	var truncateErr error

	done := make(chan struct{})
	go func() {
		defer close(done)
		stmt, _ := parser.Parse("INSERT INTO heroes VALUES (10, 'Concurrent', 1, TRUE, 5.0, 'Race');")
		sess2 := &Session{
			currentDB: "mydb",
			TxManager: txm,
		}
		exec.Run(stmt, sess2)
	}()

	stmt, _ := parser.Parse("TRUNCATE heroes;")
	_, truncateErr = exec.Run(stmt, session)
	<-done

	count := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	rowCount := 0
	fmt.Sscanf(count.Rows[0][0], "%d", &rowCount)

	if truncateErr == nil {
		// TRUNCATE committed. The concurrent INSERT may or may not have landed
		// after our commit. Either 0 rows (INSERT lost) or 1 row (INSERT landed after).
		if rowCount > 1 {
			t.Fatalf("TRUNCATE succeeded but table has %d rows (expected 0 or 1)", rowCount)
		}
	} else {
		// TRUNCATE commit failed due to version conflict. Original rows should be intact.
		if rowCount < 4 {
			t.Fatalf("TRUNCATE failed but table has only %d rows (expected >= 4), err: %v", rowCount, truncateErr)
		}
	}
}

func TestCreateTableWithNotNull(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE nn_test (id INT NOT NULL, name VARCHAR(100), age INT);")

	// Verify the table was created
	result := executeSQL(t, session, "SELECT * FROM nn_test;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
}

func TestInsertNullIntoNotNullColumn(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE nn_test (id INT NOT NULL, name VARCHAR(100), age INT);")

	// Inserting NULL into a NOT NULL column should fail
	executeSQLExpectError(t, session, "INSERT INTO nn_test (id, name) VALUES (NULL, 'Alice');")

	// Inserting a valid value should succeed
	executeSQL(t, session, "INSERT INTO nn_test (id, name) VALUES (1, 'Alice');")

	// Inserting NULL into a nullable column should succeed
	executeSQL(t, session, "INSERT INTO nn_test (id, name, age) VALUES (2, 'Bob', NULL);")
}

func TestUpdateNullIntoNotNullColumn(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE nn_test (id INT NOT NULL, name VARCHAR(100), age INT);")
	executeSQL(t, session, "INSERT INTO nn_test (id, name, age) VALUES (1, 'Alice', 30);")

	// Updating a NOT NULL column to NULL should fail
	executeSQLExpectError(t, session, "UPDATE nn_test SET id = NULL WHERE id = 1;")

	// Updating a nullable column to NULL should succeed
	executeSQL(t, session, "UPDATE nn_test SET age = NULL WHERE id = 1;")

	// Verify the update worked
	result := executeSQL(t, session, "SELECT age FROM nn_test WHERE id = 1;")
	if result.Rows[0][0] != "" {
		t.Fatalf("expected NULL age, got %q", result.Rows[0][0])
	}
}

func TestCreateTableWithDefault(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE def_test (id INT, name VARCHAR(100) DEFAULT 'unknown', score FLOAT DEFAULT 0.0, active BOOL DEFAULT TRUE);")

	// Insert without specifying columns — defaults should be applied
	executeSQL(t, session, "INSERT INTO def_test (id) VALUES (1);")

	result := executeSQL(t, session, "SELECT name, score, active FROM def_test WHERE id = 1;")
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "unknown" {
		t.Fatalf("expected default name 'unknown', got %q", result.Rows[0][0])
	}
	if result.Rows[0][1] != "0" {
		t.Fatalf("expected default score '0', got %q", result.Rows[0][1])
	}
	if result.Rows[0][2] != "true" {
		t.Fatalf("expected default active 'true', got %q", result.Rows[0][2])
	}
}

func TestDefaultOnlyAppliedWhenColumnNotSpecified(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE def_test2 (id INT, label VARCHAR(100) DEFAULT 'fallback');")

	// Explicitly specify column with NULL — default should NOT replace it
	executeSQL(t, session, "INSERT INTO def_test2 (id, label) VALUES (1, NULL);")

	result := executeSQL(t, session, "SELECT label FROM def_test2 WHERE id = 1;")
	if result.Rows[0][0] != "" {
		t.Fatalf("expected NULL label (explicit NULL), got %q", result.Rows[0][0])
	}

	// Column not specified — default should apply
	executeSQL(t, session, "INSERT INTO def_test2 (id) VALUES (2);")

	result = executeSQL(t, session, "SELECT label FROM def_test2 WHERE id = 2;")
	if result.Rows[0][0] != "fallback" {
		t.Fatalf("expected default 'fallback', got %q", result.Rows[0][0])
	}
}

func TestDefaultWithInt(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE def_int (id INT, counter INT DEFAULT 42);")
	executeSQL(t, session, "INSERT INTO def_int (id) VALUES (1);")

	result := executeSQL(t, session, "SELECT counter FROM def_int WHERE id = 1;")
	if result.Rows[0][0] != "42" {
		t.Fatalf("expected default counter '42', got %q", result.Rows[0][0])
	}
}

func TestDefaultMultipleRows(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "CREATE TABLE def_multi (id INT, status VARCHAR(50) DEFAULT 'pending');")
	executeSQL(t, session, "INSERT INTO def_multi (id) VALUES (1), (2), (3);")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM def_multi WHERE status = 'pending';")
	if result.Rows[0][0] != "3" {
		t.Fatalf("expected 3 rows with default, got %q", result.Rows[0][0])
	}
}
