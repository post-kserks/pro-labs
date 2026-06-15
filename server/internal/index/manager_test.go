package index

import (
	"testing"
)

func TestManagerAddRemove(t *testing.T) {
	m := NewManager()

	h1 := New("idx_name", "name", 0)
	h2 := New("idx_age", "age", 1)

	m.Add(h1)
	m.Add(h2)

	if !m.Has("idx_name") || !m.Has("idx_age") {
		t.Fatal("Has() should return true for added indexes")
	}

	all := m.All()
	if len(all) != 2 {
		t.Fatalf("All() returned %d indexes, want 2", len(all))
	}

	m.Remove("idx_name")

	if m.Has("idx_name") {
		t.Fatal("Has() should return false after Remove")
	}
	if !m.Has("idx_age") {
		t.Fatal("Has() should still return true for idx_age")
	}

	m.Remove("nonexistent")
}

func TestManagerFindForColumn(t *testing.T) {
	m := NewManager()

	idx := New("idx_email", "Email", 2)
	m.Add(idx)

	found, ok := m.FindForColumn("email")
	if !ok || found.Name() != "idx_email" {
		t.Fatalf("FindForColumn('email') = %v, %v; want idx_email, true", found, ok)
	}

	found, ok = m.FindForColumn("EMAIL")
	if !ok || found.Name() != "idx_email" {
		t.Fatalf("FindForColumn('EMAIL') = %v, %v; want idx_email, true (case insensitive)", found, ok)
	}

	_, ok = m.FindForColumn("nonexistent")
	if ok {
		t.Fatal("FindForColumn('nonexistent') should return false")
	}
}

func TestManagerHas(t *testing.T) {
	m := NewManager()

	if m.Has("anything") {
		t.Fatal("Has() should return false for empty manager")
	}

	m.Add(New("idx1", "col1", 0))
	if !m.Has("idx1") {
		t.Fatal("Has('idx1') should return true")
	}
}

func TestManagerNewByType(t *testing.T) {
	tests := []struct {
		indexType string
		wantType  string
	}{
		{"hash", "hash"},
		{"btree", "btree"},
		{"gin", "gin"},
		{"gin_jsonb", "gin"},
		{"gist", "gist"},
		{"unknown", "hash"},
	}

	for _, tc := range tests {
		idx := NewByType("idx", "col", 0, tc.indexType)
		if idx == nil {
			t.Errorf("NewByType(%q) returned nil", tc.indexType)
			continue
		}
		if idx.Type() != tc.wantType {
			t.Errorf("NewByType(%q).Type() = %q, want %q", tc.indexType, idx.Type(), tc.wantType)
		}
	}
}

func TestManagerAddReplace(t *testing.T) {
	m := NewManager()

	m.Add(New("idx1", "col1", 0))
	m.Add(New("idx1", "col_new", 1))

	if !m.Has("idx1") {
		t.Fatal("Has('idx1') should return true")
	}

	found, ok := m.FindForColumn("col_new")
	if !ok || found.Name() != "idx1" {
		t.Fatalf("replaced index not found for col_new: %v, %v", found, ok)
	}

	_, ok = m.FindForColumn("col1")
	if ok {
		t.Fatal("old column should not have an index after replacement")
	}
}

func TestManagerRenameColumn(t *testing.T) {
	m := NewManager()

	m.Add(New("idx1", "old_col", 0))

	m.RenameColumn("idx1", "new_col")

	_, ok := m.FindForColumn("old_col")
	if ok {
		t.Fatal("FindForColumn('old_col') should return false after rename")
	}

	found, ok := m.FindForColumn("new_col")
	if !ok || found.Name() != "idx1" {
		t.Fatalf("FindForColumn('new_col') = %v, %v; want idx1, true", found, ok)
	}
}
