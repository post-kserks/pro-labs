package index

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestGINInsertLookup(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Insert("hello world", 0)
	idx.Insert("hello go", 1)
	idx.Insert("goodbye world", 2)

	positions, ok := idx.Lookup("hello")
	if !ok {
		t.Fatal("Lookup(hello) should return true")
	}
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions for 'hello', got %d: %v", len(positions), positions)
	}

	positions, ok = idx.Lookup("world")
	if !ok {
		t.Fatal("Lookup(world) should return true")
	}
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions for 'world', got %d: %v", len(positions), positions)
	}

	positions, ok = idx.Lookup("goodbye")
	if !ok || len(positions) != 1 || positions[0] != 2 {
		t.Fatalf("Lookup(goodbye) = %v, %v; want [2], true", positions, ok)
	}
}

func TestGINSearch(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Insert("quick brown fox jumps", 0)
	idx.Insert("lazy brown dog sleeps", 1)
	idx.Insert("quick red fox runs", 2)

	ids := idx.Search("brown fox")
	if len(ids) != 1 {
		t.Fatalf("Search('brown fox') returned %d results, want 1", len(ids))
	}
	if ids[0] != 0 {
		t.Fatalf("Search('brown fox') = %v, want [0]", ids)
	}

	ids = idx.Search("quick")
	if len(ids) != 2 {
		t.Fatalf("Search('quick') returned %d results, want 2", len(ids))
	}
}

func TestGINDelete(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Insert("hello world", 0)
	idx.Insert("hello go", 1)

	idx.Delete(0)

	positions, ok := idx.Lookup("hello")
	if !ok {
		t.Fatal("Lookup(hello) should return true after deleting row 0")
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position for 'hello' after delete, got %d", len(positions))
	}

	if _, ok := idx.Lookup("world"); ok {
		t.Fatal("Lookup(world) should return false after deleting row 0")
	}
}

func TestGINSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gin_index.json")

	idx := NewGINIndex("test_gin", "body", 0)
	idx.Insert("hello world", 0)
	idx.Insert("foo bar", 1)

	if err := idx.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("saved file is empty")
	}

	loaded := NewGINIndex("other", "other", 0)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Name() != "test_gin" {
		t.Errorf("loaded Name() = %q, want %q", loaded.Name(), "test_gin")
	}
	if loaded.Column() != "body" {
		t.Errorf("loaded Column() = %q, want %q", loaded.Column(), "body")
	}
	if loaded.ColIndex() != 0 {
		t.Errorf("loaded ColIndex() = %d, want 0", loaded.ColIndex())
	}
}

func TestGINMetadata(t *testing.T) {
	idx := NewGINIndex("gin_idx", "content", 2)

	if idx.Type() != "gin" {
		t.Errorf("Type() = %q, want %q", idx.Type(), "gin")
	}
	if idx.Name() != "gin_idx" {
		t.Errorf("Name() = %q, want %q", idx.Name(), "gin_idx")
	}

	idx.RenameColumn("content", "text")
	if idx.Column() != "text" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "text")
	}
}

func TestGINRebuild(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)
	idx.Insert("old data", 0)

	rows := []IndexableRow{
		{DeletedTx: 0, Data: []interface{}{"new data here"}},
		{DeletedTx: 1, Data: []interface{}{"deleted row"}},
		{DeletedTx: 0, Data: []interface{}{"another new entry"}},
	}
	idx.Rebuild(rows)

	if _, ok := idx.Lookup("old"); ok {
		t.Fatal("Lookup(old) should return false after rebuild")
	}
	if _, ok := idx.Lookup("deleted"); ok {
		t.Fatal("Lookup(deleted) should return false (DeletedTx != 0)")
	}
	positions, ok := idx.Lookup("new")
	if !ok || len(positions) < 1 {
		t.Fatalf("Lookup(new) should return results after rebuild, got %v, %v", positions, ok)
	}
}

func TestGINLookupEmpty(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	_, ok := idx.Lookup("anything")
	if ok {
		t.Fatal("Lookup on empty index should return false")
	}

	_, ok = idx.Lookup("")
	if ok {
		t.Fatal("Lookup('') should return false")
	}
}

func TestGINSearchEmptyQuery(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)
	idx.Insert("hello world", 0)

	ids := idx.Search("")
	if len(ids) != 0 {
		t.Errorf("Search('') should return empty, got %v", ids)
	}
}

