package index

import (
	"os"
	"path/filepath"
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
