package storage

import (
	"container/list"
	"fmt"
	"log/slog"
	"sync"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

const defaultBufferPoolCapacity = 1024

// BufferPool — LRU кэш для страниц.
// Сидит между page engine и HeapFile, кэширует прочитанные страницы в памяти.
//
// ВАЖНО: buffer pool НЕ выполняет отложенную запись (write-back). Все модификации
// записываются на диск напрямую вызывающим кодом (heap.WritePage + Sync) до/во
// время UnpinPage. Поэтому кэш всегда содержит только ЧИСТЫЕ страницы, а
// вытеснение просто удаляет страницу из памяти и никогда не пишет на диск.
type BufferPool struct {
	mu       sync.RWMutex
	capacity int                           // максимальное количество страниц в кэше
	cache    map[page.PageID]*list.Element // PageID → элемент в lru
	lru      *list.List                    // двусвязный список для LRU (front = recently used)
	count    int                           // текущее количество страниц в кэше
	wal      *wal.WAL                      // WAL для записи full page images
}

// bufferEntry — запись в кэше.
type bufferEntry struct {
	pid          page.PageID
	page         *page.Page
	pinCnt       int  // количество активных пользователей (нельзя вытеснить)
	imageWritten bool // full page image уже записан в WAL
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

// SetWAL устанавливает WAL для записи full page images.
func (bp *BufferPool) SetWAL(w *wal.WAL) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.wal = w
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
		if err := bp.evict(); err != nil {
			// Не удалось вытеснить (все страницы запинованы)
			break
		}
	}

	// Добавляем в кэш
	entry := &bufferEntry{
		pid:    pid,
		page:   pg,
		pinCnt: 1,
	}
	elem := bp.lru.PushFront(entry)
	bp.cache[pid] = elem
	bp.count++

	return pg, 0, nil
}

// UnpinPage уменьшает pinCnt конкретной страницы.
//
// Buffer pool НЕ выполняет отложенную запись: вызывающий код уже записал
// страницу на диск напрямую (heap.WritePage + Sync) до вызова UnpinPage,
// поэтому кэш хранит только чистые страницы. Параметр dirty используется лишь
// для записи full page image в WAL (защита от torn pages) при первой
// модификации страницы.
func (bp *BufferPool) UnpinPage(pid page.PageID, dirty bool) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	elem, ok := bp.cache[pid]
	if !ok {
		return
	}
	entry := elem.Value.(*bufferEntry)
	if entry.pinCnt > 0 {
		entry.pinCnt--
	}

	if dirty && bp.wal != nil && !entry.imageWritten {
		// Страница впервые помечена как грязная — записываем full page image в WAL.
		// Копию делаем под локом, саму запись в WAL выполняем асинхронно, чтобы
		// не блокировать buffer pool.
		entry.imageWritten = true
		w := bp.wal
		p := pid
		pageData := make([]byte, len(entry.page))
		copy(pageData, entry.page[:])
		go func() {
			if err := w.WriteFullPageImage(0, "", "", p.SegmentNo, p.PageNo, pageData); err != nil {
				slog.Error("failed to write full page image to WAL", "segment", p.SegmentNo, "page", p.PageNo, "error", err)
			}
		}()
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
//
// Кэш хранит только чистые страницы (все модификации пишутся на диск напрямую),
// поэтому вытеснение просто удаляет страницу из памяти и НИКОГДА не пишет на
// диск. Это исключает риск записи страницы в чужой heap-файл.
// Возвращает nil если удалось, ошибку если все страницы запинованы.
func (bp *BufferPool) evict() error {
	for elem := bp.lru.Back(); elem != nil; elem = elem.Prev() {
		entry := elem.Value.(*bufferEntry)
		if entry.pinCnt > 0 {
			continue
		}
		bp.lru.Remove(elem)
		delete(bp.cache, entry.pid)
		bp.count--
		return nil
	}
	return fmt.Errorf("evict: all pages pinned")
}

// FlushAll — no-op. Buffer pool хранит только чистые страницы (все записи идут
// на диск напрямую через heap.WritePage), поэтому сбрасывать нечего. Метод
// сохранён для совместимости и явности вызова на checkpoint/Close.
func (bp *BufferPool) FlushAll(hf *heap.HeapFile) error {
	return nil
}

// FlushDirtyPagesUpToLSN — no-op по той же причине, что и FlushAll: кэш не
// содержит грязных страниц. Сохранён для совместимости с логикой checkpoint.
func (bp *BufferPool) FlushDirtyPagesUpToLSN(maxLSN uint64, hf *heap.HeapFile) error {
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

// Close освобождает ресурсы buffer pool. Фоновых горутин нет, поэтому это no-op;
// метод сохранён для совместимости.
func (bp *BufferPool) Close() {}

// Stats возвращает статистику кэша.
func (bp *BufferPool) Stats() BufferPoolStats {
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	return BufferPoolStats{
		Capacity: bp.capacity,
		Used:     bp.count,
		// Кэш никогда не хранит грязные страницы.
		DirtyCount: 0,
	}
}

// BufferPoolStats статистика buffer pool.
type BufferPoolStats struct {
	Capacity   int
	Used       int
	DirtyCount int
}
