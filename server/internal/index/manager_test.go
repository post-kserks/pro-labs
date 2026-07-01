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

	m.RenameColumn("idx1", "old_col", "new_col")

	_, ok := m.FindForColumn("old_col")
	if ok {
		t.Fatal("FindForColumn('old_col') should return false after rename")
	}

	found, ok := m.FindForColumn("new_col")
	if !ok || found.Name() != "idx1" {
		t.Fatalf("FindForColumn('new_col') = %v, %v; want idx1, true", found, ok)
	}
}

func TestManagerFindForColumnMultiple(t *testing.T) {
	m := NewManager()

	m.Add(New("idx1", "email", 0))
	m.Add(New("idx2", "email", 1))
	m.Add(New("idx3", "name", 2))

	idxs, ok := m.FindForColumnMultiple("email")
	if !ok || len(idxs) != 2 {
		t.Fatalf("FindForColumnMultiple('email') = %v, %v; want 2 indexes", idxs, ok)
	}

	idxs, ok = m.FindForColumnMultiple("name")
	if !ok || len(idxs) != 1 {
		t.Fatalf("FindForColumnMultiple('name') = %v, %v; want 1 index", idxs, ok)
	}

	idxs, ok = m.FindForColumnMultiple("nonexistent")
	if ok {
		t.Fatal("FindForColumnMultiple('nonexistent') should return false")
	}
}

func TestManagerFullTextSearch(t *testing.T) {
	m := NewManager()

	gin := NewGINIndex("idx_text", "body", 0)
	gin.Insert("hello world", 0)
	gin.Insert("hello go", 1)
	gin.Insert("goodbye world", 2)
	m.Add(gin)

	ids, ok := m.FullTextSearch("body", "hello")
	if !ok {
		t.Fatal("FullTextSearch should return true")
	}
	if len(ids) != 2 {
		t.Errorf("FullTextSearch('hello') returned %d results, want 2", len(ids))
	}

	ids, ok = m.FullTextSearch("body", "brown fox")
	if !ok {
		t.Fatal("FullTextSearch should return true")
	}
	if len(ids) != 0 {
		t.Errorf("FullTextSearch('brown fox') returned %d results, want 0", len(ids))
	}

	_, ok = m.FullTextSearch("nonexistent", "query")
	if ok {
		t.Fatal("FullTextSearch on nonexistent column should return false")
	}
}

func TestManagerRangeSearch(t *testing.T) {
	m := NewManager()

	bt := NewBTreeIndex("idx_range", "price", 0)
	bt.Insert("10", 0)
	bt.Insert("20", 1)
	bt.Insert("30", 2)
	m.Add(bt)

	ids, ok := m.RangeSearch("price", "10", "25")
	if !ok {
		t.Fatal("RangeSearch should return true")
	}
	if len(ids) < 1 {
		t.Errorf("RangeSearch(10,25) returned %d results, want >= 1", len(ids))
	}

	_, ok = m.RangeSearch("nonexistent", "1", "10")
	if ok {
		t.Fatal("RangeSearch on nonexistent column should return false")
	}
}

func TestManagerRangeSearchGiST(t *testing.T) {
	m := NewManager()

	gist := NewGiSTIndex("idx_range", "col", 0)
	gist.Add(0, "1-5")
	gist.Add(1, "10-20")
	gist.Add(2, "30-50")
	m.Add(gist)

	ids, ok := m.RangeSearchGiST("col", 4, 6)
	if !ok {
		t.Fatal("RangeSearchGiST should return true")
	}
	if len(ids) < 1 {
		t.Errorf("RangeSearchGiST(4,6) returned %d results, want >= 1", len(ids))
	}

	_, ok = m.RangeSearchGiST("nonexistent", 0, 10)
	if ok {
		t.Fatal("RangeSearchGiST on nonexistent column should return false")
	}
}

