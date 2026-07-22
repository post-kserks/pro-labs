package executor

import (
	"sync"
	"testing"
	"vaultdb/internal/core/parser"
)

func TestASTCache_HitMiss(t *testing.T) {
	cache := NewASTCache(10)

	sql := "SELECT * FROM t"
	res := cache.Get(sql)
	if res != nil {
		t.Fatalf("Expected nil on miss, got %v", res)
	}

	stmt := &parser.SelectStatement{TableName: "t"}
	cache.Put(sql, stmt)

	res = cache.Get(sql)
	if res == nil {
		t.Fatalf("Expected AST on hit, got nil")
	}
}

func TestASTCache_LRUEviction(t *testing.T) {
	cache := NewASTCache(2)

	stmt1 := &parser.SelectStatement{TableName: "t1"}
	stmt2 := &parser.SelectStatement{TableName: "t2"}
	stmt3 := &parser.SelectStatement{TableName: "t3"}

	cache.Put("Q1", stmt1)
	cache.Put("Q2", stmt2)
	
	// Q1 should be accessible
	if cache.Get("Q1") == nil {
		t.Fatalf("Expected Q1 to be in cache")
	}

	// Because we accessed Q1, Q2 is now the oldest.
	// Adding Q3 should evict Q2.
	cache.Put("Q3", stmt3)

	if cache.Get("Q2") != nil {
		t.Fatalf("Expected Q2 to be evicted")
	}
	if cache.Get("Q1") == nil {
		t.Fatalf("Expected Q1 to remain in cache")
	}
	if cache.Get("Q3") == nil {
		t.Fatalf("Expected Q3 to be in cache")
	}
}

func TestASTCache_Concurrent(t *testing.T) {
	cache := NewASTCache(10)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sql := "SELECT " + string(rune('A'+id))
			stmt := &parser.SelectStatement{TableName: "t"}
			
			// Put and Get multiple times
			for j := 0; j < 100; j++ {
				cache.Put(sql, stmt)
				res := cache.Get(sql)
				if res == nil {
					t.Errorf("Expected hit for %s", sql)
				}
			}
		}(i)
	}
	wg.Wait()
}
