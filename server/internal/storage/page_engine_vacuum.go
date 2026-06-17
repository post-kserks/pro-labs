package storage

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

func (e *PageStorageEngine) Vacuum(dbName, tableName string) (*VacuumStats, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()
	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return nil, err
	}

	sizeBefore := e.tableSizeLocked(dbName, tableName)

	// Записываем начало vacuum в WAL
	if e.wal != nil {
		vacuumPayload := wal.WALVacuumPayload{
			DB:    dbName,
			Table: tableName,
		}
		if _, err := e.wal.Append(wal.OpVacuumBegin, vacuumPayload); err != nil {
			return nil, fmt.Errorf("vacuum: wal begin: %w", err)
		}
	}

	// Создаём shadow file для новой версии таблицы
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
			if binary.LittleEndian.Uint64(tuple[8:16]) == 0 {
				live = append(live, append([]byte(nil), tuple...))
				rowsAfter++
			}
		}
		// Пересобираем страницу только из живых версий
		pg.Init(page.PageTypeHeap)
		for _, tuple := range live {
			if _, err := pg.InsertTuple(tuple); err != nil {
				os.Remove(shadowPath)
				return nil, err
			}
		}
		// Пишем в shadow file
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

	// Записываем завершение vacuum в WAL (перед заменой файлов)
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

	// Атомарная замена: переименовываем shadow в оригинальный путь
	// На Linux os.Rename атомарно заменяет директорию
	originalPath := e.tablePath(dbName, tableName)
	if err := t.heap.Close(); err != nil {
		os.RemoveAll(shadowPath)
		return nil, err
	}

	// Используем rename с заменой (атомарно на Linux)
	if err := os.Rename(shadowPath, originalPath); err != nil {
		os.RemoveAll(shadowPath)
		return nil, err
	}

	// Открываем новый heap file
	newHF, err := heap.OpenHeapFile(originalPath)
	if err != nil {
		return nil, err
	}
	t.heap = newHF

	// Обновляем каталог
	key := dbName + "/" + tableName
	e.catalog.RowCounts[key] = rowsAfter
	if err := e.saveCatalogLocked(); err != nil {
		return nil, err
	}

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

// FinalCheckpoint сбрасывает все dirty pages на диск.
func (e *PageStorageEngine) FinalCheckpoint() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tables {
		if err := e.bufPool.FlushAll(t.heap); err != nil {
			return err
		}
		if err := t.heap.Sync(); err != nil {
			return err
		}
	}
	return nil
}

// Close закрывает движок и все ресурсы.
func (e *PageStorageEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, t := range e.tables {
		_ = e.bufPool.FlushAll(t.heap)
		_ = t.heap.Sync()
		_ = t.heap.Close()
	}
	if e.wal != nil {
		return e.wal.Close()
	}
	return nil
}
