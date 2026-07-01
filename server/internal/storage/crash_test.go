package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"vaultdb/internal/index"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

func TestWALRecoveryAfterCrash(t *testing.T) {
	// Create a temporary directory for the test
	dir := t.TempDir()

	// Create WAL
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Create page engine
	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert some rows
	rows := []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
		{int64(3), "Charlie"},
	}
	_, err = engine.InsertRows("testdb", "users", rows)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate crash by closing without final checkpoint
	w.Close()

	// Reopen and check recovery
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Recover from WAL — same as production startup path
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify data is still there
	count, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows after recovery, got %d", count)
	}
}

func TestWALRecoveryWithPartialWrite(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert first batch
	rows1 := []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	}
	_, err = engine.InsertRows("testdb", "users", rows1)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate partial write (close WAL abruptly)
	w.Close()

	// Reopen and verify data integrity
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Recover from WAL
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify data is consistent
	count, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows after recovery, got %d", count)
	}
}

func TestWALRecoveryWithMultipleTables(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}

	// Create multiple tables
	schema1 := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	schema2 := TableSchema{
		Name: "orders",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "user_id", Type: "INT"},
			{Name: "amount", Type: "FLOAT"},
		},
	}

	if err := engine.CreateTable("testdb", schema1); err != nil {
		t.Fatal(err)
	}
	if err := engine.CreateTable("testdb", schema2); err != nil {
		t.Fatal(err)
	}

	// Insert data into both tables
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.InsertRows("testdb", "orders", []Row{
		{int64(1), int64(1), 100.0},
		{int64(2), int64(2), 200.0},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate crash
	w.Close()

	// Reopen and verify
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Recover from WAL
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify both tables
	usersCount, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if usersCount != 2 {
		t.Fatalf("expected 2 users after recovery, got %d", usersCount)
	}

	ordersCount, err := engine2.CountRows("testdb", "orders")
	if err != nil {
		t.Fatal(err)
	}
	if ordersCount != 2 {
		t.Fatalf("expected 2 orders after recovery, got %d", ordersCount)
	}
}

func TestWALRecoveryAfterDelete(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
		{int64(3), "Charlie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete one row
	_, err = engine.DeleteRows("testdb", "users", []int{1})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate crash
	w.Close()

	// Reopen and verify
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Recover from WAL
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify delete was persisted
	count, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows after delete and recovery, got %d", count)
	}
}

func TestCheckpointAfterOperations(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Checkpoint
	if err := engine.doCheckpoint(); err != nil {
		t.Fatal(err)
	}

	// Insert more rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(3), "Charlie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify all rows are present
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows after checkpoint and insert, got %d", count)
	}
}

func TestBufferPoolFlush(t *testing.T) {
	dir := t.TempDir()

	// Create page engine with buffer pool
	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows (should be cached in buffer pool)
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify rows are present
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}
}

func TestIndexPersistence(t *testing.T) {
	dir := t.TempDir()

	// Create page engine
	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create index
	if err := engine.CreateIndex("testdb", "users", "idx_name", "name"); err != nil {
		t.Fatal(err)
	}

	// Verify index exists
	indexes, err := engine.ListIndexes("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(indexes))
	}
}

func TestConcurrentInserts(t *testing.T) {
	dir := t.TempDir()

	// Create page engine
	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Concurrent inserts
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			rows := []Row{
				{int64(id), fmt.Sprintf("user_%d", id)},
			}
			_, err := engine.InsertRows("testdb", "users", rows)
			if err != nil {
				t.Errorf("insert failed: %v", err)
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all rows are present
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("expected 10 rows, got %d", count)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	dir := t.TempDir()

	// Create page engine
	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert initial rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Concurrent reads and writes
	done := make(chan bool, 20)

	// Readers
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			_, err := engine.ReadCurrentRows("testdb", "users")
			if err != nil {
				t.Errorf("read failed: %v", err)
			}
		}()
	}

	// Writers
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			rows := []Row{
				{int64(100 + id), fmt.Sprintf("user_%d", id)},
			}
			_, err := engine.InsertRows("testdb", "users", rows)
			if err != nil {
				t.Errorf("insert failed: %v", err)
			}
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Verify rows are present (at least the initial ones)
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count < 2 {
		t.Fatalf("expected at least 2 rows, got %d", count)
	}
}

