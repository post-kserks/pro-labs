package executor

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

// TestChaosRecovery имитирует серию аварийных завершений и проверяет целостность данных.
func TestChaosRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping chaos test in short mode")
	}

	dbPath := t.TempDir()
	walPath := filepath.Join(dbPath, "wal", "vaultdb.wal")
	tableName := "chaos_table"
	dbName := "chaos_db"

	var expectedCount atomic.Int64

	// Параметры теста
	numCycles := 3
	numWorkers := 5
	opsPerCycle := 50

	for cycle := 0; cycle < numCycles; cycle++ {
		t.Run(fmt.Sprintf("Cycle-%d", cycle), func(t *testing.T) {
			// 1. Инициализация инстанса
			txm := txmanager.NewManager()
			store, err := storage.NewPageStorageEngine(dbPath, nil, txm)
			if err != nil {
				t.Fatal(err)
			}
			exec := New(store, metrics.New(), txm, nil)
			
			// Если это первый цикл, создаем БД и таблицу
			if cycle == 0 {
				runSQL(t, exec, &Session{}, fmt.Sprintf("CREATE DATABASE %s;", dbName))
				sess := &Session{currentDB: dbName}
				runSQL(t, exec, sess, fmt.Sprintf("CREATE TABLE %s (id INT, val TEXT);", tableName))
			}

			// 2. Нагрузка
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

			// Даем немного поработать и "роняем"
			time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			
			// 3. DIRTY CRASH
			// Закрываем без фиксации состояния в _data.json
			store.Close()

			t.Logf("Cycle %d: Crashed. Expected committed rows so far: %d", cycle, expectedCount.Load())

			// 4. Имитация повреждения WAL (опционально)
			// Допишем в конец WAL случайный мусор, который должен быть отброшен при восстановлении
			f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
			if err == nil {
				f.Write([]byte("CORRUPT_TAIL_DATA_12345"))
				f.Close()
			}

			// 5. Восстановление и проверка
			txm2 := txmanager.NewManager()
			storeRecover, err := storage.NewPageStorageEngine(dbPath, nil, txm2)
			if err != nil {
				t.Fatal(err)
			}
			
			// Проверяем количество строк
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
