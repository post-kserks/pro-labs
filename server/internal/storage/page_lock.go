package storage

import (
	"log/slog"
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
	pm.mu.Lock()
	lock, ok := pm.locks[pid]
	if !ok {
		lock = &sync.RWMutex{}
		pm.locks[pid] = lock
		pm.evictIfTooLarge()
	}
	pm.mu.Unlock()

	lock.RLock()
}

// UnlockPage снимает блокировку чтения со страницы.
func (pm *PageLockManager) UnlockPage(pid page.PageID) {
	pm.mu.RLock()
	lock, ok := pm.locks[pid]
	pm.mu.RUnlock()

	if ok {
		lock.RUnlock()
	} else {
		slog.Warn("page lock not found on unlock", "pageID", pid)
	}
}

// LockPage блокирует страницу для записи.
func (pm *PageLockManager) LockPage(pid page.PageID) {
	pm.mu.Lock()
	lock, ok := pm.locks[pid]
	if !ok {
		lock = &sync.RWMutex{}
		pm.locks[pid] = lock
		pm.evictIfTooLarge()
	}
	pm.mu.Unlock()

	lock.Lock()
}

// UnlockPageWrite снимает блокировку записи со страницы.
func (pm *PageLockManager) UnlockPageWrite(pid page.PageID) {
	pm.mu.RLock()
	lock, ok := pm.locks[pid]
	pm.mu.RUnlock()

	if ok {
		lock.Unlock()
	} else {
		slog.Warn("page lock not found on unlock", "pageID", pid)
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

func (pm *PageLockManager) evictIfTooLarge() {
	if len(pm.locks) <= maxLocksBeforeEviction {
		return
	}
	target := maxLocksBeforeEviction / 2
	for pid, lock := range pm.locks {
		if len(pm.locks) <= target {
			break
		}
		if lock.TryLock() {
			lock.Unlock()
			delete(pm.locks, pid)
		}
	}
}

// EvictUnused вызывается извне (без mu.Lock) для массовой очистки.
// Удаляет все незаблокированные записи. Возвращает количество удалённых.
func (pm *PageLockManager) EvictUnused() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	removed := 0
	for pid, lock := range pm.locks {
		if lock.TryLock() {
			lock.Unlock()
			delete(pm.locks, pid)
			removed++
		}
	}
	return removed
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
