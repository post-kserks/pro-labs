package storage

import (
	"testing"

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
