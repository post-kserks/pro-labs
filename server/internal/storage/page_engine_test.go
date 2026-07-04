package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

// Компайл-проверка: PageStorageEngine реализует StorageEngine.
var _ StorageEngine = (*PageStorageEngine)(nil)

func newPageEngine(t *testing.T) *PageStorageEngine {
	t.Helper()
	e, err := NewPageStorageEngine(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func usersSchema() TableSchema {
	return TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
		},
	}
}

func TestPageEngineCRUD(t *testing.T) {
	e := newPageEngine(t)

	if err := e.CreateDatabase("shop"); err != nil {
		t.Fatal(err)
	}
	if !e.DatabaseExists("shop") {
		t.Fatal("database must exist")
	}
	if err := e.CreateTable("shop", usersSchema()); err != nil {
		t.Fatal(err)
	}

	n, err := e.InsertRows("shop", "users", []Row{
		{int64(1), "alice", 9.5},
		{int64(2), "bob", 7.0},
		{int64(3), "carol", 8.2},
	})
	if err != nil || n != 3 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}

	rows, err := e.ReadCurrentRows("shop", "users")
	if err != nil || len(rows) != 3 {
		t.Fatalf("read: %d rows, err=%v", len(rows), err)
	}
	if rows[0][0] != int64(1) || rows[0][1] != "alice" || rows[0][2] != 9.5 {
		t.Fatalf("row roundtrip mismatch: %#v", rows[0])
	}

	// UPDATE второй строки (позиция 1)
	if n, err := e.UpdateRows("shop", "users", []int{1}, map[string]Value{"score": 7.7}); err != nil || n != 1 {
		t.Fatalf("update: n=%d err=%v", n, err)
	}
	rows, _ = e.ReadCurrentRows("shop", "users")
	if len(rows) != 3 {
		t.Fatalf("after update: %d rows", len(rows))
	}
	foundUpdated := false
	for _, r := range rows {
		if r[0] == int64(2) && r[2] == 7.7 {
			foundUpdated = true
		}
	}
	if !foundUpdated {
		t.Fatalf("updated row not found: %#v", rows)
	}

	// DELETE первой строки
	if n, err := e.DeleteRows("shop", "users", []int{0}); err != nil || n != 1 {
		t.Fatalf("delete: n=%d err=%v", n, err)
	}
	rows, _ = e.ReadCurrentRows("shop", "users")
	if len(rows) != 2 {
		t.Fatalf("after delete: %d rows", len(rows))
	}

	count, err := e.CountRows("shop", "users")
	if err != nil || count != 2 {
		t.Fatalf("count: %d err=%v", count, err)
	}
}

func TestPageEngineTimeTravel(t *testing.T) {
	e := newPageEngine(t)
	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}

	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.0}})
	txAfterInsert := e.CurrentTxID()
	_, _ = e.DeleteRows("db", "users", []int{0})

	current, _ := e.ReadCurrentRows("db", "users")
	if len(current) != 0 {
		t.Fatalf("current rows = %d, want 0", len(current))
	}

	asOf, err := e.ReadRowsAsOf("db", "users", txAfterInsert)
	if err != nil || len(asOf) != 1 {
		t.Fatalf("AS OF rows = %d err=%v, want 1", len(asOf), err)
	}

	history, err := e.RowHistory("db", "users", int64(1))
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %d err=%v", len(history), err)
	}
	if history[0].DeletedTx == 0 {
		t.Fatal("history entry must be marked deleted")
	}
}

func TestPageEnginePersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.5}}); err != nil {
		t.Fatal(err)
	}
	// Flush dirty pages to disk before simulating restart.
	// With write-back buffer pool, data only reaches disk on flush/close.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	tx := uint64(1) // txID was 1 after insert

	e2, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if e2.CurrentTxID() != tx {
		t.Fatalf("tx counter lost: %d != %d", e2.CurrentTxID(), tx)
	}
	rows, err := e2.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 1 || rows[0][1] != "alice" {
		t.Fatalf("rows after reopen: %#v err=%v", rows, err)
	}
}

