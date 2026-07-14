package txmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRecordAccess(t *testing.T) {
	m := NewManager()
	tx := m.Begin()

	// First access should record snapshot
	m.RecordAccess(tx, "db", "users")
	if _, ok := tx.TableSnapshots["db/users"]; !ok {
		t.Fatal("expected snapshot to be recorded")
	}

	// Bump version after recording
	m.BumpTableVersion("db", "users")
	// Second access should not overwrite
	m.RecordAccess(tx, "db", "users")
	snap := tx.TableSnapshots["db/users"]
	if snap != 0 {
		t.Fatalf("expected snapshot to remain at 0, got %d", snap)
	}

	// nil tx should be safe
	m.RecordAccess(nil, "db", "users")
}

func TestTableKeyFunc(t *testing.T) {
	got := TableKey("mydb", "mytable")
	if got != "mydb/mytable" {
		t.Fatalf("expected 'mydb/mytable', got %q", got)
	}
}

func TestLockTablesUnlock(t *testing.T) {
	m := NewManager()
	unlock := m.LockTables([]string{"db/t1", "db/t2"})

	// Second lock on same tables should block (but not deadlock since we unlock)
	done := make(chan struct{})
	go func() {
		m.LockTables([]string{"db/t1", "db/t2"})()
		close(done)
	}()

	// Give goroutine time to start
	time.Sleep(10 * time.Millisecond)
	unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("LockTables deadlocked or didn't release")
	}
}

func TestReleaseSavepointFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})
	tx.Savepoint("sp1")
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})

	if !tx.ReleaseSavepoint("sp1") {
		t.Fatal("expected ReleaseSavepoint to return true")
	}
	// Releasing again should return false
	if tx.ReleaseSavepoint("sp1") {
		t.Fatal("expected ReleaseSavepoint to return false for already released")
	}
	// Unknown savepoint
	if tx.ReleaseSavepoint("nonexistent") {
		t.Fatal("expected ReleaseSavepoint to return false for unknown")
	}
}

func TestRollbackFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})
	tx.Savepoint("sp1")
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})

	tx.Rollback()
	if tx.State != TxIdle {
		t.Fatal("expected state TxIdle after rollback")
	}
	if len(tx.Ops) != 0 {
		t.Fatal("expected ops to be cleared after rollback")
	}
	// Savepoint should be cleared
	if err := tx.RollbackToSavepoint("sp1"); err == nil {
		t.Fatal("expected error rolling back to savepoint after rollback")
	}
}

func TestRollbackWithSpillFunc(t *testing.T) {
	dir := t.TempDir()
	m := NewManager()
	m.SpillDir = dir
	m.SpillThreshold = 1

	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})
	if !tx.spilled {
		t.Fatal("expected spill")
	}

	tx.Rollback()
	if tx.spilled {
		t.Fatal("expected spilled to be false after rollback")
	}
	// Spill file should be removed
	if _, err := os.Stat(tx.spillPath); !os.IsNotExist(err) {
		t.Fatal("expected spill file to be removed")
	}
}

func TestIsCommittedFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	xid := tx.ID
	if !m.IsCommitted(xid - 1) {
		t.Fatal("expected older xid to be committed")
	}
	if m.IsCommitted(xid + 100) {
		t.Fatal("expected future xid to not be committed")
	}
}

func TestEnsureCounterAtLeastFunc(t *testing.T) {
	m := NewManager()
	m.EnsureCounterAtLeast(50)
	if m.counter.Load() != 50 {
		t.Fatalf("expected counter 50, got %d", m.counter.Load())
	}
	// Lower value should not change
	m.EnsureCounterAtLeast(30)
	if m.counter.Load() != 50 {
		t.Fatalf("expected counter to remain 50, got %d", m.counter.Load())
	}
	// Higher value should update
	m.EnsureCounterAtLeast(100)
	if m.counter.Load() != 100 {
		t.Fatalf("expected counter 100, got %d", m.counter.Load())
	}
}

