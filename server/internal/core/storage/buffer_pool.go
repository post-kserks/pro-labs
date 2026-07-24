package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vaultdb/internal/core/storage/heap"
	"vaultdb/internal/core/storage/page"
	"vaultdb/internal/core/wal"
)

const defaultBufferPoolCapacity = 16384 // 128MB with 8KB pages

// maxUsageCount is the maximum usage count for clock-sweep (like PostgreSQL's BM_MAX_USAGE = 5).
const maxUsageCount uint8 = 5

// BufferPool — clock-sweep cache for pages with write-back policy.
// Sits between the page engine and HeapFile, caching read pages in memory.
//
// Write-back: modifications are applied only in the cache. Pages are written
// to disk on eviction, explicit FlushAll/FlushDirtyPagesUpToLSN, or
// by a background goroutine.
//
// Clock-sweep: instead of LRU, the clock-sweep algorithm with usage counts is used.
// On page access, the usage count is incremented (up to maxUsageCount).
// On eviction, the clock hand scans the array: pages with usage > 0 lose
// one per pass (second chance), pages with usage == 0 are evicted.
type BufferPool struct {
	mu            sync.RWMutex
	capacity      int                 // maximum number of pages in cache
	cache         map[page.PageID]int // PageID → index in buffers
	buffers       []*bufferEntry      // fixed-size array of buffer entries
	freeSlots     []int               // stack of free buffer indices for O(1) allocation
	clockHand     int                 // current clock hand position
	count         int                 // current number of pages in cache
	wal           *wal.WAL            // WAL for writing full page images
	bgFlushCancel context.CancelFunc  // stops the background goroutine
	bgFlushWg     sync.WaitGroup      // waits for background flush to finish
}

// bufferEntry — cache entry.
type bufferEntry struct {
	pid             page.PageID
	page            *page.Page
	hf              *heap.HeapFile // heap from which the page was loaded (for write-back)
	pinCnt          int            // number of active users (cannot be evicted)
	usageCount      uint8          // clock-sweep usage count (0..maxUsageCount)
	imageWritten    bool           // full page image already written to WAL
	db              string         // database name (for WAL full page image)
	table           string         // table name (for WAL full page image)
	dirty           bool           // page was modified and not yet written to disk
	lastModifiedLSN uint64         // LSN of the transaction that last modified the page
}

// NewBufferPool creates a new buffer pool with the given capacity.
func NewBufferPool(capacity int) *BufferPool {
	if capacity <= 0 {
		capacity = defaultBufferPoolCapacity
	}
	freeSlots := make([]int, capacity)
	for i := 0; i < capacity; i++ {
		freeSlots[i] = capacity - 1 - i
	}
	return &BufferPool{
		capacity:  capacity,
		cache:     make(map[page.PageID]int, capacity),
		buffers:   make([]*bufferEntry, capacity),
		freeSlots: freeSlots,
	}
}

// SetWAL sets the WAL for writing full page images.
func (bp *BufferPool) SetWAL(w *wal.WAL) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.wal = w
}

// Reference increments the page usage count (second chance for clock-sweep).
func (bp *BufferPool) Reference(buf *bufferEntry) {
	if buf.usageCount < maxUsageCount {
		buf.usageCount++
	}
}

// FetchPage loads a page from cache or disk.
// If the page is not in cache — reads from HeapFile and caches it.
// pinCnt is incremented by 1; calling UnpinPage is required afterward.
// Optional db/table parameters are used for WAL full page image.
func (bp *BufferPool) FetchPage(pid page.PageID, hf *heap.HeapFile, dbTable ...string) (*page.Page, int, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Check cache
	if idx, ok := bp.cache[pid]; ok {
		entry := bp.buffers[idx]
		entry.pinCnt++
		bp.Reference(entry)
		return entry.page, 0, nil
	}

	// Page not in cache — read from disk
	pg := &page.Page{}
	if err := hf.ReadPage(pid, pg); err != nil {
		return nil, 0, err
	}

	// If cache is full — evict via clock-sweep
	for bp.count >= bp.capacity {
		if err := bp.evict(); err != nil {
			// Could not evict (all pages are pinned)
			break
		}
	}

	// Find free slot
	idx := bp.findEmptySlot()

	// Add to cache
	entry := &bufferEntry{
		pid:        pid,
		page:       pg,
		hf:         hf,
		pinCnt:     1,
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

// CachePage adds a freshly allocated page to the cache (for write-back).
// Used for pages just created via HeapFile.AllocatePage that have not yet been
// written to disk. The page is added with pinCnt=1 and dirty=true.
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
		pid:        pid,
		page:       pg,
		hf:         hf,
		pinCnt:     1,
		usageCount: 1,
		dirty:      true,
	}
	if len(dbTable) >= 2 {
		entry.db = dbTable[0]
		entry.table = dbTable[1]
	}
	bp.buffers[idx] = entry
	bp.cache[pid] = idx
	bp.count++
}

