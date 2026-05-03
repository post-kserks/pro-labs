package storage

import (
	"sync"
	"testing"
)

func testSchema(dbName string) TableSchema {
	return TableSchema{
		Name:     "heroes",
		Database: dbName,
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "VARCHAR", VarcharLen: 100},
			{Name: "level", Type: "INT"},
			{Name: "alive", Type: "BOOL"},
		},
	}
}

func TestDatabaseLifecycle(t *testing.T) {
	store := NewFileStorageEngine(t.TempDir())

	if store.DatabaseExists("mydb") {
		t.Fatal("database should not exist")
	}
	if err := store.CreateDatabase("mydb"); err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}
	if !store.DatabaseExists("mydb") {
		t.Fatal("database should exist")
	}

	dbs, err := store.ListDatabases()
	if err != nil {
		t.Fatalf("ListDatabases failed: %v", err)
	}
	if len(dbs) != 1 || dbs[0] != "mydb" {
		t.Fatalf("unexpected db list: %#v", dbs)
	}

	if err := store.DropDatabase("mydb"); err != nil {
		t.Fatalf("DropDatabase failed: %v", err)
	}
	if store.DatabaseExists("mydb") {
		t.Fatal("database should be removed")
	}
}

func TestTableLifecycleAndDataOperations(t *testing.T) {
	store := NewFileStorageEngine(t.TempDir())
	if err := store.CreateDatabase("mydb"); err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}

	schema := testSchema("mydb")
	if err := store.CreateTable("mydb", schema); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if !store.TableExists("mydb", "heroes") {
		t.Fatal("table should exist")
	}

	inserted, err := store.InsertRows("mydb", "heroes", []Row{
		{int64(1), "Aragorn", int64(10), true},
		{int64(2), "Legolas", int64(9), true},
	})
	if err != nil {
		t.Fatalf("InsertRows failed: %v", err)
	}
	if inserted != 2 {
		t.Fatalf("expected 2 inserted rows, got %d", inserted)
	}

	tables, err := store.ListTables("mydb")
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "heroes" || tables[0].RowCount != 2 {
		t.Fatalf("unexpected table list: %#v", tables)
	}

	count, err := store.CountRows("mydb", "heroes")
	if err != nil {
		t.Fatalf("CountRows failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected row count 2, got %d", count)
	}

	rows, err := store.SelectRows("mydb", "heroes")
	if err != nil {
		t.Fatalf("SelectRows failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	updated, err := store.UpdateRows("mydb", "heroes", []int{1}, map[string]Value{"level": int64(11)})
	if err != nil {
		t.Fatalf("UpdateRows failed: %v", err)
	}
	if updated != 1 {
		t.Fatalf("expected 1 updated row, got %d", updated)
	}

	rows, err = store.SelectRows("mydb", "heroes")
	if err != nil {
		t.Fatalf("SelectRows failed: %v", err)
	}
	if rows[1][2].(int64) != 11 {
		t.Fatalf("expected updated level=11, got %#v", rows[1][2])
	}

	deleted, err := store.DeleteRows("mydb", "heroes", []int{0})
	if err != nil {
		t.Fatalf("DeleteRows failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted row, got %d", deleted)
	}

	rows, err = store.SelectRows("mydb", "heroes")
	if err != nil {
		t.Fatalf("SelectRows failed: %v", err)
	}
	if len(rows) != 1 || rows[0][1].(string) != "Legolas" {
		t.Fatalf("unexpected rows after delete: %#v", rows)
	}

	if err := store.DropTable("mydb", "heroes"); err != nil {
		t.Fatalf("DropTable failed: %v", err)
	}
	if store.TableExists("mydb", "heroes") {
		t.Fatal("table should be removed")
	}
}

func TestPersistenceAcrossInstances(t *testing.T) {
	root := t.TempDir()

	store1 := NewFileStorageEngine(root)
	if err := store1.CreateDatabase("mydb"); err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}
	if err := store1.CreateTable("mydb", testSchema("mydb")); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	if _, err := store1.InsertRows("mydb", "heroes", []Row{{int64(1), "Aragorn", int64(10), true}}); err != nil {
		t.Fatalf("InsertRows failed: %v", err)
	}

	store2 := NewFileStorageEngine(root)
	rows, err := store2.SelectRows("mydb", "heroes")
	if err != nil {
		t.Fatalf("SelectRows failed: %v", err)
	}
	if len(rows) != 1 || rows[0][1].(string) != "Aragorn" {
		t.Fatalf("unexpected persisted rows: %#v", rows)
	}
}

func TestParallelInsertRows(t *testing.T) {
	store := NewFileStorageEngine(t.TempDir())
	if err := store.CreateDatabase("mydb"); err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}
	if err := store.CreateTable("mydb", testSchema("mydb")); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.InsertRows("mydb", "heroes", []Row{{int64(i + 1), "Hero", int64(i), true}})
			if err != nil {
				t.Errorf("InsertRows failed: %v", err)
			}
		}()
	}
	wg.Wait()

	rows, err := store.SelectRows("mydb", "heroes")
	if err != nil {
		t.Fatalf("SelectRows failed: %v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("expected 20 rows, got %d", len(rows))
	}
}
