package storage

import (
	"sync"
)

// LockMode defines the level of row lock.
type LockMode int

const (
	LockModeNone LockMode = iota
	LockModeShare
	LockModeUpdate
)

// RowLock represents a lock held on a specific row.
type RowLock struct {
	Mode    LockMode
	OwnerTx uint64
}

// LockManager handles row-level locking for the storage engine.
// In a real system, this would implement deadlock detection and wait queues.
type LockManager struct {
	mu    sync.Mutex
	locks map[string]*rowLockEntry
}

type rowLockEntry struct {
	// A slice of locks held by transactions.
	// For simplicity, we only allow one exclusive lock or multiple share locks.
	owners []RowLock
	cond   *sync.Cond
}

func NewLockManager() *LockManager {
	return &LockManager{
		locks: make(map[string]*rowLockEntry),
	}
}

// Acquire attempts to acquire a row-level lock.
// key should be a combination of dbName, tableName, pageNo, slotNo.
// If the lock cannot be acquired, it blocks until it is available.
func (lm *LockManager) Acquire(key string, txID uint64, mode LockMode) {
	lm.mu.Lock()
	entry, ok := lm.locks[key]
	if !ok {
		entry = &rowLockEntry{
			owners: make([]RowLock, 0, 1),
			cond:   sync.NewCond(&lm.mu),
		}
		lm.locks[key] = entry
	}

	for !lm.canAcquire(entry, txID, mode) {
		entry.cond.Wait()
	}

	// Upgrade logic if tx already holds a share lock and wants an update lock
	upgraded := false
	for i, owner := range entry.owners {
		if owner.OwnerTx == txID {
			if owner.Mode < mode {
				entry.owners[i].Mode = mode
			}
			upgraded = true
			break
		}
	}
	if !upgraded {
		entry.owners = append(entry.owners, RowLock{Mode: mode, OwnerTx: txID})
	}
	lm.mu.Unlock()
}

// canAcquire returns true if the lock can be acquired.
func (lm *LockManager) canAcquire(entry *rowLockEntry, txID uint64, mode LockMode) bool {
	for _, owner := range entry.owners {
		if owner.OwnerTx == txID {
			continue // Self-lock
		}
		if owner.Mode == LockModeUpdate || mode == LockModeUpdate {
			return false // Conflict
		}
	}
	return true
}

// ReleaseAll releases all locks held by a given transaction.
func (lm *LockManager) ReleaseAll(txID uint64) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	for key, entry := range lm.locks {
		filtered := make([]RowLock, 0, len(entry.owners))
		released := false
		for _, owner := range entry.owners {
			if owner.OwnerTx == txID {
				released = true
			} else {
				filtered = append(filtered, owner)
			}
		}
		if released {
			entry.owners = filtered
			if len(entry.owners) == 0 {
				delete(lm.locks, key)
			}
			entry.cond.Broadcast()
		}
	}
}
