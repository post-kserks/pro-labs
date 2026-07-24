package storage

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vaultdb/internal/core/storage/heap"
	"vaultdb/internal/core/storage/page"
	"vaultdb/internal/core/wal"
)

func (e *PageStorageEngine) Vacuum(dbName, tableName string) (*VacuumStats, error) {
	// Brief global lock to get a reference to the table.
	e.mu.Lock()
	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	sizeBefore := e.tableSizeLocked(dbName, tableName)
	e.mu.Unlock()

	// Per-table lock for the duration of vacuum — does not block other tables.
	t.mu.Lock()
	defer t.mu.Unlock()
	t.invalidatePosDirectory()

	start := time.Now()

	// Write start vacuum to WAL
	if e.wal != nil {
		vacuumPayload := wal.WALVacuumPayload{
			DB:    dbName,
			Table: tableName,
		}
		if _, err := e.wal.Append(wal.OpVacuumBegin, vacuumPayload); err != nil {
			return nil, fmt.Errorf("vacuum: wal begin: %w", err)
		}
	}

	// Create shadow file for the new table version
	shadowPath := e.tablePath(dbName, tableName) + ".vacuum"
	shadowHF, err := heap.CreateHeapFile(shadowPath)
	if err != nil {
		return nil, fmt.Errorf("vacuum: create shadow file: %w", err)
	}
	defer shadowHF.Close()

	total, err := t.heap.PageCount()
	if err != nil {
		os.Remove(shadowPath)
		return nil, err
	}

	rowsBefore, rowsAfter := 0, 0
	// Flush all dirty pages to disk before reading directly from heap files.
	// Without this, write-back cached pages would be invisible to the scan.
	if err := e.bufPool.FlushAll(); err != nil {
		os.Remove(shadowPath)
		return nil, fmt.Errorf("vacuum: flush dirty pages: %w", err)
	}
	for g := uint32(0); g < total; g++ {
		pid := pageIDAt(t.tableID, g)
		var pg page.Page
		if err := t.heap.ReadPage(pid, &pg); err != nil {
			os.Remove(shadowPath)
			return nil, err
		}
		h := pg.Header()
		var live [][]byte
		for slot := uint16(0); slot < h.NItems; slot++ {
			tuple := pg.GetTuple(slot)
			if tuple == nil {
				continue
			}
			rowsBefore++
			createdTx := binary.LittleEndian.Uint64(tuple[0:8])
			deletedTx := binary.LittleEndian.Uint64(tuple[8:16])

			isDead := false
			if deletedTx != 0 {
				if e.txMgr != nil {
					if e.txMgr.IsCommitted(deletedTx) && deletedTx < e.txMgr.OldestActiveXID() {
						isDead = true
					}
				} else {
					isDead = true
				}
			} else if createdTx != 0 && e.txMgr != nil {
				if e.txMgr.IsAborted(createdTx) {
					isDead = true
				}
			}

			if !isDead {
				live = append(live, append([]byte(nil), tuple...))
				rowsAfter++
			}
		}
		// Rebuild page from live versions only
		pg.Init(page.PageTypeHeap)
		for _, tuple := range live {
			if _, err := pg.InsertTuple(tuple); err != nil {
				os.Remove(shadowPath)
				return nil, err
			}
		}
		// Write to shadow file
		if err := shadowHF.WritePage(pid, &pg); err != nil {
			os.Remove(shadowPath)
			return nil, err
		}
	}
	if err := shadowHF.Sync(); err != nil {
		os.Remove(shadowPath)
		return nil, err
	}
	shadowHF.Close()

	// Write completion vacuum to WAL (before file replacement)
	if e.wal != nil {
		vacuumPayload := wal.WALVacuumPayload{
			DB:    dbName,
			Table: tableName,
		}
		if _, err := e.wal.Append(wal.OpVacuumCommit, vacuumPayload); err != nil {
			os.Remove(shadowPath)
			return nil, fmt.Errorf("vacuum: wal commit: %w", err)
		}
	}

	// Atomic replacement: remove the old directory and rename shadow.
	// On Linux, os.Rename for directories requires the target to not exist
	// or be empty. Since originalPath is a non-empty directory with segments,
	// we remove it first, then rename.
	originalPath := e.tablePath(dbName, tableName)
	if err := t.heap.Close(); err != nil {
		os.RemoveAll(shadowPath)
		return nil, err
	}

	if err := os.RemoveAll(originalPath); err != nil {
		os.RemoveAll(shadowPath)
		return nil, err
	}
	if err := os.Rename(shadowPath, originalPath); err != nil {
		os.RemoveAll(shadowPath)
		return nil, err
	}

	// Open new heap file
	newHF, err := heap.OpenHeapFile(originalPath)
	if err != nil {
		return nil, err
	}
	t.heap = newHF
	e.bufPool.InvalidateTable(t.tableID)

	// Update catalog (brief global lock).
	e.mu.Lock()
	key := dbName + "/" + tableName
	e.catalog.RowCounts[key] = rowsAfter
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()

	return &VacuumStats{
		TableName:      tableName,
		RowsBefore:     rowsBefore,
		RowsAfter:      rowsAfter,
		ReclaimedRows:  rowsBefore - rowsAfter,
		FileSizeBefore: sizeBefore,
		FileSizeAfter:  e.tableSizeLocked(dbName, tableName),
		DurationMs:     float64(time.Since(start).Microseconds()) / 1000.0,
	}, nil
}

