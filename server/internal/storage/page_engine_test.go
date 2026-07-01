package storage

import (
	"testing"

	"vaultdb/internal/txmanager"
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