func TestGINDuplicateTokens(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Insert("hello hello hello", 0)

	positions, ok := idx.Lookup("hello")
	if !ok || len(positions) != 1 {
		t.Fatalf("Lookup(hello) with duplicate tokens = %v, %v; want [0], true", positions, ok)
	}
}

func TestGINMultiWordSearch(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Insert("the quick brown fox", 0)
	idx.Insert("the lazy brown dog", 1)
	idx.Insert("quick brown bear", 2)

	ids := idx.Search("brown fox")
	if len(ids) != 1 || ids[0] != 0 {
		t.Errorf("Search('brown fox') = %v, want [0]", ids)
	}

	ids = idx.Search("the brown")
	if len(ids) != 2 {
		t.Errorf("Search('the brown') returned %d results, want 2", len(ids))
	}
}

func TestGINLargeDataset(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	n := 500
	for i := 0; i < n; i++ {
		idx.Insert("word"+string(rune('a'+i%26))+" doc"+string(rune('0'+i/26%10)), i)
	}

	// Search for a specific word
	positions, ok := idx.Lookup("worda")
	if !ok || len(positions) == 0 {
		t.Fatal("Lookup(worda) should return results")
	}

	// Search for a less common term
	positions, ok = idx.Lookup("wordz")
	if !ok {
		t.Fatal("Lookup(wordz) should return results")
	}
	if len(positions) == 0 {
		t.Fatal("Lookup(wordz) returned empty")
	}
}

func TestGINConcurrentAccess(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Insert("word test "+string(rune('a'+n%26)), n)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Search("word")
			idx.Lookup("test")
		}(i)
	}

	wg.Wait()
}

func TestGINJSONBIndexMetadata(t *testing.T) {
	idx := NewGINJSONBIndex("test_jsonb", "data", 1)

	if idx.Type() != "gin" {
		t.Errorf("Type() = %q, want %q", idx.Type(), "gin")
	}
	if idx.Name() != "test_jsonb" {
		t.Errorf("Name() = %q, want %q", idx.Name(), "test_jsonb")
	}
	if idx.Column() != "data" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "data")
	}
}

func TestGINJSONBSearchContains(t *testing.T) {
	idx := NewGINJSONBIndex("test_jsonb", "data", 0)

	idx.Add(0, `{"name": "alice", "age": 25}`)
	idx.Add(1, `{"name": "bob", "age": 30}`)
	idx.Add(2, `{"name": "alice", "city": "NYC"}`)

	ids := idx.SearchJSONBContains(`{"name": "alice"}`)
	if len(ids) != 2 {
		t.Errorf("SearchJSONBContains(name:alice) returned %d results, want 2", len(ids))
	}

	ids = idx.SearchJSONBContains(`{"age": 30}`)
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("SearchJSONBContains(age:30) = %v, want [1]", ids)
	}

	ids = idx.SearchJSONBContains(`{"city": "NYC"}`)
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("SearchJSONBContains(city:NYC) = %v, want [2]", ids)
	}
}

func TestGINJSONBSearchHasKey(t *testing.T) {
	idx := NewGINJSONBIndex("test_jsonb", "data", 0)

	idx.Add(0, `{"name": "alice", "age": 25}`)
	idx.Add(1, `{"name": "bob"}`)
	idx.Add(2, `{"email": "carol@test.com"}`)

	ids := idx.SearchJSONBHasKey("name")
	if len(ids) != 2 {
		t.Errorf("SearchJSONBHasKey(name) returned %d results, want 2", len(ids))
	}

	ids = idx.SearchJSONBHasKey("email")
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("SearchJSONBHasKey(email) = %v, want [2]", ids)
	}

	ids = idx.SearchJSONBHasKey("nonexistent")
	if len(ids) != 0 {
		t.Errorf("SearchJSONBHasKey(nonexistent) = %v, want []", ids)
	}
}

func TestGINJSONBContainsNonJSONB(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)
	idx.Add(0, "hello world")

	ids := idx.SearchJSONBContains("hello")
	if ids != nil {
		t.Errorf("SearchJSONBContains on non-jsonb index should return nil, got %v", ids)
	}
}

func TestGINJSONBHasKeyNonJSONB(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)
	idx.Add(0, "hello world")

	ids := idx.SearchJSONBHasKey("key")
	if ids != nil {
		t.Errorf("SearchJSONBHasKey on non-jsonb index should return nil, got %v", ids)
	}
}

