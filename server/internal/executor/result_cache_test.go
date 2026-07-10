package executor

import (
	"fmt"
	"testing"
	"time"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func setupCacheSession(t *testing.T) *Session {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	session := NewSession(store, nil, txm, nil)
	executeSQL(t, session, "CREATE DATABASE cache_db;")
	executeSQL(t, session, "USE cache_db;")
	executeSQL(t, session, "CREATE TABLE products (id INT, name TEXT, price FLOAT, category TEXT);")
	for i := 0; i < 1000; i++ {
		executeSQL(t, session, fmt.Sprintf("INSERT INTO products VALUES (%d, 'product_%d', %.2f, 'cat_%d');", i, i, float64(i)*1.5, i%10))
	}
	return session
}

// TestResultCacheHitMiss verifies cache works: first query — miss, repeat — hit.
func TestResultCacheHitMiss(t *testing.T) {
	session := setupCacheSession(t)

	// First query — MISS (cache empty)
	start := time.Now()
	res1 := executeSQL(t, session, "SELECT * FROM products WHERE id = 42;")
	missDuration := time.Since(start)
	if len(res1.Rows) != 1 || res1.Rows[0][0] != "42" {
		t.Fatalf("expected row with id=42, got %v", res1.Rows)
	}

	// Second query — HIT (from cache)
	start = time.Now()
	res2 := executeSQL(t, session, "SELECT * FROM products WHERE id = 42;")
	hitDuration := time.Since(start)
	if len(res2.Rows) != 1 || res2.Rows[0][0] != "42" {
		t.Fatalf("expected cached row with id=42, got %v", res2.Rows)
	}

	hits, misses, size := session.resultCache.Stats()
	fmt.Printf("Cache stats: hits=%d misses=%d size=%d\n", hits, misses, size)
	fmt.Printf("Miss: %v, Hit: %v\n", missDuration, hitDuration)

	if hits < 1 {
		t.Fatalf("expected at least 1 cache hit, got %d", hits)
	}
	if misses < 1 {
		t.Fatalf("expected at least 1 cache miss, got %d", misses)
	}
}

// TestResultCacheInvalidationOnInsert checks cache invalidation on INSERT.
func TestResultCacheInvalidationOnInsert(t *testing.T) {
	session := setupCacheSession(t)

	// COUNT query — populating cache
	res1 := executeSQL(t, session, "SELECT COUNT(*) FROM products;")
	count1 := res1.Rows[0][0]
	fmt.Printf("Before insert: count=%s\n", count1)

	// Insert new row
	executeSQL(t, session, "INSERT INTO products VALUES (9999, 'new', 0.0, 'new_cat');")

	// Repeat query — should return updated count
	res2 := executeSQL(t, session, "SELECT COUNT(*) FROM products;")
	count2 := res2.Rows[0][0]
	fmt.Printf("After insert: count=%s\n", count2)

	if count1 == count2 {
		t.Fatalf("cache was NOT invalidated after INSERT: count still %s", count1)
	}
}

// TestResultCacheInvalidationOnDelete checks cache invalidation on DELETE.
func TestResultCacheInvalidationOnDelete(t *testing.T) {
	session := setupCacheSession(t)

	res1 := executeSQL(t, session, "SELECT COUNT(*) FROM products;")
	fmt.Printf("Before delete: count=%s\n", res1.Rows[0][0])

	executeSQL(t, session, "DELETE FROM products WHERE id = 0;")

	res2 := executeSQL(t, session, "SELECT COUNT(*) FROM products;")
	fmt.Printf("After delete: count=%s\n", res2.Rows[0][0])

	if res1.Rows[0][0] == res2.Rows[0][0] {
		t.Fatalf("cache was NOT invalidated after DELETE")
	}
}

// TestResultCacheDifferentQueries verifies different queries have different keys.
func TestResultCacheDifferentQueries(t *testing.T) {
	session := setupCacheSession(t)

	// Query 1
	executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
	// Query 2 (different WHERE)
	executeSQL(t, session, "SELECT * FROM products WHERE id = 2;")
	// Query 3 (different function)
	executeSQL(t, session, "SELECT LENGTH(name) FROM products WHERE id = 1;")

	hits, _, _ := session.resultCache.Stats()
	fmt.Printf("After 3 different queries: hits=%d (should be 0, all are first-time)\n", hits)
	if hits > 0 {
		t.Fatalf("expected 0 cache hits for different queries, got %d", hits)
	}
}

// TestResultCacheTTL verifies cache expires through TTL.
func TestResultCacheTTL(t *testing.T) {
	session := setupCacheSession(t)
	session.resultCache = NewResultCache(256, 100*time.Millisecond)

	executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
	hits1, _, _ := session.resultCache.Stats()
	fmt.Printf("Immediate: hits=%d\n", hits1)

	time.Sleep(150 * time.Millisecond)

	executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
	hits2, _, _ := session.resultCache.Stats()
	fmt.Printf("After TTL: hits=%d (should still be 1, not 2)\n", hits2)

	if hits2 > hits1 {
		t.Fatalf("cache returned stale data after TTL: hits went from %d to %d", hits1, hits2)
	}
}

// TestResultCachePerformanceCompare compares speed with and without cache.
func TestResultCachePerformanceCompare(t *testing.T) {
	session := setupCacheSession(t)

	// Warm up cache
	for i := 0; i < 100; i++ {
		executeSQL(t, session, fmt.Sprintf("SELECT * FROM products WHERE id = %d;", i))
	}

	// Benchmark with cache
	start := time.Now()
	for i := 0; i < 1000; i++ {
		executeSQL(t, session, fmt.Sprintf("SELECT * FROM products WHERE id = %d;", i%100))
	}
	cachedDuration := time.Since(start)

	// Benchmark without cache (clear cache before each query)
	start = time.Now()
	for i := 0; i < 1000; i++ {
		session.resultCache.InvalidateAll()
		executeSQL(t, session, fmt.Sprintf("SELECT * FROM products WHERE id = %d;", i%100))
	}
	uncachedDuration := time.Since(start)

	speedup := float64(uncachedDuration) / float64(cachedDuration)
	fmt.Printf("\n=== Cache Performance ===\n")
	fmt.Printf("1000 queries WITH cache:    %v\n", cachedDuration)
	fmt.Printf("1000 queries WITHOUT cache: %v\n", uncachedDuration)
	fmt.Printf("Speedup: %.1fx\n", speedup)
}
