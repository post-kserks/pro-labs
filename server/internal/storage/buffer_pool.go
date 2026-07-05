package storage

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

const defaultBufferPoolCapacity = 16384 // 128MB with 8KB pages

// maxUsageCount is the maximum usage count for clock-sweep (like PostgreSQL's BM_MAX_USAGE = 5).
const maxUsageCount uint8 = 5

// BufferPool — clock-sweep кэш для страниц с отложенной записью (write-back).
// Сидит между page engine и HeapFile, кэширует прочитанные страницы в памяти.
//
// Write-back: модификации применяются только в кэше. Страницы записываются
// на диск при вытеснении, явном FlushAll/FlushDirtyPagesUpToLSN или
// фоновой горутиной.
//
// Clock-sweep: вместо LRU используется алгоритм clock-sweep с usage counts.
// При обращении к странице usage count увеличивается (до maxUsageCount).
// При вытеснении clock hand сканирует массив: страницы с usage > 0 теряют
// по одному за проход (второй шанс), с usage == 0 вытесняются.
type BufferPool struct {
	mu         sync.RWMutex
	capacity   int                        // максимальное количество страниц в кэше
	cache      map[page.PageID]int        // PageID → индекс в buffers
	buffers    []*bufferEntry             // фиксированный массив буферов
	clockHand  int                        // текущая позиция clock hand
	count      int                        // текущее количество страниц в кэше
	wal        *wal.WAL                   // WAL для записи full page images
	bgFlushCancel context.CancelFunc     // останавливает фоновую горутину
}

// bufferEntry — запись в кэше.
type bufferEntry struct {
	pid             page.PageID
	page            *page.Page
	hf              *heap.HeapFile // heap, из которого загружена страница (для write-back)
	pinCnt          int            // количество активных пользователей (нельзя вытеснить)
	usageCount      uint8          // clock-sweep usage count (0..maxUsageCount)
	imageWritten    bool           // full page image уже записан в WAL
	db              string         // имя БД (для WAL full page image)
	table           string         // имя таблицы (для WAL full page image)
	dirty           bool           // страница была изменена и не записана на диск
	lastModifiedLSN uint64         // LSN транзакции, последний раз изменившей страницу
}

// NewBufferPool создаёт новый buffer pool с указанным capacity.
func NewBufferPool(capacity int) *BufferPool {
	if capacity <= 0 {
		capacity = defaultBufferPoolCapacity
	}
	return &BufferPool{
		capacity: capacity,
		cache:    make(map[page.PageID]int, capacity),
		buffers:  make([]*bufferEntry, capacity),
	}
}

// SetWAL устанавливает WAL для записи full page images.
func (bp *BufferPool) SetWAL(w *wal.WAL) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.wal = w
}

// Reference увеличивает usage count страницы (второй шанс при clock-sweep).
func (bp *BufferPool) Reference(buf *bufferEntry) {
	if buf.usageCount < maxUsageCount {
		buf.usageCount++
	}
}

// FetchPage загружает страницу из кэша или с диска.
// Если страницы нет в кэше — читает из HeapFile и кэширует.
// pinCnt увеличивается на 1; вызов UnpinRequired обязателен.
// Опциональные db/table параметры используются для WAL full page image.
func (bp *BufferPool) FetchPage(pid page.PageID, hf *heap.HeapFile, dbTable ...string) (*page.Page, int, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Проверяем кэш
	if idx, ok := bp.cache[pid]; ok {
		entry := bp.buffers[idx]
		entry.pinCnt++
		bp.Reference(entry)
		return entry.page, 0, nil
	}

	// Страницы нет в кэше — читаем с диска
	pg := &page.Page{}
	if err := hf.ReadPage(pid, pg); err != nil {
		return nil, 0, err
	}

	// Если кэш полон — вытесняем через clock-sweep
	for bp.count >= bp.capacity {
		if err := bp.evict(); err != nil {
			// Не удалось вытеснить (все страницы запинованы)
			break
		}
	}

	// Находим свободный слот
	idx := bp.findEmptySlot()

	// Добавляем в кэш
	entry := &bufferEntry{
		pid:       pid,
		page:      pg,
		hf:        hf,
		pinCnt:    1,
		usageCount: 1,
	}
	if len(dbTable) >= 2 {
		entry.db = dbTable[0]
		entry.table = dbTable[1]
	}
	bp.buffers[idx] = entry
	bp.cache[pid] = idx
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

	idx := bp.findEmptySlot()

	entry := &bufferEntry{
		pid:       pid,
		page:      pg,
		hf:        hf,
		pinCnt:    1,
		usageCount: 1,
		dirty:     true,
	}
	if len(dbTable) >= 2 {
		entry.db = dbTable[0]
		entry.table = dbTable[1]
	}
	bp.buffers[idx] = entry
	bp.cache[pid] = idx
	bp.count++
}

