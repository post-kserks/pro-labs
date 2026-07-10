package storage

import (
	"log/slog"
	"sync"
	"vaultdb/internal/storage/page"
)

const maxLocksBeforeEviction = 10000

// PageLockManager manages page-level locks.
// Allows concurrent writes to different pages of the same table.
type PageLockManager struct {
	locks sync.Map // map[page.PageID]*sync.RWMutex
}

// NewPageLockManager creates a new lock manager.
func NewPageLockManager() *PageLockManager {
	return &PageLockManager{}
}

// getLock returns (or creates) a lock for a page.
// The operation is atomic — eliminates the race between creating the entry and acquiring the lock.
func (pm *PageLockManager) getLock(pid page.PageID) *sync.RWMutex {
	if v, ok := pm.locks.Load(pid); ok {
		return v.(*sync.RWMutex)
	}
	lock := &sync.RWMutex{}
	actual, _ := pm.locks.LoadOrStore(pid, lock)
	return actual.(*sync.RWMutex)
}

// RLockPage locks the page for reads.
func (pm *PageLockManager) RLockPage(pid page.PageID) {
	lock := pm.getLock(pid)
	lock.RLock()
}

// UnlockPage releases read lock on page.
func (pm *PageLockManager) UnlockPage(pid page.PageID) {
	v, ok := pm.locks.Load(pid)
	if ok {
		v.(*sync.RWMutex).RUnlock()
	} else {
		slog.Warn("page lock not found on unlock", "pageID", pid)
	}
}

// LockPage locks the page for writes.
func (pm *PageLockManager) LockPage(pid page.PageID) {
	lock := pm.getLock(pid)
	lock.Lock()
}

// UnlockPageWrite releases write lock on page.
func (pm *PageLockManager) UnlockPageWrite(pid page.PageID) {
	v, ok := pm.locks.Load(pid)
	if ok {
		v.(*sync.RWMutex).Unlock()
	} else {
		slog.Warn("page lock not found on unlock", "pageID", pid)
	}
}

// LockTable locks all pages of the table for writes (for ALTER TABLE, etc.).
func (pm *PageLockManager) LockTable(pids []page.PageID) {
	sortedPids := make([]page.PageID, len(pids))
	copy(sortedPids, pids)
	sortPageIDs(sortedPids)

	for _, pid := range sortedPids {
		pm.LockPage(pid)
	}
}

// UnlockTable releases locks on all pages of a table.
func (pm *PageLockManager) UnlockTable(pids []page.PageID) {
	for _, pid := range pids {
		pm.UnlockPageWrite(pid)
	}
}

// evictIfTooLarge removes lock entries not held by any goroutine.
func (pm *PageLockManager) evictIfTooLarge() {
	count := 0
	pm.locks.Range(func(k, v any) bool {
		count++
		return true
	})
	if count <= maxLocksBeforeEviction {
		return
	}
	target := maxLocksBeforeEviction / 2
	pm.locks.Range(func(k, v any) bool {
		if count <= target {
			return false
		}
		lock := v.(*sync.RWMutex)
		if lock.TryLock() {
			lock.Unlock()
			pm.locks.Delete(k)
			count--
		}
		return true
	})
}

// EvictUnused is called externally for bulk cleanup.
// Removes all unlocked entries. Returns the number removed.
func (pm *PageLockManager) EvictUnused() int {
	removed := 0
	pm.locks.Range(func(k, v any) bool {
		lock := v.(*sync.RWMutex)
		if lock.TryLock() {
			lock.Unlock()
			pm.locks.Delete(k)
			removed++
		}
		return true
	})
	return removed
}

// sortPageIDs sorts PageIDs to prevent deadlock.
func sortPageIDs(pids []page.PageID) {
	for i := 1; i < len(pids); i++ {
		for j := i; j > 0; j-- {
			if lessPageID(pids[j], pids[j-1]) {
				pids[j], pids[j-1] = pids[j-1], pids[j]
			}
		}
	}
}

func lessPageID(a, b page.PageID) bool {
	if a.SegmentNo != b.SegmentNo {
		return a.SegmentNo < b.SegmentNo
	}
	return a.PageNo < b.PageNo
}
