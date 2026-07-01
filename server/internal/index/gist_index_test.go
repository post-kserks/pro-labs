package index

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestGiSTIndexType(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "range_col", 2)
	if idx.Type() != "gist" {
		t.Errorf("Type() = %q, want %q", idx.Type(), "gist")
	}
	if idx.Name() != "test_gist" {
		t.Errorf("Name() = %q, want %q", idx.Name(), "test_gist")
	}
	if idx.Column() != "range_col" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "range_col")
	}
	if idx.ColIndex() != 2 {
		t.Errorf("ColIndex() = %d, want 2", idx.ColIndex())
	}
}

func TestGiSTAddAndLookup(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "100")
	idx.Add(1, "200")
	idx.Add(2, "300")

	if positions, ok := idx.Lookup("100"); !ok || len(positions) != 1 || positions[0] != 0 {
		t.Errorf("Lookup(100) = %v, ok=%v, want [0], true", positions, ok)
	}
	if positions, ok := idx.Lookup("200"); !ok || len(positions) != 1 || positions[0] != 1 {
		t.Errorf("Lookup(200) = %v, ok=%v, want [1], true", positions, ok)
	}
	if positions, ok := idx.Lookup("300"); !ok || len(positions) != 1 || positions[0] != 2 {
		t.Errorf("Lookup(300) = %v, ok=%v, want [2], true", positions, ok)
	}
}

func TestGiSTLookupNotFound(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "100")

	if _, ok := idx.Lookup("999"); ok {
		t.Error("Lookup(999) should return false for non-existent key")
	}
}

func TestGiSTRemove(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "100")
	idx.Add(1, "200")
	idx.Add(2, "300")

	idx.Remove(1)

	if _, ok := idx.Lookup("200"); ok {
		t.Error("Lookup(200) should return false after Remove")
	}
	if positions, ok := idx.Lookup("100"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(100) after remove = %v, want [0]", positions)
	}
	if positions, ok := idx.Lookup("300"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(300) after remove = %v, want [2]", positions)
	}
}

func TestGiSTSearchRange(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "1-5")
	idx.Add(1, "3-8")
	idx.Add(2, "10-15")

	// Search [4, 6]: should match "1-5" (max 5 >= 4, min 1 <= 6) and "3-8" (max 8 >= 4, min 3 <= 6)
	positions := idx.SearchRange(4, 6)
	if len(positions) != 2 {
		t.Errorf("SearchRange(4, 6) returned %d positions, want 2", len(positions))
	}

	// Search [9, 11]: should match "10-15" only
	positions = idx.SearchRange(9, 11)
	if len(positions) != 1 {
		t.Errorf("SearchRange(9, 11) returned %d positions, want 1", len(positions))
	}

	// Search [0, 0.5]: should match nothing
	positions = idx.SearchRange(0, 0.5)
	if len(positions) != 0 {
		t.Errorf("SearchRange(0, 0.5) returned %d positions, want 0", len(positions))
	}
}

func TestGiSTSearchOverlap(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "1-5")
	idx.Add(1, "3-8")
	idx.Add(2, "10-15")

	// Overlap [4, 6]: matches "1-5" (1<=6 && 5>=4) and "3-8" (3<=6 && 8>=4)
	positions := idx.SearchOverlap(4, 6)
	if len(positions) != 2 {
		t.Errorf("SearchOverlap(4, 6) returned %d positions, want 2", len(positions))
	}

	// Overlap [9, 11]: matches "10-15" (10<=11 && 15>=9)
	positions = idx.SearchOverlap(9, 11)
	if len(positions) != 1 {
		t.Errorf("SearchOverlap(9, 11) returned %d positions, want 1", len(positions))
	}

	// Overlap [16, 20]: no overlap with any
	positions = idx.SearchOverlap(16, 20)
	if len(positions) != 0 {
		t.Errorf("SearchOverlap(16, 20) returned %d positions, want 0", len(positions))
	}
}

func TestGiSTRebuild(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "old")
	idx.Add(1, "data")

	rows := []IndexableRow{
		{DeletedTx: 0, Data: []interface{}{"new-10"}},
		{DeletedTx: 0, Data: []interface{}{"20-30"}},
		{DeletedTx: 1, Data: []interface{}{"deleted"}},
		{DeletedTx: 0, Data: []interface{}{"new-40"}},
	}
	idx.Rebuild(rows)

	// Old data gone
	if _, ok := idx.Lookup("old"); ok {
		t.Error("Lookup(old) should return false after rebuild")
	}

	// New non-deleted data present
	if positions, ok := idx.Lookup("new-10"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(new-10) after rebuild = %v, want [0]", positions)
	}
	if positions, ok := idx.Lookup("20-30"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(20-30) after rebuild = %v, want [1]", positions)
	}
	// Deleted row (DeletedTx=1) should be skipped
	if _, ok := idx.Lookup("deleted"); ok {
		t.Error("Lookup(deleted) should return false for row with DeletedTx=1")
	}
	if positions, ok := idx.Lookup("new-40"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(new-40) after rebuild = %v, want [2]", positions)
	}
}

