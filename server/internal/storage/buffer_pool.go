package storage

import (
	"container/list"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

const defaultBufferPoolCapacity = 1024

// BufferPool — LRU кэш для страниц с отложенной записью (write-back).
// Сидит между page engine и HeapFile, кэширует прочитанные страницы в памяти.
//
// Write-back: модификации применяются только в кэше. Страницы записываются
// на диск при вытеснении, явном FlushAll/FlushDirtyPagesUpToLSN или
// фоновой горутиной.
type BufferPool struct {
	mu       sync.RWMutex
	capacity int                           // максимальное количество страниц в кэше
	cache    map[page.PageID]*list.Element // PageID → элемент в lru
	lru      *list.List                    // двусвязный список для LRU (front = recently used)
	count    int                           // текущее количество страниц в кэше
	wal      *wal.WAL                      // WAL для записи full page images

	// Background flush
	bgFlushCancel context.CancelFunc // останавливает фоновую горутину
}

// bufferEntry — запись в кэше.
type bufferEntry struct {
	pid               page.PageID
	page              *page.Page
	hf                *heap.HeapFile  // heap, из которого загружена страница (для write-back)
	pinCnt            int             // количество активных пользователей (нельзя вытеснить)
	imageWritten      bool            // full page image уже записан в WAL
	db                string          // имя БД (для WAL full page image)
	table             string          // имя таблицы (для WAL full page image)
	dirty             bool            // страница была изменена и не записана на диск
	lastModifiedLSN   uint64          // LSN транзакции, последний раз изменившей страницу
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
// Опциональные db/table параметры используются для WAL full page image.
func (bp *BufferPool) FetchPage(pid page.PageID, hf *heap.HeapFile, dbTable ...string) (*page.Page, int, error) {
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
		pid:  pid,
		page: pg,
		hf:   hf,
		pinCnt: 1,
	}
	if len(dbTable) >= 2 {
		entry.db = dbTable[0]
		entry.table = dbTable[1]
	}
	elem := bp.lru.PushFront(entry)
	bp.cache[pid] = elem
	bp.count++

	return pg, 0, nil
}

// CachePage добавляет свежевыделенную страницу в кэш (для write-back).
// Используется для страниц, только что созданных через HeapFile.AllocatePage,
// которые ещё не записаны на диск. Страница добавляется с pinCnt=1 и dirty=true.
func (bp *BufferPool) CachePage(pid page.PageID, pg *page.Page, hf *heap.HeapFile, dbTable ...string) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if _, ok := bp.cache[pid]; ok {
		return
	}

	for bp.count >= bp.capacity {
		if err := bp.evict(); err != nil {
			break
		}
	}

	entry := &bufferEntry{
		pid:    pid,
		page:   pg,
		hf:     hf,
		pinCnt: 1,
		dirty:  true,
	}
	if len(dbTable) >= 2 {
		entry.db = dbTable[0]
		entry.table = dbTable[1]
	}
	elem := bp.lru.PushFront(entry)
	bp.cache[pid] = elem
	bp.count++
}

// WritePreImage записывает full page image в WAL ДО модификации страницы
// (ARIES protocol: BEFORE image must be durable before page is modified).
// Вызывается из InsertRows/mutateRows перед InsertTuple/MarkDeleted.
func (bp *BufferPool) WritePreImage(pid page.PageID) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	elem, ok := bp.cache[pid]
	if !ok || bp.wal == nil {
		return
	}
	entry := elem.Value.(*bufferEntry)
	if entry.imageWritten {
		return
	}
	entry.imageWritten = true
	pageData := make([]byte, len(entry.page))
	copy(pageData, entry.page[:])
	if err := bp.wal.WriteFullPageImage(0, entry.db, entry.table, pid.SegmentNo, pid.PageNo, pageData); err != nil {
		slog.Error("failed to write full page pre-image to WAL", "segment", pid.SegmentNo, "page", pid.PageNo, "error", err)
	}
}