// WritePreImage writes the full page image to WAL BEFORE modifying the page
// (ARIES protocol: BEFORE image must be durable before page is modified).
// Called from InsertRows/mutateRows before InsertTuple/MarkDeleted.
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

// UnpinPage decrements the page pinCnt and marks it dirty if needed.
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

// UnpinPageDirty decrements pinCnt, marks the page dirty, and records the LSN.
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

// InvalidatePage removes a page from the cache (after direct disk write).
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
	bp.freeSlots = append(bp.freeSlots, idx)
	delete(bp.cache, pid)
	bp.count--
}

// evict evicts a page using the clock-sweep algorithm.
// Clock hand scans the array: if page is pinned — skip,
// if usageCount > 0 — decrement and move on,
// if usageCount == 0 — evict (flush dirty to disk).
func (bp *BufferPool) evict() error {
	// Limit the number of passes to avoid infinite loop
	maxAttempts := bp.capacity * 2

	for i := 0; i < maxAttempts; i++ {
		idx := bp.clockHand
		bp.clockHand = (bp.clockHand + 1) % bp.capacity

		buf := bp.buffers[idx]
		if buf == nil {
			// Empty slot — use it
			return nil
		}

		if buf.pinCnt > 0 {
			// Pinned — skip
			continue
		}

		if buf.usageCount > 0 {
			// Second chance — decrement usage count
			buf.usageCount--
			continue
		}

		// Evict this page
		if buf.dirty && buf.hf != nil {
			if bp.wal != nil {
				_, _ = bp.wal.Flush() // ARIES: flush WAL before writing page to disk
			}
			if err := buf.hf.WritePage(buf.pid, buf.page); err != nil {
				return fmt.Errorf("evict: flush dirty page %v: %w", buf.pid, err)
			}
			buf.dirty = false
			buf.lastModifiedLSN = 0
		}
		bp.buffers[idx] = nil
		bp.freeSlots = append(bp.freeSlots, idx)
		delete(bp.cache, buf.pid)
		bp.count--
		return nil
	}

	return fmt.Errorf("evict: all pages pinned")
}

// FlushAll writes all dirty pages from cache to disk.
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if bp.wal != nil {
		_, _ = bp.wal.Flush() // ARIES: ensure WAL is durable before pages are flushed
	}

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.dirty && entry.hf != nil {
			if err := entry.hf.WritePage(entry.pid, entry.page); err != nil {
				return fmt.Errorf("FlushAll: %w", err)
			}
			entry.dirty = false
			entry.lastModifiedLSN = 0
			entry.imageWritten = false
		}
	}
	return nil
}

// FlushDirtyPagesUpToLSN writes dirty pages whose lastModifiedLSN <= maxLSN.
func (bp *BufferPool) FlushDirtyPagesUpToLSN(maxLSN uint64) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	if bp.wal != nil {
		_, _ = bp.wal.Flush() // ARIES: ensure WAL is durable before pages are flushed
	}

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.dirty && entry.lastModifiedLSN <= maxLSN && entry.hf != nil {
			if err := entry.hf.WritePage(entry.pid, entry.page); err != nil {
				return fmt.Errorf("FlushDirtyPagesUpToLSN: %w", err)
			}
			entry.dirty = false
			entry.lastModifiedLSN = 0
			entry.imageWritten = false
		}
	}
	return nil
}

