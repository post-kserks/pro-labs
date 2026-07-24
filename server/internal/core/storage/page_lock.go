package storage

import (
	"sync"
	"vaultdb/internal/core/storage/page"
)

const numPageLocks = 16384

// PageLockManager manages page-level locks using a striped lock array.
// Allows concurrent writes to different pages of the same table without allocation overhead.
type PageLockManager struct {
	locks [numPageLocks]sync.RWMutex
}

// NewPageLockManager creates a new lock manager.
func NewPageLockManager() *PageLockManager {
	return &PageLockManager{}
}

// getLock returns a lock for a page based on a hash.
func (pm *PageLockManager) getLock(pid page.PageID) *sync.RWMutex {
	hash := (uint32(pid.TableID) * 16777619) ^ (uint32(pid.SegmentNo) * 1140071481) ^ (pid.PageNo * 2654435761)
	return &pm.locks[hash%numPageLocks]
}

// RLockPage locks the page for reads.
func (pm *PageLockManager) RLockPage(pid page.PageID) {
	pm.getLock(pid).RLock()
}

// UnlockPage releases read lock on page.
func (pm *PageLockManager) UnlockPage(pid page.PageID) {
	pm.getLock(pid).RUnlock()
}

// LockPage locks the page for writes.
func (pm *PageLockManager) LockPage(pid page.PageID) {
	pm.getLock(pid).Lock()
}

// UnlockPageWrite releases write lock on page.
func (pm *PageLockManager) UnlockPageWrite(pid page.PageID) {
	pm.getLock(pid).Unlock()
}

// LockTable locks all pages of the table for writes (for ALTER TABLE, etc.).
func (pm *PageLockManager) LockTable(pids []page.PageID) {
	// Gather unique hashes to prevent self-deadlock when multiple pages hash to the same lock
	hashes := make([]uint32, 0, len(pids))
	seen := make(map[uint32]bool)
	for _, pid := range pids {
		hash := ((uint32(pid.TableID) * 16777619) ^ (uint32(pid.SegmentNo) * 1140071481) ^ (pid.PageNo * 2654435761)) % numPageLocks
		if !seen[hash] {
			seen[hash] = true
			hashes = append(hashes, hash)
		}
	}

	// Sort hashes to prevent deadlocks across concurrent table locks
	for i := 1; i < len(hashes); i++ {
		for j := i; j > 0 && hashes[j] < hashes[j-1]; j-- {
			hashes[j], hashes[j-1] = hashes[j-1], hashes[j]
		}
	}

	for _, hash := range hashes {
		pm.locks[hash].Lock()
	}
}

// UnlockTable releases locks on all pages of a table.
func (pm *PageLockManager) UnlockTable(pids []page.PageID) {
	// Must use the exact same unique hashes logic to avoid double unlocking
	hashes := make([]uint32, 0, len(pids))
	seen := make(map[uint32]bool)
	for _, pid := range pids {
		hash := ((uint32(pid.TableID) * 16777619) ^ (uint32(pid.SegmentNo) * 1140071481) ^ (pid.PageNo * 2654435761)) % numPageLocks
		if !seen[hash] {
			seen[hash] = true
			hashes = append(hashes, hash)
		}
	}

	// Unlocking in reverse order is generally safe and a good habit, though not strictly required
	for i := 1; i < len(hashes); i++ {
		for j := i; j > 0 && hashes[j] < hashes[j-1]; j-- {
			hashes[j], hashes[j-1] = hashes[j-1], hashes[j]
		}
	}

	for _, hash := range hashes {
		pm.locks[hash].Unlock()
	}
}

// evictIfTooLarge is a no-op for striped locks.
func (pm *PageLockManager) evictIfTooLarge() {}

// EvictUnused is a no-op for striped locks. Returns 0.
func (pm *PageLockManager) EvictUnused() int { return 0 }

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
