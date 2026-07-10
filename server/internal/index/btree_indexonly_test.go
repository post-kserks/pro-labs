package index

import (
	"fmt"
	"testing"
)

func TestBTreeIndexOnlyScanBasic(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Insert values with stored columns
	idx.InsertWithColumns("10", 0, map[string]interface{}{
		"id":    int64(10),
		"name":  "Alice",
		"level": int64(5),
	})
	idx.InsertWithColumns("20", 1, map[string]interface{}{
		"id":    int64(20),
		"name":  "Bob",
		"level": int64(8),
	})
	idx.InsertWithColumns("30", 2, map[string]interface{}{
		"id":    int64(30),
		"name":  "Charlie",
		"level": int64(3),
	})

	// Verify stored columns exist
	if !idx.HasStoredColumns() {
		t.Error("HasStoredColumns() should return true")
	}

	// Verify Columns() returns column names
	cols := idx.Columns()
	if len(cols) != 3 {
		t.Errorf("Columns() returned %d columns, want 3", len(cols))
	}

	// Test LookupWithColumns
	positions, storedCols, ok := idx.LookupWithColumns("20")
	if !ok {
		t.Fatal("LookupWithColumns(20) should return true")
	}
	if len(positions) != 1 || positions[0] != 1 {
		t.Errorf("LookupWithColumns(20) positions = %v, want [1]", positions)
	}
	if cols, ok := storedCols[1]; !ok {
		t.Error("LookupWithColumns(20) should return stored columns")
	} else if cols["name"] != "Bob" {
		t.Errorf("LookupWithColumns(20) storedCols[1][name] = %v, want 'Bob'", cols["name"])
	}

	// Test RangeWithColumns
	positions, storedCols = idx.RangeWithColumns("10", "20")
	if len(positions) != 2 {
		t.Errorf("RangeWithColumns(10,20) returned %d positions, want 2", len(positions))
	}
	if _, ok := storedCols[0]; !ok {
		t.Error("RangeWithColumns should return stored columns for position 0")
	}
	if _, ok := storedCols[1]; !ok {
		t.Error("RangeWithColumns should return stored columns for position 1")
	}
}

func TestBTreeIndexOnlyScanMultipleValuesPerKey(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "category", 0)

	// Insert multiple rows with same key
	idx.InsertWithColumns("electronics", 0, map[string]interface{}{
		"category": "electronics",
		"name":     "Laptop",
		"price":    float64(999.99),
	})
	idx.InsertWithColumns("electronics", 1, map[string]interface{}{
		"category": "electronics",
		"name":     "Phone",
		"price":    float64(499.99),
	})
	idx.InsertWithColumns("clothing", 2, map[string]interface{}{
		"category": "clothing",
		"name":     "Shirt",
		"price":    float64(29.99),
	})

	// Lookup should return both electronics rows
	positions, storedCols, ok := idx.LookupWithColumns("electronics")
	if !ok {
		t.Fatal("LookupWithColumns('electronics') should return true")
	}
	if len(positions) != 2 {
		t.Errorf("LookupWithColumns('electronics') returned %d positions, want 2", len(positions))
	}

	// Verify stored columns
	for _, pos := range positions {
		if cols, ok := storedCols[pos]; !ok {
			t.Errorf("Stored columns not found for position %d", pos)
		} else if cols["category"] != "electronics" {
			t.Errorf("Stored columns for position %d have wrong category", pos)
		}
	}
}

func TestBTreeIndexOnlyScanSerialization(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Insert values with stored columns
	idx.InsertWithColumns("10", 0, map[string]interface{}{
		"id":   int64(10),
		"name": "Alice",
	})
	idx.InsertWithColumns("20", 1, map[string]interface{}{
		"id":   int64(20),
		"name": "Bob",
	})

	// Save to temp file
	tmpFile := t.TempDir() + "/test_index.json"
	if err := idx.Save(tmpFile); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load from file
	loaded, err := LoadBTreeIndex(tmpFile)
	if err != nil {
		t.Fatalf("LoadBTreeIndex() error: %v", err)
	}

	// Verify stored columns were preserved
	if !loaded.HasStoredColumns() {
		t.Error("Loaded index should have stored columns")
	}

	positions, storedCols, ok := loaded.LookupWithColumns("10")
	if !ok {
		t.Fatal("LookupWithColumns('10') on loaded index should return true")
	}
	if len(positions) != 1 {
		t.Errorf("LookupWithColumns('10') returned %d positions, want 1", len(positions))
	}
	if cols, ok := storedCols[0]; !ok {
		t.Error("Stored columns not found in loaded index")
	} else if cols["name"] != "Alice" {
		t.Errorf("Loaded storedCols[0][name] = %v, want 'Alice'", cols["name"])
	}
}

