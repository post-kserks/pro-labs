package executor

import (
	"fmt"
	"strings"
	"testing"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

// BenchmarkResultCache100K tests cache performance with 100K rows on disk.
func BenchmarkResultCache100K(b *testing.B) {
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

	b.Logf("Populating 100K rows...")
	cities := []string{"Moscow", "SPB", "Kazan", "Novosibirsk", "Ekaterinburg", "Nizhny", "Samara", "Omsk", "Rostov", "Ufa"}
	statuses := []string{"active", "pending", "closed", "archived"}
	for i := 0; i < 100000; {
		batchSize := 1000
		if 100000-i < batchSize {
			batchSize = 100000 - i
		}
		vals := make([]string, batchSize)
		for j := 0; j < batchSize; j++ {
			idx := i + j
			vals[j] = fmt.Sprintf("(%d, 'rec_%d', %.2f, '%s', '%s')", idx, idx, float64(idx)*0.01, statuses[idx%4], cities[idx%10])
		}
		execSQL(b, session, fmt.Sprintf("INSERT INTO records VALUES %s;", strings.Join(vals, ",")))
		i += batchSize
		if i%20000 == 0 {
			b.Logf("  ...%d rows", i)
		}
	}
	b.Logf("100K rows populated")

	b.Run("EqualityScan", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT * FROM records WHERE id = 50000;")
		}
	})

	b.Run("EqualityScan_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		stmt, _ := parser.Parse("SELECT * FROM records WHERE id = 50000;")
		execStmt(b, session, stmt)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execStmt(b, session, stmt)
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
		stmt, _ := parser.Parse("SELECT COUNT(*) FROM records;")
		execStmt(b, session, stmt)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execStmt(b, session, stmt)
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
		stmt, _ := parser.Parse("SELECT * FROM records WHERE status = 'active' AND region = 'Moscow';")
		execStmt(b, session, stmt)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execStmt(b, session, stmt)
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
		stmt, _ := parser.Parse("SELECT status, COUNT(*) FROM records GROUP BY status;")
		execStmt(b, session, stmt)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execStmt(b, session, stmt)
		}
	})

	b.Run("StringFunction100K", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execSQL(b, session, "SELECT UPPER(name) FROM records WHERE id = 99999;")
		}
	})

	b.Run("StringFunction100K_Cached", func(b *testing.B) {
		session.resultCache.InvalidateAll()
		stmt, _ := parser.Parse("SELECT UPPER(name) FROM records WHERE id = 99999;")
		execStmt(b, session, stmt)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			execStmt(b, session, stmt)
		}
	})
}