func TestGINJSONBSearch(t *testing.T) {
	idx := NewGINJSONBIndex("test_jsonb", "data", 0)

	idx.Add(0, `{"type": "error", "message": "timeout"}`)
	idx.Add(1, `{"type": "info", "message": "connected"}`)
	idx.Add(2, `{"type": "error", "code": 500}`)

	ids := idx.Search("error")
	if len(ids) != 2 {
		t.Errorf("Search('error') returned %d results, want 2", len(ids))
	}

	ids = idx.Search("timeout")
	if len(ids) != 1 || ids[0] != 0 {
		t.Errorf("Search('timeout') = %v, want [0]", ids)
	}
}

func TestGINJSONBRemove(t *testing.T) {
	idx := NewGINJSONBIndex("test_jsonb", "data", 0)

	idx.Add(0, `{"key": "value1"}`)
	idx.Add(1, `{"key": "value2"}`)

	idx.Remove(0)

	ids := idx.SearchJSONBHasKey("key")
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("SearchJSONBHasKey after remove = %v, want [1]", ids)
	}
}

func TestGINJSONBRebuild(t *testing.T) {
	idx := NewGINJSONBIndex("test_jsonb", "data", 0)

	idx.Add(0, `{"old": true}`)

	rows := []IndexableRow{
		{DeletedTx: 0, Data: []interface{}{`{"new": "data"}`}},
		{DeletedTx: 1, Data: []interface{}{`{"deleted": true}`}},
		{DeletedTx: 0, Data: []interface{}{`{"new": "more"}`}},
	}
	idx.Rebuild(rows)

	ids := idx.SearchJSONBHasKey("old")
	if len(ids) != 0 {
		t.Errorf("SearchJSONBHasKey(old) after rebuild should be empty, got %v", ids)
	}

	ids = idx.SearchJSONBHasKey("new")
	if len(ids) != 2 {
		t.Errorf("SearchJSONBHasKey(new) after rebuild = %d results, want 2", len(ids))
	}
}

func TestGINInsertDeleteInterface(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Insert("hello", 0)
	idx.Insert("world", 1)

	idx.Delete(0)

	_, ok := idx.Lookup("hello")
	if ok {
		t.Fatal("Lookup(hello) should return false after Delete")
	}

	positions, ok := idx.Lookup("world")
	if !ok || len(positions) != 1 {
		t.Fatalf("Lookup(world) = %v, %v; want [1], true", positions, ok)
	}
}

func TestGINAddWithValueTypes(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	idx.Add(0, 42)
	idx.Add(1, 3.14)
	idx.Add(2, true)
	idx.Add(3, nil)

	// integer value "42"
	positions, ok := idx.Lookup("42")
	if !ok || len(positions) != 1 {
		t.Errorf("Lookup(42) = %v, %v; want [0], true", positions, ok)
	}

	// float value
	positions, ok = idx.Lookup("3.14")
	if !ok || len(positions) != 1 {
		t.Errorf("Lookup(3.14) = %v, %v; want [1], true", positions, ok)
	}

	// bool true -> "true"
	positions, ok = idx.Lookup("true")
	if !ok || len(positions) != 1 {
		t.Errorf("Lookup(true) = %v, %v; want [2], true", positions, ok)
	}
}

func TestGINRemoveNonExistent(t *testing.T) {
	idx := NewGINIndex("test_gin", "body", 0)

	// Removing from empty index should not panic
	idx.Remove(0)
	idx.Remove(999)
}

func TestGINTokenizeJSONB(t *testing.T) {
	tests := []struct {
		input   string
		wantLen int
		desc    string
	}{
		{`{"a": 1, "b": 2}`, 5, "object with two keys"}, // key:a, 1, key:b, 2
		{`[1, 2, 3]`, 3, "array of ints"},
		{`{"nested": {"inner": true}}`, 4, "nested object"}, // key:nested, key:inner, true
		{`"plain string"`, 1, "plain string"},
		{``, 0, "empty string"},
	}

	for _, tc := range tests {
		tokens := tokenizeJSONB(tc.input)
		if tc.wantLen > 0 && len(tokens) == 0 {
			t.Errorf("tokenizeJSONB(%s) returned empty tokens", tc.desc)
		}
	}
}
