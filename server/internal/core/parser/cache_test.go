package parser

import (
	"fmt"
	"sync"
	"testing"
)

func TestCacheHit(t *testing.T) {
	cache := NewStatementCache(16)
	sql := "SELECT * FROM heroes;"

	// First call: cache miss, parses and stores
	stmt1, err := Parse(sql)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	cache.Put(sql, stmt1)

	// Second call: cache hit
	stmt2, ok := cache.Get(sql)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if stmt2.StatementType() != stmt1.StatementType() {
		t.Fatalf("expected same statement type, got %v vs %v", stmt2.StatementType(), stmt1.StatementType())
	}
}

func TestCacheMiss(t *testing.T) {
	cache := NewStatementCache(16)
	sql1 := "SELECT * FROM heroes;"
	sql2 := "SELECT * FROM villains;"

	stmt1, err := Parse(sql1)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	cache.Put(sql1, stmt1)

	// Different SQL should miss
	_, ok := cache.Get(sql2)
	if ok {
		t.Fatal("expected cache miss for different SQL")
	}
}

func TestCacheEviction(t *testing.T) {
	cache := NewStatementCache(2)

	queries := []string{
		"SELECT * FROM a;",
		"SELECT * FROM b;",
		"SELECT * FROM c;",
	}

	for _, q := range queries {
		stmt, err := Parse(q)
		if err != nil {
			t.Fatalf("Parse(%q) failed: %v", q, err)
		}
		cache.Put(q, stmt)
	}

	// Cache capacity is 2, so first entry should be evicted
	if cache.Len() != 2 {
		t.Fatalf("expected cache length 2, got %d", cache.Len())
	}
	if _, ok := cache.Get("SELECT * FROM a;"); ok {
		t.Fatal("expected first entry to be evicted")
	}
	// Second and third should still be present
	if _, ok := cache.Get("SELECT * FROM b;"); !ok {
		t.Fatal("expected second entry to be present")
	}
	if _, ok := cache.Get("SELECT * FROM c;"); !ok {
		t.Fatal("expected third entry to be present")
	}
}

func TestCacheConcurrency(t *testing.T) {
	const n = 100
	cache := NewStatementCache(n)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sql := fmt.Sprintf("SELECT * FROM t%d;", n)
			stmt, err := Parse(sql)
			if err != nil {
				t.Errorf("Parse failed: %v", err)
				return
			}
			cache.Put(sql, stmt)
		}(i)
	}
	wg.Wait()

	if cache.Len() != n {
		t.Fatalf("expected %d entries, got %d", n, cache.Len())
	}

	// Concurrent reads
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sql := fmt.Sprintf("SELECT * FROM t%d;", n)
			if _, ok := cache.Get(sql); !ok {
				t.Errorf("expected cache hit for t%d", n)
			}
		}(i)
	}
	wg.Wait()
}

func TestCacheNormalize(t *testing.T) {
	cache := NewStatementCache(16)
	stmt, err := Parse("SELECT * FROM heroes;")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Different casing and whitespace should hit the same cache entry
	cache.Put("  SELECT * FROM heroes;  ", stmt)
	hit, ok := cache.Get("SELECT * FROM HEROES;")
	if !ok || hit == nil {
		t.Fatal("expected normalized cache hit")
	}
}

func TestCacheClear(t *testing.T) {
	cache := NewStatementCache(16)
	stmt, _ := Parse("SELECT * FROM heroes;")
	cache.Put("SELECT * FROM heroes;", stmt)

	if cache.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cache.Len())
	}

	cache.Clear()
	if cache.Len() != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", cache.Len())
	}
}

func TestParseCached(t *testing.T) {
	sql := "SELECT id FROM heroes WHERE id > 5;"

	stmt1, err := ParseCached(sql)
	if err != nil {
		t.Fatalf("ParseCached failed: %v", err)
	}

	stmt2, err := ParseCached(sql)
	if err != nil {
		t.Fatalf("ParseCached second call failed: %v", err)
	}

	if stmt1.StatementType() != stmt2.StatementType() {
		t.Fatalf("expected same statement type from cache")
	}
}

func BenchmarkParse(b *testing.B) {
	sql := "SELECT id, name FROM heroes WHERE alive = TRUE AND level >= 3;"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Parse(sql)
	}
}

func BenchmarkParseCached(b *testing.B) {
	cache := NewStatementCache(256)
	sql := "SELECT id, name FROM heroes WHERE alive = TRUE AND level >= 3;"

	// Pre-populate cache
	stmt, _ := Parse(sql)
	cache.Put(sql, stmt)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseCachedWith(sql, cache)
	}
}

func BenchmarkParseCachedMiss(b *testing.B) {
	cache := NewStatementCache(256)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sql := fmt.Sprintf("SELECT * FROM t%d;", i%1000)
		_, _ = ParseCachedWith(sql, cache)
	}
}