func TestManagerOverlapSearchGiST(t *testing.T) {
	m := NewManager()

	gist := NewGiSTIndex("idx_overlap", "col", 0)
	gist.Add(0, "1-5")
	gist.Add(1, "10-20")
	m.Add(gist)

	ids, ok := m.OverlapSearchGiST("col", 4, 6)
	if !ok {
		t.Fatal("OverlapSearchGiST should return true")
	}
	if len(ids) < 1 {
		t.Errorf("OverlapSearchGiST(4,6) returned %d results, want >= 1", len(ids))
	}

	_, ok = m.OverlapSearchGiST("nonexistent", 0, 10)
	if ok {
		t.Fatal("OverlapSearchGiST on nonexistent column should return false")
	}
}

func TestManagerSearchJSONBContains(t *testing.T) {
	m := NewManager()

	gin := NewGINJSONBIndex("idx_jsonb", "data", 0)
	gin.Add(0, `{"name": "alice", "age": 25}`)
	gin.Add(1, `{"name": "bob", "age": 30}`)
	m.Add(gin)

	ids, ok := m.SearchJSONBContains("data", `{"name": "alice"}`)
	if !ok {
		t.Fatal("SearchJSONBContains should return true")
	}
	if len(ids) != 1 || ids[0] != 0 {
		t.Errorf("SearchJSONBContains(name:alice) = %v, want [0]", ids)
	}

	_, ok = m.SearchJSONBContains("nonexistent", `{"x": 1}`)
	if ok {
		t.Fatal("SearchJSONBContains on nonexistent column should return false")
	}
}

func TestManagerSearchJSONBHasKey(t *testing.T) {
	m := NewManager()

	gin := NewGINJSONBIndex("idx_jsonb", "data", 0)
	gin.Add(0, `{"name": "alice"}`)
	gin.Add(1, `{"email": "bob@test.com"}`)
	m.Add(gin)

	ids, ok := m.SearchJSONBHasKey("data", "name")
	if !ok {
		t.Fatal("SearchJSONBHasKey should return true")
	}
	if len(ids) != 1 || ids[0] != 0 {
		t.Errorf("SearchJSONBHasKey(name) = %v, want [0]", ids)
	}

	_, ok = m.SearchJSONBHasKey("nonexistent", "key")
	if ok {
		t.Fatal("SearchJSONBHasKey on nonexistent column should return false")
	}
}

func TestManagerNewCompositeByType(t *testing.T) {
	tests := []struct {
		indexType string
		wantType  string
	}{
		{"btree", "composite"},
		{"composite", "composite"},
		{"unknown", "composite"},
	}

	for _, tc := range tests {
		idx := NewCompositeByType("idx", []string{"a", "b"}, []int{0, 1}, tc.indexType)
		if idx == nil {
			t.Errorf("NewCompositeByType(%q) returned nil", tc.indexType)
			continue
		}
		if idx.Type() != tc.wantType {
			t.Errorf("NewCompositeByType(%q).Type() = %q, want %q", tc.indexType, idx.Type(), tc.wantType)
		}
	}
}

func TestManagerEmpty(t *testing.T) {
	m := NewManager()

	all := m.All()
	if len(all) != 0 {
		t.Fatalf("All() on empty manager returned %d, want 0", len(all))
	}

	_, ok := m.FindForColumn("anything")
	if ok {
		t.Fatal("FindForColumn on empty manager should return false")
	}
}

func TestManagerRenameColumnNonexistent(t *testing.T) {
	m := NewManager()

	// Should not panic
	m.RenameColumn("nonexistent", "old", "new")
}

func TestManagerMultipleIndexesSameColumn(t *testing.T) {
	m := NewManager()

	m.Add(New("idx_hash", "email", 0))
	m.Add(NewGINIndex("idx_gin", "email", 0))
	m.Add(NewBTreeIndex("idx_btree", "email", 0))

	idxs, ok := m.FindForColumnMultiple("email")
	if !ok || len(idxs) != 3 {
		t.Fatalf("FindForColumnMultiple('email') = %d indexes, want 3", len(idxs))
	}

	// First returned should be the hash (added first)
	first, ok := m.FindForColumn("email")
	if !ok || first.Type() != "hash" {
		t.Errorf("FindForColumn('email').Type() = %q, want %q", first.Type(), "hash")
	}
}
