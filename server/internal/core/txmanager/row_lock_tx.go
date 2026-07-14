package txmanager

import (
	"context"
)

// RowLockMode definitions matching storage layer constants without import cycles.
const (
	LockShared    int = 1
	LockExclusive int = 2
)

// RowLocker interface defines the methods required by TxManager to interact with storage.RowLockManager.
type RowLocker interface {
	LockRowInt(ctx context.Context, db, table, rowKey string, txID uint64, mode int) error
	UnlockTx(txID uint64)
}

// AcquireRowLock attempts to acquire a pessimistic row lock for the transaction via the registered RowLockManager or RowLocker.
func (m *Manager) AcquireRowLock(ctx context.Context, txID uint64, db, table, rowKey string, mode int) error {
	if m.RowLocks != nil {
		return m.RowLocks.LockRowInt(ctx, db, table, rowKey, txID, mode)
	}
	m.commitLocksMu.Lock()
	rl := m.rowLocker
	m.commitLocksMu.Unlock()
	if rl != nil {
		return rl.LockRowInt(ctx, db, table, rowKey, txID, mode)
	}
	return nil
}

// AcquireRowLockTx helper delegates directly to AcquireRowLock using tx.ID.
func (m *Manager) AcquireRowLockTx(ctx context.Context, tx *Transaction, db, table, rowKey string, mode int) error {
	if tx == nil {
		return nil
	}
	return m.AcquireRowLock(ctx, tx.ID, db, table, rowKey, mode)
}

// ReleaseRowLocks releases all row locks held by the given transaction ID.
func (m *Manager) ReleaseRowLocks(txID uint64) {
	if m.RowLocks != nil {
		m.RowLocks.UnlockTx(txID)
	}
	m.commitLocksMu.Lock()
	rl := m.rowLocker
	m.commitLocksMu.Unlock()
	if rl != nil {
		rl.UnlockTx(txID)
	}
}

// SetRowLocker registers a RowLocker with this TxManager instance.
func (m *Manager) SetRowLocker(rl RowLocker) {
	m.commitLocksMu.Lock()
	defer m.commitLocksMu.Unlock()
	m.rowLocker = rl
}

// GetRowLocker returns the registered RowLocker.
func (m *Manager) GetRowLocker() RowLocker {
	m.commitLocksMu.Lock()
	defer m.commitLocksMu.Unlock()
	return m.rowLocker
}
