package txmanager

import (
	"sync"
)

type LockGranularity int

const (
	TupleGranularity LockGranularity = iota
	PageGranularity
	RelationGranularity
)

type PredicateLock struct {
	TxID        uint64
	Granularity LockGranularity
	DBName      string
	TableName   string
	PageID      uint64
	TupleID     uint16
}

type PredicateLockManager struct {
	mu    sync.RWMutex
	locks []PredicateLock
}

func NewPredicateLockManager() *PredicateLockManager {
	return &PredicateLockManager{
		locks: make([]PredicateLock, 0),
	}
}

func (m *PredicateLockManager) AcquireSIREADLock(txID uint64, lock PredicateLock) {
	m.mu.Lock()
	defer m.mu.Unlock()

	lock.TxID = txID
	m.locks = append(m.locks, lock)
}

func (m *PredicateLockManager) CheckConflict(txID uint64, dbName, tableName string, pageID uint64, tupleID uint16) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, lock := range m.locks {
		// Ignore locks held by the same transaction
		if lock.TxID == txID {
			continue
		}

		if lock.DBName != dbName || lock.TableName != tableName {
			continue
		}

		switch lock.Granularity {
		case RelationGranularity:
			return true
		case PageGranularity:
			if lock.PageID == pageID {
				return true
			}
		case TupleGranularity:
			if lock.PageID == pageID && lock.TupleID == tupleID {
				return true
			}
		}
	}
	return false
}
