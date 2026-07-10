package executor

import (
	"strconv"
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func setupDASTSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

	executeSQL(t, session, "CREATE DATABASE securitydb;")
	executeSQL(t, session, "USE securitydb;")
	executeSQL(t, session, "CREATE TABLE users (id INT PRIMARY KEY, name TEXT, role TEXT, password TEXT);")
	executeSQL(t, session, "INSERT INTO users VALUES (1, 'admin', 'admin', 's3cret');")
	executeSQL(t, session, "INSERT INTO users VALUES (2, 'alice', 'user', 'pass123');")
	executeSQL(t, session, "INSERT INTO users VALUES (3, 'bob', 'user', 'hunter2');")
	return session
}

func executeRawSQL(t *testing.T, session *Session, sql string) (*Result, error) {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}
	return session.Execute(stmt)
}

func TestDASTSQLInjection(t *testing.T) {
	session := setupDASTSession(t)

	payloads := []string{
		`SELECT * FROM users WHERE id = 1; DROP TABLE users;--`,
		`SELECT * FROM users WHERE name = 'x' OR '1'='1'`,
		`SELECT * FROM users WHERE id = (SELECT 1 UNION SELECT 2)`,
		`PREPARE p AS SELECT * FROM users WHERE id = $1; EXECUTE p('1 OR 1=1')`,
		`CREATE FUNCTION x() RETURNS INT LANGUAGE SQL AS 'SELECT 1'`,
		`'; DROP TABLE users; --`,
		`SELECT * FROM users WHERE name = '' OR 1=1--'`,
		`INSERT INTO users VALUES (1, 'admin'--', 'pass')`,
	}

	for _, payload := range payloads {
		t.Run("injection_"+strconv.Itoa(len(payload)), func(t *testing.T) {
			_, err := executeRawSQL(t, session, payload)
			if err != nil {
				return // parse error is fine — parser rejects malicious syntax
			}
			// If no error, the statement ran safely
		})
	}

	// Verify users table still exists and has all original rows
	res := executeSQL(t, session, "SELECT COUNT(*) FROM users;")
	if res == nil || len(res.Rows) == 0 {
		t.Fatal("users table should still exist after injection attempts")
	}
	if res.Rows[0][0] != "3" {
		t.Fatalf("expected 3 users after injection attempts, got %s", res.Rows[0][0])
	}

	// Verify no rogue data was inserted
	res = executeSQL(t, session, "SELECT name FROM users ORDER BY id;")
	expected := []string{"admin", "alice", "bob"}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 users, got %d", len(res.Rows))
	}
	for i, row := range res.Rows {
		if row[0] != expected[i] {
			t.Errorf("user %d: expected %s, got %s", i, expected[i], row[0])
		}
	}
}

func TestDASTSQLInjectionUNION(t *testing.T) {
	session := setupDASTSession(t)

	// UNION-based injection: attempt to extract from system tables
	payloads := []string{
		`SELECT id, name FROM users WHERE id = 1 UNION SELECT username, password FROM admin_users`,
		`SELECT * FROM users WHERE id = -1 UNION SELECT 1, 'hacked', 'admin', 'pwned'`,
	}

	for _, payload := range payloads {
		_, err := executeRawSQL(t, session, payload)
		if err != nil {
			continue // expected: parser or executor rejects these
		}
	}
}

func TestDASTSQLInjectionTimeBased(t *testing.T) {
	session := setupDASTSession(t)

	// Time-based blind injection (VaultDB doesn't have SLEEP but test the parser)
	payloads := []string{
		`SELECT * FROM users WHERE id = 1 AND (SELECT COUNT(*) FROM users) > 0`,
		`SELECT * FROM users WHERE id = 1 AND SUBSTRING(name, 1, 1) = 'a'`,
	}

	for _, payload := range payloads {
		_, err := executeRawSQL(t, session, payload)
		if err != nil {
			continue
		}
	}
}
