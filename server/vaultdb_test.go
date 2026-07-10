package vaultdb

import (
	"os"
	"testing"
)

func TestQueryPublicAPI(t *testing.T) {
	// Create temp directory
	dir, err := os.MkdirTemp("", "vaultdb-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Open database
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// Create database
	result, err := db.Query("", "CREATE DATABASE testdb;")
	if err != nil {
		t.Fatalf("Create database failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Use database
	_, err = db.Query("", "USE testdb;")
	if err != nil {
		t.Fatalf("Use database failed: %v", err)
	}

	// Create table
	_, err = db.Query("testdb", "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);")
	if err != nil {
		t.Fatalf("Create table failed: %v", err)
	}

	// Insert data
	result, err = db.Query("testdb", "INSERT INTO users VALUES (1, 'Alice');")
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	if result.Affected != 1 {
		t.Errorf("expected 1 affected row, got %d", result.Affected)
	}

	// Query data
	result, err = db.Query("testdb", "SELECT * FROM users;")
	if err != nil {
		t.Fatalf("Select failed: %v", err)
	}
	if len(result.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(result.Columns))
	}
	if len(result.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "1" {
		t.Errorf("expected id=1, got %s", result.Rows[0][0])
	}
	if result.Rows[0][1] != "Alice" {
		t.Errorf("expected name=Alice, got %s", result.Rows[0][1])
	}
}

func TestResultTypeAccessible(t *testing.T) {
	// This test verifies that the Result type is accessible
	// from external modules (compilation test)
	r := &Result{
		Type:     "rows",
		Columns:  []string{"id", "name"},
		Rows:     [][]string{{"1", "Alice"}},
		Affected: 0,
		Message:  "",
	}
	if r.Type != "rows" {
		t.Errorf("expected type 'rows', got %s", r.Type)
	}
}
