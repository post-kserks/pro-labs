package executor

import (
	"fmt"
	"testing"
	"time"
)

func TestPlanCacheHitRate(t *testing.T) {
	cache := NewPlanCacheWithTTL(100, time.Minute)
	schemaVer := uint64(1)

	// First query - miss
	hash := QueryHash("SELECT * FROM users WHERE id = $1", []interface{}{1})
	if _, ok := cache.Get(hash, schemaVer); ok {
		t.Fatal("expected cache miss on first query")
	}

	// Store plan
	cache.Put(hash, &CachedPlan{
		Plan:      "mock_plan",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
		QueryStr:  "SELECT * FROM users WHERE id = $1",
		tableName: "users",
	})

	// Second query - hit
	if _, ok := cache.Get(hash, schemaVer); !ok {
		t.Fatal("expected cache hit on second query")
	}

	// Verify hit count
	hits, misses, _, _ := cache.Stats()
	if hits != 1 {
		t.Fatalf("expected 1 hit, got %d", hits)
	}
	if misses != 1 {
		t.Fatalf("expected 1 miss, got %d", misses)
	}
}

func TestPlanCacheInvalidationByVersion(t *testing.T) {
	cache := NewPlanCacheWithTTL(100, time.Minute)
	schemaVer := uint64(1)

	// Store plan with schema version 1
	hash := QueryHash("SELECT * FROM users", nil)
	cache.Put(hash, &CachedPlan{
		Plan:      "mock_plan",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
	})

	// Verify it's cached
	if _, ok := cache.Get(hash, schemaVer); !ok {
		t.Fatal("expected cache hit")
	}

	// Invalidate by version
	cache.InvalidateByVersion(schemaVer)

	// Verify it's gone
	if _, ok := cache.Get(hash, schemaVer); ok {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestPlanCacheInvalidationByTable(t *testing.T) {
	cache := NewPlanCacheWithTTL(100, time.Minute)
	schemaVer := uint64(1)

	// Store plans for different tables
	hash1 := QueryHash("SELECT * FROM users", nil)
	hash2 := QueryHash("SELECT * FROM orders", nil)

	cache.Put(hash1, &CachedPlan{
		Plan:      "plan1",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
		tableName: "users",
	})
	cache.Put(hash2, &CachedPlan{
		Plan:      "plan2",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
		tableName: "orders",
	})

	// Invalidate users table
	cache.Invalidate("users")

	// Verify users plan is gone
	if _, ok := cache.Get(hash1, schemaVer); ok {
		t.Fatal("expected cache miss for users table")
	}

	// Verify orders plan still exists
	if _, ok := cache.Get(hash2, schemaVer); !ok {
		t.Fatal("expected cache hit for orders table")
	}
}

func TestPlanCacheEviction(t *testing.T) {
	cache := NewPlanCacheWithTTL(3, time.Minute) // Small cache
	schemaVer := uint64(1)

	// Fill cache to capacity
	for i := 0; i < 3; i++ {
		hash := QueryHash(fmt.Sprintf("query_%d", i), nil)
		cache.Put(hash, &CachedPlan{
			Plan:      fmt.Sprintf("plan_%d", i),
			SchemaVer: schemaVer,
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second), // Different times
		})
	}

	// Add one more - should evict oldest
	hash := QueryHash("query_new", nil)
	cache.Put(hash, &CachedPlan{
		Plan:      "plan_new",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
	})

	// Verify oldest (query_0) was evicted
	hash0 := QueryHash("query_0", nil)
	if _, ok := cache.Get(hash0, schemaVer); ok {
		t.Fatal("expected oldest entry to be evicted")
	}

	// Verify new entry exists
	if _, ok := cache.Get(hash, schemaVer); !ok {
		t.Fatal("expected new entry to exist")
	}

	// Check eviction count
	_, _, evictions, _ := cache.Stats()
	if evictions != 1 {
		t.Fatalf("expected 1 eviction, got %d", evictions)
	}
}

func TestPlanCacheTTLExpiration(t *testing.T) {
	cache := NewPlanCacheWithTTL(100, 10*time.Millisecond) // Very short TTL
	schemaVer := uint64(1)

	hash := QueryHash("SELECT * FROM users", nil)
	cache.Put(hash, &CachedPlan{
		Plan:      "mock_plan",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
	})

	// Verify it's cached
	if _, ok := cache.Get(hash, schemaVer); !ok {
		t.Fatal("expected cache hit before TTL")
	}

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Verify it's expired
	if _, ok := cache.Get(hash, schemaVer); ok {
		t.Fatal("expected cache miss after TTL expiration")
	}
}

func TestPlanCacheSchemaVersionMismatch(t *testing.T) {
	cache := NewPlanCacheWithTTL(100, time.Minute)
	schemaVer := uint64(1)

	hash := QueryHash("SELECT * FROM users", nil)
	cache.Put(hash, &CachedPlan{
		Plan:      "mock_plan",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
	})

	// Query with different schema version - should miss and invalidate entry
	if _, ok := cache.Get(hash, 2); ok {
		t.Fatal("expected cache miss on schema version mismatch")
	}

	// Entry should be deleted after schema version mismatch
	if _, ok := cache.Get(hash, schemaVer); ok {
		t.Fatal("expected cache miss after schema version mismatch deleted entry")
	}
}

func TestPlanCacheReset(t *testing.T) {
	cache := NewPlanCacheWithTTL(100, time.Minute)
	schemaVer := uint64(1)

	// Add some entries
	for i := 0; i < 5; i++ {
		hash := QueryHash(fmt.Sprintf("query_%d", i), nil)
		cache.Put(hash, &CachedPlan{
			Plan:      "plan",
			SchemaVer: schemaVer,
			CreatedAt: time.Now(),
		})
	}

	// Reset
	cache.Reset()

	// Verify empty
	if cache.Len() != 0 {
		t.Fatalf("expected empty cache after reset, got %d", cache.Len())
	}

	hits, misses, evictions, _ := cache.Stats()
	if hits != 0 || misses != 0 || evictions != 0 {
		t.Fatalf("expected stats reset, got hits=%d misses=%d evictions=%d", hits, misses, evictions)
	}
}

func TestQueryHashDeterministic(t *testing.T) {
	query := "SELECT * FROM users WHERE id = $1"
	params := []interface{}{1}

	hash1 := QueryHash(query, params)
	hash2 := QueryHash(query, params)

	if hash1 != hash2 {
		t.Fatal("query hash should be deterministic")
	}
}

func TestQueryHashDifferentQueries(t *testing.T) {
	hash1 := QueryHash("SELECT * FROM users", nil)
	hash2 := QueryHash("SELECT * FROM orders", nil)

	if hash1 == hash2 {
		t.Fatal("different queries should have different hashes")
	}
}

func TestQueryHashDifferentParams(t *testing.T) {
	hash1 := QueryHash("SELECT * FROM users WHERE id = $1", []interface{}{1})
	hash2 := QueryHash("SELECT * FROM users WHERE id = $1", []interface{}{2})

	if hash1 == hash2 {
		t.Fatal("different params should have different hashes")
	}
}

func BenchmarkPlanCacheHit(b *testing.B) {
	cache := NewPlanCacheWithTTL(1000, time.Minute)
	schemaVer := uint64(1)
	query := "SELECT * FROM users WHERE id = $1"
	params := []interface{}{42}

	// Pre-populate cache
	hash := QueryHash(query, params)
	cache.Put(hash, &CachedPlan{
		Plan:      "mock_plan",
		SchemaVer: schemaVer,
		CreatedAt: time.Now(),
		QueryStr:  query,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Get(hash, schemaVer)
		}
	})
}

func BenchmarkPlanCacheMiss(b *testing.B) {
	cache := NewPlanCacheWithTTL(1000, time.Minute)
	schemaVer := uint64(1)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			hash := QueryHash(fmt.Sprintf("query_%d", i), nil)
			cache.Get(hash, schemaVer)
			i++
		}
	})
}

func BenchmarkQueryHash(b *testing.B) {
	query := "SELECT * FROM users WHERE id = $1 AND name = $2"
	params := []interface{}{42, "test"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		QueryHash(query, params)
	}
}