func TestBTreeIndexOnlyScanDelete(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Insert values
	idx.InsertWithColumns("10", 0, map[string]interface{}{"id": int64(10)})
	idx.InsertWithColumns("20", 1, map[string]interface{}{"id": int64(20)})

	// Delete one row
	idx.Delete(0)

	// Verify stored columns are cleaned up
	if _, ok := idx.GetStoredColumns(0); ok {
		t.Error("GetStoredColumns(0) should return false after delete")
	}

	// Verify the other row still has stored columns
	if _, ok := idx.GetStoredColumns(1); !ok {
		t.Error("GetStoredColumns(1) should return true")
	}
}

func TestBTreeIndexOnlyScanBatchInsert(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Use batch insert
	batch := map[int]map[string]interface{}{
		0: {"id": int64(1), "name": "Alice"},
		1: {"id": int64(2), "name": "Bob"},
		2: {"id": int64(3), "name": "Charlie"},
	}
	idx.StoreColumnsBatch(batch)

	// Verify all stored
	for pos := 0; pos < 3; pos++ {
		if _, ok := idx.GetStoredColumns(pos); !ok {
			t.Errorf("GetStoredColumns(%d) should return true", pos)
		}
	}
}

func TestBTreeIndexOnlyScanLargeDataset(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Insert 1000 values with stored columns
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("%04d", i)
		idx.InsertWithColumns(key, i, map[string]interface{}{
			"id":    int64(i),
			"name":  fmt.Sprintf("User%d", i),
			"score": float64(i) * 1.5,
		})
	}

	// Verify all stored
	if !idx.HasStoredColumns() {
		t.Error("HasStoredColumns() should return true")
	}

	// Test range query
	positions, storedCols := idx.RangeWithColumns("0100", "0200")
	if len(positions) != 101 {
		t.Errorf("RangeWithColumns returned %d positions, want 101", len(positions))
	}

	// Verify stored columns for range
	for _, pos := range positions {
		if _, ok := storedCols[pos]; !ok {
			t.Errorf("Stored columns not found for position %d", pos)
		}
	}
}

func TestBTreeIndexOnlyScanNoStoredColumns(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Insert without stored columns
	idx.Insert("10", 0)
	idx.Insert("20", 1)

	// Should not have stored columns
	if idx.HasStoredColumns() {
		t.Error("HasStoredColumns() should return false without stored columns")
	}

	// Columns() should return nil
	if cols := idx.Columns(); cols != nil {
		t.Errorf("Columns() = %v, want nil", cols)
	}

	// LookupWithColumns still returns positions (empty stored cols map)
	positions, storedCols, ok := idx.LookupWithColumns("10")
	if !ok {
		t.Error("LookupWithColumns('10') should return true for key lookup")
	}
	if len(positions) != 1 {
		t.Errorf("LookupWithColumns('10') returned %d positions, want 1", len(positions))
	}
	if len(storedCols) != 0 {
		t.Errorf("LookupWithColumns('10') storedCols should be empty, got %d entries", len(storedCols))
	}
}

func BenchmarkBTreeIndexOnlyScan(b *testing.B) {
	idx := NewBTreeIndex("bench_idx", "id", 0)

	// Insert 10000 values with stored columns
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("%06d", i)
		idx.InsertWithColumns(key, i, map[string]interface{}{
			"id":    int64(i),
			"name":  fmt.Sprintf("User%d", i),
			"email": fmt.Sprintf("user%d@example.com", i),
			"score": float64(i) * 1.5,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("%06d", i%10000)
		idx.LookupWithColumns(key)
	}
}

func BenchmarkBTreeRangeWithColumns(b *testing.B) {
	idx := NewBTreeIndex("bench_idx", "id", 0)

	// Insert 10000 values with stored columns
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("%06d", i)
		idx.InsertWithColumns(key, i, map[string]interface{}{
			"id":    int64(i),
			"name":  fmt.Sprintf("User%d", i),
			"email": fmt.Sprintf("user%d@example.com", i),
			"score": float64(i) * 1.5,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		low := fmt.Sprintf("%06d", (i*100)%10000)
		high := fmt.Sprintf("%06d", ((i*100)+99)%10000)
		idx.RangeWithColumns(low, high)
	}
}
