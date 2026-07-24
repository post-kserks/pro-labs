package storage

import (
	"sync"
)

// PredicateLockManager implements the foundation for Serializable Snapshot Isolation (SSI).
// It tracks read dependencies (SIREAD locks) to detect rw-conflicts.
type PredicateLockManager struct {
	mu sync.Mutex

	// Tracks which transaction read which page/tuple (rw-conflict detection)
	// Map of TxID -> map of (Table+PageNo) -> true
	readDependencies map[uint64]map[string]bool

	// Tracks index ranges read by transactions to prevent phantom reads.
	// Map of TxID -> map of (Table+IndexName+StartKey+EndKey) -> true
	indexRangeDependencies map[uint64]map[string]bool

	// Tracks rw-conflicts (rw-edges in the serialization graph)
	// Map of in-node TxID -> out-node TxID
	rwConflicts map[uint64]map[uint64]bool
}

func NewPredicateLockManager() *PredicateLockManager {
	return &PredicateLockManager{
		readDependencies:       make(map[uint64]map[string]bool),
		indexRangeDependencies: make(map[uint64]map[string]bool),
		rwConflicts:            make(map[uint64]map[uint64]bool),
	}
}

// AcquireIndexRangeSIReadLock is called when a transaction reads an index range.
func (pm *PredicateLockManager) AcquireIndexRangeSIReadLock(txID uint64, db, table, index, startKey, endKey string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, ok := pm.indexRangeDependencies[txID]; !ok {
		pm.indexRangeDependencies[txID] = make(map[string]bool)
	}

	key := db + "/" + table + "/" + index + "/" + startKey + "/" + endKey
	pm.indexRangeDependencies[txID][key] = true
}

// AcquireSIReadLock is called when a transaction reads a tuple or page.
func (pm *PredicateLockManager) AcquireSIReadLock(txID uint64, db, table string, pageNo uint32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	key := db + ":" + table + ":" + string(rune(pageNo))
	if _, ok := pm.readDependencies[txID]; !ok {
		pm.readDependencies[txID] = make(map[string]bool)
	}
	pm.readDependencies[txID][key] = true
}

// CheckAndRecordConflict is called when a transaction WRITES to a page.
// If another active transaction has an SIREAD lock on it, we create an rw-conflict edge.
func (pm *PredicateLockManager) CheckAndRecordConflict(writingTxID uint64, db, table string, pageNo uint32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	key := db + ":" + table + ":" + string(rune(pageNo))
	for readingTx, deps := range pm.readDependencies {
		if readingTx == writingTxID {
			continue
		}
		if deps[key] {
			if _, ok := pm.rwConflicts[readingTx]; !ok {
				pm.rwConflicts[readingTx] = make(map[uint64]bool)
			}
			pm.rwConflicts[readingTx][writingTxID] = true
		}
	}
}

// CheckSerializationFailure returns true if the transaction has dangerous structures
// (like two consecutive rw-edges: T1 -> T2 -> T3).
func (pm *PredicateLockManager) CheckSerializationFailure(txID uint64) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Simplistic check: if txID has both an incoming rw-edge and an outgoing rw-edge
	hasIncoming := false
	for _, edges := range pm.rwConflicts {
		if edges[txID] {
			hasIncoming = true
			break
		}
	}

	hasOutgoing := len(pm.rwConflicts[txID]) > 0

	return hasIncoming && hasOutgoing
}

// ReleaseLocks cleans up when a transaction commits or aborts.
func (pm *PredicateLockManager) ReleaseLocks(txID uint64) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.readDependencies, txID)
	delete(pm.rwConflicts, txID)
	for _, edges := range pm.rwConflicts {
		delete(edges, txID)
	}
}
