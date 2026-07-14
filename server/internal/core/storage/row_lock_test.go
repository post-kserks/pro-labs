package storage

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRowLockBasicAndLegacy(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	// Test LockRow with string key
	if err := rlm.LockRow(context.Background(), "db1", "t1", "rowA", 10, LockExclusive); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rlm.ActiveLockCount() != 1 {
		t.Fatalf("expected 1 active lock, got %d", rlm.ActiveLockCount())
	}

	// Test LockRowLegacy with uint64 key
	if err := rlm.LockRowLegacy("db1", "t1", 100, 20, LockShared); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rlm.ActiveLockCount() != 2 {
		t.Fatalf("expected 2 active locks, got %d", rlm.ActiveLockCount())
	}

	rlm.UnlockRow("db1", "t1", "rowA", 10)
	if rlm.ActiveLockCount() != 1 {
		t.Fatalf("expected 1 active lock, got %d", rlm.ActiveLockCount())
	}

	rlm.UnlockTx(20)
	if rlm.ActiveLockCount() != 0 {
		t.Fatalf("expected 0 active locks, got %d", rlm.ActiveLockCount())
	}
}

func TestRowLockSharedCompatibility(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	// Multiple transactions acquire shared lock on the same row
	if err := rlm.LockRow(context.Background(), "db", "tbl", "1", 1, LockShared); err != nil {
		t.Fatalf("tx 1 shared lock failed: %v", err)
	}
	if err := rlm.LockRow(context.Background(), "db", "tbl", "1", 2, LockShared); err != nil {
		t.Fatalf("tx 2 shared lock failed: %v", err)
	}
	if err := rlm.LockRow(context.Background(), "db", "tbl", "1", 3, LockShared); err != nil {
		t.Fatalf("tx 3 shared lock failed: %v", err)
	}

	// Exclusive lock by tx 4 should block until all shared locks are released
	done := make(chan error, 1)
	go func() {
		done <- rlm.LockRow(context.Background(), "db", "tbl", "1", 4, LockExclusive)
	}()

	select {
	case <-done:
		t.Fatal("exclusive lock should have blocked")
	case <-time.After(30 * time.Millisecond):
	}

	// Unlock tx 1 and 2
	rlm.UnlockRow("db", "tbl", "1", 1)
	rlm.UnlockRow("db", "tbl", "1", 2)

	select {
	case <-done:
		t.Fatal("exclusive lock should still be blocked by tx 3")
	case <-time.After(30 * time.Millisecond):
	}

	// Unlock tx 3
	rlm.UnlockRow("db", "tbl", "1", 3)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tx 4 exclusive lock failed: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("exclusive lock timed out waiting after all shared locks released")
	}

	rlm.UnlockTx(4)
	if rlm.ActiveLockCount() != 0 {
		t.Fatalf("expected 0 active locks, got %d", rlm.ActiveLockCount())
	}
}

func TestRowLockExclusiveBlocking(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	if err := rlm.LockRow(context.Background(), "db", "tbl", "key-1", 100, LockExclusive); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- rlm.LockRow(context.Background(), "db", "tbl", "key-1", 200, LockShared)
	}()

	select {
	case <-done:
		t.Fatal("shared lock should block when exclusive lock is held")
	case <-time.After(30 * time.Millisecond):
	}

	rlm.UnlockTx(100)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("tx 200 shared lock failed: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("tx 200 timed out after exclusive lock released")
	}
}

func TestRowLockReentrant(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	// Same tx acquiring exclusive lock twice
	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 10, LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 10, LockExclusive); err != nil {
		t.Fatalf("reentrant exclusive lock failed: %v", err)
	}
	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 10, LockShared); err != nil {
		t.Fatalf("reentrant shared lock failed: %v", err)
	}

	rlm.UnlockTx(10)
	if rlm.ActiveLockCount() != 0 {
		t.Fatalf("expected 0 active locks after UnlockTx, got %d", rlm.ActiveLockCount())
	}

	// Lock upgrade: holding LockShared solely, requesting LockExclusive
	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-2", 20, LockShared); err != nil {
		t.Fatal(err)
	}
	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-2", 20, LockExclusive); err != nil {
		t.Fatalf("lock upgrade failed: %v", err)
	}

	rlm.UnlockTx(20)
}

func TestRowLockTimeoutViaContext(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 1, LockExclusive); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := rlm.LockRow(ctx, "db", "tbl", "row-1", 2, LockExclusive)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}

	// Verify tx 1 still holds the lock and waiters are cleaned up
	rlm.mu.Lock()
	lock := rlm.locks["db/tbl/row-1"]
	if len(lock.Waiters) != 0 {
		t.Fatalf("expected 0 waiters after timeout, got %d", len(lock.Waiters))
	}
	rlm.mu.Unlock()

	rlm.UnlockTx(1)
}

func TestRowLockInternalTimeout(t *testing.T) {
	rlm := NewRowLockManager(50 * time.Millisecond)

	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 1, LockExclusive); err != nil {
		t.Fatal(err)
	}

	err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 2, LockExclusive)
	if err != ErrRowLocked {
		t.Fatalf("expected ErrRowLocked, got %v", err)
	}

	rlm.UnlockTx(1)
}

func TestRowLockTryLock(t *testing.T) {
	rlm := NewRowLockManager(0) // 0 timeout -> immediate non-blocking check

	if err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 1, LockExclusive); err != nil {
		t.Fatal(err)
	}

	err := rlm.LockRow(context.Background(), "db", "tbl", "row-1", 2, LockShared)
	if err != ErrRowLocked {
		t.Fatalf("expected immediate ErrRowLocked on trylock, got %v", err)
	}

	rlm.UnlockTx(1)
}

func TestRowLockConcurrentContention(t *testing.T) {
	rlm := NewRowLockManager(2 * time.Second)
	const numTx = 20
	var successCount atomic.Int32

	var wg sync.WaitGroup
	wg.Add(numTx)

	for i := 1; i <= numTx; i++ {
		txID := uint64(i)
		go func(id uint64) {
			defer wg.Done()
			err := rlm.LockRow(context.Background(), "db", "tbl", "shared-row", id, LockExclusive)
			if err == nil {
				successCount.Add(1)
				time.Sleep(2 * time.Millisecond)
				rlm.UnlockTx(id)
			}
		}(txID)
	}

	wg.Wait()
	if successCount.Load() != numTx {
		t.Fatalf("expected %d transactions to succeed sequentially, got %d", numTx, successCount.Load())
	}
	if rlm.ActiveLockCount() != 0 {
		t.Fatalf("expected 0 active locks at end, got %d", rlm.ActiveLockCount())
	}
}
