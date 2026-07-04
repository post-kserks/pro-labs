package storage

import (
	"runtime"
	"testing"
	"time"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
)

func setupHeapFile(t *testing.T) *heap.HeapFile {
	t.Helper()
	dir := t.TempDir()
	hf, err := heap.CreateHeapFile(dir)
	if err != nil {
		t.Fatalf("CreateHeapFile: %v", err)
	}
	t.Cleanup(func() { hf.Close() })
	return hf
}

func TestBufferPoolFetchAndUnpin(t *testing.T) {
	bp := NewBufferPool(4)
	hf := setupHeapFile(t)

	// Allocate a page so we can read it
	pid, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatalf("AllocatePage: %v", err)
	}

	pg, _, err := bp.FetchPage(pid, hf)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if pg == nil {
		t.Fatal("expected non-nil page")
	}

	// Unpin
	bp.UnpinPage(pid, false)

	stats := bp.Stats()
	if stats.Used != 1 {
		t.Fatalf("expected 1 page in cache, got %d", stats.Used)
	}
}

func TestBufferPoolEviction(t *testing.T) {
	bp := NewBufferPool(2)
	hf := setupHeapFile(t)

	// Allocate 3 pages
	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid2, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid3, _, _ := hf.AllocatePage(page.PageTypeHeap)

	// Fetch all 3 into cache (capacity 2)
	bp.FetchPage(pid1, hf)
	bp.UnpinPage(pid1, false)

	bp.FetchPage(pid2, hf)
	bp.UnpinPage(pid2, false)

	bp.FetchPage(pid3, hf)
	bp.UnpinPage(pid3, false)

	// Cache should have evicted pid1
	stats := bp.Stats()
	if stats.Used > 2 {
		t.Fatalf("expected at most 2 pages in cache, got %d", stats.Used)
	}
}

func TestBufferPoolPinnedPageNotEvicted(t *testing.T) {
	bp := NewBufferPool(2)
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid2, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid3, _, _ := hf.AllocatePage(page.PageTypeHeap)

	// Pin pid1 (don't unpin)
	bp.FetchPage(pid1, hf)

	// Fetch and unpin pid2
	bp.FetchPage(pid2, hf)
	bp.UnpinPage(pid2, false)

	// Fetch pid3 — should evict pid2, not pid1
	bp.FetchPage(pid3, hf)
	bp.UnpinPage(pid3, false)

	// pid1 should still be in cache
	_, _, err := bp.FetchPage(pid1, hf)
	if err != nil {
		t.Fatalf("pid1 should still be cached: %v", err)
	}
	bp.UnpinPage(pid1, false)
}

func TestBufferPoolDirtyFlush(t *testing.T) {
	bp := NewBufferPool(4)
	hf := setupHeapFile(t)

	pid, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatalf("AllocatePage: %v", err)
	}

	_, _, err = bp.FetchPage(pid, hf)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	bp.UnpinPage(pid, true) // mark dirty

	err = bp.FlushAll()
	if err != nil {
		t.Fatalf("FlushAll: %v", err)
	}

	stats := bp.Stats()
	if stats.DirtyCount != 0 {
		t.Fatalf("expected 0 dirty pages after flush, got %d", stats.DirtyCount)
	}
}

func TestBufferPoolInvalidateTable(t *testing.T) {
	bp := NewBufferPool(4)
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)

	bp.FetchPage(pid1, hf)
	bp.UnpinPage(pid1, false)

	stats := bp.Stats()
	if stats.Used != 1 {
		t.Fatalf("expected 1 page before invalidate, got %d", stats.Used)
	}

	bp.InvalidateTable(pid1.TableID)

	stats = bp.Stats()
	if stats.Used != 0 {
		t.Fatalf("expected 0 pages after invalidate, got %d", stats.Used)
	}
}

