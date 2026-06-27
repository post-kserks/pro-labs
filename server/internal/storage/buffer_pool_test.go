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

	err = bp.FlushAll(hf)
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
