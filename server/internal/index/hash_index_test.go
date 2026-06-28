package index

import (
	"testing"
)

func TestHashInsertLookup(t *testing.T) {
	idx := New("test_hash", "email", 1)

	idx.Insert("alice@example.com", 0)
	idx.Insert("bob@example.com", 1)
	idx.Insert("alice@example.com", 2)

	positions, ok := idx.Lookup("alice@example.com")
	if !ok {
		t.Fatal("Lookup(alice@example.com) should return true")
	}
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions for alice, got %d", len(positions))
	}
	if positions[0] != 0 || positions[1] != 2 {
		t.Fatalf("expected [0 2], got %v", positions)
	}

	positions, ok = idx.Lookup("bob@example.com")
	if !ok || len(positions) != 1 || positions[0] != 1 {
		t.Fatalf("Lookup(bob@example.com) = %v, %v; want [1], true", positions, ok)
	}
}

func TestHashLookupMiss(t *testing.T) {
	idx := New("test_hash", "email", 1)
	idx.Insert("alice@example.com", 0)

	if _, ok := idx.Lookup("nobody@example.com"); ok {
		t.Fatal("Lookup for non-existent key should return false")
	}
}

func TestHashDelete(t *testing.T) {
	idx := New("test_hash", "email", 1)

	idx.Insert("alice@example.com", 0)
	idx.Insert("bob@example.com", 1)

	idx.Delete(0)

	if _, ok := idx.Lookup("alice@example.com"); ok {
		t.Fatal("Lookup(alice) should return false after delete")
	}

	positions, ok := idx.Lookup("bob@example.com")
	if !ok || len(positions) != 1 || positions[0] != 1 {
		t.Fatalf("Lookup(bob) = %v, %v; want [1], true", positions, ok)
	}

	idx.Delete(999)
}

func TestHashRebuild(t *testing.T) {
	idx := New("test_hash", "name", 0)

	idx.Insert("old", 0)

	rows := []IndexableRow{
		{DeletedTx: 0, Data: []interface{}{"alice"}},
		{DeletedTx: 1, Data: []interface{}{"bob"}},
		{DeletedTx: 0, Data: []interface{}{"carol"}},
		{DeletedTx: 3, Data: []interface{}{"dave"}},
		{DeletedTx: 0, Data: []interface{}{"alice"}},
	}
	idx.Rebuild(rows)

	if _, ok := idx.Lookup("old"); ok {
		t.Fatal("Lookup(old) should return false after rebuild")
	}
	if _, ok := idx.Lookup("bob"); ok {
		t.Fatal("Lookup(bob) should return false (deleted row)")
	}
	if _, ok := idx.Lookup("dave"); ok {
		t.Fatal("Lookup(dave) should return false (deleted row)")
	}

	positions, ok := idx.Lookup("alice")
	if !ok || len(positions) != 2 {
		t.Fatalf("Lookup(alice) = %v, %v; want 2 positions, true", positions, ok)
	}
	if positions[0] != 0 || positions[1] != 4 {
		t.Fatalf("expected [0 4], got %v", positions)
	}

	positions, ok = idx.Lookup("carol")
	if !ok || len(positions) != 1 || positions[0] != 2 {
		t.Fatalf("Lookup(carol) = %v, %v; want [2], true", positions, ok)
	}
}

func TestValueToIndexKey(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{nil, "\x00NULL"},
		{int64(42), "42"},
		{"hello", "hello"},
		{3.14, "3.14"},
	}
	for _, tc := range tests {
		got := ValueToIndexKey(tc.input)
		if got != tc.want {
			t.Errorf("ValueToIndexKey(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHashMetadata(t *testing.T) {
	idx := New("my_idx", "col_a", 3)

	if idx.Type() != "hash" {
		t.Errorf("Type() = %q, want %q", idx.Type(), "hash")
	}
	if idx.Name() != "my_idx" {
		t.Errorf("Name() = %q, want %q", idx.Name(), "my_idx")
	}
	if idx.Column() != "col_a" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "col_a")
	}
	if idx.ColIndex() != 3 {
		t.Errorf("ColIndex() = %d, want %d", idx.ColIndex(), 3)
	}

	idx.RenameColumn("col_a", "col_b")
	if idx.Column() != "col_b" {
		t.Errorf("Column() after RenameColumn = %q, want %q", idx.Column(), "col_b")
	}
}
