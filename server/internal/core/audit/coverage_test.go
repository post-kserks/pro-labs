package audit

import (
	"vaultdb/internal/core/storage"

	"testing"
)

func TestAuthEventLogging(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Log a successful auth event
	entry := Entry{
		Actor:  "testuser",
		Action: "AUTH_LOGIN",
		Detail: "success",
	}
	if err := log.Append(entry); err != nil {
		t.Fatal(err)
	}

	// Log a failed auth event
	failedEntry := Entry{
		Actor:  "192.168.1.1",
		Action: "AUTH_LOGIN",
		Detail: "failed: invalid token",
	}
	if err := log.Append(failedEntry); err != nil {
		t.Fatal(err)
	}

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Verify first entry (success)
	if entries[0].Actor != "testuser" {
		t.Errorf("expected actor 'testuser', got %q", entries[0].Actor)
	}
	if entries[0].Action != "AUTH_LOGIN" {
		t.Errorf("expected action 'AUTH_LOGIN', got %q", entries[0].Action)
	}
	if entries[0].Detail != "success" {
		t.Errorf("expected detail 'success', got %q", entries[0].Detail)
	}

	// Verify second entry (failed)
	if entries[1].Actor != "192.168.1.1" {
		t.Errorf("expected actor '192.168.1.1', got %q", entries[1].Actor)
	}
	if entries[1].Detail != "failed: invalid token" {
		t.Errorf("expected detail 'failed: invalid token', got %q", entries[1].Detail)
	}

	// Verify chain integrity
	valid, count, err := log.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Error("expected valid chain")
	}
	if count != 2 {
		t.Errorf("expected 2 entries, got %d", count)
	}
}

func TestKeyRotationLogging(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Log key rotation event
	entry := Entry{
		Actor:  "system",
		Action: "KEY_ROTATION",
		Target: "key_version=2",
	}
	if err := log.Append(entry); err != nil {
		t.Fatal(err)
	}

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Actor != "system" {
		t.Errorf("expected actor 'system', got %q", entries[0].Actor)
	}
	if entries[0].Action != "KEY_ROTATION" {
		t.Errorf("expected action 'KEY_ROTATION', got %q", entries[0].Action)
	}
	if entries[0].Target != "key_version=2" {
		t.Errorf("expected target 'key_version=2', got %q", entries[0].Target)
	}

	// Verify chain integrity
	valid, count, err := log.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Error("expected valid chain")
	}
	if count != 1 {
		t.Errorf("expected 1 entry, got %d", count)
	}
}

func TestMixedOperationChainIntegrity(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Log mixed operations
	operations := []Entry{
		{Actor: "user1", Action: "AUTH_LOGIN", Detail: "success"},
		{Actor: "session", Action: "CREATE DATABASE", Target: "mydb"},
		{Actor: "session", Action: "CREATE TABLE", Target: "mydb.users", Detail: "columns=3"},
		{Actor: "user1", Action: "INSERT", Target: "mydb.users"},
		{Actor: "system", Action: "KEY_ROTATION", Target: "key_version=2"},
		{Actor: "user1", Action: "DROP TABLE", Target: "mydb.users"},
		{Actor: "user2", Action: "AUTH_LOGIN", Detail: "failed: invalid token"},
	}

	for _, entry := range operations {
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Verify all entries are logged
	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != len(operations) {
		t.Fatalf("expected %d entries, got %d", len(operations), len(entries))
	}

	// Verify chain integrity
	valid, count, err := log.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Error("expected valid chain")
	}
	if count != len(operations) {
		t.Errorf("expected %d entries, got %d", len(operations), count)
	}

	// Verify specific entries
	for i, entry := range entries {
		expected := operations[i]
		if entry.Actor != expected.Actor {
			t.Errorf("entry %d: expected actor %q, got %q", i, expected.Actor, entry.Actor)
		}
		if entry.Action != expected.Action {
			t.Errorf("entry %d: expected action %q, got %q", i, expected.Action, entry.Action)
		}
	}
}

func (m *mockStorage) SelectForUpdateVM(dbName, tableName string, predicate func(rawTuple []byte) (bool, error), txID uint64, mode storage.LockMode) ([]storage.Row, error) {
	return nil, nil
}

func (m *mockStorage) ReleaseRowLocks(txID uint64) {
}
