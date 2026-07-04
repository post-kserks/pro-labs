package benchmark

import (
	"fmt"
	"strings"
	"testing"

	"vaultdb"
)

func setupBenchDB(b *testing.B) *vaultdb.VaultDB {
	b.Helper()
	dir := b.TempDir()
	db, err := vaultdb.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	db.Query("", "CREATE DATABASE benchdb;")
	db.Query("benchdb", "CREATE TABLE bench (id INT PRIMARY KEY, name TEXT, value FLOAT);")
	return db
}

func setupBenchDBWithData(b *testing.B, rows int) *vaultdb.VaultDB {
	b.Helper()
	db := setupBenchDB(b)
	for i := 0; i < rows; i++ {
		db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES (%d, 'row_%d', %f);", i, i, float64(i)*1.1))
	}
	return db
}

// --- INSERT benchmarks ---

func BenchmarkInsertSingle(b *testing.B) {
	db := setupBenchDB(b)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES (%d, 'value_%d', %f);", i, i, float64(i)*1.1))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInsertBatch(b *testing.B) {
	db := setupBenchDB(b)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		values := make([]string, 100)
		for j := 0; j < 100; j++ {
			values[j] = fmt.Sprintf("(%d, 'batch_%d', %f)", i*100+j, j, float64(j)*1.1)
		}
		query := fmt.Sprintf("INSERT INTO bench VALUES %s;", strings.Join(values, ","))
		_, err := db.Query("benchdb", query)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- SELECT benchmarks ---

func BenchmarkSelectFullScan(b *testing.B) {
	db := setupBenchDBWithData(b, 10000)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", "SELECT * FROM bench;")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelectIndexed(b *testing.B) {
	db := setupBenchDBWithData(b, 10000)
	db.Query("benchdb", "CREATE INDEX idx_bench_id ON bench (id);")
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"SELECT * FROM bench WHERE id = %d;", i%10000))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- UPDATE benchmarks ---

func BenchmarkUpdateSingle(b *testing.B) {
	db := setupBenchDBWithData(b, 10000)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"UPDATE bench SET value = 'updated_%d' WHERE id = %d;", i, i%10000))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- Mixed workload benchmark ---

func BenchmarkMixedWorkload(b *testing.B) {
	db := setupBenchDBWithData(b, 1000)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := i % 10
		switch {
		case op < 4: // 40% reads
			db.Query("benchdb", fmt.Sprintf(
				"SELECT * FROM bench WHERE id = %d;", i%1000))
		case op < 7: // 30% updates
			db.Query("benchdb", fmt.Sprintf(
				"UPDATE bench SET value = 'u%d' WHERE id = %d;", i, i%1000))
		case op < 9: // 20% inserts
			db.Query("benchdb", fmt.Sprintf(
				"INSERT INTO bench VALUES (%d, 'new', 1.0);", 1000000+i))
		default: // 10% deletes
			db.Query("benchdb", fmt.Sprintf(
				"DELETE FROM bench WHERE id = %d;", 1000000+i-1))
		}
	}
}

// --- Transaction throughput benchmark ---

func BenchmarkTransactionThroughput(b *testing.B) {
	db := setupBenchDBWithData(b, 100)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Query("benchdb", "BEGIN;")
		db.Query("benchdb", fmt.Sprintf(
			"UPDATE bench SET value = 'tx_%d' WHERE id = %d;", i, i%100))
		db.Query("benchdb", "COMMIT;")
	}
}

// --- Concurrent read benchmark ---
// NOTE: b.RunParallel deadlocks because the storage engine uses exclusive
// locks (mu.Lock) even for reads. This benchmark measures sequential read
// throughput to expose the serialization bottleneck.

func BenchmarkConcurrentReads(b *testing.B) {
	db := setupBenchDBWithData(b, 1000)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"SELECT * FROM bench WHERE id = %d;", i%1000))
		if err != nil {
			b.Fatal(err)
		}
	}
}
