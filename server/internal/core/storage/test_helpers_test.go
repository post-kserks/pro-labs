package storage

import (
	"sync"
	"sync/atomic"
	"testing"

	"vaultdb/internal/core/metrics"
)

type testTxManager struct {
	counter   atomic.Uint64
	committed map[uint64]bool
	mu        sync.Mutex
}

func newTestTxManager() *testTxManager {
	return &testTxManager{committed: make(map[uint64]bool)}
}

func (m *testTxManager) EnsureCounterAtLeast(val uint64) {
	for {
		cur := m.counter.Load()
		if cur >= val {
			break
		}
		if m.counter.CompareAndSwap(cur, val) {
			break
		}
	}
}

func (m *testTxManager) IsCommitted(xid uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.committed[xid] || xid < m.counter.Load()
}

func (m *testTxManager) IsAborted(xid uint64) bool {
	return false
}

func (m *testTxManager) GetSnapshot(txID uint64) map[uint64]bool {
	return nil
}

func (m *testTxManager) OldestActiveXID() uint64 {
	return m.counter.Load()
}

func (m *testTxManager) Begin() interface{} {
	m.counter.Add(1)
	return nil
}

// newTestPageEngine creates a PageStorageEngine for tests.
func newTestPageEngine(t *testing.T) *PageStorageEngine {
	t.Helper()
	dir := t.TempDir()
	txm := newTestTxManager()
	m := metrics.New()
	store, err := NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	_ = m
	return store
}
