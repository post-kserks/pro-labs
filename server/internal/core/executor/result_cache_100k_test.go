package executor

import (
	"fmt"
	"testing"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

// BenchmarkResultCache10K tests cache performance with 100K rows on disk.
func BenchmarkResultCache10K(b *testing.B) {
	dir := b.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	session := NewSession(store, nil, txm, nil)
	execSQL(b, session, "CREATE DATABASE bench100k;")
	execSQL(b, session, "USE bench100k;")
	execSQL(b, session, "CREATE TABLE records (id INT, name TEXT, value FLOAT, status TEXT, region TEXT);")

	b.Logf("Populating 10K rows...")
	cities := []string{"Moscow", "SPB", "Kazan", "Novosibirsk", "Ekaterinburg", "Nizhny", "Samara", "Omsk", "Rostov", "Ufa"}
	statuses := []string{"active", "pending", "closed", "archived"}
	for i := 0; i < 10000; i++ {
		execSQL(b, session, fmt.Sprintf(
			"INSERT INTO records VALUES (%d, 'rec_%d', %.2f, '%s', '%s');",
			i, i, float64(i)*0.01, statuses[i%4], cities[i%10]))
		if i%1000 == 0 {
			b.Logf("  ...%d rows", i)
		}
	}
	b.Logf("10K rows populated")

	b.Run("EqualityScan", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM records WHERE id = 50000;")
		}
	})

	b.Run("EqualityScan_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT * FROM records WHERE id = 50000;")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM records WHERE id = 50000;")
		}
	})

	b.Run("FullTableScan", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT COUNT(*) FROM records;")
		}
	})

	b.Run("FullTableScan_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT COUNT(*) FROM records;")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT COUNT(*) FROM records;")
		}
	})

	b.Run("FilteredScan", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM records WHERE status = 'active' AND region = 'Moscow';")
		}
	})

	b.Run("FilteredScan_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT * FROM records WHERE status = 'active' AND region = 'Moscow';")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM records WHERE status = 'active' AND region = 'Moscow';")
		}
	})

	b.Run("AggregationGroupBy", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT status, COUNT(*) FROM records GROUP BY status;")
		}
	})

	b.Run("AggregationGroupBy_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT status, COUNT(*) FROM records GROUP BY status;")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT status, COUNT(*) FROM records GROUP BY status;")
		}
	})

	b.Run("StringFunction10K", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT UPPER(name) FROM records WHERE id = 99999;")
		}
	})

	b.Run("StringFunction10K_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		execSQL(b, session, "SELECT UPPER(name) FROM records WHERE id = 99999;")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT UPPER(name) FROM records WHERE id = 99999;")
		}
	})
}