// WritePreImage записывает full page image в WAL ДО модификации страницы
// (ARIES protocol: BEFORE image must be durable before page is modified).
// Вызывается из InsertRows/mutateRows перед InsertTuple/MarkDeleted.
func (bp *BufferPool) WritePreImage(pid page.PageID) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	idx, ok := bp.cache[pid]
	if !ok || bp.wal == nil {
		return
	}
	entry := bp.buffers[idx]
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

	idx, ok := bp.cache[pid]
	if !ok {
		return
	}
	entry := bp.buffers[idx]
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

	idx, ok := bp.cache[pid]
	if !ok {
		return
	}
	entry := bp.buffers[idx]
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

	idx, ok := bp.cache[pid]
	if !ok {
		return
	}
	entry := bp.buffers[idx]
	if entry.pinCnt > 0 {
		return
	}
	bp.buffers[idx] = nil
	delete(bp.cache, pid)
	bp.count--
}

// evict вытесняет страницу через clock-sweep алгоритм.
// Clock hand сканирует массив: если страница запинована — пропускаем,
// если usageCount > 0 — уменьшаем и переходим дальше,
// если usageCount == 0 — вытесняем (с записью dirty на диск).
func (bp *BufferPool) evict() error {
	// Ограничение на количество проходов, чтобы не зациклиться
	maxAttempts := bp.capacity * 2

	for i := 0; i < maxAttempts; i++ {
		idx := bp.clockHand
		bp.clockHand = (bp.clockHand + 1) % bp.capacity

		buf := bp.buffers[idx]
		if buf == nil {
			// Пустой слот — используем
			return nil
		}

		if buf.pinCnt > 0 {
			// Запинована — пропускаем
			continue
		}

		if buf.usageCount > 0 {
			// Второй шанс — уменьшаем usage count
			buf.usageCount--
			continue
		}

		// Вытесняем эту страницу
		if buf.dirty && buf.hf != nil {
			if err := buf.hf.WritePage(buf.pid, buf.page); err != nil {
				return fmt.Errorf("evict: flush dirty page %v: %w", buf.pid, err)
			}
			buf.dirty = false
			buf.lastModifiedLSN = 0
		}
		bp.buffers[idx] = nil
		delete(bp.cache, buf.pid)
		bp.count--
		return nil
	}

	return fmt.Errorf("evict: all pages pinned")
}

// FlushAll записывает все грязные страницы из кэша на диск.
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.dirty && entry.hf != nil {
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

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.dirty && entry.lastModifiedLSN <= maxLSN && entry.hf != nil {
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

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.pid.TableID == tableID {
			if entry.pinCnt > 0 {
				continue
			}
			bp.buffers[i] = nil
			delete(bp.cache, entry.pid)
			bp.count--
		}
	}
}

// InvalidateTableForce удаляет ВСЕ страницы таблицы из кэша,
// включая запинованные. Используется при DROP DATABASE/TABLE.
func (bp *BufferPool) InvalidateTableForce(tableID uint32) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.pid.TableID == tableID {
			bp.buffers[i] = nil
			delete(bp.cache, entry.pid)
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

// PrefetchPages загружает страницы в кэш асинхронно для read-ahead при
// последовательном сканировании. Страницы, уже находящиеся в кэше, пропускаются.
// Ошибки чтения логируются, но не прерывают вызывающий код — это best-effort.
func (bp *BufferPool) PrefetchPages(pids []page.PageID, hf *heap.HeapFile) {
	for _, pid := range pids {
		// Skip pages already in cache
		bp.mu.RLock()
		_, cached := bp.cache[pid]
		bp.mu.RUnlock()
		if cached {
			continue
		}

		// Read from disk (no lock held)
		pg := &page.Page{}
		if err := hf.ReadPage(pid, pg); err != nil {
			// Page might be beyond EOF or corrupt — silently skip
			continue
		}

		// Insert into cache (acquires lock)
		bp.mu.Lock()
		if _, already := bp.cache[pid]; already {
			bp.mu.Unlock()
			continue
		}
		for bp.count >= bp.capacity {
			if err := bp.evict(); err != nil {
				bp.mu.Unlock()
				return
			}
		}
		idx := bp.findEmptySlot()
		bp.buffers[idx] = &bufferEntry{
			pid:       pid,
			page:      pg,
			hf:        hf,
			pinCnt:    0,
			usageCount: 1,
		}
		bp.cache[pid] = idx
		bp.count++
		bp.mu.Unlock()
	}
}

// Stats возвращает статистику кэша.
func (bp *BufferPool) Stats() BufferPoolStats {
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	var dirtyCount int
	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.dirty {
			dirtyCount++
		}
	}

	return BufferPoolStats{
		Capacity:   bp.capacity,
		Used:       bp.count,
		DirtyCount: dirtyCount,
	}
}

// findEmptySlot находит первый пустой слот в массиве буферов.
func (bp *BufferPool) findEmptySlot() int {
	for i := 0; i < bp.capacity; i++ {
		if bp.buffers[i] == nil {
			return i
		}
	}
	// Не должно happen если count < capacity
	return bp.clockHand % bp.capacity
}

// BufferPoolStats статистика buffer pool.
type BufferPoolStats struct {
	Capacity   int
	Used       int
	DirtyCount int
}

// StorageOptions содержит параметры конфигурации хранилища,
// передаваемые из config.StorageConfig без создания циклических импортов.
type StorageOptions struct {
	BufferPoolPages int
}