func TestPageEngineVacuum(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}, {int64(2), "b", 2.0}})
	_, _ = e.DeleteRows("db", "users", []int{0})

	stats, err := e.Vacuum("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if stats.RowsBefore != 2 || stats.RowsAfter != 1 || stats.ReclaimedRows != 1 {
		t.Fatalf("vacuum stats: %+v", stats)
	}

	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 1 || rows[0][0] != int64(2) {
		t.Fatalf("rows after vacuum: %#v", rows)
	}

	vstats, _ := e.TableVersionStats("db", "users")
	if vstats.TotalRows != 1 || vstats.DeadRows != 0 {
		t.Fatalf("version stats after vacuum: %+v", vstats)
	}
}

func TestPageEngineAlterTable(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.0}})

	if err := e.AlterTableAddColumn("db", "users", ColumnSchema{Name: "age", Type: "INT"}, int64(18)); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 1 || len(rows[0]) != 4 || rows[0][3] != int64(18) {
		t.Fatalf("after add column: %#v", rows)
	}

	if err := e.AlterTableRenameColumn("db", "users", "age", "years"); err != nil {
		t.Fatal(err)
	}
	schema, _ := e.GetTableSchema("db", "users")
	if schema.Columns[3].Name != "years" {
		t.Fatalf("rename column: %#v", schema.Columns)
	}

	if err := e.AlterTableDropColumn("db", "users", "score"); err != nil {
		t.Fatal(err)
	}
	rows, _ = e.ReadCurrentRows("db", "users")
	if len(rows[0]) != 3 || rows[0][2] != int64(18) {
		t.Fatalf("after drop column: %#v", rows)
	}

	if err := e.AlterTableRenameTable("db", "users", "people"); err != nil {
		t.Fatal(err)
	}
	if e.TableExists("db", "users") || !e.TableExists("db", "people") {
		t.Fatal("rename table failed")
	}
}

