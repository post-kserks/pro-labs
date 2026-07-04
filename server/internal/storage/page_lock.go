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
	locks sync.Map // map[page.PageID]*sync.RWMutex
}

// NewPageLockManager создаёт новый менеджер блокировок.
func NewPageLockManager() *PageLockManager {
	return &PageLockManager{}
}

// getLock возвращает (или создаёт) блокировку для страницы.
// Операция атомарна — устраняет гонку между созданием записи и захватом блокировки.
func (pm *PageLockManager) getLock(pid page.PageID) *sync.RWMutex {
	if v, ok := pm.locks.Load(pid); ok {
		return v.(*sync.RWMutex)
	}
	lock := &sync.RWMutex{}
	actual, _ := pm.locks.LoadOrStore(pid, lock)
	return actual.(*sync.RWMutex)
}

// RLockPage блокирует страницу для чтения.
func (pm *PageLockManager) RLockPage(pid page.PageID) {
	lock := pm.getLock(pid)
	lock.RLock()
}

// UnlockPage снимает блокировку чтения со страницы.
func (pm *PageLockManager) UnlockPage(pid page.PageID) {
	v, ok := pm.locks.Load(pid)
	if ok {
		v.(*sync.RWMutex).RUnlock()
	} else {
		slog.Warn("page lock not found on unlock", "pageID", pid)
	}
}

// LockPage блокирует страницу для записи.
func (pm *PageLockManager) LockPage(pid page.PageID) {
	lock := pm.getLock(pid)
	lock.Lock()
}

// UnlockPageWrite снимает блокировку записи со страницы.
func (pm *PageLockManager) UnlockPageWrite(pid page.PageID) {
	v, ok := pm.locks.Load(pid)
	if ok {
		v.(*sync.RWMutex).Unlock()
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

// evictIfTooLarge удаляет записи блокировок, которые не удерживаются ни одной горутиной.
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

// EvictUnused вызывается извне для массовой очистки.
// Удаляет все незаблокированные записи. Возвращает количество удалённых.
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