func TestGiSTSaveLoad(t *testing.T) {
	idx := NewGiSTIndex("save_test", "range_col", 3)
	idx.Add(0, "1-5")
	idx.Add(1, "10-20")
	idx.Add(2, "100")

	tmpFile, err := os.CreateTemp("", "gist_test_*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	if err := idx.Save(tmpFile.Name()); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded := NewGiSTIndex("", "", 0)
	if err := loaded.Load(tmpFile.Name()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.Name() != "save_test" {
		t.Errorf("Loaded Name() = %q, want %q", loaded.Name(), "save_test")
	}
	if loaded.Column() != "range_col" {
		t.Errorf("Loaded Column() = %q, want %q", loaded.Column(), "range_col")
	}
	if loaded.ColIndex() != 3 {
		t.Errorf("Loaded ColIndex() = %d, want 3", loaded.ColIndex())
	}

	// Note: entries are not persisted by Save/Load (only metadata).
	// The Load method restores name, column, colIndex but not the entries slice.
	if loaded.Type() != "gist" {
		t.Errorf("Loaded Type() = %q, want %q", loaded.Type(), "gist")
	}
}

func TestGiSTParseRange(t *testing.T) {
	tests := []struct {
		input   string
		wantMin float64
		wantMax float64
	}{
		{"5", 5, 5},
		{"1.5-3.0", 1.5, 3.0},
		{"abc", 0, 0},
		{"", 0, 0},
		{"0", 0, 0},
		{"10-20", 10, 20},
		{"-1.5-2.5", -1.5, 2.5},
	}

	for _, tt := range tests {
		min, max := gistParseRange(tt.input)
		if min != tt.wantMin || max != tt.wantMax {
			t.Errorf("gistParseRange(%q) = (%v, %v), want (%v, %v)",
				tt.input, min, max, tt.wantMin, tt.wantMax)
		}
	}
}

func TestGiSTConcurrentAccess(t *testing.T) {
	idx := NewGiSTIndex("concurrent_test", "col", 0)
	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Add(n, n*10)
		}(i)
	}

	// Concurrent readers (will return empty since adds may not have completed)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Lookup(fmt.Sprintf("%d", n))
		}(i)
	}

	// Concurrent range searches
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n float64) {
			defer wg.Done()
			idx.SearchRange(n, n+100)
			idx.SearchOverlap(n, n+100)
		}(float64(i))
	}

	wg.Wait()

	// After all goroutines finish, verify some entries exist
	// At least some of the 50 adds should be present
	total := 0
	for i := 0; i < 50; i++ {
		if _, ok := idx.Lookup(fmt.Sprintf("%d", i*10)); ok {
			total++
		}
	}
	if total == 0 {
		t.Error("Expected at least some entries to be present after concurrent access")
	}
}

func TestGiSTEmptyIndex(t *testing.T) {
	idx := NewGiSTIndex("empty", "col", 0)

	_, ok := idx.Lookup("anything")
	if ok {
		t.Fatal("Lookup on empty index should return false")
	}

	positions := idx.SearchRange(0, 100)
	if len(positions) != 0 {
		t.Errorf("SearchRange on empty index should return empty, got %v", positions)
	}

	positions = idx.SearchOverlap(0, 100)
	if len(positions) != 0 {
		t.Errorf("SearchOverlap on empty index should return empty, got %v", positions)
	}
}

func TestGiSTInsertDeleteInterface(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Insert("100-200", 0)
	idx.Insert("300-400", 1)

	if _, ok := idx.Lookup("100-200"); !ok {
		t.Fatal("Lookup(100-200) should return true after Insert")
	}

	idx.Delete(0)

	_, ok := idx.Lookup("100-200")
	if ok {
		t.Fatal("Lookup(100-200) should return false after Delete")
	}

	positions, ok := idx.Lookup("300-400")
	if !ok || len(positions) != 1 {
		t.Fatalf("Lookup(300-400) = %v, %v; want [1], true", positions, ok)
	}
}

func TestGiSTRenameColumn(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "old_col", 0)
	idx.RenameColumn("old_col", "new_col")

	if idx.Column() != "new_col" {
		t.Errorf("Column() = %q, want %q", idx.Column(), "new_col")
	}
}

func TestGiSTLargeDataset(t *testing.T) {
	idx := NewGiSTIndex("large", "col", 0)

	n := 500
	for i := 0; i < n; i++ {
		min := float64(i * 10)
		max := float64(i*10 + 5)
		idx.Add(i, fmt.Sprintf("%g-%g", min, max))
	}

	// Verify all entries are findable
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("%g-%g", float64(i*10), float64(i*10+5))
		if _, ok := idx.Lookup(key); !ok {
			t.Errorf("Lookup(%s) should return true", key)
		}
	}

	// Range search: [50, 55] should match entry 5 (50-55)
	positions := idx.SearchRange(50, 55)
	if len(positions) < 1 {
		t.Errorf("SearchRange(50, 55) should find at least 1 result, got %d", len(positions))
	}
}

func TestGiSTNilValue(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, nil)

	// Nil value gets stringified to ""
	if _, ok := idx.Lookup(""); !ok {
		t.Error("Lookup('') should find nil-added entry")
	}
}

func TestGiSTRebuildEmpty(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)
	idx.Add(0, "100-200")

	rows := []IndexableRow{}
	idx.Rebuild(rows)

	_, ok := idx.Lookup("100-200")
	if ok {
		t.Fatal("Lookup should return false after rebuild with empty rows")
	}

	positions := idx.SearchRange(0, 200)
	if len(positions) != 0 {
		t.Errorf("SearchRange on empty rebuild should return empty, got %v", positions)
	}
}

func TestGiSTRemoveNonExistent(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	// Removing from empty index should not panic
	idx.Remove(0)
	idx.Remove(999)
}

func TestGiSTSearchRangeExact(t *testing.T) {
	idx := NewGiSTIndex("test_gist", "col", 0)

	idx.Add(0, "5-5") // single point
	idx.Add(1, "10-20")
	idx.Add(2, "15-15")

	positions := idx.SearchRange(5, 5)
	if len(positions) != 1 || positions[0] != 0 {
		t.Errorf("SearchRange(5,5) = %v, want [0]", positions)
	}

	positions = idx.SearchRange(10, 15)
	if len(positions) < 1 {
		t.Errorf("SearchRange(10,15) should find results")
	}
}
