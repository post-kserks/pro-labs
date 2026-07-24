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

func TestInsertRowsConcurrent(t *testing.T) {
	engine := newTestPageEngine(t)
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name:    "items",
		Columns: []ColumnSchema{{Name: "id", Type: "INT"}, {Name: "val", Type: "TEXT"}},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var total atomic.Int64
	nWriters := 8
	nPerWriter := 50

	for w := 0; w < nWriters; w++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < nPerWriter; i++ {
				id := int64(writer*nPerWriter + i)
				row := Row{id, "data"}
				n, err := engine.InsertRows("testdb", "items", []Row{row})
				if err != nil {
					t.Errorf("writer %d: %v", writer, err)
					return
				}
				total.Add(int64(n))
			}
		}(w)
	}
	wg.Wait()

	rows, err := engine.ReadCurrentRows("testdb", "items")
	if err != nil {
		t.Fatal(err)
	}
	want := int64(nWriters * nPerWriter)
	if int64(len(rows)) != want {
		t.Errorf("row count = %d, want %d", len(rows), want)
	}
}

func TestTruncateConcurrent(t *testing.T) {
	engine := newTestPageEngine(t)
	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatal(err)
	}
	schema := TableSchema{
		Name:    "t1",
		Columns: []ColumnSchema{{Name: "id", Type: "INT"}},
	}
	if err := engine.CreateTable("testdb", schema); err != nil {
		t.Fatal(err)
	}

	// Seed some rows.
	for i := 0; i < 50; i++ {
		if _, err := engine.InsertRows("testdb", "t1", []Row{{int64(i)}}); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var errs atomic.Int64

	// Concurrent writers inserting rows.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(writer int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				id := int64(writer*1000 + i)
				_, _ = engine.InsertRows("testdb", "t1", []Row{{id}})
			}
		}(w)
	}

	// Concurrent truncates — must not race with writers.
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				if err := engine.TruncateTable("testdb", "t1"); err != nil {
					errs.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// After all operations complete the table must still be readable.
	_, err := engine.ReadCurrentRows("testdb", "t1")
	if err != nil {
		t.Fatalf("read after concurrent truncate failed: %v", err)
	}
}
