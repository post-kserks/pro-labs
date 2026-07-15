package storage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// ErrRowLocked is returned when a row lock cannot be acquired within the timeout or on immediate check.
var ErrRowLocked = errors.New("row locked")

// RowLockMode determines the compatibility mode for row locks.
type RowLockMode int

const (
	// LockShared allows multiple readers concurrently.
	LockShared RowLockMode = 1
	// LockExclusive allows a single writer, blocking all others.
	LockExclusive RowLockMode = 2
)

// LockType alias for backwards compatibility.
type LockType = RowLockMode

// RowLock represents the lock state for a single logical row.
type RowLock struct {
	Key     string
	Mode    RowLockMode
	Holders map[uint64]bool
	Waiters []chan struct{}
}

// RowLockManager manages row-level locks across tables.
// Keys are formatted as "db/table/rowKey". Concurrent access is safe.
type RowLockManager struct {
	locks   map[string]*RowLock
	mu      sync.Mutex
	timeout time.Duration
}

// NewRowLockManager creates a lock manager with an optional acquire timeout.
// Defaults to 30 seconds if not specified.
func NewRowLockManager(timeout ...time.Duration) *RowLockManager {
	t := 30 * time.Second
	if len(timeout) > 0 {
		t = timeout[0]
	}
	return &RowLockManager{
		locks:   make(map[string]*RowLock),
		timeout: t,
	}
}

func rowLockKey(db, table string, rowKey interface{}) string {
	return fmt.Sprintf("%s/%s/%v", db, table, rowKey)
}

// LockRow acquires a lock on the specified row for the given transaction.
// It supports cancellation/timeout via context, as well as an internal timeout if configured.
func (rlm *RowLockManager) LockRow(ctx context.Context, db, table, rowKey string, txID uint64, mode RowLockMode) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	key := rowLockKey(db, table, rowKey)

	rlm.mu.Lock()
	lock, exists := rlm.locks[key]
	if !exists {
		lock = &RowLock{
			Key:     key,
			Mode:    mode,
			Holders: make(map[uint64]bool),
		}
		lock.Holders[txID] = true
		rlm.locks[key] = lock
		rlm.mu.Unlock()
		return nil
	}

	// Reentrant check: if txID already holds this lock
	if lock.Holders[txID] {
		if lock.Mode == LockExclusive || mode == LockShared {
			rlm.mu.Unlock()
			return nil
		}
		// Requesting LockExclusive while holding LockShared (lock upgrade)
		if len(lock.Holders) == 1 {
			lock.Mode = LockExclusive
			rlm.mu.Unlock()
			return nil
		}
		// If len(lock.Holders) > 1, other transactions hold LockShared. We must wait for them to release.
	} else if len(lock.Holders) == 0 || (lock.Mode == LockShared && mode == LockShared) {
		if len(lock.Holders) == 0 {
			lock.Mode = mode
		}
		lock.Holders[txID] = true
		rlm.mu.Unlock()
		return nil
	}

	// Check if context is already canceled or timed out
	if ctx != nil && ctx.Err() != nil {
		rlm.mu.Unlock()
		return ctx.Err()
	}

	// Cannot acquire immediately. If timeout == 0 and no context deadline is set, return ErrRowLocked non-blocking.
	hasDeadline := false
	if ctx != nil {
		_, hasDeadline = ctx.Deadline()
	}
	if rlm.timeout == 0 && !hasDeadline {
		rlm.mu.Unlock()
		return ErrRowLocked
	}

	waiter := make(chan struct{}, 1)
	lock.Waiters = append(lock.Waiters, waiter)
	rlm.mu.Unlock()

	var timeoutCh <-chan time.Time
	if rlm.timeout > 0 {
		timer := time.NewTimer(rlm.timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}
	var ctxDone <-chan struct{}
	if ctx != nil {
		ctxDone = ctx.Done()
	}

	select {
	case _, ok := <-waiter:
		if !ok {
			return rlm.LockRow(ctx, db, table, rowKey, txID, mode)
		}
		return rlm.LockRow(ctx, db, table, rowKey, txID, mode)
	case <-ctxDone:
		rlm.removeWaiter(key, waiter)
		return ctx.Err()
	case <-timeoutCh:
		rlm.removeWaiter(key, waiter)
		return ErrRowLocked
	}
}

