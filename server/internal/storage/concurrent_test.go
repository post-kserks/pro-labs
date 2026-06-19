package storage

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentInsertRows(t *testing.T) {
	engine := newTestPageEngine(t)

	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name:    "users",
		Columns: []ColumnSchema{{Name: "id", Type: "INT"}, {Name: "name", Type: "TEXT"}},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	rowsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < rowsPerGoroutine; j++ {
				row := Row{int64(g*rowsPerGoroutine + j), "user"}
				_, err := engine.InsertRows("testdb", "users", []Row{row})
				if err != nil {
					t.Errorf("goroutine %d: insert failed: %v", g, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	rows, err := engine.ReadCurrentRows("testdb", "users")
	if err != nil {
		t.Fatal(err)
	}
	expected := numGoroutines * rowsPerGoroutine
	if len(rows) != expected {
		t.Errorf("row count = %d, want %d", len(rows), expected)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	engine := newTestPageEngine(t)

	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name:    "users",
		Columns: []ColumnSchema{{Name: "id", Type: "INT"}, {Name: "name", Type: "TEXT"}},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		row := Row{int64(i), "user"}
		if _, err := engine.InsertRows("testdb", "users", []Row{row}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	numReaders := 5
	numWriters := 5

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, err := engine.ReadCurrentRows("testdb", "users")
				if err != nil {
					t.Errorf("reader %d: read failed: %v", g, err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				row := Row{int64(1000 + g*10 + j), "newuser"}
				if _, err := engine.InsertRows("testdb", "users", []Row{row}); err != nil {
					t.Errorf("writer %d: insert failed: %v", g, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestConcurrentTableOperations(t *testing.T) {
	engine := newTestPageEngine(t)

	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var inserts atomic.Int64
	numOps := 10

	for i := 0; i < numOps; i++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			tableName := "table" + string(rune('A'+g))
			schema := TableSchema{
				Name:    tableName,
				Columns: []ColumnSchema{{Name: "id", Type: "INT"}},
			}
			if err := engine.CreateTable("testdb", schema); err != nil {
				return
			}
			row := Row{int64(g)}
			if _, err := engine.InsertRows("testdb", tableName, []Row{row}); err == nil {
				inserts.Add(1)
			}
			_, _ = engine.ReadCurrentRows("testdb", tableName)
			_ = engine.DropTable("testdb", tableName)
		}(i)
	}

	wg.Wait()

	tables, err := engine.ListTables("testdb")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 0 {
		t.Errorf("expected 0 tables after drops, got %d", len(tables))
	}
}
