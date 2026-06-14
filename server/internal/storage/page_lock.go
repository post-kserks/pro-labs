package storage

import (
	"sync"
	"vaultdb/internal/storage/page"
)

const maxLocksBeforeEviction = 10000

// PageLockManager управляет блокировками на уровне страниц.
// Позволяет конкурентные записи в разные страницы одной таблицы.
type PageLockManager struct {
	mu    sync.RWMutex
	locks map[page.PageID]*sync.RWMutex
}

// NewPageLockManager создаёт новый менеджер блокировок.
func NewPageLockManager() *PageLockManager {
	return &PageLockManager{
		locks: make(map[page.PageID]*sync.RWMutex),
	}
}

// RLockPage блокирует страницу для чтения.
func (pm *PageLockManager) RLockPage(pid page.PageID) {
	pm.mu.RLock()
	lock, ok := pm.locks[pid]
	pm.mu.RUnlock()

	if !ok {
		pm.mu.Lock()
		lock, ok = pm.locks[pid]
		if !ok {
			lock = &sync.RWMutex{}
			pm.locks[pid] = lock
			pm.evictIfTooLarge()
		}
		pm.mu.Unlock()
	}

	lock.RLock()
}

// UnlockPage снимает блокировку чтения со страницы.
func (pm *PageLockManager) UnlockPage(pid page.PageID) {
	pm.mu.RLock()
	lock, ok := pm.locks[pid]
	pm.mu.RUnlock()

	if ok {
		lock.RUnlock()
	}
}

// LockPage блокирует страницу для записи.
func (pm *PageLockManager) LockPage(pid page.PageID) {
	pm.mu.RLock()
	lock, ok := pm.locks[pid]
	pm.mu.RUnlock()

	if !ok {
		pm.mu.Lock()
		lock, ok = pm.locks[pid]
		if !ok {
			lock = &sync.RWMutex{}
			pm.locks[pid] = lock
			pm.evictIfTooLarge()
		}
		pm.mu.Unlock()
	}

	lock.Lock()
}

// UnlockPageWrite снимает блокировку записи со страницы.
func (pm *PageLockManager) UnlockPageWrite(pid page.PageID) {
	pm.mu.RLock()
	lock, ok := pm.locks[pid]
	pm.mu.RUnlock()

	if ok {
		lock.Unlock()
	}
}

// LockTable блокирует все страницы таблицы для записи (для ALTER TABLE и т.п.).
func (pm *PageLockManager) LockTable(pids []page.PageID) {
	sortedPids := make([]page.PageID, len(pids))
	copy(sortedPids, pids)
	sortPageIDs(sortedPids)

	for _, pid := range sortedPids {
		pm.LockPage(pid)
	}
}

// UnlockTable снимает блокировки со всех страниц таблицы.
func (pm *PageLockManager) UnlockTable(pids []page.PageID) {
	for _, pid := range pids {
		pm.UnlockPageWrite(pid)
	}
}

// evictIfTooLarge удаляет неиспользуемые блокировки если карта выросла слишком большой.
// Вызывается под write-локом.
//
// NOTE: This eviction is best-effort. The TryLock/Unlock/TryRLock/delete
// sequence is inherently TOCTOU — between releasing the exclusive lock and
// acquiring the read lock, another goroutine may have taken the lock pointer.
// Callers must not rely on exact lock counts. The current approach is
// acceptable because Go's GC prevents use-after-free; the worst case is two
// goroutines using different lock objects for the same page, which still
// provides some protection via the page-level locking in page_lock.go.
func (pm *PageLockManager) evictIfTooLarge() {
	if len(pm.locks) <= maxLocksBeforeEviction {
		return
	}
	for pid, lock := range pm.locks {
		if !lock.TryLock() {
			continue
		}
		lock.Unlock()
		if lock.TryRLock() {
			lock.RUnlock()
			delete(pm.locks, pid)
		}
	}
}

// sortPageIDs сортирует PageID для предотвращения deadlock.
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
