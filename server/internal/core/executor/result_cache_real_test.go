package executor

import (
	"fmt"
	"testing"
	"time"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

// BenchmarkResultCache benchmarks cache performance across different query patterns.
func BenchmarkResultCache(b *testing.B) {
	dir := b.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	session := NewSession(store, nil, txm, nil)
	execSQL(b, session, "CREATE DATABASE bench;")
	execSQL(b, session, "USE bench;")
	execSQL(b, session, "CREATE TABLE users (id INT, name TEXT, email TEXT, age INT, city TEXT);")

	// Populate 1K rows
	for i := 0; i < 1000; i++ {
		cities := []string{"Moscow", "SPB", "Kazan", "Novosibirsk", "Ekaterinburg"}
		execSQL(b, session, fmt.Sprintf(
			"INSERT INTO users VALUES (%d, 'user_%d', 'user%d@bench.com', %d, '%s');",
			i, i, i, 18+i%50, cities[i%5]))
	}

	b.Run("EqualityLookup", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM users WHERE id = 5000;")
		}
	})

	b.Run("EqualityLookup_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT * FROM users WHERE id = 5000;") // warm up
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM users WHERE id = 5000;")
		}
	})

	b.Run("AggregateCOUNT", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT COUNT(*) FROM users;")
		}
	})

	b.Run("AggregateCOUNT_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT COUNT(*) FROM users;") // warm up
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT COUNT(*) FROM users;")
		}
	})

	b.Run("FilterMultiple", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM users WHERE city = 'Moscow' AND age > 30;")
		}
	})

	b.Run("FilterMultiple_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT * FROM users WHERE city = 'Moscow' AND age > 30;") // warm up
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM users WHERE city = 'Moscow' AND age > 30;")
		}
	})

	b.Run("StringFunction", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT UPPER(name) FROM users WHERE id = 100;")
		}
	})

	b.Run("StringFunction_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT UPPER(name) FROM users WHERE id = 100;") // warm up
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT UPPER(name) FROM users WHERE id = 100;")
		}
	})
}

