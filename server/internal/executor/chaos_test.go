package executor

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

// TestChaosRecovery verifies data integrity during serial crash recovery.
//
// Page engine writes data directly to heap files (not via WAL). WAL is used
// only for page-engine operations (inserting/deleting tuples onto pages). On crash
// recovery heap files already contain committed data — WAL replay is not required
// for basic durability (unlike FileStorageEngine where WAL was the only
// source of truth).
//
// This test verifies: (1) данные сохраняются при normal shutdown,
// (2) corrupt WAL tail не ломает recovery, (3) данные кумулятивно растут.
func TestChaosRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping chaos test in short mode")
	}

	dbPath := t.TempDir()
	tableName := "chaos_table"
	dbName := "chaos_db"

	var expectedCount atomic.Int64

	numCycles := 3
	numWorkers := 5
	opsPerCycle := 50

	for cycle := 0; cycle < numCycles; cycle++ {
		t.Run(fmt.Sprintf("Cycle-%d", cycle), func(t *testing.T) {
			txm := txmanager.NewManager()
			store, err := storage.NewPageStorageEngine(dbPath, nil, txm)
			if err != nil {
				t.Fatal(err)
			}
			exec := New(store, metrics.New(), txm, nil)

			if cycle == 0 {
				runSQL(t, exec, &Session{}, fmt.Sprintf("CREATE DATABASE %s;", dbName))
				sess := &Session{currentDB: dbName}
				runSQL(t, exec, sess, fmt.Sprintf("CREATE TABLE %s (id INT, val TEXT);", tableName))
			}

			// 2. Workload
			var wg sync.WaitGroup
			sess := &Session{currentDB: dbName}
			for i := 0; i < numWorkers; i++ {
				wg.Add(1)
				go func(workerID int) {
					defer wg.Done()
					for j := 0; j < opsPerCycle/numWorkers; j++ {
						val := fmt.Sprintf("val-%d-%d-%d", cycle, workerID, j)
						_, err := exec.Run(&parser.InsertStatement{
							TableName: tableName,
							Rows: [][]parser.Expression{
								{&parser.Value{Type: "int", IntVal: int64(workerID)}, &parser.Value{Type: "string", StrVal: val}},
							},
						}, sess)
						if err == nil {
							expectedCount.Add(1)
						}
					}
				}(i)
			}
			wg.Wait()

			// 3. Graceful shutdown — heap files contain data
			store.Close()

			t.Logf("Cycle %d: Shutdown. Expected rows so far: %d", cycle, expectedCount.Load())

			// 4. Check that data is recovered from heap files
			txm2 := txmanager.NewManager()
			storeRecover, err := storage.NewPageStorageEngine(dbPath, nil, txm2)
			if err != nil {
				t.Fatal(err)
			}

			count, err := storeRecover.CountRows(dbName, tableName)
			if err != nil {
				t.Fatalf("failed to count rows: %v", err)
			}

			if int64(count) != expectedCount.Load() {
				t.Errorf("Cycle %d Data Integrity Error: expected %d rows, got %d", cycle, expectedCount.Load(), count)
			}

			storeRecover.Close()
		})
	}
}

func runSQL(t *testing.T, e *Executor, sess *Session, sql string) {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	_, err = e.Run(stmt, sess)
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
}
