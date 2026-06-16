package executor

import (
	"strings"
	"testing"

	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func newSmokeSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sess := NewSession(store, metrics.New(), txm, NewBroadcaster())

	mustExec(t, sess, "CREATE DATABASE smoke;")
	sess.SetCurrentDatabase("smoke")
	mustExec(t, sess, "CREATE TABLE people (id INT, name TEXT, dept TEXT, salary INT);")
	mustExec(t, sess, `INSERT INTO people (id, name, dept, salary) VALUES
		(1, 'alice', 'eng', 100), (2, 'bob', 'eng', 80),
		(3, 'carol', 'ops', 90), (4, 'dave', 'ops', 70), (5, 'eve', 'hr', 60);`)
	return sess
}

func mustExec(t *testing.T, sess *Session, sql string) *Result {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("parse %q: %v", sql, err)
	}
	res, err := sess.Execute(stmt)
	if err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
	return res
}

func TestLikeOperator(t *testing.T) {
	sess := newSmokeSession(t)

	res := mustExec(t, sess, "SELECT name FROM people WHERE name LIKE 'a%';")
	if len(res.Rows) != 1 || res.Rows[0][0] != "alice" {
		t.Fatalf("LIKE 'a%%' = %v, want [alice]", res.Rows)
	}

	res = mustExec(t, sess, "SELECT name FROM people WHERE name LIKE '_ob';")
	if len(res.Rows) != 1 || res.Rows[0][0] != "bob" {
		t.Fatalf("LIKE '_ob' = %v, want [bob]", res.Rows)
	}

	res = mustExec(t, sess, "SELECT name FROM people WHERE name NOT LIKE '%e%';")
	for _, row := range res.Rows {
		if strings.Contains(row[0], "e") {
			t.Fatalf("NOT LIKE '%%e%%' returned %q", row[0])
		}
	}
}

func TestGroupByOrderByLimit(t *testing.T) {
	sess := newSmokeSession(t)

	res := mustExec(t, sess, "SELECT dept, COUNT(*) AS cnt FROM people GROUP BY dept ORDER BY cnt DESC, dept ASC LIMIT 2;")
	if len(res.Rows) != 2 {
		t.Fatalf("LIMIT 2 returned %d rows: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "eng" || res.Rows[0][1] != "2" {
		t.Fatalf("first group = %v, want [eng 2]", res.Rows[0])
	}
	if res.Rows[1][0] != "ops" {
		t.Fatalf("second group = %v, want ops", res.Rows[1])
	}
}

func TestIsNull(t *testing.T) {
	sess := newSmokeSession(t)
	mustExec(t, sess, "INSERT INTO people (id, name) VALUES (6, 'frank');")

	res := mustExec(t, sess, "SELECT name FROM people WHERE dept IS NULL;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "frank" {
		t.Fatalf("IS NULL = %v, want [frank]", res.Rows)
	}

	res = mustExec(t, sess, "SELECT COUNT(*) FROM people WHERE dept IS NOT NULL;")
	if res.Rows[0][0] != "5" {
		t.Fatalf("IS NOT NULL count = %v, want 5", res.Rows[0][0])
	}
}

func TestPreparedStatementKeepsOrderBy(t *testing.T) {
	sess := newSmokeSession(t)
	mustExec(t, sess, "PREPARE top AS SELECT name FROM people WHERE salary > $1 ORDER BY salary DESC;")

	res := mustExec(t, sess, "EXECUTE top(75);")
	want := []string{"alice", "carol", "bob"}
	if len(res.Rows) != len(want) {
		t.Fatalf("EXECUTE returned %v, want %v", res.Rows, want)
	}
	for i, name := range want {
		if res.Rows[i][0] != name {
			t.Fatalf("row %d = %q, want %q (ORDER BY dropped?)", i, res.Rows[i][0], name)
		}
	}
}

func TestSumIntStaysInt(t *testing.T) {
	sess := newSmokeSession(t)
	res := mustExec(t, sess, "SELECT SUM(salary) FROM people;")
	if res.Rows[0][0] != "400" {
		t.Fatalf("SUM(salary) = %q, want 400", res.Rows[0][0])
	}
}

func TestMultipleWindowFunctions(t *testing.T) {
	sess := newSmokeSession(t)

	res := mustExec(t, sess,
		"SELECT name, ROW_NUMBER() OVER (ORDER BY salary DESC) AS rn, SUM(salary) OVER () AS total FROM people;")
	if len(res.Rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(res.Rows))
	}
	for _, row := range res.Rows {
		// rn is 1..5, total is always 400; before the fix both columns
		// projected the first window function's value.
		if row[2] != "400" {
			t.Fatalf("total column = %q, want 400 (window columns mixed up): %v", row[2], row)
		}
	}
	if res.Rows[0][1] == res.Rows[0][2] {
		t.Fatalf("rn and total identical (%q), window functions share a column", res.Rows[0][1])
	}
}

func TestCommitConflictDetected(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	br := NewBroadcaster()

	sess1 := NewSession(store, metrics.New(), txm, br)
	mustExec(t, sess1, "CREATE DATABASE smoke;")
	sess1.SetCurrentDatabase("smoke")
	mustExec(t, sess1, "CREATE TABLE items (id INT, name TEXT);")
	mustExec(t, sess1, "INSERT INTO items (id, name) VALUES (1, 'first');")

	sess2 := NewSession(store, metrics.New(), txm, br)
	sess2.SetCurrentDatabase("smoke")

	mustExec(t, sess1, "BEGIN;")
	mustExec(t, sess1, "INSERT INTO items (id, name) VALUES (2, 'buffered');")

	// Another session writes to the same table after the snapshot.
	mustExec(t, sess2, "INSERT INTO items (id, name) VALUES (3, 'intruder');")

	stmt, err := parser.Parse("COMMIT;")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess1.Execute(stmt); err == nil {
		t.Fatal("COMMIT succeeded despite concurrent modification, want conflict error")
	}
}

func TestMigrationAppliedOnce(t *testing.T) {
	sess := newSmokeSession(t)
	mustExec(t, sess, "CREATE MIGRATION add_bio ('ALTER TABLE people ADD COLUMN bio TEXT;');")
	mustExec(t, sess, "APPLY MIGRATION add_bio;")

	stmt, err := parser.Parse("APPLY MIGRATION add_bio;")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Execute(stmt); err == nil {
		t.Fatal("second APPLY MIGRATION succeeded, want already-applied error")
	}
}
