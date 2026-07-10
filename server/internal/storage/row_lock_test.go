package storage

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRowLockAcquireRelease(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatalf("first lock: %v", err)
	}
	rlm.UnlockRow("db1", "t1", 1, 10)

	if err := rlm.LockRow("db1", "t1", 1, 20, LockExclusive); err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	rlm.UnlockRow("db1", "t1", 1, 20)
}

func TestRowLockReentrant(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatalf("reentrant lock: %v", err)
	}
	rlm.UnlockRow("db1", "t1", 1, 10)
}

func TestRowLockConflictThenAcquireAfterRelease(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatal(err)
	}

	// TX 2 blocks on the held lock (long timeout so it won't time out).
	done := make(chan error, 1)
	go func() {
		done <- rlm.LockRow("db1", "t1", 1, 20, LockExclusive)
	}()

	// Let TX 2 register as a waiter.
	time.Sleep(20 * time.Millisecond)

	// Release TX 1 — TX 2 should wake up and acquire.
	rlm.UnlockRow("db1", "t1", 1, 10)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TX2 after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TX2 did not acquire after release")
	}
	rlm.UnlockRow("db1", "t1", 1, 20)
}

func TestRowLockSharedCompatible(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockShared); err != nil {
		t.Fatal(err)
	}
	if err := rlm.LockRow("db1", "t1", 1, 20, LockShared); err != nil {
		t.Fatalf("shared+shared should be compatible: %v", err)
	}

	rlm.UnlockRow("db1", "t1", 1, 10)
	rlm.UnlockRow("db1", "t1", 1, 20)
}

func TestRowLockExclusiveBlocksOnShared(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockShared); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- rlm.LockRow("db1", "t1", 1, 20, LockExclusive)
	}()

	time.Sleep(20 * time.Millisecond)

	// Release shared — exclusive waiter should wake.
	rlm.UnlockRow("db1", "t1", 1, 10)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TX2 after shared release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TX2 did not wake up")
	}
	rlm.UnlockRow("db1", "t1", 1, 20)
}

func TestRowLockTimeout(t *testing.T) {
	rlm := NewRowLockManager(100 * time.Millisecond)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := rlm.LockRow("db1", "t1", 1, 20, LockExclusive)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed < 90*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Errorf("timeout duration = %v, want ~100ms", elapsed)
	}

	rlm.UnlockRow("db1", "t1", 1, 10)
}

func TestRowLockDifferentRowsNonBlocking(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := rlm.LockRow("db1", "t1", 2, 20, LockExclusive); err != nil {
		t.Fatalf("different row should not block: %v", err)
	}

	rlm.UnlockRow("db1", "t1", 1, 10)
	rlm.UnlockRow("db1", "t1", 2, 20)
}

func TestRowLockDifferentTablesNonBlocking(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatal(err)
	}
	if err := rlm.LockRow("db1", "t2", 1, 20, LockExclusive); err != nil {
		t.Fatalf("different table should not block: %v", err)
	}

	rlm.UnlockRow("db1", "t1", 1, 10)
	rlm.UnlockRow("db1", "t2", 1, 20)
}

func TestRowLockConcurrentDifferentRows(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)
	const numRows = 50
	const numGoroutines = 10

	var completed atomic.Int64
	var wg sync.WaitGroup

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rowID := uint64(id % numRows)
			txID := uint64(100 + id)
			if err := rlm.LockRow("db1", "t1", rowID, txID, LockExclusive); err != nil {
				t.Errorf("goroutine %d: lock failed: %v", id, err)
				return
			}
			time.Sleep(time.Millisecond)
			rlm.UnlockRow("db1", "t1", rowID, txID)
			completed.Add(1)
		}(g)
	}

	wg.Wait()
	if int(completed.Load()) != numGoroutines {
		t.Errorf("completed = %d, want %d", completed.Load(), numGoroutines)
	}
}

func TestRowLockWaiterWakeup(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockExclusive); err != nil {
		t.Fatal(err)
	}

	// Start two waiters.
	errCh := make(chan error, 2)
	go func() {
		errCh <- rlm.LockRow("db1", "t1", 1, 20, LockExclusive)
	}()
	go func() {
		errCh <- rlm.LockRow("db1", "t1", 1, 30, LockExclusive)
	}()

	time.Sleep(20 * time.Millisecond)

	// Release TX 1 — one waiter wakes up.
	rlm.UnlockRow("db1", "t1", 1, 10)

	// Read whichever TX woke up first.
	var firstTxID uint64
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("first waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first waiter did not wake up")
	}

	// Release whichever TX acquired the lock, so the other waiter can proceed.
	// We don't know the order, so release both potential holders.
	rlm.UnlockRow("db1", "t1", 1, 20)
	rlm.UnlockRow("db1", "t1", 1, 30)
	_ = firstTxID

	// The second waiter should now complete.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("second waiter: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second waiter did not wake up")
	}
}