// TestBufferPoolEvictionNoDataLoss проверяет, что вытеснение страниц из
// переполненного кэша не теряет записанные данные. Это регрессионный тест на
// баг #5: раньше async flush очищал dirty-флаг без записи на диск, и вытеснение
// такой страницы молча теряло изменения. Теперь кэш read-through и хранит только
// чистые страницы (запись на диск делает вызывающий код), поэтому вытеснение
// безопасно.
func TestBufferPoolEvictionNoDataLoss(t *testing.T) {
	bp := NewBufferPool(2) // маленький кэш, чтобы форсировать вытеснение
	defer bp.Close()
	hf := setupHeapFile(t)

	const nPages = 8
	pids := make([]page.PageID, nPages)
	want := make([]byte, nPages)

	// Записываем на каждую страницу уникальный кортеж и пишем напрямую на диск
	// (как это делает движок), затем UnpinPage(true).
	for i := 0; i < nPages; i++ {
		pid, _, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			t.Fatalf("AllocatePage: %v", err)
		}
		pids[i] = pid

		pg, _, err := bp.FetchPage(pid, hf)
		if err != nil {
			t.Fatalf("FetchPage: %v", err)
		}
		marker := byte('A' + i)
		want[i] = marker
		if _, err := pg.InsertTuple([]byte{marker, marker, marker}); err != nil {
			t.Fatalf("InsertTuple: %v", err)
		}
		if err := hf.WritePage(pid, pg); err != nil {
			t.Fatalf("WritePage: %v", err)
		}
		bp.UnpinPage(pid, true)
	}

	// Кэш переполнен — большинство страниц вытеснено. Перечитываем все страницы и
	// проверяем, что данные на месте (никаких потерянных записей).
	for i := 0; i < nPages; i++ {
		pg, _, err := bp.FetchPage(pids[i], hf)
		if err != nil {
			t.Fatalf("re-FetchPage page %d: %v", i, err)
		}
		tuple := pg.GetTuple(0)
		bp.UnpinPage(pids[i], false)
		if tuple == nil {
			t.Fatalf("page %d: expected tuple, got nil (data lost)", i)
		}
		if tuple[0] != want[i] {
			t.Fatalf("page %d: expected marker %q, got %q (data lost)", i, want[i], tuple[0])
		}
	}

	// Кэш не должен превышать capacity.
	if stats := bp.Stats(); stats.Used > 2 {
		t.Fatalf("expected at most 2 cached pages, got %d", stats.Used)
	}
}

