package storage

import (
	"sync"
	"testing"
	"time"

	"vaultdb/internal/core/storage/page"
)

func TestCheckpointerWorkerFlushBatch(t *testing.T) {
	bp := NewBufferPool(10)

	// Create pages
	pg1 := &page.Page{}
	pg2 := &page.Page{}
	pg3 := &page.Page{}
	pg4 := &page.Page{}
	pg5 := &page.Page{}

	pid1 := page.PageID{TableID: 1, PageNo: 1}
	pid2 := page.PageID{TableID: 1, PageNo: 2}
	pid3 := page.PageID{TableID: 1, PageNo: 3}
	pid4 := page.PageID{TableID: 1, PageNo: 4}
	pid5 := page.PageID{TableID: 1, PageNo: 5}

	bp.CachePage(pid1, pg1, nil, "testdb", "t1")
	bp.CachePage(pid2, pg2, nil, "testdb", "t1")
	bp.CachePage(pid3, pg3, nil, "testdb", "t1")
	bp.CachePage(pid4, pg4, nil, "testdb", "t1")
	bp.CachePage(pid5, pg5, nil, "testdb", "t1")

	// Unpin pid1, pid2, pid3 while keeping dirty=true (dirty unpinned)
	bp.UnpinPage(pid1, true)
	bp.UnpinPage(pid2, true)
	bp.UnpinPage(pid3, true)

	// Keep pid4 pinned and dirty (dirty pinned)
	// (CachePage sets pinCnt=1, dirty=true initially)

	// Unpin pid5 and flush/mark clean (clean unpinned)
	bp.UnpinPage(pid5, false)
	_ = bp.FlushPage("testdb", "t1", pid5)

	// Create CheckpointerWorker with batchSize = 2
	worker := NewCheckpointerWorker(bp, 10*time.Second, 2)

	// First batch should flush exactly 2 unpinned dirty pages out of 3
	flushed, err := worker.FlushBatch()
	if err != nil {
		t.Fatalf("FlushBatch error: %v", err)
	}
	if flushed != 2 {
		t.Fatalf("expected 2 flushed pages, got %d", flushed)
	}

	// Verify pid4 is still pinned and dirty
	bp.mu.RLock()
	idx4, ok := bp.cache[pid4]
	if !ok || !bp.buffers[idx4].dirty || bp.buffers[idx4].pinCnt != 1 {
		bp.mu.RUnlock()
		t.Fatalf("pid4 (pinned) was improperly touched or flushed")
	}
	bp.mu.RUnlock()

	// Second batch should flush the remaining 1 unpinned dirty page
	flushed, err = worker.FlushBatch()
	if err != nil {
		t.Fatalf("FlushBatch 2 error: %v", err)
	}
	if flushed != 1 {
		t.Fatalf("expected 1 flushed page, got %d", flushed)
	}

	// Third batch should find 0 unpinned dirty pages (only pid4 remains dirty but pinned)
	flushed, err = worker.FlushBatch()
	if err != nil {
		t.Fatalf("FlushBatch 3 error: %v", err)
	}
	if flushed != 0 {
		t.Fatalf("expected 0 flushed pages, got %d", flushed)
	}
}

func TestCheckpointerWorkerStartStopThreadSafe(t *testing.T) {
	bp := NewBufferPool(10)
	worker := NewCheckpointerWorker(bp, 5*time.Millisecond, 5)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Start()
			time.Sleep(2 * time.Millisecond)
			worker.Stop()
		}()
	}
	wg.Wait()
}