func TestPageEngineManyRowsSpanPages(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Достаточно строк, чтобы заполнить несколько страниц по 8 КБ
	batch := make([]Row, 500)
	for i := range batch {
		batch[i] = Row{int64(i), "user-with-a-reasonably-long-name-" + string(rune('a'+i%26)), float64(i)}
	}
	for i := 0; i < 4; i++ {
		if _, err := e.InsertRows("db", "users", batch); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 2000 {
		t.Fatalf("rows = %d err=%v, want 2000", len(rows), err)
	}
}

func TestPageEngineSecondaryIndexes(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert some rows
	_, err := e.InsertRows("db", "users", []Row{
		{int64(1), "Alice"},
		{int64(2), "Bob"},
		{int64(3), "Charlie"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create index
	if err := e.CreateIndex("db", "users", "idx_name", "name"); err != nil {
		t.Fatalf("CreateIndex failed: %v", err)
	}

	// Verify index exists
	idxName, found := e.FindIndexForColumn("db", "users", "name")
	if !found || idxName != "idx_name" {
		t.Fatalf("FindIndexForColumn: got (%q, %v), want (idx_name, true)", idxName, found)
	}

	// List indexes
	indexes, err := e.ListIndexes("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 1 || indexes[0] != "idx_name" {
		t.Fatalf("ListIndexes: got %v, want [idx_name]", indexes)
	}

	// Index lookup
	positions, ok := e.IndexLookup("db", "users", "name", "Bob")
	if !ok {
		t.Fatal("IndexLookup should find Bob")
	}
	if len(positions) != 1 {
		t.Fatalf("IndexLookup: got %d positions, want 1", len(positions))
	}

	// Duplicate index should fail
	if err := e.CreateIndex("db", "users", "idx_name2", "name"); err == nil {
		t.Fatal("duplicate CreateIndex should fail")
	}

	// Drop index
	if err := e.DropIndex("db", "idx_name"); err != nil {
		t.Fatalf("DropIndex failed: %v", err)
	}
	if _, found := e.FindIndexForColumn("db", "users", "name"); found {
		t.Fatal("FindIndexForColumn should report no index after drop")
	}
}

func TestMVCCVisibility(t *testing.T) {
	mgr := txmanager.NewManager()
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, mgr)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}

	// Insert row via auto-commit (page engine assigns createdTx)
	_, err = e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.0}})
	if err != nil {
		t.Fatal(err)
	}

	// Begin a new transaction — previously committed rows must remain visible
	_ = mgr.Begin()
	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("committed row not visible after Begin: got %d rows, want 1", len(rows))
	}
	if rows[0][0] != int64(1) || rows[0][1] != "alice" {
		t.Fatalf("wrong row data: %#v", rows[0])
	}

	// Insert another row, commit via auto-commit
	_, err = e.InsertRows("db", "users", []Row{{int64(2), "bob", 2.0}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestTruncateTable(t *testing.T) {
	e := newPageEngine(t)

	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}

	// Insert rows
	_, err := e.InsertRows("db", "users", []Row{
		{int64(1), "alice", 9.5},
		{int64(2), "bob", 7.0},
		{int64(3), "carol", 8.2},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify rows exist
	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("before truncate: expected 3 rows, got %d", len(rows))
	}

	// Truncate the table
	if err := e.TruncateTable("db", "users"); err != nil {
		t.Fatal(err)
	}

	// Verify all rows are gone
	rows, err = e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("after truncate: expected 0 rows, got %d", len(rows))
	}

	// Verify catalog row count
	count, err := e.CountRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("after truncate: expected 0 count, got %d", count)
	}

	// Verify table still exists and can accept new inserts
	if !e.TableExists("db", "users") {
		t.Fatal("table should still exist after truncate")
	}
	_, err = e.InsertRows("db", "users", []Row{
		{int64(4), "dave", 6.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after re-insert: expected 1 row, got %d", len(rows))
	}
	if rows[0][0] != int64(4) || rows[0][1] != "dave" {
		t.Fatalf("re-inserted row mismatch: %#v", rows[0])
	}
}

func TestTruncateTableEmpty(t *testing.T) {
	e := newPageEngine(t)

	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}

	// Truncate an empty table — should succeed
	if err := e.TruncateTable("db", "users"); err != nil {
		t.Fatal(err)
	}

	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestTruncateTablePersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.InsertRows("db", "users", []Row{
		{int64(1), "alice", 1.0},
		{int64(2), "bob", 2.0},
	}); err != nil {
		t.Fatal(err)
	}

	// Truncate and close
	if err := e.TruncateTable("db", "users"); err != nil {
		t.Fatal(err)
	}
	tx := e.CurrentTxID()
	_ = e.Close()

	// Reopen and verify
	e2, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if e2.CurrentTxID() != tx {
		t.Fatalf("tx counter lost: %d != %d", e2.CurrentTxID(), tx)
	}
	rows, err := e2.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("after reopen: expected 0 rows, got %d", len(rows))
	}

	// Verify we can insert new data
	if _, err := e2.InsertRows("db", "users", []Row{
		{int64(3), "carol", 3.0},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = e2.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 1 {
		t.Fatalf("after re-insert post-truncate: %d rows, err=%v", len(rows), err)
	}
	if rows[0][0] != int64(3) || rows[0][1] != "carol" {
		t.Fatalf("re-inserted row mismatch: %#v", rows[0])
	}
}

func TestVacuumReclaimSpace(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert many rows
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
		{int64(3), "c", 3.0},
		{int64(4), "d", 4.0},
	})

	// Delete half
	_, _ = e.DeleteRows("db", "users", []int{0, 2})

	// Check version stats before vacuum — dead rows exist
	vstats, _ := e.TableVersionStats("db", "users")
	if vstats.TotalRows != 4 {
		t.Fatalf("before vacuum: total=%d, want 4", vstats.TotalRows)
	}
	if vstats.DeadRows != 2 {
		t.Fatalf("before vacuum: dead=%d, want 2", vstats.DeadRows)
	}

	// Record file size before vacuum
	sizeBefore := e.tableSizeLocked("db", "users")

	stats, err := e.Vacuum("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if stats.ReclaimedRows != 2 {
		t.Fatalf("vacuum reclaimed: %d, want 2", stats.ReclaimedRows)
	}

	// Verify no dead rows remain
	vstats, _ = e.TableVersionStats("db", "users")
	if vstats.DeadRows != 0 {
		t.Fatalf("after vacuum: dead=%d, want 0", vstats.DeadRows)
	}
	if vstats.TotalRows != 2 {
		t.Fatalf("after vacuum: total=%d, want 2", vstats.TotalRows)
	}

	// File size should not have grown
	sizeAfter := e.tableSizeLocked("db", "users")
	if sizeAfter > sizeBefore {
		t.Fatalf("file grew after vacuum: before=%d after=%d", sizeBefore, sizeAfter)
	}

	// Remaining rows should be correct
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 2 {
		t.Fatalf("rows after vacuum: %d, want 2", len(rows))
	}
}

