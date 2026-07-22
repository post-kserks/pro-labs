package txmanager

import "sync"

type SSITracker struct {
	mu          sync.RWMutex
	InConflict  map[uint64]bool
	OutConflict map[uint64]bool
}

func NewSSITracker() *SSITracker {
	return &SSITracker{
		InConflict:  make(map[uint64]bool),
		OutConflict: make(map[uint64]bool),
	}
}

func (t *SSITracker) RecordRWConflict(readerTxID, writerTxID uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.OutConflict[readerTxID] = true
	t.InConflict[writerTxID] = true
}

func (t *SSITracker) HasCycle(txID uint64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.InConflict[txID] && t.OutConflict[txID]
}
