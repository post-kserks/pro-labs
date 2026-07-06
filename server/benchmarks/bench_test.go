package benchmarks

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"vaultdb"
)

// setupDB creates a fresh database with a benchdb containing a single table.
func setupDB(b *testing.B) *vaultdb.VaultDB {
	b.Helper()
	dir := b.TempDir()
	db, err := vaultdb.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	db.Query("", "CREATE DATABASE benchdb;")
	db.Query("benchdb", `CREATE TABLE bench (
		id INT PRIMARY KEY,
		name TEXT,
		value FLOAT
	);`)
	return db
}

// setupDBWithData creates a database pre-populated with n rows.
func setupDBWithData(b *testing.B, n int) *vaultdb.VaultDB {
	b.Helper()
	db := setupDB(b)
	for i := 0; i < n; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES (%d, 'row_%d', %f);", i, i, float64(i)*1.1))
		if err != nil {
			b.Fatal(err)
		}
	}
	return db
}

// setupDBWithIndex creates a database with data and an index on id.
func setupDBWithIndex(b *testing.B, n int) *vaultdb.VaultDB {
	b.Helper()
	db := setupDBWithData(b, n)
	db.Query("benchdb", "CREATE INDEX idx_bench_id ON bench (id);")
	return db
}

// --- INSERT benchmarks ---

func BenchmarkInsertSingle(b *testing.B) {
	db := setupDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES (%d, 'value_%d', %f);", i, i, float64(i)*1.1))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInsertBatch100(b *testing.B) {
	db := setupDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vals := make([]string, 100)
		for j := 0; j < 100; j++ {
			vals[j] = fmt.Sprintf("(%d, 'b%d_%d', %f)", i*100+j, i, j, float64(j)*1.1)
		}
		_, err := db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES %s;", strings.Join(vals, ",")))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkInsertBatch1000(b *testing.B) {
	db := setupDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vals := make([]string, 1000)
		for j := 0; j < 1000; j++ {
			vals[j] = fmt.Sprintf("(%d, 'b%d_%d', %f)", i*1000+j, i, j, float64(j)*1.1)
		}
		_, err := db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES %s;", strings.Join(vals, ",")))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- SELECT benchmarks ---

func BenchmarkSelectScan(b *testing.B) {
	db := setupDBWithData(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", "SELECT * FROM bench;")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelectWhere(b *testing.B) {
	db := setupDBWithIndex(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"SELECT * FROM bench WHERE id = %d;", i%1000))
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelectJoin(b *testing.B) {
	db := setupDB(b)
	// Create a second table and populate both for the join.
	db.Query("benchdb", `CREATE TABLE bench2 (
		id INT PRIMARY KEY,
		bench_id INT,
		extra TEXT
	);`)
	for i := 0; i < 500; i++ {
		db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES (%d, 'row_%d', %f);", i, i, float64(i)*1.1))
		db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench2 VALUES (%d, %d, 'extra_%d');", i, i, i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb",
			"SELECT bench.id, bench.name, bench2.extra FROM bench JOIN bench2 ON bench.id = bench2.bench_id;")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- UPDATE benchmark ---

func BenchmarkUpdateSingle(b *testing.B) {
	db := setupDBWithIndex(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"UPDATE bench SET value = 'updated_%d' WHERE id = %d;", i, i%1000))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- DELETE benchmark ---

func BenchmarkDeleteSingle(b *testing.B) {
	// Pre-populate with enough rows so each b.N iteration deletes a different row.
	db := setupDB(b)
	initialRows := 2000
	for i := 0; i < initialRows; i++ {
		db.Query("benchdb", fmt.Sprintf(
			"INSERT INTO bench VALUES (%d, 'del_%d', %f);", i, i, float64(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := db.Query("benchdb", fmt.Sprintf(
			"DELETE FROM bench WHERE id = %d;", i%initialRows))
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- Transaction benchmark ---

func BenchmarkTransaction10(b *testing.B) {
	db := setupDBWithIndex(b, 100)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Query("benchdb", "BEGIN;")
		for j := 0; j < 10; j++ {
			rowID := (i*10 + j) % 100
			db.Query("benchdb", fmt.Sprintf(
				"UPDATE bench SET value = 'tx_%d_%d' WHERE id = %d;", i, j, rowID))
		}
		db.Query("benchdb", "COMMIT;")
	}
}

// --- Concurrent inserts benchmark ---

func BenchmarkConcurrentInserts(b *testing.B) {
	db := setupDB(b)
	const goroutines = 10
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(gid int) {
				defer wg.Done()
				rowID := i*goroutines + gid
				db.Query("benchdb", fmt.Sprintf(
					"INSERT INTO bench VALUES (%d, 'c%d_%d', %f);", rowID, gid, i, float64(rowID)))
			}(g)
		}
		wg.Wait()
	}
}
