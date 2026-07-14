package storage

import (
	"sync"
	"testing"
)

func TestCatalogManagerCRUD(t *testing.T) {
	cm := NewCatalogManager()

	if cm.Len() != 0 {
		t.Fatalf("empty manager: Len() = %d, want 0", cm.Len())
	}

	schema := &TableSchema{
		Name: "users",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}

	// Set schema
	cm.SetSchema("shop", "users", schema)

	// Get schema
	got, ok := cm.GetSchema("shop", "users")
	if !ok {
		t.Fatal("GetSchema should find the schema")
	}
	if got.Name != "users" {
		t.Fatalf("schema name = %q, want %q", got.Name, "users")
	}

	// Len should be 1
	if cm.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", cm.Len())
	}

	// Remove schema
	cm.RemoveSchema("shop", "users")
	if _, ok := cm.GetSchema("shop", "users"); ok {
		t.Fatal("GetSchema should return false after RemoveSchema")
	}
	if cm.Len() != 0 {
		t.Fatalf("Len() after remove = %d, want 0", cm.Len())
	}

	// Clear
	cm.SetSchema("db1", "t1", schema)
	cm.SetSchema("db2", "t2", schema)
	cm.Clear()
	if cm.Len() != 0 {
		t.Fatalf("Len() after Clear = %d, want 0", cm.Len())
	}
}

func TestCatalogManagerConcurrent(t *testing.T) {
	cm := NewCatalogManager()

	var wg sync.WaitGroup
	const n = 100

	// Concurrent writers
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			schema := &TableSchema{Name: "table"}
			cm.SetSchema("db", "table", schema)
		}(i)
	}

	// Concurrent readers
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cm.GetSchema("db", "table")
		}()
	}

	wg.Wait()
}

func TestDMLExecutorIntegration(t *testing.T) {
	e := newPageEngine(t)

	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}
	if err := e.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}

	dml := e.DML()
	if dml == nil {
		t.Fatal("DML() returned nil")
	}

	// Insert
	n, err := dml.InsertRows("db", "users", []Row{
		{int64(1), "alice", 9.0},
		{int64(2), "bob", 7.0},
	})
	if err != nil || n != 2 {
		t.Fatalf("InsertRows: n=%d err=%v", n, err)
	}

	// Read to verify
	rows, err := e.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 2 {
		t.Fatalf("ReadCurrentRows: %d rows, err=%v", len(rows), err)
	}

	// Update
	n, err = dml.UpdateRows("db", "users", []int{0}, map[string]Value{"name": "ALICE"})
	if err != nil || n != 1 {
		t.Fatalf("UpdateRows: n=%d err=%v", n, err)
	}

	// Delete
	n, err = dml.DeleteRows("db", "users", []int{1})
	if err != nil || n != 1 {
		t.Fatalf("DeleteRows: n=%d err=%v", n, err)
	}

	rows, err = e.ReadCurrentRows("db", "users")
	if err != nil || len(rows) != 1 {
		t.Fatalf("final rows: %d, err=%v", len(rows), err)
	}
}

func TestDDLExecutorIntegration(t *testing.T) {
	e := newPageEngine(t)

	if err := e.CreateDatabase("db"); err != nil {
		t.Fatal(err)
	}

	ddl := e.DDL()
	if ddl == nil {
		t.Fatal("DDL() returned nil")
	}

	// Create
	if err := ddl.CreateTable("db", usersSchema()); err != nil {
		t.Fatal(err)
	}
	if !e.TableExists("db", "users") {
		t.Fatal("table should exist after Create")
	}

	// Insert data to verify the table works
	_, err := e.InsertRows("db", "users", []Row{{int64(1), "alice", 1.0}})
	if err != nil {
		t.Fatal(err)
	}

	// Drop
	if err := ddl.DropTable("db", "users"); err != nil {
		t.Fatal(err)
	}
	if e.TableExists("db", "users") {
		t.Fatal("table should not exist after Drop")
	}
}

func TestSubsystemsCreatedDuringInit(t *testing.T) {
	e := newPageEngine(t)

	if e.CatalogManager() == nil {
		t.Fatal("CatalogManager should be initialized")
	}
	if e.DML() == nil {
		t.Fatal("DML should be initialized")
	}
	if e.DDL() == nil {
		t.Fatal("DDL should be initialized")
	}
}