func TestCleanupSpillFilesFunc(t *testing.T) {
	dir := t.TempDir()
	// Create temp files
	os.WriteFile(filepath.Join(dir, "tx_1.tmp"), []byte("data"), 0600)
	os.WriteFile(filepath.Join(dir, "tx_2.tmp"), []byte("data"), 0600)
	os.WriteFile(filepath.Join(dir, "data.json"), []byte("keep"), 0600)

	CleanupSpillFiles(dir)

	if _, err := os.Stat(filepath.Join(dir, "tx_1.tmp")); !os.IsNotExist(err) {
		t.Fatal("expected tx_1.tmp to be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "tx_2.tmp")); !os.IsNotExist(err) {
		t.Fatal("expected tx_2.tmp to be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "data.json")); err != nil {
		t.Fatal("expected data.json to remain")
	}
}

func TestCleanupSpillFilesNonExistentDir(t *testing.T) {
	// Should not panic
	CleanupSpillFiles("/nonexistent/path/that/does/not/exist")
}

func TestRollbackToSavepointSpillEmptyFunc(t *testing.T) {
	dir := t.TempDir()
	m := NewManager()
	m.SpillDir = dir
	m.SpillThreshold = 1

	tx := m.Begin()
	tx.Savepoint("empty_sp")
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})
	tx.Savepoint("after_add")

	// Rollback to "empty_sp" should remove spill file since kept=0
	if err := tx.RollbackToSavepoint("empty_sp"); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
	if tx.spilled {
		t.Fatal("expected spilled to be false after rollback to empty savepoint")
	}
}

func TestRollbackToSavepointSpillRewriteFunc(t *testing.T) {
	dir := t.TempDir()
	m := NewManager()
	m.SpillDir = dir
	m.SpillThreshold = 1

	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t", Type: "op1"})
	tx.Savepoint("sp1")
	m.AddOp(tx, PendingOp{DB: "db", Table: "t", Type: "op2"})
	m.AddOp(tx, PendingOp{DB: "db", Table: "t", Type: "op3"})

	if err := tx.RollbackToSavepoint("sp1"); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
	ops, err := tx.ReadOps()
	if err != nil {
		t.Fatalf("ReadOps: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op after rollback, got %d", len(ops))
	}
	if ops[0].Type != "op1" {
		t.Fatalf("expected op1, got %s", ops[0].Type)
	}
}

func TestSavepointOverwriteFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})
	tx.Savepoint("sp") // sp at opCounter=1
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})
	tx.Savepoint("sp") // overwrite: sp now at opCounter=2
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})

	// Rollback to sp should keep ops[:2] (2 ops since overwritten)
	if err := tx.RollbackToSavepoint("sp"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	ops, _ := tx.ReadOps()
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
}

func TestCommitApplyFnErrorFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})

	customErr := fmt.Errorf("disk failure")
	err := m.Commit(tx, func(ops []PendingOp) error {
		return customErr
	})
	if !errors.Is(err, customErr) {
		t.Fatalf("expected customErr, got: %v", err)
	}
}

func TestCommitReadOpsSpillErrorFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	tx.spilled = true
	tx.spillPath = "/nonexistent/path.tmp"
	tx.spillErr = fmt.Errorf("spill failed")

	err := m.Commit(tx, func(ops []PendingOp) error {
		t.Fatal("apply should not be called")
		return nil
	})
	if err == nil {
		t.Fatal("expected error from Commit")
	}
}

