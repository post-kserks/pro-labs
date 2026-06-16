package storage

import (
	"container/list"
	"sync"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
)

const defaultBufferPoolCapacity = 1024

// BufferPool — LRU кэш для страниц.
// Сидит между page engine и HeapFile, кэширует прочитанные страницы в памяти.
type BufferPool struct {
	mu       sync.RWMutex
	capacity int                    // максимальное количество страниц в кэше
	cache    map[page.PageID]*list.Element // PageID → элемент в lru
	lru      *list.List             // двусвязный список для LRU (front = recently used)
	count    int                    // текущее количество страниц в кэше
}

// bufferEntry — запись в кэше.
type bufferEntry struct {
	pid    page.PageID
	page   *page.Page
	dirty  bool // страница была изменена и не сброшена на диск
	pinCnt int  // количество активных пользователей (нельзя вытеснить)
}

// NewBufferPool создаёт новый buffer pool с указанным capacity.
func NewBufferPool(capacity int) *BufferPool {
	if capacity <= 0 {
		capacity = defaultBufferPoolCapacity
	}
	return &BufferPool{
		capacity: capacity,
		cache:    make(map[page.PageID]*list.Element, capacity),
		lru:      list.New(),
	}
}

// FetchPage загружает страницу из кэша или с диска.
// Если страницы нет в кэше — читает из HeapFile и кэширует.
// pinCnt увеличивается на 1; вызов UnpinRequired обязателен.
//
// IMPORTANT: FetchPage returns a pointer to the shared cached *page.Page.
// Two concurrent callers will receive the SAME mutable object. This is by
// design — identical to PostgreSQL's buffer pool. The pinCnt mechanism
// prevents eviction while the page is in use. The caller MUST call
// UnpinPage when done. Concurrent access to the same page is the caller's
// responsibility, enforced by page-level locking in PageLockManager.
func (bp *BufferPool) FetchPage(pid page.PageID, hf *heap.HeapFile) (*page.Page, int, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Проверяем кэш
	if elem, ok := bp.cache[pid]; ok {
		entry := elem.Value.(*bufferEntry)
		entry.pinCnt++
		bp.lru.MoveToFront(elem)
		return entry.page, 0, nil
	}

	// Страницы нет в кэше — читаем с диска
	pg := &page.Page{}
	if err := hf.ReadPage(pid, pg); err != nil {
		return nil, 0, err
	}

	// Если кэш полон — вытесняем LRU
	for bp.count >= bp.capacity {
		if !bp.evict(hf) {
			// Не удалось вытестить (все страницы запинованы)
			break
		}
	}

	// Добавляем в кэш
	entry := &bufferEntry{
		pid:    pid,
		page:   pg,
		dirty:  false,
		pinCnt: 1,
	}
	elem := bp.lru.PushFront(entry)
	bp.cache[pid] = elem
	bp.count++

	return pg, 0, nil
}

// UnpinPage уменьшает pinCnt конкретной страницы.
func (bp *BufferPool) UnpinPage(pid page.PageID, dirty bool) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if elem, ok := bp.cache[pid]; ok {
		entry := elem.Value.(*bufferEntry)
		if entry.pinCnt > 0 {
			entry.pinCnt--
			if dirty {
				entry.dirty = true
			}
		}
	}
}

// InvalidatePage удаляет страницу из кэша (после прямой записи на диск).
func (bp *BufferPool) InvalidatePage(pid page.PageID) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if elem, ok := bp.cache[pid]; ok {
		entry := elem.Value.(*bufferEntry)
		if entry.pinCnt > 0 {
			return
		}
		bp.lru.Remove(elem)
		delete(bp.cache, pid)
		bp.count--
	}
}

// evict вытесняет самую старую незапинованную страницу из кэша.
// Грязные страницы сбрасываются на диск перед удалением.
// Возвращает true если удалось, false если все страницы запинованы.
func (bp *BufferPool) evict(hf *heap.HeapFile) bool {
	for elem := bp.lru.Back(); elem != nil; elem = elem.Prev() {
		entry := elem.Value.(*bufferEntry)
		if entry.pinCnt > 0 {
			continue
		}
		if entry.dirty && hf != nil {
			_ = hf.WritePage(entry.pid, entry.page)
			entry.dirty = false
		}
		bp.lru.Remove(elem)
		delete(bp.cache, entry.pid)
		bp.count--
		return true
	}
	return false
}

// evictDirty сбрасывает грязную страницу на диск перед вытеснением.
// Вызывается когда known dirty page нужен для записи на диск.
func (bp *BufferPool) evictDirty(pid page.PageID, hf *heap.HeapFile) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	elem, ok := bp.cache[pid]
	if !ok {
		return nil
	}
	entry := elem.Value.(*bufferEntry)
	if entry.dirty {
		if err := hf.WritePage(pid, entry.page); err != nil {
			return err
		}
		entry.dirty = false
	}
	return nil
}

// FlushAll сбрасывает все dirty pages на диск.
func (bp *BufferPool) FlushAll(hf *heap.HeapFile) error {
	type dirtyEntry struct {
		pid  page.PageID
		page *page.Page
	}

	bp.mu.Lock()
	var dirty []dirtyEntry
	for pid, elem := range bp.cache {
		entry := elem.Value.(*bufferEntry)
		if entry.dirty {
			dirty = append(dirty, dirtyEntry{pid: pid, page: entry.page})
			entry.dirty = false
		}
	}
	bp.mu.Unlock()

	for _, d := range dirty {
		if err := hf.WritePage(d.pid, d.page); err != nil {
			return err
		}
	}
	return nil
}

// FlushDirtyPagesUpToLSN сбрасывает dirty pages с LSN <= maxLSN.
func (bp *BufferPool) FlushDirtyPagesUpToLSN(maxLSN uint64, hf *heap.HeapFile) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for pid, elem := range bp.cache {
		entry := elem.Value.(*bufferEntry)
		if entry.dirty && entry.page.LSN() <= maxLSN {
			if err := hf.WritePage(pid, entry.page); err != nil {
				return err
			}
			entry.dirty = false
		}
	}
	return nil
}

// InvalidateTable удаляет все незапинованные страницы указанной таблицы из кэша.
func (bp *BufferPool) InvalidateTable(tableID uint32) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	var toRemove []page.PageID
	for pid := range bp.cache {
		if pid.TableID == tableID {
			toRemove = append(toRemove, pid)
		}
	}

	for _, pid := range toRemove {
		if elem, ok := bp.cache[pid]; ok {
			entry := elem.Value.(*bufferEntry)
			if entry.pinCnt > 0 {
				continue
			}
			bp.lru.Remove(elem)
			delete(bp.cache, pid)
			bp.count--
		}
	}
}

// Stats возвращает статистику кэша.
func (bp *BufferPool) Stats() BufferPoolStats {
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	dirtyCount := 0
	for _, elem := range bp.cache {
		entry := elem.Value.(*bufferEntry)
		if entry.dirty {
			dirtyCount++
		}
	}

	return BufferPoolStats{
		Capacity:   bp.capacity,
		Used:       bp.count,
		DirtyCount: dirtyCount,
	}
}

// BufferPoolStats статистика buffer pool.
type BufferPoolStats struct {
	Capacity   int
	Used       int
	DirtyCount int
}
