package storage

import (
	"fmt"
	"sync"
	"time"

	"vaultdb/internal/core/storage/fsm"
	"vaultdb/internal/core/storage/page"
)

// AutoVacuumConfig contains parameters for automatic vacuuming.
type AutoVacuumConfig struct {
	Interval time.Duration
	MinAge   uint64
}

// AutoVacuumWorker periodically checks and reclaims dead tuples in tables.
type AutoVacuumWorker struct {
	engine  *PageStorageEngine
	config  AutoVacuumConfig
	stopCh  chan struct{}
	mu      sync.Mutex
	running bool
}

// NewAutoVacuumWorker creates a new AutoVacuumWorker with given engine and config.
func NewAutoVacuumWorker(e *PageStorageEngine, cfg AutoVacuumConfig) *AutoVacuumWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = 1 * time.Minute
	}
	return &AutoVacuumWorker{
		engine: e,
		config: cfg,
	}
}

// Start launches the background ticker looping over RunVacuumAll.
func (v *AutoVacuumWorker) Start() {
	v.mu.Lock()
	if v.running {
		v.mu.Unlock()
		return
	}
	v.running = true
	v.stopCh = make(chan struct{})
	stopCh := v.stopCh
	interval := v.config.Interval
	v.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = v.RunVacuumAll()
			}
		}
	}()
}

// Stop terminates the background worker cleanly and thread-safely.
func (v *AutoVacuumWorker) Stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.running {
		return
	}
	v.running = false
	if v.stopCh != nil {
		close(v.stopCh)
	}
}

// RunVacuumAll runs vacuum across all databases and tables.
func (v *AutoVacuumWorker) RunVacuumAll() (int, error) {
	if v.engine == nil {
		return 0, nil
	}
	dbs, err := v.engine.ListDatabases()
	if err != nil {
		return 0, err
	}
	currTx := v.engine.CurrentTxID()
	var minActiveTxID uint64
	if currTx > v.config.MinAge {
		minActiveTxID = currTx - v.config.MinAge
	}
	totalFreed := 0
	for _, db := range dbs {
		tables, err := v.engine.ListTables(db)
		if err != nil {
			continue
		}
		for _, t := range tables {
			freed, err := v.RunVacuumOnce(db, t.Name, minActiveTxID)
			if err != nil {
				continue
			}
			totalFreed += freed
		}
	}
	return totalFreed, nil
}

// RunVacuumOnce scans pages/tuples for dead slots and marks them free.
func (v *AutoVacuumWorker) RunVacuumOnce(dbName, tableName string, minActiveTxID uint64) (freedTuples int, err error) {
	if v.engine == nil {
		return 0, fmt.Errorf("vacuum worker: engine is nil")
	}

	v.engine.mu.Lock()
	t, err := v.engine.getTableLocked(dbName, tableName, true)
	v.engine.mu.Unlock()
	if err != nil {
		return 0, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	err = v.engine.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx != 0 && deletedTx < minActiveTxID {
			tuple := pg.GetTuple(slot)
			if len(tuple) >= 16 {
				for i := 0; i < 16; i++ {
					tuple[i] = 0
				}
			}
			pg.MarkDead(slot)

			type fsmHolder interface {
				GetFSM() *fsm.FSM
			}
			if fh, ok := interface{}(t).(fsmHolder); ok && fh.GetFSM() != nil {
				fh.GetFSM().Update(pid.PageNo, pg.Header().FreeSpace)
			}

			v.engine.unpinPage(pid, true)
			freedTuples++
		}
		return false, nil
	})
	if err != nil {
		return freedTuples, err
	}
	return freedTuples, nil
}
