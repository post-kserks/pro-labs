package wal

import (
	"log/slog"
	"sync"
	"time"
)

// WriteBehindBuffer batches WAL writes by buffering records in memory and
// flushing them to disk periodically or when the buffer reaches capacity.
// This amortizes fsync cost across multiple records for better throughput.
//
// Unlike GroupCommit which expects pre-built WALRecords from the caller,
// WriteBehindBuffer integrates directly with WAL.AppendWithWriteBehind for
// a simpler API: callers pass (xid, opType, payload) and the buffer
// handles serialization and batching.
type WriteBehindBuffer struct {
	wal           *WAL
	buffer        []*WALRecord
	mu            sync.Mutex
	maxBuffer     int
	flushInterval time.Duration
	flushCh       chan struct{}
	done          chan struct{}
	stopped       chan struct{}
	dirty         bool
}

// NewWriteBehindBuffer creates a write-behind buffer that batches WAL writes.
// maxBuffer is the maximum number of records before triggering a flush.
// flushInterval is the maximum time between flushes.
func NewWriteBehindBuffer(wal *WAL, maxBuffer int, flushInterval time.Duration) *WriteBehindBuffer {
	wbb := &WriteBehindBuffer{
		wal:           wal,
		buffer:        make([]*WALRecord, 0, maxBuffer),
		maxBuffer:     maxBuffer,
		flushInterval: flushInterval,
		flushCh:       make(chan struct{}, 1),
		done:          make(chan struct{}),
		stopped:       make(chan struct{}),
	}
	go wbb.flushWorker()
	return wbb
}

// Append queues a pre-built WAL record for batched writing.
// The record will be written to disk and fsynced when the buffer is full
// or the flush interval fires, whichever comes first.
func (wbb *WriteBehindBuffer) Append(rec *WALRecord) error {
	wbb.mu.Lock()
	wbb.buffer = append(wbb.buffer, rec)
	wbb.dirty = true
	shouldFlush := len(wbb.buffer) >= wbb.maxBuffer
	wbb.mu.Unlock()

	if shouldFlush {
		select {
		case wbb.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (wbb *WriteBehindBuffer) flushWorker() {
	defer close(wbb.stopped)
	ticker := time.NewTicker(wbb.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-wbb.flushCh:
			wbb.doFlush()
		case <-ticker.C:
			wbb.doFlush()
		case <-wbb.done:
			wbb.doFlush() // Final flush — ensure durability
			return
		}
	}
}

func (wbb *WriteBehindBuffer) doFlush() {
	wbb.mu.Lock()
	if !wbb.dirty || len(wbb.buffer) == 0 {
		wbb.mu.Unlock()
		return
	}
	batch := wbb.buffer
	wbb.buffer = wbb.buffer[:0]
	wbb.dirty = false
	wbb.mu.Unlock()

	// Write all records under WAL lock, then one fsync.
	wbb.wal.mu.Lock()
	for _, rec := range batch {
		if err := wbb.wal.writeRecordRaw(*rec); err != nil {
			wbb.wal.mu.Unlock()
			slog.Error("wal write-behind: write failed", "txID", rec.TxID, "error", err)
			return
		}
	}
	if err := wbb.wal.file.Sync(); err != nil {
		wbb.wal.mu.Unlock()
		slog.Error("wal write-behind: sync failed", "error", err)
		return
	}
	wbb.wal.mu.Unlock()
}

// Flush forces an immediate flush of all pending records.
func (wbb *WriteBehindBuffer) Flush() {
	wbb.doFlush()
}

// Close signals the flush worker to stop, performs a final flush, and waits
// for the worker goroutine to exit.
func (wbb *WriteBehindBuffer) Close() {
	close(wbb.done)
	<-wbb.stopped
}