// UnpinPage уменьшает pinCnt страницы и отмечает её как dirty при необходимости.
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
	if dirty {
		entry.dirty = true
	}
}

// UnpinPageDirty уменьшает pinCnt, помечает страницу dirty и записывает LSN.
func (bp *BufferPool) UnpinPageDirty(pid page.PageID, lsn uint64) {
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
	entry.dirty = true
	entry.lastModifiedLSN = lsn
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
// Если страница dirty — записывает её на диск перед удалением.
// Возвращает nil если удалось, ошибку если все страницы запинованы.
func (bp *BufferPool) evict() error {
	for elem := bp.lru.Back(); elem != nil; elem = elem.Prev() {
		entry := elem.Value.(*bufferEntry)
		if entry.pinCnt > 0 {
			continue
		}
		if entry.dirty && entry.hf != nil {
			if err := entry.hf.WritePage(entry.pid, entry.page); err != nil {
				return fmt.Errorf("evict: flush dirty page %v: %w", entry.pid, err)
			}
			entry.dirty = false
			entry.lastModifiedLSN = 0
		}
		bp.lru.Remove(elem)
		delete(bp.cache, entry.pid)
		bp.count--
		return nil
	}
	return fmt.Errorf("evict: all pages pinned")
}

// FlushAll записывает все грязные страницы из кэша на диск.
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for elem := bp.lru.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*bufferEntry)
		if entry.dirty && entry.hf != nil {
			if err := entry.hf.WritePage(entry.pid, entry.page); err != nil {
				return fmt.Errorf("FlushAll: %w", err)
			}
			entry.dirty = false
			entry.lastModifiedLSN = 0
		}
	}
	return nil
}

// FlushDirtyPagesUpToLSN записывает dirty страницы, чей lastModifiedLSN <= maxLSN.
func (bp *BufferPool) FlushDirtyPagesUpToLSN(maxLSN uint64) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for elem := bp.lru.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*bufferEntry)
		if entry.dirty && entry.lastModifiedLSN <= maxLSN && entry.hf != nil {
			if err := entry.hf.WritePage(entry.pid, entry.page); err != nil {
				return fmt.Errorf("FlushDirtyPagesUpToLSN: %w", err)
			}
			entry.dirty = false
			entry.lastModifiedLSN = 0
		}
	}
	return nil
}

// InvalidateTable удаляет незапинованные страницы таблицы из кэша.
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

// InvalidateTableForce удаляет ВСЕ страницы таблицы из кэша,
// включая запинованные. Используется при DROP DATABASE/TABLE.
func (bp *BufferPool) InvalidateTableForce(tableID uint32) {
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
			bp.lru.Remove(elem)
			delete(bp.cache, pid)
			bp.count--
		}
	}
}

// StartBackgroundFlush запускает фоновую горутину, которая периодически
// сбрасывает dirty страницы на диск.
func (bp *BufferPool) StartBackgroundFlush(ctx context.Context, interval time.Duration) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Останавливаем предыдущую горутину, если была
	if bp.bgFlushCancel != nil {
		bp.bgFlushCancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	bp.bgFlushCancel = cancel

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				if err := bp.FlushAll(); err != nil {
					slog.Error("background flush final failed", "error", err)
				}
				return
			case <-ticker.C:
				if err := bp.FlushAll(); err != nil {
					slog.Error("background flush failed", "error", err)
				}
			}
		}
	}()
}

// Close останавливает фоновую горутину и сбрасывает dirty страницы.
func (bp *BufferPool) Close() {
	bp.mu.Lock()
	cancel := bp.bgFlushCancel
	bp.bgFlushCancel = nil
	bp.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Stats возвращает статистику кэша.
func (bp *BufferPool) Stats() BufferPoolStats {
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	var dirtyCount int
	for elem := bp.lru.Front(); elem != nil; elem = elem.Next() {
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