func TestVacuumEmptyTable(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	stats, err := e.Vacuum("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if stats.RowsBefore != 0 || stats.RowsAfter != 0 || stats.ReclaimedRows != 0 {
		t.Fatalf("vacuum on empty: %+v", stats)
	}
}

func TestVacuumAllDead(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})
	// Delete all rows
	_, _ = e.DeleteRows("db", "users", []int{0, 1})

	stats, err := e.Vacuum("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if stats.RowsBefore != 2 || stats.RowsAfter != 0 || stats.ReclaimedRows != 2 {
		t.Fatalf("vacuum all dead: %+v", stats)
	}

	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 0 {
		t.Fatalf("after vacuum all dead: %d rows, want 0", len(rows))
	}
}

func TestVacuumConcurrentReads(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert data
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})
	_, _ = e.DeleteRows("db", "users", []int{0})

	// Start concurrent reads while vacuum runs
	var wg sync.WaitGroup
	done := make(chan struct{})
	errCh := make(chan error, 10)

	// Launch readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					rows, err := e.ReadCurrentRows("db", "users")
					if err != nil {
						errCh <- err
						return
					}
					// Rows count should be 0 or 1 (vacuum may be in progress)
					if len(rows) > 1 {
						errCh <- fmt.Errorf("reader %d: unexpected row count %d", id, len(rows))
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Launch vacuum
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := e.Vacuum("db", "users"); err != nil {
			errCh <- err
		}
	}()

	// Wait a bit for concurrent operations
	time.Sleep(50 * time.Millisecond)
	close(done)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent error: %v", err)
	}

	// Verify final state
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 1 {
		t.Fatalf("final rows: %d, want 1", len(rows))
	}
}

func TestTruncateTableRecovery(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	txm := txmanager.NewManager()
	e, err := NewPageStorageEngine(dir, w, txm)
	if err != nil {
		t.Fatal(err)
	}

	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})

	// Truncate
	if err := e.TruncateTable("db", "users"); err != nil {
		t.Fatal(err)
	}

	// Verify empty
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 0 {
		t.Fatalf("after truncate: %d rows, want 0", len(rows))
	}

	// Close without checkpoint (simulate crash)
	w.Close()

	// Reopen and recover
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	txm2 := txmanager.NewManager()
	e2, err := NewPageStorageEngine(dir, w2, txm2)
	if err != nil {
		t.Fatal(err)
	}

	if err := e2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// Table should still be empty after recovery
	rows, err = e2.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("after recovery: %d rows, want 0", len(rows))
	}

	// Should be able to insert new data
	_, err = e2.InsertRows("db", "users", []Row{{int64(3), "c", 3.0}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = e2.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 1 {
		t.Fatalf("after re-insert: %d rows, err=%v", len(rows), err)
	}
}

func TestTruncateTableMultiple(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert, truncate, insert again, truncate again
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})
	_ = e.TruncateTable("db", "users")

	_, _ = e.InsertRows("db", "users", []Row{
		{int64(2), "b", 2.0},
		{int64(3), "c", 3.0},
	})
	_ = e.TruncateTable("db", "users")

	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 0 {
		t.Fatalf("after double truncate: %d rows, err=%v", len(rows), err)
	}

	// Insert once more — should work
	_, _ = e.InsertRows("db", "users", []Row{{int64(4), "d", 4.0}})
	rows, _ = e.ReadCurrentRows("db", "users")
	if len(rows) != 1 || rows[0][0] != int64(4) {
		t.Fatalf("after triple insert: %#v", rows)
	}
}