// recoverOrphanedVacuums scans all database directories for leftover .vacuum
// shadow directories created during incomplete vacuum operations. If a crash
// occurs after the shadow file is created but before the atomic rename, the
// orphaned .vacuum directory is left behind. Since the original table file
// is still intact (rename hasn't happened yet), we simply remove the orphan.
func (e *PageStorageEngine) recoverOrphanedVacuums() {
	dbs, err := os.ReadDir(e.rootDir)
	if err != nil {
		return
	}
	for _, dbEntry := range dbs {
		if !dbEntry.IsDir() {
			continue
		}
		dbDir := filepath.Join(e.rootDir, dbEntry.Name())
		entries, err := os.ReadDir(dbDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasSuffix(entry.Name(), ".vacuum") {
				vacuumPath := filepath.Join(dbDir, entry.Name())
				slog.Warn("recovering orphaned vacuum directory",
					"path", vacuumPath)
				os.RemoveAll(vacuumPath)
			}
		}
	}
}

func (e *PageStorageEngine) tableSizeLocked(db, table string) int64 {
	var size int64
	entries, err := os.ReadDir(e.tablePath(db, table))
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil {
			size += info.Size()
		}
	}
	return size
}

func (e *PageStorageEngine) TableVersionStats(dbName, tableName string) (*TableVersionStats, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		return nil, err
	}

	stats := &TableVersionStats{}
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, _, deletedTx uint64, _ Row) (bool, error) {
		stats.TotalRows++
		if deletedTx != 0 {
			stats.DeadRows++
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func (e *PageStorageEngine) TableModifiedSince(db, table string, txID uint64) (bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.catalog.LastModified[db+"/"+table] > txID, nil
}

func (e *PageStorageEngine) CurrentTxID() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.catalog.CurrentTxID
}

// SchemaVersion returns a version number that changes when any table schema is modified.
func (e *PageStorageEngine) SchemaVersion() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var ver uint64
	for _, txID := range e.catalog.LastModified {
		ver += txID
	}
	return ver
}

// FinalCheckpoint flushes all dirty pages to disk.
func (e *PageStorageEngine) FinalCheckpoint() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.bufPool.FlushAll(); err != nil {
		return err
	}
	for _, t := range e.tables {
		if err := t.heap.Sync(); err != nil {
			return err
		}
	}
	return nil
}

// StartBackgroundFlush starts a background goroutine for periodic flush of dirty pages.
func (e *PageStorageEngine) StartBackgroundFlush(ctx context.Context, interval time.Duration) {
	e.bufPool.StartBackgroundFlush(ctx, interval)
}

// Close shuts down the engine and all resources.
func (e *PageStorageEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Flush any pending catalog changes before closing.
	if e.catalogDirty {
		if err := e.saveCatalogLocked(); err != nil {
			slog.Error("catalog flush on close failed", "error", err)
		}
		e.catalogDirty = false
	}

	e.bufPool.Close()
	_ = e.bufPool.FlushAll()
	for _, t := range e.tables {
		_ = t.heap.Sync()
		_ = t.heap.Close()
	}
	if e.wal != nil {
		return e.wal.Close()
	}
	return nil
}