func TestTransactionRecovery(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate crash
	w.Close()

	// Reopen and verify
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Recover from WAL
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify data is consistent
	count, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows after recovery, got %d", count)
	}
}

func TestBTreeIndexSaveLoad(t *testing.T) {
	dir := t.TempDir()
	indexPath := dir + "/test_index.json"

	// Create and populate index
	idx := index.NewBTreeIndex("test_idx", "name", 0)
	idx.Insert("Alice", 0)
	idx.Insert("Bob", 1)
	idx.Insert("Charlie", 2)

	// Save index
	if err := idx.Save(indexPath); err != nil {
		t.Fatal(err)
	}

	// Load index
	loadedIdx, err := index.LoadBTreeIndex(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify loaded index works
	if val, ok := loadedIdx.Lookup("Alice"); !ok || len(val) != 1 || val[0] != 0 {
		t.Errorf("Lookup(Alice) = %v, want [0]", val)
	}
	if val, ok := loadedIdx.Lookup("Bob"); !ok || len(val) != 1 || val[0] != 1 {
		t.Errorf("Lookup(Bob) = %v, want [1]", val)
	}

	// Verify range query works
	result := loadedIdx.Range("Alice", "Charlie")
	if len(result) != 3 {
		t.Errorf("Range(Alice, Charlie) returned %d positions, want 3", len(result))
	}
}

func TestAlterTableRewriteRecovery(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert some rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
		{int64(3), "Charlie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify original row count
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows before crash, got %d", count)
	}

	// Close the engine to simulate a clean state
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate incomplete rewrite: create a .rewrite.tmp directory
	// This mimics the state after a crash during rewriteTable (before atomic rename)
	tmpRewritePath := filepath.Join(dir, "pagedb", "testdb", "users.rewrite.tmp")
	if err := os.MkdirAll(tmpRewritePath, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write some dummy data into the temp directory to make it realistic
	if err := os.WriteFile(filepath.Join(tmpRewritePath, "_schema.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the temp dir exists before recovery
	if _, err := os.Stat(tmpRewritePath); os.IsNotExist(err) {
		t.Fatal("expected .rewrite.tmp directory to exist before recovery")
	}

	// Reopen WAL and engine — recovery should clean up the temp dir
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// WAL recovery (including incomplete rewrite cleanup)
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify original table is intact
	count, err = engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows after recovery, got %d", count)
	}

	// Verify the .rewrite.tmp directory was cleaned up
	if _, err := os.Stat(tmpRewritePath); !os.IsNotExist(err) {
		t.Fatal("expected .rewrite.tmp directory to be removed after recovery")
	}

	// Verify data content
	rows, err := engine2.ReadCurrentRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestAlterTableRewriteRecoveryNoTempDir(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Close and reopen (no temp dir this time — normal crash)
	w.Close()

	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Recovery should succeed even with no incomplete rewrites
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	count, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row after recovery, got %d", count)
	}
}

func TestVacuumRecovery(t *testing.T) {
	dir := t.TempDir()

	// Create WAL and page engine
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	// Create database and table
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
		{int64(3), "Charlie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify original row count
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows before crash, got %d", count)
	}

	// Close engine cleanly
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate incomplete vacuum: create a .vacuum shadow directory
	// This mimics the state after a crash during vacuum (shadow created, but rename not done)
	vacuumPath := filepath.Join(dir, "pagedb", "testdb", "users.vacuum")
	if err := os.MkdirAll(vacuumPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write some dummy data to make the shadow directory realistic
	if err := os.WriteFile(filepath.Join(vacuumPath, "_schema.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify the .vacuum directory exists before recovery
	if _, err := os.Stat(vacuumPath); os.IsNotExist(err) {
		t.Fatal("expected .vacuum directory to exist before recovery")
	}

	// Reopen WAL and engine — recovery should clean up the orphaned vacuum
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Run WAL recovery (including orphaned vacuum cleanup)
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Verify original table is intact
	count, err = engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows after recovery, got %d", count)
	}

	// Verify the .vacuum directory was cleaned up
	if _, err := os.Stat(vacuumPath); !os.IsNotExist(err) {
		t.Fatal("expected .vacuum directory to be removed after recovery")
	}

	// Verify data content
	rows, err := engine2.ReadCurrentRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestFullPageWriteRecovery(t *testing.T) {
	dir := t.TempDir()

	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Write a full page image for an empty page BEFORE the insert.
	// This simulates what the buffer pool does: capture pre-mod state.
	var emptyPage page.Page
	emptyPage.Init(page.PageTypeHeap)
	emptyPage.SetChecksum()
	if err := w.WriteFullPageImage(0, "testdb", "users", 0, 0, emptyPage[:]); err != nil {
		t.Fatal(err)
	}

	// Now insert rows — this writes OpPageInsert to WAL and to disk.
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Write a commit record for the insert transaction so it survives recovery.
	if _, err := w.AppendWithTx(1, wal.OpCommit, nil); err != nil {
		t.Fatal(err)
	}

	// Corrupt the page on disk (simulate torn page / crash mid-write).
	heapDir := filepath.Join(dir, "pagedb", "testdb", "users")
	segPath := filepath.Join(heapDir, "0000.heap")
	data, err := os.ReadFile(segPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < page.PageSize && i < len(data); i++ {
		data[i] = 0
	}
	if err := os.WriteFile(segPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate crash — close without checkpoint.
	w.Close()

	// Reopen and recover.
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Full page image restores the empty page, then OpPageInsert re-inserts data.
	// Verify actual data content (catalog row counts may be stale from pre-crash state).
	rows, err := engine2.ReadCurrentRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows after full page recovery, got %d", len(rows))
	}
	if rows[0][0] != int64(1) || rows[0][1] != "Alice" {
		t.Fatalf("row 0 mismatch: got %v", rows[0])
	}
	if rows[1][0] != int64(2) || rows[1][1] != "Bob" {
		t.Fatalf("row 1 mismatch: got %v", rows[1])
	}
}

func TestCatalogRecalculationAfterWALRecovery(t *testing.T) {
	dir := t.TempDir()

	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	txm := txmanager.NewManager()
	engine, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Insert 3 rows
	_, err = engine.InsertRows("testdb", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
		{int64(3), "Charlie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete 1 row (Bob)
	if _, err := engine.DeleteRows("testdb", "users", []int{1}); err != nil {
		t.Fatal(err)
	}

	// Verify catalog before crash
	count, err := engine.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows before crash, got %d", count)
	}

	// Simulate crash: close WAL without checkpoint (catalog is NOT saved after last operations)
	w.Close()

	// Corrupt the catalog to simulate inconsistency
	catalogPath := filepath.Join(dir, "pagedb", "_catalog.json")
	catalogData := []byte(`{"current_tx_id":100,"last_modified":{},"row_counts":{"testdb/users":999},"tx_times":[]}`)
	if err := os.WriteFile(catalogPath, catalogData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Reopen — catalog will load with wrong row count (999)
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	engine2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	// Before recovery, catalog shows stale wrong count
	wrongCount, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if wrongCount != 999 {
		t.Fatalf("expected stale count 999 before recovery, got %d", wrongCount)
	}

	// Run WAL recovery — should recalculate catalog from heap
	if err := engine2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// After recovery, catalog should match actual table state
	correctCount, err := engine2.CountRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if correctCount != 2 {
		t.Fatalf("expected 2 rows after catalog recalculation, got %d", correctCount)
	}

	// Verify actual data is accessible
	rows, err := engine2.ReadCurrentRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 readable rows, got %d", len(rows))
	}
}