func TestRowHistoryMultipleVersions(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert version 1
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "alice_v1", 1.0}})
	tx1 := e.CurrentTxID()

	// Delete
	_, _ = e.DeleteRows("db", "users", []int{0})

	// Insert version 2 (re-insert same PK)
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "alice_v2", 2.0}})
	tx3 := e.CurrentTxID()

	history, err := e.RowHistory("db", "users", int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history length: %d, want 2", len(history))
	}

	// First version: created at tx1, deleted at tx2
	if history[0].CreatedTx != tx1 {
		t.Fatalf("history[0].CreatedTx = %d, want %d", history[0].CreatedTx, tx1)
	}
	if history[0].DeletedTx == 0 {
		t.Fatal("first version should be deleted")
	}

	// Second version: created at tx3, not deleted
	if history[1].CreatedTx != tx3 {
		t.Fatalf("history[1].CreatedTx = %d, want %d", history[1].CreatedTx, tx3)
	}
	if history[1].DeletedTx != 0 {
		t.Fatalf("second version should not be deleted, got DeletedTx=%d", history[1].DeletedTx)
	}

	// Current rows should show only the live version
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 1 || rows[0][1] != "alice_v2" {
		t.Fatalf("current rows: %#v", rows)
	}
}

func TestRowHistoryNonExistentPK(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.0}})

	history, err := e.RowHistory("db", "users", int64(999))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("history for non-existent PK: %d, want 0", len(history))
	}
}

func TestTableVersionStatsAfterInsertDelete(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Empty table
	stats, _ := e.TableVersionStats("db", "users")
	if stats.TotalRows != 0 || stats.DeadRows != 0 {
		t.Fatalf("empty table stats: %+v", stats)
	}

	// Insert 5 rows
	rows := make([]Row, 5)
	for i := range rows {
		rows[i] = Row{int64(i), "user", float64(i)}
	}
	_, _ = e.InsertRows("db", "users", rows)

	stats, _ = e.TableVersionStats("db", "users")
	if stats.TotalRows != 5 || stats.DeadRows != 0 {
		t.Fatalf("after insert: %+v", stats)
	}

	// Delete 3 rows (indices 0, 1, 2)
	_, _ = e.DeleteRows("db", "users", []int{0, 1, 2})

	stats, _ = e.TableVersionStats("db", "users")
	if stats.TotalRows != 5 {
		t.Fatalf("after delete total: %d, want 5", stats.TotalRows)
	}
	if stats.DeadRows != 3 {
		t.Fatalf("after delete dead: %d, want 3", stats.DeadRows)
	}
}

func TestTableVersionStatsAfterUpdate(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "alice", 1.0},
		{int64(2), "bob", 2.0},
	})

	// Update creates a new version (old one marked dead)
	_, _ = e.UpdateRows("db", "users", []int{0}, map[string]Value{"name": "alice_v2"})

	stats, _ := e.TableVersionStats("db", "users")
	if stats.TotalRows != 3 {
		t.Fatalf("after update total: %d, want 3", stats.TotalRows)
	}
	if stats.DeadRows != 1 {
		t.Fatalf("after update dead: %d, want 1", stats.DeadRows)
	}

	// After vacuum, only live rows remain (updated alice + bob)
	_, _ = e.Vacuum("db", "users")

	stats, _ = e.TableVersionStats("db", "users")
	if stats.TotalRows != 2 || stats.DeadRows != 0 {
		t.Fatalf("after vacuum: %+v", stats)
	}
}

func TestVacuumPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})
	_, _ = e.DeleteRows("db", "users", []int{0})

	_, _ = e.Vacuum("db", "users")

	// Verify vacuum result within the same engine session
	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0][0] != int64(2) {
		t.Fatalf("after vacuum: %#v", rows)
	}

	stats, _ := e.TableVersionStats("db", "users")
	if stats.TotalRows != 1 || stats.DeadRows != 0 {
		t.Fatalf("after vacuum stats: %+v", stats)
	}

	// Verify version stats stay correct after more operations
	_, _ = e.InsertRows("db", "users", []Row{{int64(3), "c", 3.0}})
	stats, _ = e.TableVersionStats("db", "users")
	if stats.TotalRows != 2 || stats.DeadRows != 0 {
		t.Fatalf("after re-insert stats: %+v", stats)
	}

	// Finalize and close cleanly
	_ = e.Close()

	// Reopen and verify catalog state persisted (no dead rows)
	e2, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	// Read current rows — catalog should reflect correct count
	count, err := e2.CountRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("after reopen count: %d, want 2", count)
	}
}

func TestTruncateTableEmptyPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Truncate an empty table, then close
	if err := e.TruncateTable("db", "users"); err != nil {
		t.Fatal(err)
	}
	_ = e.Close()

	// Reopen
	e2, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	rows, err := e2.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("after reopen empty truncate: %d rows, want 0", len(rows))
	}
}

// pkSchema returns a schema with a PRIMARY KEY column on "id".
func pkSchema() TableSchema {
	return TableSchema{
		Name: "items",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT", PrimaryKey: true},
			{Name: "name", Type: "TEXT"},
		},
	}
}

func TestInsertWithPKIndex(t *testing.T) {
	e := newPageEngine(t)
	if err := e.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("testdb", pkSchema()); err != nil {
		t.Fatal(err)
	}

	// Verify that the BTree index was auto-created
	indexes, err := e.ListIndexes("testdb", "items")
	if err != nil {
		t.Fatal(err)
	}
	if len(indexes) == 0 {
		t.Fatal("expected auto-created PK index")
	}

	// Insert should succeed
	n, err := e.InsertRows("testdb", "items", []Row{
		{int64(1), "apple"},
		{int64(2), "banana"},
		{int64(3), "cherry"},
	})
	if err != nil || n != 3 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}

	// Duplicate PK should fail
	_, err = e.InsertRows("testdb", "items", []Row{
		{int64(1), "apple_dup"},
	})
	if err == nil {
		t.Fatal("expected duplicate PK error")
	}

	// Batch with duplicate within itself should fail
	_, err = e.InsertRows("testdb", "items", []Row{
		{int64(4), "date"},
		{int64(4), "date_dup"},
	})
	if err == nil {
		t.Fatal("expected duplicate PK error within batch")
	}
}

func BenchmarkInsertWithIndex(b *testing.B) {
	dir := b.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()

	if err := e.CreateDatabase("benchdb"); err != nil {
		b.Fatal(err)
	}
	if err := e.CreateTable("benchdb", pkSchema()); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.InsertRows("benchdb", "items", []Row{
			{int64(i), "item"},
		})
	}
}

func BenchmarkInsertWithoutIndex(b *testing.B) {
	dir := b.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()

	if err := e.CreateDatabase("benchdb"); err != nil {
		b.Fatal(err)
	}
	// Table WITHOUT primary key (no auto-index)
	schema := TableSchema{
		Name: "items_nopk",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := e.CreateTable("benchdb", schema); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.InsertRows("benchdb", "items_nopk", []Row{
			{int64(i), "item"},
		})
	}
}

func BenchmarkInsertBatchWithIndex(b *testing.B) {
	dir := b.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()

	if err := e.CreateDatabase("benchdb"); err != nil {
		b.Fatal(err)
	}
	if err := e.CreateTable("benchdb", pkSchema()); err != nil {
		b.Fatal(err)
	}

	batchSize := 100
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := make([]Row, batchSize)
		for j := 0; j < batchSize; j++ {
			rows[j] = Row{int64(i*batchSize + j), "item"}
		}
		_, _ = e.InsertRows("benchdb", "items", rows)
	}
}

func BenchmarkInsertBatchWithoutIndex(b *testing.B) {
	dir := b.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()

	if err := e.CreateDatabase("benchdb"); err != nil {
		b.Fatal(err)
	}
	schema := TableSchema{
		Name: "items_nopk",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	if err := e.CreateTable("benchdb", schema); err != nil {
		b.Fatal(err)
	}

	batchSize := 100
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := make([]Row, batchSize)
		for j := 0; j < batchSize; j++ {
			rows[j] = Row{int64(i*batchSize + j), "item"}
		}
		_, _ = e.InsertRows("benchdb", "items_nopk", rows)
	}
}

