package executor

import (
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

// ExecuteSQL parses and executes a SQL statement, failing the test on error.
func ExecuteSQL(t *testing.T, session *Session, sql string) *Result {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("Parse failed for %q: %v", sql, err)
	}
	result, err := session.Execute(stmt)
	if err != nil {
		t.Fatalf("Execute failed for %q: %v", sql, err)
	}
	return result
}

// ExecuteSQLExpectError parses and executes a SQL statement, failing the test if no error occurs.
func ExecuteSQLExpectError(t *testing.T, session *Session, sql string) {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("Parse failed for %q: %v", sql, err)
	}
	_, err = session.Execute(stmt)
	if err == nil {
		t.Fatalf("Expected error for %q, but got none", sql)
	}
}

// SetupSession creates a new test session with a temporary database.
func SetupSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

	ExecuteSQL(t, session, "CREATE DATABASE mydb;")
	ExecuteSQL(t, session, "USE mydb;")
	ExecuteSQL(t, session, "CREATE TABLE heroes (id INT, name VARCHAR(100), level INT, alive BOOL, score FLOAT, bio TEXT);")
	return session
}

// SetupSessionWithDB creates a new test session with a named database.
func SetupSessionWithDB(t *testing.T, dbName string) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)

	ExecuteSQL(t, session, "CREATE DATABASE "+dbName+";")
	ExecuteSQL(t, session, "USE "+dbName+";")
	return session
}

// SeedHeroes inserts test data into the heroes table.
func SeedHeroes(t *testing.T, session *Session) {
	t.Helper()
	ExecuteSQL(t, session, "INSERT INTO heroes VALUES (1, 'Aragorn', 10, TRUE, 9.8, 'King of Gondor');")
	ExecuteSQL(t, session, "INSERT INTO heroes VALUES (2, 'Legolas', 9, TRUE, 9.5, 'Elven archer');")
	ExecuteSQL(t, session, "INSERT INTO heroes VALUES (3, 'Gimli', 8, TRUE, 8.2, 'Dwarf warrior');")
	ExecuteSQL(t, session, "INSERT INTO heroes VALUES (4, 'Boromir', 5, FALSE, 7.1, 'Captain of Gondor');")
}

// NewTestSession creates a minimal session for concurrent testing.
func NewTestSession(currentDB string, txm *txmanager.Manager) *Session {
	return &Session{
		executor:  &Executor{},
		currentDB: currentDB,
		TxManager: txm,
	}
}
