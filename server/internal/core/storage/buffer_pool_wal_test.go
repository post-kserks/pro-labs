package storage

import (
	"path/filepath"
	"testing"

	"vaultdb/internal/core/storage/heap"
	"vaultdb/internal/core/storage/page"
	"vaultdb/internal/core/wal"
)

// brokenWAL returns a WAL whose Flush always fails (closed file).
func brokenWAL(t *testing.T) *wal.WAL {
	t.Helper()
	w, err := wal.Open(filepath.Join(t.TempDir(), "broken.wal"))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Flush(); err == nil {
		t.Fatal("expected Flush on closed WAL to fail")
	}
	return w
}

// dirtyPage caches a modified page in bp and returns its id plus the on-disk
// image before modification.
func dirtyPage(t *testing.T, bp *BufferPool, hf *heap.HeapFile) (page.PageID, *page.Page) {
	t.Helper()
	pid, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	before := new(page.Page)
	if err := hf.ReadPage(pid, before); err != nil {
		t.Fatal(err)
	}
	pg, _, err := bp.FetchPage(pid, hf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.InsertTuple([]byte("must not reach disk before its WAL record")); err != nil {
		t.Fatal(err)
	}
	bp.UnpinPageDirty(pid, 1)
	return pid, before
}

func assertPageUnchanged(t *testing.T, hf *heap.HeapFile, pid page.PageID, before *page.Page) {
	t.Helper()
	onDisk := new(page.Page)
	if err := hf.ReadPage(pid, onDisk); err != nil {
		t.Fatal(err)
	}
	if *onDisk != *before {
		t.Fatal("dirty page was written to disk although the WAL flush failed (WAL-before-data violated)")
	}
}

func TestBufferPool_WALFlushErrorPreventsPageWrite(t *testing.T) {
	cases := []struct {
		name  string
		flush func(bp *BufferPool, pid page.PageID) error
	}{
		{"FlushAll", func(bp *BufferPool, _ page.PageID) error { return bp.FlushAll() }},
		{"FlushDirtyPagesUpToLSN", func(bp *BufferPool, _ page.PageID) error { return bp.FlushDirtyPagesUpToLSN(10) }},
		{"FlushPage", func(bp *BufferPool, pid page.PageID) error { return bp.FlushPage("db", "t", pid) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bp := NewBufferPool(4)
			hf := setupHeapFile(t)
			pid, before := dirtyPage(t, bp, hf)
			bp.SetWAL(brokenWAL(t))

			if err := tc.flush(bp, pid); err == nil {
				t.Fatal("expected flush to fail when WAL flush fails")
			}
			assertPageUnchanged(t, hf, pid, before)
		})
	}
}

func TestBufferPool_EvictWALFlushErrorPreventsPageWrite(t *testing.T) {
	bp := NewBufferPool(1)
	hf := setupHeapFile(t)
	pid, before := dirtyPage(t, bp, hf)
	bp.SetWAL(brokenWAL(t))

	pid2, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bp.FetchPage(pid2, hf); err == nil {
		t.Fatal("expected FetchPage to fail: evicting the dirty page requires a WAL flush")
	}
	assertPageUnchanged(t, hf, pid, before)
}

// With a failing WAL, clean pages are still evictable; the dirty page stays
// cached and unwritten.
func TestBufferPool_EvictSkipsDirtyPagesWhenWALFlushFails(t *testing.T) {
	bp := NewBufferPool(2)
	hf := setupHeapFile(t)
	pid, before := dirtyPage(t, bp, hf)

	clean, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bp.FetchPage(clean, hf); err != nil {
		t.Fatal(err)
	}
	bp.UnpinPage(clean, false)
	bp.SetWAL(brokenWAL(t))

	third, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bp.FetchPage(third, hf); err != nil {
		t.Fatalf("expected clean page to be evicted: %v", err)
	}
	assertPageUnchanged(t, hf, pid, before)
	if _, ok := bp.cache[pid]; !ok {
		t.Fatal("dirty page must remain cached when it cannot be written")
	}
}
