package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompositeIndexMetadata(t *testing.T) {
	idx := NewCompositeIndex("comp_idx", []string{"a", "b"}, []int{0, 1})

	if idx.Type() != "composite" {
		t.Errorf("Type() = %q, want %q", idx.Type(), "composite")
	}
	if idx.Name() != "comp_idx" {
		t.Errorf("Name() = %q, want %q", idx.Name(), "comp_idx")
	}
	if idx.Column() != "a,b" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "a,b")
	}
	if idx.ColIndex() != 0 {
		t.Errorf("ColIndex() = %d, want 0", idx.ColIndex())
	}
}

func TestCompositeIndexInsertLookup(t *testing.T) {
	idx := NewCompositeIndex("test_comp", []string{"name", "age"}, []int{0, 1})

	idx.Insert("alice\x0025", 0)
	idx.Insert("bob\x0030", 1)
	idx.Insert("alice\x0025", 2)

	positions, ok := idx.Lookup("alice\x0025")
	if !ok {
		t.Fatal("Lookup(alice\\x0025) should return true")
	}
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions, got %d: %v", len(positions), positions)
	}

	positions, ok = idx.Lookup("bob\x0030")
	if !ok || len(positions) != 1 || positions[0] != 1 {
		t.Fatalf("Lookup(bob\\x0030) = %v, %v; want [1], true", positions, ok)
	}

	_, ok = idx.Lookup("nonexistent")
	if ok {
		t.Fatal("Lookup(nonexistent) should return false")
	}
}

func TestCompositeIndexDelete(t *testing.T) {
	idx := NewCompositeIndex("test_comp", []string{"a", "b"}, []int{0, 1})

	idx.Insert("x\x001", 0)
	idx.Insert("y\x002", 1)

	idx.Delete(0)

	_, ok := idx.Lookup("x\x001")
	if ok {
		t.Fatal("Lookup(x\\x001) should return false after Delete")
	}

	positions, ok := idx.Lookup("y\x002")
	if !ok || len(positions) != 1 {
		t.Fatalf("Lookup(y\\x002) after delete = %v, %v; want [1], true", positions, ok)
	}

	// Deleting non-existent row should not panic
	idx.Delete(999)
}

func TestCompositeIndexDeleteLastForKey(t *testing.T) {
	idx := NewCompositeIndex("test_comp", []string{"a"}, []int{0})

	idx.Insert("key1", 0)
	idx.Delete(0)

	_, ok := idx.Lookup("key1")
	if ok {
		t.Fatal("Lookup(key1) should return false after deleting all rows for key")
	}
}

func TestCompositeIndexRebuild(t *testing.T) {
	idx := NewCompositeIndex("test_comp", []string{"name", "status"}, []int{0, 1})

	idx.Insert("old\x00inactive", 0)

	rows := []IndexableRow{
		{DeletedTx: 0, Data: []interface{}{"alice", "active"}},
		{DeletedTx: 1, Data: []interface{}{"bob", "deleted"}},
		{DeletedTx: 0, Data: []interface{}{"alice", "pending"}},
	}
	idx.Rebuild(rows)

	_, ok := idx.Lookup("old\x00inactive")
	if ok {
		t.Fatal("Lookup(old) should return false after Rebuild")
	}

	_, ok = idx.Lookup("bob\x00deleted")
	if ok {
		t.Fatal("Lookup(bob\\x00deleted) should return false (DeletedTx != 0)")
	}

	positions, ok := idx.Lookup("alice\x00active")
	if !ok || len(positions) != 1 {
		t.Fatalf("Lookup(alice\\x00active) = %v, %v; want [0], true", positions, ok)
	}

	positions, ok = idx.Lookup("alice\x00pending")
	if !ok || len(positions) != 1 {
		t.Fatalf("Lookup(alice\\x00pending) = %v, %v; want [2], true", positions, ok)
	}
}

func TestCompositeIndexRenameColumn(t *testing.T) {
	idx := NewCompositeIndex("test_comp", []string{"old_name", "age"}, []int{0, 1})

	idx.RenameColumn("old_name", "new_name")
	if idx.Column() != "new_name,age" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "new_name,age")
	}

	// Renaming non-existent column should be no-op
	idx.RenameColumn("nonexistent", "something")
	if idx.Column() != "new_name,age" {
		t.Errorf("Column() after noop rename = %q, want %q", idx.Column(), "new_name,age")
	}
}

func TestCompositeIndexSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "composite.json")

	idx := NewCompositeIndex("save_test", []string{"a", "b"}, []int{0, 1})
	idx.Insert("val1\x00val2", 0)
	idx.Insert("val3\x00val4", 1)

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
}

func TestCompositeIndexEmpty(t *testing.T) {
	idx := NewCompositeIndex("empty", []string{"col"}, []int{0})

	_, ok := idx.Lookup("anything")
	if ok {
		t.Fatal("Lookup on empty index should return false")
	}
}

func TestCompositeIndexLargeDataset(t *testing.T) {
	idx := NewCompositeIndex("large", []string{"a", "b"}, []int{0, 1})

	n := 1000
	for i := 0; i < n; i++ {
		key := formatIndexValue(i) + "\x00" + formatIndexValue(i*2)
		idx.Insert(key, i)
	}

	// Verify all keys are findable
	for i := 0; i < n; i++ {
		key := formatIndexValue(i) + "\x00" + formatIndexValue(i*2)
		positions, ok := idx.Lookup(key)
		if !ok || len(positions) != 1 || positions[0] != i {
			t.Errorf("Lookup(%d) = %v, %v; want [%d], true", i, positions, ok, i)
		}
	}

	// Delete half
	for i := 0; i < n/2; i++ {
		idx.Delete(i)
	}

	for i := 0; i < n/2; i++ {
		key := formatIndexValue(i) + "\x00" + formatIndexValue(i*2)
		if _, ok := idx.Lookup(key); ok {
			t.Errorf("Lookup(%d) should return false after delete", i)
		}
	}

	for i := n / 2; i < n; i++ {
		key := formatIndexValue(i) + "\x00" + formatIndexValue(i*2)
		if _, ok := idx.Lookup(key); !ok {
			t.Errorf("Lookup(%d) should return true after delete of other keys", i)
		}
	}
}

func TestFormatIndexValue(t *testing.T) {
	tests := []struct {
		input interface{}
		check func(string) bool
		desc  string
	}{
		{nil, func(s string) bool { return s == "" }, "nil returns empty"},
		{"hello", func(s string) bool { return s == "hello" }, "string passthrough"},
		{42, func(s string) bool { return len(s) > 0 }, "int formats"},
		{3.14, func(s string) bool { return len(s) > 0 }, "float formats"},
		{true, func(s string) bool { return s == "1" }, "true returns 1"},
		{false, func(s string) bool { return s == "0" }, "false returns 0"},
	}

	for _, tc := range tests {
		result := formatIndexValue(tc.input)
		if !tc.check(result) {
			t.Errorf("formatIndexValue(%v) = %q failed check: %s", tc.input, result, tc.desc)
		}
	}
}
