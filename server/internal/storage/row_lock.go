package storage

import (
	"fmt"
	"sync"
	"time"
)

// LockType determines the compatibility mode for row locks.
type LockType int

const (
	// LockShared allows multiple readers concurrently.
	LockShared LockType = iota
	// LockExclusive allows a single writer, blocking all others.
	LockExclusive
)

// RowLock represents the lock state for a single logical row.
type RowLock struct {
	txID     uint64
	lockType LockType
	granted  time.Time
	mu       sync.Mutex
	waiters  []chan struct{}
}

// RowLockManager manages row-level locks across all tables.
// Keys are "db:table:rowID" strings. Concurrent access is safe.
type RowLockManager struct {
	locks   map[string]*RowLock
	mu      sync.RWMutex
	timeout time.Duration
}

// NewRowLockManager creates a lock manager with the given acquire timeout.
func NewRowLockManager(timeout time.Duration) *RowLockManager {
	return &RowLockManager{
		locks:   make(map[string]*RowLock),
		timeout: timeout,
	}
}

func rowLockKey(db, table string, rowID uint64) string {
	return fmt.Sprintf("%s:%s:%d", db, table, rowID)
}

// LockRow acquires a lock on the specified row for the given transaction.
// Returns nil on success, or an error if the timeout expires.
func (rlm *RowLockManager) LockRow(db, table string, rowID uint64, txID uint64, lockType LockType) error {
	key := rowLockKey(db, table, rowID)

	rlm.mu.Lock()
	lock, exists := rlm.locks[key]
	if !exists {
		lock = &RowLock{}
		rlm.locks[key] = lock
	}
	rlm.mu.Unlock()

	return lock.acquire(txID, lockType, rlm.timeout)
}

// UnlockRow releases the lock held by the given transaction on the specified row.
func (rlm *RowLockManager) UnlockRow(db, table string, rowID uint64, txID uint64) {
	key := rowLockKey(db, table, rowID)

	rlm.mu.RLock()
	lock, exists := rlm.locks[key]
	rlm.mu.RUnlock()

	if exists {
		lock.release(txID)
	}
}

// UnlockRows releases locks on multiple rows for the given transaction.
func (rlm *RowLockManager) UnlockRows(db, table string, rowIDs []uint64, txID uint64) {
	for _, rowID := range rowIDs {
		rlm.UnlockRow(db, table, rowID, txID)
	}
}

// ActiveLockCount returns the number of tracked lock entries.
// Used for diagnostics and testing.
func (rlm *RowLockManager) ActiveLockCount() int {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()
	return len(rlm.locks)
}

// acquire attempts to obtain the lock. Blocks until granted or timeout.
func (l *RowLock) acquire(txID uint64, lockType LockType, timeout time.Duration) error {
	l.mu.Lock()

	// Reentrant: same transaction already holds this lock.
	if l.txID == txID {
		l.mu.Unlock()
		return nil
	}

	// Lock is free or compatible (shared+shared).
	if l.txID == 0 || (l.lockType == LockShared && lockType == LockShared) {
		l.txID = txID
		l.lockType = lockType
		l.granted = time.Now()
		l.mu.Unlock()
		return nil
	}

	// Must wait — register a waiter channel, then block outside the lock.
	waiter := make(chan struct{}, 1)
	l.waiters = append(l.waiters, waiter)
	l.mu.Unlock()

	select {
	case <-waiter:
		// Signalled — retry acquisition (the releaser cleared the lock).
		return l.acquire(txID, lockType, timeout)
	case <-time.After(timeout):
		// Timed out — remove ourselves from the waiter list.
		l.mu.Lock()
		for i, w := range l.waiters {
			if w == waiter {
				l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
				break
			}
		}
		l.mu.Unlock()
		return fmt.Errorf("row lock timeout after %v", timeout)
	}
}

// release drops the lock and wakes the next waiter.
func (l *RowLock) release(txID uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.txID != txID {
		return
	}

	l.txID = 0
	l.lockType = LockShared

	if len(l.waiters) > 0 {
		waiter := l.waiters[0]
		l.waiters = l.waiters[1:]
		select {
		case waiter <- struct{}{}:
		default:
		}
	}
}