func TestAtomicTxIDAllocation(t *testing.T) {
	e := newPageEngine(t)
	if err := e.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("testdb", usersSchema()); err != nil {
		t.Fatal(err)
	}

	// Allocate txIDs concurrently and verify they are unique.
	const numGoroutines = 20
	const perGoroutine = 50
	seen := make(map[uint64]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				txID := e.nextTxID()
				mu.Lock()
				if seen[txID] {
					t.Errorf("duplicate txID %d", txID)
				}
				seen[txID] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	expected := uint64(numGoroutines * perGoroutine)
	if uint64(len(seen)) != expected {
		t.Errorf("expected %d unique txIDs, got %d", expected, len(seen))
	}

	// Verify counter is at least as high as what we allocated.
	if got := e.txCounter.Load(); got < expected {
		t.Errorf("txCounter = %d, want >= %d", got, expected)
	}
}

func TestPerTableCounters(t *testing.T) {
	e := newPageEngine(t)
	if err := e.CreateDatabase("shop"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("shop", usersSchema()); err != nil {
		t.Fatal(err)
	}

	// Insert rows.
	n, err := e.InsertRows("shop", "users", []Row{
		{int64(1), "alice", 9.0},
		{int64(2), "bob", 7.0},
	})
	if err != nil || n != 2 {
		t.Fatalf("insert: n=%d err=%v", n, err)
	}

	// Verify per-table atomic counters.
	key := "shop/users"
	e.mu.RLock()
	tbl := e.tables[key]
	e.mu.RUnlock()
	if tbl == nil {
		t.Fatal("table not found")
	}
	if got := tbl.rowCount.Load(); got != 2 {
		t.Errorf("rowCount = %d, want 2", got)
	}
	if got := tbl.lastTxID.Load(); got == 0 {
		t.Error("lastTxID should be non-zero after insert")
	}

	// Delete one row.
	if _, err := e.DeleteRows("shop", "users", []int{0}); err != nil {
		t.Fatal(err)
	}
	if got := tbl.rowCount.Load(); got != 1 {
		t.Errorf("rowCount after delete = %d, want 1", got)
	}

	// Verify catalog is in sync.
	e.mu.RLock()
	catalogCount := e.catalog.RowCounts[key]
	catalogLM := e.catalog.LastModified[key]
	e.mu.RUnlock()
	if catalogCount != 1 {
		t.Errorf("catalog RowCount = %d, want 1", catalogCount)
	}
	if catalogLM != tbl.lastTxID.Load() {
		t.Errorf("catalog LastModified = %d, want %d", catalogLM, tbl.lastTxID.Load())
	}
}

func TestAtomicCounterAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("testdb", usersSchema()); err != nil {
		t.Fatal(err)
	}

	// Insert a row to generate txIDs.
	_, err = e.InsertRows("testdb", "users", []Row{{int64(1), "alice", 9.0}})
	if err != nil {
		t.Fatal(err)
	}
	txBefore := e.txCounter.Load()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify counter is restored.
	e2, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	txAfter := e2.txCounter.Load()
	if txAfter != txBefore {
		t.Errorf("txCounter after reopen = %d, want %d", txAfter, txBefore)
	}

	// New inserts should continue from where we left off.
	_, err = e2.InsertRows("testdb", "users", []Row{{int64(2), "bob", 7.0}})
	if err != nil {
		t.Fatal(err)
	}
	if got := e2.txCounter.Load(); got <= txBefore {
		t.Errorf("txCounter after second insert = %d, want > %d", got, txBefore)
	}
}

func BenchmarkConcurrentInsertsDifferentTables(b *testing.B) {
	dir := b.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer e.Close()

	if err := e.CreateDatabase("benchdb"); err != nil {
		b.Fatal(err)
	}

	numTables := 4
	for i := 0; i < numTables; i++ {
		schema := TableSchema{
			Name:    fmt.Sprintf("table_%d", i),
			Columns: []ColumnSchema{{Name: "id", Type: "INT"}, {Name: "val", Type: "TEXT"}},
		}
		if err := e.CreateTable("benchdb", schema); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tableName := fmt.Sprintf("table_%d", i%numTables)
			_, _ = e.InsertRows("benchdb", tableName, []Row{
				{int64(i), "val"},
			})
			i++
		}
	})
}
