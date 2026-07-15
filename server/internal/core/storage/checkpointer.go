package storage

import (
	"sync"
	"time"
)

// CheckpointerWorker periodically flushes batches of dirty unpinned pages to disk.
type CheckpointerWorker struct {
	pool      *BufferPool
	interval  time.Duration
	batchSize int
	stopCh    chan struct{}
	mu        sync.Mutex
	running   bool
}

// NewCheckpointerWorker creates a new CheckpointerWorker with given buffer pool, interval and batch size.
func NewCheckpointerWorker(pool *BufferPool, interval time.Duration, batchSize int) *CheckpointerWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 64
	}
	return &CheckpointerWorker{
		pool:      pool,
		interval:  interval,
		batchSize: batchSize,
	}
}

// Start launches the background checkpointer goroutine.
func (c *CheckpointerWorker) Start() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.stopCh = make(chan struct{})
	stopCh := c.stopCh
	interval := c.interval
	c.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = c.FlushBatch()
			}
		}
	}()
}

// Stop cleanly terminates the checkpointer background loop.
func (c *CheckpointerWorker) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	if c.stopCh != nil {
		close(c.stopCh)
	}
}

// FlushBatch flushes up to batchSize dirty unpinned pages from the buffer pool cleanly.
func (c *CheckpointerWorker) FlushBatch() (flushedCount int, err error) {
	c.mu.Lock()
	pool := c.pool
	batchSize := c.batchSize
	c.mu.Unlock()

	if pool == nil || batchSize <= 0 {
		return 0, nil
	}

	keys := pool.CollectDirtyPages(batchSize)
	for _, key := range keys {
		err := pool.FlushPage(key.DB, key.Table, key.PageID)
		if err == ErrPagePinned || err == ErrPageNotDirty {
			continue
		}
		if err != nil {
			return flushedCount, err
		}
		flushedCount++
	}
	return flushedCount, nil
}
