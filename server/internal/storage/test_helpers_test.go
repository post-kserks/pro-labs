package storage

import (
	"testing"

	"vaultdb/internal/metrics"
	"vaultdb/internal/txmanager"
)

// newTestPageEngine создаёт PageStorageEngine для тестов.
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