func TestRowLockUnlockRows(t *testing.T) {
	rlm := NewRowLockManager(1 * time.Second)

	rows := []uint64{1, 2, 3, 4, 5}
	for _, r := range rows {
		if err := rlm.LockRow("db1", "t1", r, 10, LockExclusive); err != nil {
			t.Fatalf("lock row %d: %v", r, err)
		}
	}

	rlm.UnlockRows("db1", "t1", rows, 10)

	for _, r := range rows {
		if err := rlm.LockRow("db1", "t1", r, 20, LockExclusive); err != nil {
			t.Fatalf("lock row %d after bulk unlock: %v", r, err)
		}
		rlm.UnlockRow("db1", "t1", r, 20)
	}
}

func TestRowLockManagerStress(t *testing.T) {
	rlm := NewRowLockManager(2 * time.Second)
	const numKeys = 20
	const numOps = 200
	const numWriters = 8

	var wg sync.WaitGroup
	var conflicts atomic.Int64

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < numOps; i++ {
				rowID := uint64(i % numKeys)
				txID := uint64(id*10000 + i)
				if err := rlm.LockRow("db1", "t1", rowID, txID, LockExclusive); err != nil {
					conflicts.Add(1)
					continue
				}
				time.Sleep(time.Microsecond)
				rlm.UnlockRow("db1", "t1", rowID, txID)
			}
		}(w)
	}

	wg.Wait()
	t.Logf("stress test: %d timeouts out of %d total ops", conflicts.Load(), numWriters*numOps)
}

func TestRowLockExclusiveBlocksOnSharedThenReleases(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	if err := rlm.LockRow("db1", "t1", 1, 10, LockShared); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- rlm.LockRow("db1", "t1", 1, 20, LockExclusive)
	}()

	time.Sleep(20 * time.Millisecond)

	rlm.UnlockRow("db1", "t1", 1, 10)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TX2 after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
	rlm.UnlockRow("db1", "t1", 1, 20)
}

func BenchmarkRowLockSameRow(b *testing.B) {
	rlm := NewRowLockManager(5 * time.Second)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txID := uint64(i + 1)
		_ = rlm.LockRow("db", "t", 1, txID, LockExclusive)
		rlm.UnlockRow("db", "t", 1, txID)
	}
}

func BenchmarkRowLockDifferentRows(b *testing.B) {
	rlm := NewRowLockManager(5 * time.Second)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		txID := uint64(i + 1)
		rowID := uint64(i % 1000)
		_ = rlm.LockRow("db", "t", rowID, txID, LockExclusive)
		rlm.UnlockRow("db", "t", rowID, txID)
	}
}

func BenchmarkRowLockParallel(b *testing.B) {
	rlm := NewRowLockManager(5 * time.Second)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		txID := uint64(time.Now().UnixNano())
		i := uint64(0)
		for pb.Next() {
			rowID := i % 1000
			_ = rlm.LockRow("db", "t", rowID, txID, LockExclusive)
			rlm.UnlockRow("db", "t", rowID, txID)
			txID++
			i++
		}
	})
}

// BenchmarkRowLockVsTableLock compares row-level locking vs table-level locking
// for concurrent updates to different rows in the same table.
func BenchmarkRowLockVsTableLock(b *testing.B) {
	rlm := NewRowLockManager(5 * time.Second)
	var tableMu sync.RWMutex

	b.Run("RowLock", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			txID := uint64(time.Now().UnixNano())
			i := uint64(0)
			for pb.Next() {
				rowID := i % 100
				_ = rlm.LockRow("db", "t", rowID, txID, LockExclusive)
				// Simulate work
				rlm.UnlockRow("db", "t", rowID, txID)
				txID++
				i++
			}
		})
	})

	b.Run("TableLock", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				tableMu.Lock()
				// Simulate work
				tableMu.Unlock()
			}
		})
	})
}

// TestRowLockConcurrentUpdatesDifferentRows verifies that row-level locks
// allow concurrent updates to different rows in the same table.
func TestRowLockConcurrentUpdatesDifferentRows(t *testing.T) {
	rlm := NewRowLockManager(5 * time.Second)

	const numGoroutines = 20
	const rowsPerGoroutine = 50

	var wg sync.WaitGroup
	var completed atomic.Int64
	var conflicts atomic.Int64

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rowsPerGoroutine; i++ {
				rowID := uint64(id*rowsPerGoroutine + i)
				txID := uint64(id*100000 + i)
				if err := rlm.LockRow("db1", "t1", rowID, txID, LockExclusive); err != nil {
					conflicts.Add(1)
					continue
				}
				// Simulate work
				time.Sleep(time.Microsecond)
				rlm.UnlockRow("db1", "t1", rowID, txID)
				completed.Add(1)
			}
		}(g)
	}

	wg.Wait()
	total := numGoroutines * rowsPerGoroutine
	t.Logf("concurrent updates: %d/%d completed, %d conflicts", completed.Load(), total, conflicts.Load())
	if conflicts.Load() > 0 {
		t.Errorf("expected no conflicts for different rows, got %d", conflicts.Load())
	}
}
