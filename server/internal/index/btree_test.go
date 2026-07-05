package index

import (
	"fmt"
	"sync"
	"testing"
)

func TestBTreeInsertAndLookup(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Вставляем значения
	idx.Insert("10", 0)
	idx.Insert("20", 1)
	idx.Insert("30", 2)
	idx.Insert("5", 3)
	idx.Insert("15", 4)

	// Ищем существующие значения
	if positions, ok := idx.Lookup("10"); !ok || len(positions) != 1 || positions[0] != 0 {
		t.Errorf("Lookup(10) = %v, want [0]", positions)
	}
	if positions, ok := idx.Lookup("20"); !ok || len(positions) != 1 || positions[0] != 1 {
		t.Errorf("Lookup(20) = %v, want [1]", positions)
	}

	// Ищем несуществующее значение
	if _, ok := idx.Lookup("99"); ok {
		t.Error("Lookup(99) should return false")
	}
}

func TestBTreeRange(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Вставляем значения с нулевым паддингом для корректной сортировки
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("%04d", i*10) // 0000, 0010, 0020, ..., 0990
		idx.Insert(key, i)
	}

	// Range query: [0100, 0500]
	positions := idx.Range("0100", "0500")
	if len(positions) != 41 { // 0100, 0110, ..., 0500 = 41 значений
		t.Errorf("Range(0100, 0500) returned %d positions, want 41", len(positions))
	}

	// Range query: [0000, 0100]
	positions = idx.Range("0000", "0100")
	if len(positions) != 11 { // 0000, 0010, ..., 0100 = 11 значений
		t.Errorf("Range(0000, 0100) returned %d positions, want 11", len(positions))
	}

	// Range query: [0500, 1000]
	positions = idx.Range("0500", "1000")
	if len(positions) != 50 { // 0500, 0510, ..., 0990 = 50 значений
		t.Errorf("Range(0500, 1000) returned %d positions, want 50", len(positions))
	}
}

func TestBTreeDelete(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Вставляем значения
	idx.Insert("10", 0)
	idx.Insert("20", 1)
	idx.Insert("30", 2)

	// Удаляем
	idx.Delete(1)

	// Проверяем что удалено
	if _, ok := idx.Lookup("20"); ok {
		t.Error("Lookup(20) should return false after delete")
	}

	// Проверяем что остались
	if positions, ok := idx.Lookup("10"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(10) after delete = %v, want [0]", positions)
	}
	if positions, ok := idx.Lookup("30"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(30) after delete = %v, want [2]", positions)
	}
}

func TestBTreeRebuild(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Вставляем значения
	idx.Insert("10", 0)
	idx.Insert("20", 1)
	idx.Insert("30", 2)

	// Rebuild с новыми данными
	rows := []IndexableRow{
		{DeletedTx: 0, Data: []interface{}{int64(100)}},
		{DeletedTx: 0, Data: []interface{}{int64(200)}},
		{DeletedTx: 0, Data: []interface{}{int64(300)}},
	}
	idx.Rebuild(rows)

	// Проверяем что старые значения удалены
	if _, ok := idx.Lookup("10"); ok {
		t.Error("Lookup(10) should return false after rebuild")
	}

	// Проверяем новые значения
	if positions, ok := idx.Lookup("100"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(100) after rebuild = %v, want [0]", positions)
	}
	if positions, ok := idx.Lookup("200"); !ok || len(positions) != 1 {
		t.Errorf("Lookup(200) after rebuild = %v, want [1]", positions)
	}
}

func TestBTreeMultipleValuesPerKey(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Вставляем несколько строк с одним ключом
	idx.Insert("10", 0)
	idx.Insert("10", 1)
	idx.Insert("10", 2)

	// Проверяем что все позиции найдены
	positions, ok := idx.Lookup("10")
	if !ok {
		t.Error("Lookup(10) should return true")
	}
	if len(positions) != 3 {
		t.Errorf("Lookup(10) returned %d positions, want 3", len(positions))
	}
}

func TestBTreeConcurrent(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			idx.Insert(fmt.Sprintf("%d", n), n)
		}(i)
	}
	wg.Wait()

	if idx.Len() != 100 {
		t.Errorf("expected 100 entries, got %d", idx.Len())
	}
}

func TestBTreeLargeDataset(t *testing.T) {
	idx := NewBTreeIndex("test_idx", "id", 0)

	// Вставляем 1000 значений с нулевым паддингом
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("%04d", i)
		idx.Insert(key, i)
	}

	// Проверяем что все значения найдены
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("%04d", i)
		if _, ok := idx.Lookup(key); !ok {
			t.Errorf("Lookup(%s) should return true", key)
		}
	}

	// Range query
	positions := idx.Range("0100", "0200")
	if len(positions) != 101 { // 0100, 0101, ..., 0200 = 101 значений
		t.Errorf("Range(0100, 0200) returned %d positions, want 101", len(positions))
	}
}

func BenchmarkBTreeInsert(b *testing.B) {
	idx := NewBTreeIndex("bench_idx", "id", 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Insert(fmt.Sprintf("%d", i), i)
	}
}

func BenchmarkBTreeLookup(b *testing.B) {
	idx := NewBTreeIndex("bench_idx", "id", 0)
	for i := 0; i < 100000; i++ {
		idx.Insert(fmt.Sprintf("%d", i), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Lookup(fmt.Sprintf("%d", i%100000))
	}
}

func BenchmarkBTreeDelete(b *testing.B) {
	idx := NewBTreeIndex("bench_idx", "id", 0)
	for i := 0; i < 100000; i++ {
		idx.Insert(fmt.Sprintf("%d", i), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Delete(i % 100000)
	}
}

func BenchmarkBTreeRangeLarge(b *testing.B) {
	idx := NewBTreeIndex("bench_idx", "id", 0)
	for i := 0; i < 100000; i++ {
		idx.Insert(fmt.Sprintf("%06d", i), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.Range("010000", "010099")
	}
}