// TestBufferPoolNoGoroutineLeak проверяет, что создание и закрытие buffer pool
// не оставляет фоновых горутин и не паникует (раньше flushLoop текла, т.к.
// Close движком не вызывался).
func TestBufferPoolNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		bp := NewBufferPool(4)
		bp.Close()
		bp.Close() // повторный Close не должен паниковать
	}

	// Даём планировщику шанс завершить любые горутины.
	for i := 0; i < 10; i++ {
		if runtime.NumGoroutine() <= before+2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestBufferPoolInvalidateTableSkipsPinned(t *testing.T) {
	bp := NewBufferPool(4)
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	bp.FetchPage(pid1, hf) // pinned, don't unpin

	bp.InvalidateTable(pid1.TableID)

	stats := bp.Stats()
	if stats.Used != 1 {
		t.Fatalf("expected pinned page to survive invalidate, got %d", stats.Used)
	}
	bp.UnpinPage(pid1, false)
}

// ── Clock-sweep specific tests ──────────────────────────────────────────────

func TestClockSweepUsageCountIncrement(t *testing.T) {
	bp := NewBufferPool(4)
	hf := setupHeapFile(t)

	pid, _, _ := hf.AllocatePage(page.PageTypeHeap)

	// First fetch — usage count should be 1
	bp.FetchPage(pid, hf)
	bp.mu.Lock()
	idx := bp.cache[pid]
	buf := bp.buffers[idx]
	if buf.usageCount != 1 {
		t.Fatalf("expected usageCount=1 after first fetch, got %d", buf.usageCount)
	}
	bp.mu.Unlock()

	// Re-fetch — usage count should increase
	bp.FetchPage(pid, hf)
	bp.mu.Lock()
	buf = bp.buffers[idx]
	if buf.usageCount != 2 {
		t.Fatalf("expected usageCount=2 after re-fetch, got %d", buf.usageCount)
	}
	bp.mu.Unlock()

	// Multiple re-fetches — should cap at maxUsageCount
	for i := 0; i < 10; i++ {
		bp.FetchPage(pid, hf)
	}
	bp.mu.Lock()
	buf = bp.buffers[idx]
	if buf.usageCount != maxUsageCount {
		t.Fatalf("expected usageCount=%d (capped), got %d", maxUsageCount, buf.usageCount)
	}
	bp.mu.Unlock()

	bp.UnpinPage(pid, false)
}

func TestClockSweepSecondChance(t *testing.T) {
	// Fill pool to capacity with 3 slots.
	// Page A at slot 0, B at slot 1, C at slot 2.
	// Give B a usage count so it gets a second chance.
	bp := NewBufferPool(3)
	hf := setupHeapFile(t)

	pidA, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pidB, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pidC, _, _ := hf.AllocatePage(page.PageTypeHeap)

	bp.FetchPage(pidA, hf)
	bp.UnpinPage(pidA, false)

	bp.FetchPage(pidB, hf)
	bp.UnpinPage(pidB, false)

	bp.FetchPage(pidC, hf)
	bp.UnpinPage(pidC, false)

	// Now re-fetch B to boost its usage count
	bp.FetchPage(pidB, hf)
	bp.UnpinPage(pidB, false)

	// Evict one — clock sweep should skip B (usage > 0) and evict something else
	// The clock hand will scan A (usage=1, decrement to 0), then B (usage>1, decrement),
	// then C (usage=1, decrement to 0), then A (usage=0, evict A).
	bp.mu.Lock()
	err := bp.evict()
	bp.mu.Unlock()
	if err != nil {
		t.Fatalf("evict: %v", err)
	}

	// B should still be in cache since it had higher usage
	if _, ok := bp.cache[pidB]; !ok {
		t.Fatal("expected pidB to still be in cache (second chance)")
	}
}

func TestClockSweepPinProtection(t *testing.T) {
	bp := NewBufferPool(3)
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid2, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid3, _, _ := hf.AllocatePage(page.PageTypeHeap)

	bp.FetchPage(pid1, hf) // pin=1, don't unpin
	bp.FetchPage(pid2, hf) // pin=1, don't unpin
	bp.FetchPage(pid3, hf) // pin=1, don't unpin

	// All pinned — evict should fail
	bp.mu.Lock()
	err := bp.evict()
	bp.mu.Unlock()
	if err == nil {
		t.Fatal("expected error when all pages are pinned")
	}

	// Unpin pid2
	bp.UnpinPage(pid2, false)

	// Now evict should succeed — it will find pid2
	bp.mu.Lock()
	err = bp.evict()
	bp.mu.Unlock()
	if err != nil {
		t.Fatalf("evict after unpin: %v", err)
	}

	// pid2 should be evicted
	if _, ok := bp.cache[pid2]; ok {
		t.Fatal("expected pid2 to be evicted")
	}

	// pid1 and pid3 should still be in cache
	if _, ok := bp.cache[pid1]; !ok {
		t.Fatal("expected pid1 to survive (pinned)")
	}
	if _, ok := bp.cache[pid3]; !ok {
		t.Fatal("expected pid3 to survive (pinned)")
	}

	bp.UnpinPage(pid1, false)
	bp.UnpinPage(pid3, false)
}

func TestClockSweepCircularEviction(t *testing.T) {
	// Verify clock hand wraps around the buffer array correctly.
	bp := NewBufferPool(4)
	hf := setupHeapFile(t)

	pids := make([]page.PageID, 6)
	for i := 0; i < 6; i++ {
		pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
		pids[i] = pid
	}

	// Fill 4 slots
	for i := 0; i < 4; i++ {
		bp.FetchPage(pids[i], hf)
		bp.UnpinPage(pids[i], false)
	}

	// Cache is full (4/4). Evict twice to make room for 2 more.
	bp.mu.Lock()
	if err := bp.evict(); err != nil {
		t.Fatalf("evict 1: %v", err)
	}
	if err := bp.evict(); err != nil {
		t.Fatalf("evict 2: %v", err)
	}
	bp.mu.Unlock()

	// Add 2 new pages (should fit in freed slots)
	bp.FetchPage(pids[4], hf)
	bp.UnpinPage(pids[4], false)
	bp.FetchPage(pids[5], hf)
	bp.UnpinPage(pids[5], false)

	stats := bp.Stats()
	if stats.Used != 4 {
		t.Fatalf("expected 4 pages in cache, got %d", stats.Used)
	}
}

func TestClockSweepDirtyEvictionWritesToDisk(t *testing.T) {
	bp := NewBufferPool(2)
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid2, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid3, _, _ := hf.AllocatePage(page.PageTypeHeap)

	// Fetch and dirty pid1
	pg1, _, _ := bp.FetchPage(pid1, hf)
	if _, err := pg1.InsertTuple([]byte{0xAA, 0xBB, 0xCC}); err != nil {
		t.Fatalf("InsertTuple: %v", err)
	}
	bp.UnpinPage(pid1, true) // dirty

	// Fetch pid2
	bp.FetchPage(pid2, hf)
	bp.UnpinPage(pid2, false)

	// Fetch pid3 — should trigger eviction of pid1 (dirty), flushing it to disk
	bp.FetchPage(pid3, hf)
	bp.UnpinPage(pid3, false)

	// Re-read pid1 from disk and verify data survived
	pg1Again, _, err := bp.FetchPage(pid1, hf)
	if err != nil {
		t.Fatalf("re-fetch pid1: %v", err)
	}
	tuple := pg1Again.GetTuple(0)
	bp.UnpinPage(pid1, false)
	if tuple == nil {
		t.Fatal("expected tuple after dirty eviction + re-read, got nil")
	}
	if tuple[0] != 0xAA {
		t.Fatalf("expected 0xAA, got %x", tuple[0])
	}
}

// ── Benchmarks ──────────────────────────────────────────────────────────────

func BenchmarkClockSweepSequentialScan(b *testing.B) {
	benchmarkBufferPoolScan(b, 256, 64)
}

func BenchmarkClockSweepRandomAccess(b *testing.B) {
	benchmarkBufferPoolRandom(b, 256, 64)
}

func benchmarkBufferPoolScan(b *testing.B, numPages, poolSize int) {
	hf := setupBenchHeapFile(b, numPages)
	bp := NewBufferPool(poolSize)
	defer bp.Close()

	pids := make([]page.PageID, numPages)
	for i := 0; i < numPages; i++ {
		pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
		pids[i] = pid
		// Write page to disk so FetchPage reads valid data
		pg := &page.Page{}
		pg.Init(page.PageTypeHeap)
		hf.WritePage(pid, pg)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pid := pids[i%numPages]
		bp.FetchPage(pid, hf)
		bp.UnpinPage(pid, false)
	}
}

func benchmarkBufferPoolRandom(b *testing.B, numPages, poolSize int) {
	hf := setupBenchHeapFile(b, numPages)
	bp := NewBufferPool(poolSize)
	defer bp.Close()

	pids := make([]page.PageID, numPages)
	for i := 0; i < numPages; i++ {
		pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
		pids[i] = pid
		pg := &page.Page{}
		pg.Init(page.PageTypeHeap)
		hf.WritePage(pid, pg)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pid := pids[i%numPages]
		bp.FetchPage(pid, hf)
		bp.UnpinPage(pid, false)
	}
}

func setupBenchHeapFile(b *testing.B, _ int) *heap.HeapFile {
	b.Helper()
	dir := b.TempDir()
	hf, err := heap.CreateHeapFile(dir)
	if err != nil {
		b.Fatalf("CreateHeapFile: %v", err)
	}
	b.Cleanup(func() { hf.Close() })
	return hf
}
