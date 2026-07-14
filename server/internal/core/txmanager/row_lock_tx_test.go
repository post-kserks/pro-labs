package txmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"vaultdb/internal/core/storage"
)

func TestTxRowLockAcquireAndCommit(t *testing.T) {
	m := NewManager()
	tx1 := m.Begin()

	ctx := context.Background()
	err := m.AcquireRowLockTx(ctx, tx1, "db1", "t1", "row-key-1", LockExclusive)
	if err != nil {
		t.Fatalf("AcquireRowLockTx failed: %v", err)
	}

	if count := m.RowLocks.ActiveLockCount(); count != 1 {
		t.Fatalf("expected 1 active lock before commit, got %d", count)
	}

	err = m.Commit(tx1, func(ops []PendingOp) error {
		return nil
	})
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if count := m.RowLocks.ActiveLockCount(); count != 0 {
		t.Fatalf("expected 0 active locks after commit, got %d", count)
	}
}

func TestTxRowLockAcquireAndRollback(t *testing.T) {
	m := NewManager()
	tx := m.Begin()

	ctx := context.Background()
	if err := m.AcquireRowLockTx(ctx, tx, "db1", "t1", "row-1", LockShared); err != nil {
		t.Fatal(err)
	}
	if err := m.AcquireRowLockTx(ctx, tx, "db1", "t1", "row-2", LockShared); err != nil {
		t.Fatal(err)
	}

	if count := m.RowLocks.ActiveLockCount(); count != 2 {
		t.Fatalf("expected 2 active locks, got %d", count)
	}

	tx.Rollback()

	if count := m.RowLocks.ActiveLockCount(); count != 0 {
		t.Fatalf("expected 0 active locks after tx.Rollback(), got %d", count)
	}
}

func TestTxRowLockManagerRollbackHelper(t *testing.T) {
	m := NewManager()
	tx := m.Begin()

	ctx := context.Background()
	if err := m.AcquireRowLockTx(ctx, tx, "db", "t", "r1", LockExclusive); err != nil {
		t.Fatal(err)
	}
	if count := m.RowLocks.ActiveLockCount(); count != 1 {
		t.Fatalf("expected 1 active lock, got %d", count)
	}

	m.Rollback(tx)
	if count := m.RowLocks.ActiveLockCount(); count != 0 {
		t.Fatalf("expected 0 active locks after m.Rollback(tx), got %d", count)
	}
}

func TestTxRowLockConcurrentContentionAndTimeout(t *testing.T) {
	m := NewManager()
	tx1 := m.Begin()
	tx2 := m.Begin()

	ctx := context.Background()
	if err := m.AcquireRowLockTx(ctx, tx1, "db", "t", "conflict-row", LockExclusive); err != nil {
		t.Fatal(err)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := m.AcquireRowLockTx(timeoutCtx, tx2, "db", "t", "conflict-row", LockExclusive)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded for tx2, got %v", err)
	}

	// Commit tx1, releasing the lock
	if err := m.Commit(tx1, func(ops []PendingOp) error { return nil }); err != nil {
		t.Fatal(err)
	}

	// Now tx2 should be able to acquire the lock immediately
	if err := m.AcquireRowLockTx(context.Background(), tx2, "db", "t", "conflict-row", LockExclusive); err != nil {
		t.Fatalf("tx2 failed to acquire lock after tx1 commit: %v", err)
	}

	tx2.Rollback()
	if count := m.RowLocks.ActiveLockCount(); count != 0 {
		t.Fatalf("expected 0 active locks at end, got %d", count)
	}
}

type mockPageEngine struct {
	rlm *storage.RowLockManager
}

func (m *mockPageEngine) GetRowLocks() *storage.RowLockManager {
	return m.rlm
}

func TestNewManagerWithPageEngineSharing(t *testing.T) {
	sharedLockMgr := storage.NewRowLockManager(10 * time.Second)
	engine := &mockPageEngine{rlm: sharedLockMgr}

	mgr := NewManagerWithPageEngine(engine)
	if mgr.GetRowLocks() != sharedLockMgr {
		t.Fatal("expected Manager to share the exact same RowLockManager instance as engine")
	}

	tx := mgr.Begin()
	if err := mgr.AcquireRowLockTx(context.Background(), tx, "db", "t", "shared-row", LockExclusive); err != nil {
		t.Fatal(err)
	}

	if count := sharedLockMgr.ActiveLockCount(); count != 1 {
		t.Fatalf("expected sharedLockMgr to reflect 1 active lock, got %d", count)
	}

	tx.Rollback()
	if count := sharedLockMgr.ActiveLockCount(); count != 0 {
		t.Fatalf("expected sharedLockMgr to have 0 active locks after rollback, got %d", count)
	}
}