// LockRowLegacy provides compatibility for callers using uint64 rowID without context.
func (rlm *RowLockManager) LockRowLegacy(db, table string, rowID uint64, txID uint64, mode RowLockMode) error {
	return rlm.LockRow(context.Background(), db, table, strconv.FormatUint(rowID, 10), txID, mode)
}

// LockRowInt allows acquiring a row lock using int mode to satisfy txmanager interfaces without circular imports.
func (rlm *RowLockManager) LockRowInt(ctx context.Context, db, table, rowKey string, txID uint64, mode int) error {
	return rlm.LockRow(ctx, db, table, rowKey, txID, RowLockMode(mode))
}

func (rlm *RowLockManager) removeWaiter(key string, waiter chan struct{}) {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()
	lock, exists := rlm.locks[key]
	if !exists {
		return
	}
	found := false
	for i, w := range lock.Waiters {
		if w == waiter {
			lock.Waiters = append(lock.Waiters[:i], lock.Waiters[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		// If waiter was not found, UnlockTx or UnlockRow already popped and closed it just as we timed out.
		// If no one currently holds the lock, wake up the next waiter so the lock does not stall.
		if len(lock.Holders) == 0 {
			rlm.notifyNextLocked(lock, key)
		}
	} else if len(lock.Holders) == 0 && len(lock.Waiters) == 0 {
		delete(rlm.locks, key)
	}
}

func (rlm *RowLockManager) notifyNextLocked(lock *RowLock, key string) {
	if len(lock.Waiters) > 0 {
		w := lock.Waiters[0]
		lock.Waiters = lock.Waiters[1:]
		close(w)
	}
	if len(lock.Holders) == 0 && len(lock.Waiters) == 0 {
		delete(rlm.locks, key)
	}
}

// UnlockTx finds and releases all row locks held by txID, waking waiting transactions.
func (rlm *RowLockManager) UnlockTx(txID uint64) {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()

	for key, lock := range rlm.locks {
		if lock.Holders[txID] {
			delete(lock.Holders, txID)
			if len(lock.Holders) == 0 {
				rlm.notifyNextLocked(lock, key)
			}
		}
	}
}

// UnlockRow releases the lock held by txID on a specific row.
func (rlm *RowLockManager) UnlockRow(db, table string, rowKey interface{}, txID uint64) {
	key := rowLockKey(db, table, rowKey)
	rlm.mu.Lock()
	defer rlm.mu.Unlock()

	lock, exists := rlm.locks[key]
	if !exists {
		return
	}
	if lock.Holders[txID] {
		delete(lock.Holders, txID)
		if len(lock.Holders) == 0 {
			rlm.notifyNextLocked(lock, key)
		}
	}
}

// UnlockRows releases locks on multiple rows held by txID.
func (rlm *RowLockManager) UnlockRows(db, table string, rowKeys interface{}, txID uint64) {
	switch keys := rowKeys.(type) {
	case []uint64:
		for _, k := range keys {
			rlm.UnlockRow(db, table, k, txID)
		}
	case []string:
		for _, k := range keys {
			rlm.UnlockRow(db, table, k, txID)
		}
	case []int:
		for _, k := range keys {
			rlm.UnlockRow(db, table, k, txID)
		}
	case []int64:
		for _, k := range keys {
			rlm.UnlockRow(db, table, k, txID)
		}
	case []interface{}:
		for _, k := range keys {
			rlm.UnlockRow(db, table, k, txID)
		}
	}
}

// ActiveLockCount returns the number of active lock entries (used for diagnostics and testing).
func (rlm *RowLockManager) ActiveLockCount() int {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()
	return len(rlm.locks)
}

// GetActiveLocks returns a snapshot copy of current active row locks.
func (rlm *RowLockManager) GetActiveLocks() []*RowLock {
	if rlm == nil {
		return nil
	}
	rlm.mu.Lock()
	defer rlm.mu.Unlock()
	list := make([]*RowLock, 0, len(rlm.locks))
	for _, l := range rlm.locks {
		if l == nil {
			continue
		}
		holders := make(map[uint64]bool, len(l.Holders))
		for k, v := range l.Holders {
			holders[k] = v
		}
		list = append(list, &RowLock{
			Key:     l.Key,
			Mode:    l.Mode,
			Holders: holders,
			Waiters: make([]chan struct{}, len(l.Waiters)),
		})
	}
	return list
}