// InvalidateTable removes unpinned pages of a table from the cache.
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
			bp.freeSlots = append(bp.freeSlots, i)
			delete(bp.cache, entry.pid)
			bp.count--
		}
	}
}

// InvalidateTableForce removes ALL pages of a table from the cache,
// including pinned ones. Used during DROP DATABASE/TABLE.
func (bp *BufferPool) InvalidateTableForce(tableID uint32) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.pid.TableID == tableID {
			bp.buffers[i] = nil
			bp.freeSlots = append(bp.freeSlots, i)
			delete(bp.cache, entry.pid)
			bp.count--
		}
	}
}

// StartBackgroundFlush starts a background goroutine that periodically
// flushes dirty pages to disk.
func (bp *BufferPool) StartBackgroundFlush(ctx context.Context, interval time.Duration) {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// Stop the previous goroutine if one was running
	if bp.bgFlushCancel != nil {
		bp.bgFlushCancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	bp.bgFlushCancel = cancel

	bp.bgFlushWg.Add(1)
	go func() {
		defer bp.bgFlushWg.Done()
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

// Close stops the background goroutine and flushes dirty pages.
func (bp *BufferPool) Close() {
	bp.mu.Lock()
	cancel := bp.bgFlushCancel
	bp.bgFlushCancel = nil
	bp.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	bp.bgFlushWg.Wait()
}

// PrefetchPages loads pages into cache asynchronously for read-ahead during
// sequential scans. Pages already in cache are skipped.
// Read errors are logged but do not interrupt the caller — this is best-effort.
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
			pid:        pid,
			page:       pg,
			hf:         hf,
			pinCnt:     0,
			usageCount: 1,
		}
		bp.cache[pid] = idx
		bp.count++
		bp.mu.Unlock()
	}
}

// Stats returns cache statistics.
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

// findEmptySlot finds the first empty slot in the buffer array.
func (bp *BufferPool) findEmptySlot() int {
	n := len(bp.freeSlots)
	if n > 0 {
		idx := bp.freeSlots[n-1]
		bp.freeSlots = bp.freeSlots[:n-1]
		return idx
	}
	// Should not happen if count < capacity
	return bp.clockHand % bp.capacity
}

// BufferPoolStats holds buffer pool statistics.
type BufferPoolStats struct {
	Capacity   int
	Used       int
	DirtyCount int
}

// StorageOptions contains storage configuration parameters,
// passed from config.StorageConfig to avoid circular imports.
type StorageOptions struct {
	BufferPoolPages int
}

// PageKey identifies a page descriptor in the buffer pool with its database and table context.
type PageKey struct {
	DB     string
	Table  string
	PageID page.PageID
}

var (
	ErrPagePinned   = errors.New("buffer_pool: page is pinned")
	ErrPageNotDirty = errors.New("buffer_pool: page is not dirty")
)

// CollectDirtyPages collects up to batchSize dirty, unpinned page descriptors.
func (bp *BufferPool) CollectDirtyPages(batchSize int) []PageKey {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	var keys []PageKey
	for i := 0; i < bp.capacity; i++ {
		entry := bp.buffers[i]
		if entry != nil && entry.dirty && entry.pinCnt == 0 {
			keys = append(keys, PageKey{
				DB:     entry.db,
				Table:  entry.table,
				PageID: entry.pid,
			})
			if len(keys) >= batchSize {
				break
			}
		}
	}
	return keys
}

// FlushPage flushes a single page identified by PageID to disk if it is dirty and unpinned.
func (bp *BufferPool) FlushPage(db, table string, pid page.PageID) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	idx, ok := bp.cache[pid]
	if !ok {
		return ErrPageNotDirty
	}
	entry := bp.buffers[idx]
	if entry == nil || !entry.dirty {
		return ErrPageNotDirty
	}
	if entry.pinCnt > 0 {
		return ErrPagePinned
	}
	if entry.hf != nil {
		if bp.wal != nil {
			_, _ = bp.wal.Flush()
		}
		if err := entry.hf.WritePage(entry.pid, entry.page); err != nil {
			return fmt.Errorf("FlushPage: %w", err)
		}
	}
	entry.dirty = false
	entry.lastModifiedLSN = 0
	entry.imageWritten = false
	return nil
}