// TestRealWorldQueryPatterns tests cache behavior with realistic query patterns.
func TestRealWorldQueryPatterns(t *testing.T) {
	session := setupCacheSession(t)
	var hits, misses int64
	hits, misses, _ = session.resultCache.Stats()
	_ = misses

	// Pattern 1: Dashboard query (frequent, same result)
	t.Run("DashboardFrequent", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			executeSQL(t, session, "SELECT COUNT(*) FROM products;")
		}
		newHits, _, _ := session.resultCache.Stats()
		fmt.Printf("Dashboard: 100 queries, %d cache hits (should be ~99)\n", newHits-hits)
		hits = newHits
	})

	// Pattern 2: Product lookup by ID (different IDs)
	t.Run("ProductLookup", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			executeSQL(t, session, fmt.Sprintf("SELECT * FROM products WHERE id = %d;", i))
		}
		newHits, _, _ := session.resultCache.Stats()
		fmt.Printf("Product lookup: 50 queries, %d new cache hits\n", newHits-hits)
		hits = newHits
	})

	// Pattern 3: After INSERT — cache invalidated
	t.Run("AfterInsert", func(t *testing.T) {
		executeSQL(t, session, "SELECT COUNT(*) FROM products;") // cache
		executeSQL(t, session, "INSERT INTO products VALUES (99999, 'test', 0.0, 'test');")
		executeSQL(t, session, "SELECT COUNT(*) FROM products;") // should miss (invalidated)
		newHits, newMisses, _ := session.resultCache.Stats()
		fmt.Printf("After INSERT: %d hits, %d misses (miss should increase)\n", newHits, newMisses)
		hits = newHits
	})

	// Pattern 4: Mixed workload (reads + writes)
	t.Run("MixedWorkload", func(t *testing.T) {
		for i := 0; i < 200; i++ {
			if i%10 == 0 {
				executeSQL(t, session, fmt.Sprintf("INSERT INTO products VALUES (%d, 'mix_%d', 1.0, 'mix');", 100000+i, i))
			} else {
				executeSQL(t, session, "SELECT COUNT(*) FROM products;")
			}
		}
		newHits, _, _ := session.resultCache.Stats()
		readHits := newHits - hits
		fmt.Printf("Mixed workload: 200 ops (20 writes, 180 reads), %d cache hits\n", readHits)
	})

	// Pattern 5: Cache TTL expiration
	t.Run("TTLExpiration", func(t *testing.T) {
		session.resultCache = NewResultCache(256, 50*time.Millisecond)
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		h1, _, _ := session.resultCache.Stats()
		time.Sleep(100 * time.Millisecond)
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		h2, _, _ := session.resultCache.Stats()
		fmt.Printf("TTL: Before=%d hits, After=%d hits (should be same = expired)\n", h1, h2)
		if h2 > h1 {
			t.Fatal("cache returned stale data after TTL")
		}
		session.resultCache = NewResultCache(256, defaultResultCacheTTL) // restore
	})

	// Pattern 6: Schema change invalidation
	t.Run("SchemaChange", func(t *testing.T) {
		executeSQL(t, session, "SELECT * FROM products LIMIT 5;")
		h1, _, _ := session.resultCache.Stats()
		executeSQL(t, session, "ALTER TABLE products ADD COLUMN discount FLOAT DEFAULT 0.0;")
		executeSQL(t, session, "SELECT * FROM products LIMIT 5;")
		h2, _, _ := session.resultCache.Stats()
		fmt.Printf("Schema change: %d hits before, %d hits after ALTER (should not cache stale)\n", h1, h2)
	})

	// Pattern 7: Time travel queries (different cache keys)
	t.Run("TimeTravel", func(t *testing.T) {
		session.resultCache.InvalidateAll()
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		h1, _, _ := session.resultCache.Stats()
		// AS OF with different version should have different key
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		h2, _, _ := session.resultCache.Stats()
		fmt.Printf("Time travel: %d hits for same query\n", h2-h1)
	})

	// Pattern 8: DISTINCT with cache
	t.Run("Distinct", func(t *testing.T) {
		session.resultCache.InvalidateAll()
		executeSQL(t, session, "SELECT DISTINCT category FROM products;")
		executeSQL(t, session, "SELECT DISTINCT category FROM products;")
		h1, _, _ := session.resultCache.Stats()
		fmt.Printf("DISTINCT: %d cache hits for repeated query\n", h1)
	})

	// Pattern 9: ORDER BY with cache
	t.Run("OrderBy", func(t *testing.T) {
		session.resultCache.InvalidateAll()
		executeSQL(t, session, "SELECT * FROM products ORDER BY price DESC LIMIT 10;")
		executeSQL(t, session, "SELECT * FROM products ORDER BY price DESC LIMIT 10;")
		h1, _, _ := session.resultCache.Stats()
		fmt.Printf("ORDER BY: %d cache hits for repeated query\n", h1)
	})

	// Pattern 10: Multiple tables
	t.Run("MultipleTables", func(t *testing.T) {
		session.resultCache.InvalidateAll()
		executeSQL(t, session, "CREATE TABLE orders (id INT, product_id INT, quantity INT);")
		executeSQL(t, session, "INSERT INTO orders VALUES (1, 1, 5);")
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		executeSQL(t, session, "SELECT * FROM orders WHERE id = 1;")
		// Insert into orders should NOT invalidate products cache
		executeSQL(t, session, "INSERT INTO orders VALUES (2, 2, 3);")
		h1, _, _ := session.resultCache.Stats()
		executeSQL(t, session, "SELECT * FROM products WHERE id = 1;")
		h2, _, _ := session.resultCache.Stats()
		fmt.Printf("Multi-table: product cache preserved after orders INSERT: %d hits\n", h2-h1)
	})
}

func execSQL(b testing.TB, session *Session, sql string) {
	b.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := session.Execute(stmt); err != nil {
		b.Fatal(err)
	}
}
