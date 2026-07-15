package storage

import (
	"sync"
	"testing"
	"time"
)

func TestAutoVacuumWorkerRunVacuumOnce(t *testing.T) {
	dir := t.TempDir()
	engine, err := NewPageStorageEngine(dir, nil, nil, &StorageOptions{BufferPoolPages: 100})
	if err != nil {
		t.Fatalf("NewPageStorageEngine: %v", err)
	}
	defer engine.Close()

	if err := engine.CreateDatabase("testdb"); err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if err := engine.CreateTable("testdb", usersSchema()); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	rows := []Row{
		{int64(10), "Alice", 100.5},
		{int64(20), "Bob", 200.5},
		{int64(30), "Charlie", 300.5},
	}
	if _, err := engine.InsertRows("testdb", "users", rows); err != nil {
		t.Fatalf("InsertRows: %v", err)
	}

	// Delete rows 0 and 1
	if _, err := engine.DeleteRows("testdb", "users", []int{0, 1}); err != nil {
		t.Fatalf("DeleteRows: %v", err)
	}

	delTxID := engine.CurrentTxID()

	worker := NewAutoVacuumWorker(engine, AutoVacuumConfig{Interval: 1 * time.Hour, MinAge: 10})

	// Test 1: minActiveTxID == delTxID -> deletedTx >= minActiveTxID, should NOT be reclaimed
	freed, err := worker.RunVacuumOnce("testdb", "users", delTxID)
	if err != nil {
		t.Fatalf("RunVacuumOnce active tx error: %v", err)
	}
	if freed != 0 {
		t.Fatalf("expected 0 freed tuples for active tx, got %d", freed)
	}

	// Test 2: minActiveTxID == delTxID + 1 -> deletedTx < minActiveTxID, should be reclaimed
	freed, err = worker.RunVacuumOnce("testdb", "users", delTxID+1)
	if err != nil {
		t.Fatalf("RunVacuumOnce old tx error: %v", err)
	}
	if freed != 2 {
		t.Fatalf("expected 2 freed tuples, got %d", freed)
	}

	// Verify live row (Charlie) is still intact
	liveRows, err := engine.ReadCurrentRows("testdb", "users")
	if err != nil {
		t.Fatalf("ReadCurrentRows: %v", err)
	}
	if len(liveRows) != 1 {
		t.Fatalf("expected 1 live row remaining, got %d", len(liveRows))
	}
}

func TestAutoVacuumWorkerStartStopThreadSafe(t *testing.T) {
	dir := t.TempDir()
	engine, _ := NewPageStorageEngine(dir, nil, nil, &StorageOptions{BufferPoolPages: 100})
	defer engine.Close()

	worker := NewAutoVacuumWorker(engine, AutoVacuumConfig{Interval: 5 * time.Millisecond})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Start()
			time.Sleep(2 * time.Millisecond)
			worker.Stop()
		}()
	}
	wg.Wait()
}