func TestParallelLockTablesFunc(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	errs := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			unlock := m.LockTables([]string{"db/shared"})
			// Simulate work
			time.Sleep(time.Millisecond)
			unlock()
			errs[idx] = nil
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestEncodeOpCustomCodecFunc(t *testing.T) {
	// Save and restore global codec
	origEncode := EncodePendingOp
	origDecode := DecodePendingOp
	defer func() {
		EncodePendingOp = origEncode
		DecodePendingOp = origDecode
	}()

	called := false
	EncodePendingOp = func(op PendingOp) ([]byte, error) {
		called = true
		return []byte(`{"custom":true}`), nil
	}
	DecodePendingOp = func(data []byte) (PendingOp, error) {
		return PendingOp{Type: "custom"}, nil
	}

	data, err := encodeOp(PendingOp{Type: "test"})
	if err != nil {
		t.Fatalf("encodeOp: %v", err)
	}
	if !called {
		t.Fatal("expected custom encoder to be called")
	}

	op, err := decodeOp(data)
	if err != nil {
		t.Fatalf("decodeOp: %v", err)
	}
	if op.Type != "custom" {
		t.Fatalf("expected custom type, got %s", op.Type)
	}
}

func TestLongRunningTransactionFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()

	// Add many ops to simulate long-running tx
	for i := 0; i < 100; i++ {
		m.AddOp(tx, PendingOp{
			Type:  "insert",
			DB:    "db",
			Table: "t",
			Pos:   i,
		})
	}

	ops, err := tx.ReadOps()
	if err != nil {
		t.Fatalf("ReadOps: %v", err)
	}
	if len(ops) != 100 {
		t.Fatalf("expected 100 ops, got %d", len(ops))
	}
}

func TestMultipleTableAccessFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()

	m.RecordAccess(tx, "db1", "t1")
	m.RecordAccess(tx, "db2", "t2")
	m.RecordAccess(tx, "db1", "t3")

	if len(tx.TableSnapshots) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(tx.TableSnapshots))
	}
}

func TestSavepointWithSpillNoOpCounterFunc(t *testing.T) {
	m := NewManager()
	tx := m.Begin()

	// Savepoint before any ops
	tx.Savepoint("early")

	// Add ops
	for i := 0; i < 5; i++ {
		m.AddOp(tx, PendingOp{DB: "db", Table: "t", Type: fmt.Sprintf("op%d", i)})
	}

	// Rollback to savepoint created at opCounter=0
	if err := tx.RollbackToSavepoint("early"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	ops, _ := tx.ReadOps()
	if len(ops) != 0 {
		t.Fatalf("expected 0 ops, got %d", len(ops))
	}
}

func TestIsConflictEdgeCasesFunc(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("some random error"), false},
		{fmt.Errorf("conflict on table x"), true},
		{fmt.Errorf("OCC retry needed"), true},
		{fmt.Errorf("version mismatch detected"), true},
	}
	for _, tt := range tests {
		got := IsConflictError(tt.err)
		if got != tt.want {
			t.Errorf("IsConflictError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestEnsureCounterAtLeastConcurrencyFunc(t *testing.T) {
	m := NewManager()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n uint64) {
			defer wg.Done()
			m.EnsureCounterAtLeast(n)
		}(uint64(i * 10))
	}
	wg.Wait()
	if m.counter.Load() < 990 {
		t.Fatalf("expected counter >= 990, got %d", m.counter.Load())
	}
}

func TestCommitWithRetryApplyFnErrorsOnConflictFunc(t *testing.T) {
	m := NewManager()
	m.OCCConfig.MaxRetries = 2
	m.OCCConfig.BaseDelay = time.Millisecond

	tx := m.Begin()
	m.AddOp(tx, PendingOp{DB: "db", Table: "t"})

	// Bump version to create conflict
	m.BumpTableVersion("db", "t")

	// Goroutine keeps bumping to ensure every retry also conflicts
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				m.BumpTableVersion("db", "t")
			}
		}
	}()

	applyCalls := 0
	err := m.CommitWithRetry(tx, func(ops []PendingOp) error {
		applyCalls++
		return nil
	})
	close(done)

	if err == nil {
		t.Fatal("expected error after retries")
	}
	// applyFn should never run because commit always conflicts
	if applyCalls != 0 {
		t.Fatalf("expected 0 apply calls, got %d", applyCalls)
	}
}
