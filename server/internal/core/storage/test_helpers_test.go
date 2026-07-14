package storage

import (
	"testing"

	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/txmanager"
)

// newTestPageEngine creates a PageStorageEngine for tests.
func newTestPageEngine(t *testing.T) *PageStorageEngine {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	m := metrics.New()
	store, err := NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	_ = m
	return store
}
