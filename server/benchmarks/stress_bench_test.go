package benchmarks

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"vaultdb"
)

func setupStressBenchDB(b *testing.B) *vaultdb.VaultDB {
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

func setupStressBenchDBWithData(b *testing.B, rows int) *vaultdb.VaultDB {
	b.Helper()
	db := setupStressBenchDB(b)
	for i := 0; i < rows; {
		batchSize := 1000
		if rows-i < batchSize {
			batchSize = rows - i
		}
		vals := make([]string, batchSize)
		for j := 0; j < batchSize; j++ {
			vals[j] = fmt.Sprintf("(%d, 'row_%d', %f)", i+j, i+j, float64(i+j)*1.1)
		}
		db.Query("benchdb", fmt.Sprintf("INSERT INTO bench VALUES %s;", strings.Join(vals, ",")))
		i += batchSize
	}
	return db
}

// --- INSERT benchmarks ---

func BenchmarkStressInsertSingle(b *testing.B) {
	db := setupStressBenchDB(b)
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

func BenchmarkStressInsertBatch(b *testing.B) {
	db := setupStressBenchDB(b)
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

func BenchmarkStressSelectFullScan(b *testing.B) {
	db := setupStressBenchDBWithData(b, 10000)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", "SELECT * FROM bench;")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStressSelectIndexed(b *testing.B) {
	db := setupStressBenchDBWithData(b, 10000)
	db.Query("benchdb", "CREATE INDEX idx_bench_id ON bench (id);")
	defer db.Close()

	lt := NewLatencyTracker()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_, err := db.Query("benchdb", fmt.Sprintf(
			"SELECT * FROM bench WHERE id = %d;", i%10000))
		if err != nil {
			b.Fatal(err)
		}
		lt.Record(time.Since(start))
	}
	b.StopTimer()
	b.Logf("\n%s", lt.Calculate().String())
}

func BenchmarkStressSelectIndexed100K(b *testing.B) {
	db := setupStressBenchDBWithData(b, 100000)
	db.Query("benchdb", "CREATE INDEX idx_bench_id ON bench (id);")
	defer db.Close()

	lt := NewLatencyTracker()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_, err := db.Query("benchdb", fmt.Sprintf(
			"SELECT * FROM bench WHERE id = %d;", i%100000))
		if err != nil {
			b.Fatal(err)
		}
		lt.Record(time.Since(start))
	}
	b.StopTimer()
	b.Logf("\n%s", lt.Calculate().String())
}

// --- UPDATE benchmarks ---

func BenchmarkStressUpdateSingle(b *testing.B) {
	db := setupStressBenchDBWithData(b, 10000)
	defer db.Close()

	lt := NewLatencyTracker()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_, err := db.Query("benchdb", fmt.Sprintf(
			"UPDATE bench SET value = 'updated_%d' WHERE id = %d;", i, i%10000))
		if err != nil {
			b.Fatal(err)
		}
		lt.Record(time.Since(start))
	}
	b.StopTimer()
	b.Logf("\n%s", lt.Calculate().String())
}

func BenchmarkStressUpdateSingle100K(b *testing.B) {
	db := setupStressBenchDBWithData(b, 100000)
	defer db.Close()

	lt := NewLatencyTracker()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_, err := db.Query("benchdb", fmt.Sprintf(
			"UPDATE bench SET value = 'updated_%d' WHERE id = %d;", i, i%100000))
		if err != nil {
			b.Fatal(err)
		}
		lt.Record(time.Since(start))
	}
	b.StopTimer()
	b.Logf("\n%s", lt.Calculate().String())
}

// --- Mixed workload benchmark ---

func BenchmarkStressMixedWorkload(b *testing.B) {
	db := setupStressBenchDBWithData(b, 1000)
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

func BenchmarkStressMixedWorkload100K(b *testing.B) {
	db := setupStressBenchDBWithData(b, 100000)
	defer db.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		op := i % 10
		switch {
		case op < 4: // 40% reads
			db.Query("benchdb", fmt.Sprintf(
				"SELECT * FROM bench WHERE id = %d;", i%100000))
		case op < 7: // 30% updates
			db.Query("benchdb", fmt.Sprintf(
				"UPDATE bench SET value = 'u%d' WHERE id = %d;", i, i%100000))
		case op < 9: // 20% inserts
			db.Query("benchdb", fmt.Sprintf(
				"INSERT INTO bench VALUES (%d, 'new', 1.0);", 2000000+i))
		default: // 10% deletes
			db.Query("benchdb", fmt.Sprintf(
				"DELETE FROM bench WHERE id = %d;", 2000000+i-1))
		}
	}
}

// --- Transaction throughput benchmark ---

func BenchmarkStressTransactionThroughput(b *testing.B) {
	db := setupStressBenchDBWithData(b, 100)
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

func BenchmarkStressConcurrentReads(b *testing.B) {
	db := setupStressBenchDBWithData(b, 1000)
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
