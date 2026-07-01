package storage

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

// retryOnError retries fn up to 3 times with exponential backoff (10ms, 20ms, 40ms).
// Returns the last error if all retries fail.
func retryOnError(fn func() error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		time.Sleep(time.Duration(1<<uint(attempt)*10) * time.Millisecond)
	}
	return err
}

// ── Сканирование ──────────────────────────────────────────────────────────

// pageIDAt переводит сквозной номер страницы в PageID (сегмент + страница).
// tableID уникально идентифицирует таблицу в buffer pool.
func pageIDAt(tableID uint32, global uint32) page.PageID {
	return page.PageID{
		TableID:   tableID,
		SegmentNo: uint16(global / page.PagesPerSegment),
		PageNo:    global % page.PagesPerSegment,
	}
}

// tableIDFromPath вычисляет уникальный ID таблицы из пути.
func tableIDFromPath(path string) uint32 {
	h := uint32(2166136261) // FNV-1a offset basis
	for i := 0; i < len(path); i++ {
		h ^= uint32(path[i])
		h *= 16777619 // FNV-1a prime
	}
	if h == 0 {
		h = 1 // избегаем нулевого ID
	}
	return h
}

type tupleVisitor func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (stop bool, err error)

// scanTuples обходит все кортежи таблицы в порядке страниц/слотов.
func (e *PageStorageEngine) scanTuples(t *pageTable, visit tupleVisitor) error {
	total, err := t.heap.PageCount()
	if err != nil {
		return err
	}
	for g := uint32(0); g < total; g++ {
		pid := pageIDAt(t.tableID, g)
		pg, err := e.getPage(pid, t.heap, t.schema.Database, t.schema.Name)
		if err != nil {
			return err
		}
		h := pg.Header()
		for slot := uint16(0); slot < h.NItems; slot++ {
			tuple := pg.GetTuple(slot)
			if tuple == nil {
				continue
			}
			createdTx, deletedTx, row, err := decodePageTuple(tuple, t.schema)
			if err != nil {
				e.unpinPage(pid, false)
				return err
			}
			stop, err := visit(pid, pg, slot, createdTx, deletedTx, row)
			if err != nil {
				e.unpinPage(pid, false)
				return err
			}
			if stop {
				e.unpinPage(pid, false)
				return nil
			}
		}
		e.unpinPage(pid, false)
	}
	return nil
}

// ── Запись ────────────────────────────────────────────────────────────────

// flushDirty сбрасывает грязную страницу на диск через heap файл.

// appendTuplesLocked добавляет кортежи в конец таблицы; вызывается под write-локом.
func (e *PageStorageEngine) appendTuplesLocked(t *pageTable, tuples [][]byte) error {
	total, err := t.heap.PageCount()
	if err != nil {
		return err
	}

	var pid page.PageID
	var pg *page.Page
	havePage := false
	if total > 0 {
		pid = pageIDAt(t.tableID, total-1)
		pg, err = e.getPage(pid, t.heap, t.schema.Database, t.schema.Name)
		if err != nil {
			return err
		}
		havePage = true
	}

	flush := func() error {
		if havePage {
			e.bufPool.UnpinPage(pid, true)
			havePage = false
		}
		return nil
	}

	for _, tuple := range tuples {
		for {
			if !havePage {
				newPid, newPg, err := t.heap.AllocatePage(page.PageTypeHeap)
				if err != nil {
					return err
				}
				newPid.TableID = t.tableID
				e.bufPool.CachePage(newPid, newPg, t.heap, t.schema.Database, t.schema.Name)
				pid, pg, havePage = newPid, newPg, true
			}
			if _, err := pg.InsertTuple(tuple); err == nil {
				break
			}
			// Страница полна — сбрасываем её и выделяем новую
			if err := flush(); err != nil {
				return err
			}
			havePage = false
		}
	}
	return flush()
}

func (e *PageStorageEngine) InsertRows(dbName, tableName string, rows []Row) (int, error) {
	// Получаем txID под e.mu (быстро)
	e.mu.Lock()
	txID := e.nextTxLocked()
	e.mu.Unlock()

	// Получаем ссылку на таблицу (освобождает e.mu)
	t, err := e.getTableForWrite(dbName, tableName)
	if err != nil {
		return 0, err
	}
	insertLockReleased := false
	defer func() {
		if !insertLockReleased {
			insertLockReleased = true
			t.mu.Unlock()
		}
	}()

	// Find PRIMARY KEY column index for constraint checking
	pkIdx := -1
	for i, col := range t.schema.Columns {
		if col.PrimaryKey {
			pkIdx = i
			break
		}
	}

	// Build set of existing PRIMARY KEY values for fast lookup
	existingPKs := make(map[interface{}]bool)
	if pkIdx >= 0 {
		_ = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
			if deletedTx == 0 && pkIdx < len(row) {
				existingPKs[row[pkIdx]] = true
			}
			return false, nil
		})
	}

	tuples := make([][]byte, 0, len(rows))
	for _, row := range rows {
		normalized := make(Row, len(t.schema.Columns))
		for i := range t.schema.Columns {
			var val Value
			if i < len(row) {
				val = row[i]
			}
			n, err := normalizeValue(val, t.schema.Columns[i])
			if err != nil {
				return 0, fmt.Errorf("column '%s': %w", t.schema.Columns[i].Name, err)
			}
			normalized[i] = n
		}

		// Check PRIMARY KEY constraint
		if pkIdx >= 0 && pkIdx < len(normalized) {
			pkVal := normalized[pkIdx]
			if existingPKs[pkVal] {
				return 0, fmt.Errorf("duplicate primary key value: %v", pkVal)
			}
			existingPKs[pkVal] = true
		}

		tuple, err := encodePageTuple(txID, 0, normalized)
		if err != nil {
			return 0, err
		}
		tuples = append(tuples, tuple)
	}

	insertedTuples := make([]struct {
		pid  page.PageID
		slot uint16
	}, 0, len(tuples))

	// Cache page count — avoid syscall per tuple in batch inserts.
	cachedPageCount, err := t.heap.PageCount()
	if err != nil {
		return 0, err
	}

	for _, tuple := range tuples {
		var pid page.PageID
		var pg *page.Page
		havePage := false

		if cachedPageCount > 0 {
			pid = pageIDAt(t.tableID, cachedPageCount-1)
			e.pageLock.RLockPage(pid)
		pg, err = e.getPage(pid, t.heap, t.schema.Database, t.schema.Name)
			e.pageLock.UnlockPage(pid)
			if err != nil {
				return 0, err
			}
			havePage = true
		}

		for {
			if !havePage {
				newPid, newPg, err := t.heap.AllocatePage(page.PageTypeHeap)
				if err != nil {
					return 0, err
				}
				newPid.TableID = t.tableID
				e.bufPool.CachePage(newPid, newPg, t.heap, t.schema.Database, t.schema.Name)
				pid, pg, havePage = newPid, newPg, true
				cachedPageCount++
			}

			e.pageLock.LockPage(pid)
			slot, err := pg.InsertTuple(tuple)
			if err == nil {
				var lsn uint64
				if e.wal != nil {
					payload := wal.WALPageInsertPayload{
						DB:        dbName,
						Table:     tableName,
						SegmentNo: pid.SegmentNo,
						PageNo:    pid.PageNo,
						SlotNo:    slot,
						XID:       txID,
						TupleData: tuple,
					}
					lsn, err = e.wal.AppendWithTx(txID, wal.OpPageInsert, payload)
					if err != nil {
						e.pageLock.UnlockPageWrite(pid)
						return 0, fmt.Errorf("wal insert: %w", err)
					}
				}

				insertedTuples = append(insertedTuples, struct {
					pid  page.PageID
					slot uint16
				}{pid, slot})

				e.bufPool.UnpinPageDirty(pid, lsn)
				e.pageLock.UnlockPageWrite(pid)
				break
			}
			e.pageLock.UnlockPageWrite(pid)

			if havePage {
				e.bufPool.UnpinPage(pid, false)
			}
			if err := t.heap.Sync(); err != nil {
				return 0, err
			}
			havePage = false
		}
	}

	insertLockReleased = true
	t.mu.Unlock()

	if err := t.heap.Sync(); err != nil {
		return 0, err
	}

	key := dbName + "/" + tableName
	e.mu.Lock()
	e.catalog.LastModified[key] = txID
	e.catalog.RowCounts[key] += len(rows)
	startPos := e.catalog.RowCounts[key] - len(rows)
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return 0, err
	}
	e.mu.Unlock()

	// WAL: mark transaction committed so recovery replays rather than rolls back
	if e.wal != nil {
		if _, err := e.wal.AppendWithTx(txID, wal.OpCommit, nil); err != nil {
			return 0, fmt.Errorf("wal commit: %w", err)
		}
	}

	e.updateIndexesOnInsert(dbName, tableName, rows, startPos)

	return len(rows), nil
}

// mutateRows помечает версии удалёнными и (для UPDATE) добавляет новые версии.
func (e *PageStorageEngine) mutateRows(dbName, tableName string, indices []int, updates map[string]Value, isDelete bool) (int, error) {
	// Получаем txID под e.mu
	e.mu.Lock()
	txID := e.nextTxLocked()
	e.mu.Unlock()

	t, err := e.getTableForWrite(dbName, tableName)
	if err != nil {
		return 0, err
	}
	mutateLockReleased := false
	defer func() {
		if !mutateLockReleased {
			mutateLockReleased = true
			t.mu.Unlock()
		}
	}()

	wanted := make(map[int]bool, len(indices))
	for _, i := range indices {
		wanted[i] = true
	}

	colIndex := make(map[string]int, len(t.schema.Columns))
	for i, col := range t.schema.Columns {
		colIndex[strings.ToLower(col.Name)] = i
	}

	var newVersions [][]byte
	affected := 0
	pos := 0

	var dirtyPid page.PageID
	dirty := false
	flushDirty := func() error {
		if dirty {
			e.bufPool.UnpinPage(dirtyPid, true)
			dirty = false
		}
		return nil
	}

	// WAL: записываем каждую операцию ПЕРЕД изменением heap
	// Важно: SlotNo должен быть физическим слотом, а не логической позицией
	if e.wal != nil {
		// Сначала сканируем чтобы найти физические слоты
		var physicalSlots []struct {
			pid  page.PageID
			slot uint16
		}
		e.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
			if deletedTx != 0 {
				return false, nil
			}
			matched := wanted[pos]
			pos++
			if matched {
				physicalSlots = append(physicalSlots, struct {
					pid  page.PageID
					slot uint16
				}{pid, slot})
			}
			return false, nil
		})

		// Записываем WAL с реальными физическими слотами
		for _, ps := range physicalSlots {
			payload := wal.WALPageDeletePayload{
				DB:        dbName,
				Table:     tableName,
				SegmentNo: ps.pid.SegmentNo,
				PageNo:    ps.pid.PageNo,
				SlotNo:    ps.slot,
				XMax:      txID,
			}
			if _, err := e.wal.AppendWithTx(txID, wal.OpPageDelete, payload); err != nil {
				return 0, fmt.Errorf("wal delete: %w", err)
			}
		}
		pos = 0 // Сбрасываем позицию для следующего сканирования
	}

	err = e.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx != 0 {
			return false, nil
		}
		matched := wanted[pos]
		pos++
		if !matched {
			return false, nil
		}

		// Сбрасываем предыдущую грязную страницу, если перешли на новую
		if dirty && dirtyPid != pid {
			if err := flushDirty(); err != nil {
				return true, err
			}
		}

		// Помечаем текущую версию удалённой: in-place запись deleted_tx
		tuple := pg.GetTuple(slot)
		binary.LittleEndian.PutUint64(tuple[8:16], txID)
		dirtyPid, dirty = pid, true

		if !isDelete {
			newRow := append(Row(nil), row...)
			for name, val := range updates {
				idx, ok := colIndex[strings.ToLower(name)]
				if !ok {
					return true, fmt.Errorf("column '%s' does not exist", name)
				}
				n, err := normalizeValue(val, t.schema.Columns[idx])
				if err != nil {
					return true, fmt.Errorf("column '%s': %w", name, err)
				}
				newRow[idx] = n
			}
			encoded, err := encodePageTuple(txID, 0, newRow)
			if err != nil {
				return true, err
			}
			newVersions = append(newVersions, encoded)
		}
		affected++
		return false, nil
	})
	if err != nil {
		return 0, err
	}
	if err := flushDirty(); err != nil {
		return 0, err
	}

	if len(newVersions) > 0 {
		if err := e.appendTuplesLocked(t, newVersions); err != nil {
			return 0, err
		}
	}
	if err := t.heap.Sync(); err != nil {
		return 0, err
	}

	// Обновляем индексы до освобождения t.mu (они не требуют e.mu)
	if isDelete && affected > 0 {
		e.updateIndexesOnDelete(dbName, tableName, indices)
	}

	// Освобождаем t.mu ПЕРЕД e.mu, чтобы избежать deadlock:
	// t.mu → e.mu vs e.mu.RLock → t.mu
	mutateLockReleased = true
	t.mu.Unlock()

	key := dbName + "/" + tableName
	e.mu.Lock()
	e.catalog.LastModified[key] = txID
	if isDelete {
		e.catalog.RowCounts[key] -= affected
		if e.catalog.RowCounts[key] < 0 {
			e.catalog.RowCounts[key] = 0
		}
	}
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return 0, err
	}
	e.mu.Unlock()

	// WAL: mark transaction committed so recovery replays rather than rolls back
	if e.wal != nil {
		if _, err := e.wal.AppendWithTx(txID, wal.OpCommit, nil); err != nil {
			return 0, fmt.Errorf("wal commit: %w", err)
		}
	}

	return affected, nil
}

func (e *PageStorageEngine) UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error) {
	return e.mutateRows(dbName, tableName, indices, updates, false)
}

// UpdateRowsDirect replaces rows at given indices with pre-computed new values.
// Used when assignment expressions reference columns (e.g., SET amount = amount - 100).
func (e *PageStorageEngine) UpdateRowsDirect(dbName, tableName string, indices []int, newValues []Row) (int, error) {
	e.mu.Lock()
	txID := e.nextTxLocked()
	e.mu.Unlock()

	t, err := e.getTableForWrite(dbName, tableName)
	if err != nil {
		return 0, err
	}
	mutateLockReleased := false
	defer func() {
		if !mutateLockReleased {
			mutateLockReleased = true
			t.mu.Unlock()
		}
	}()

	wanted := make(map[int]bool, len(indices))
	for _, i := range indices {
		wanted[i] = true
	}

	// Map logical index to pre-computed new value
	newByIndex := make(map[int]Row, len(indices))
	for i, idx := range indices {
		if i < len(newValues) {
			newByIndex[idx] = newValues[i]
		}
	}

	var newVersions [][]byte
	affected := 0
	pos := 0

	var dirtyPid page.PageID
	dirty := false
	flushDirty := func() error {
		if dirty {
			e.bufPool.UnpinPage(dirtyPid, true)
			dirty = false
		}
		return nil
	}

	if e.wal != nil {
		var physicalSlots []struct {
			pid  page.PageID
			slot uint16
		}
		e.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
			if deletedTx != 0 {
				return false, nil
			}
			matched := wanted[pos]
			pos++
			if matched {
				physicalSlots = append(physicalSlots, struct {
					pid  page.PageID
					slot uint16
				}{pid, slot})
			}
			return false, nil
		})
		for _, ps := range physicalSlots {
			payload := wal.WALPageDeletePayload{
				DB:        dbName,
				Table:     tableName,
				SegmentNo: ps.pid.SegmentNo,
				PageNo:    ps.pid.PageNo,
				SlotNo:    ps.slot,
				XMax:      txID,
			}
			if _, err := e.wal.AppendWithTx(txID, wal.OpPageDelete, payload); err != nil {
				return 0, fmt.Errorf("wal delete: %w", err)
			}
		}
		pos = 0
	}

	err = e.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx != 0 {
			return false, nil
		}
		matched := wanted[pos]
		pos++
		if !matched {
			return false, nil
		}
		if dirty && dirtyPid != pid {
			if err := flushDirty(); err != nil {
				return true, err
			}
		}
		tuple := pg.GetTuple(slot)
		binary.LittleEndian.PutUint64(tuple[8:16], txID)
		dirtyPid, dirty = pid, true

		if nv, ok := newByIndex[pos-1]; ok {
			encoded, err := encodePageTuple(txID, 0, nv)
			if err != nil {
				return true, err
			}
			newVersions = append(newVersions, encoded)
		}
		affected++
		return false, nil
	})
	if err != nil {
		return 0, err
	}
	if err := flushDirty(); err != nil {
		return 0, err
	}
	if len(newVersions) > 0 {
		if err := e.appendTuplesLocked(t, newVersions); err != nil {
			return 0, err
		}
	}
	if err := t.heap.Sync(); err != nil {
		return 0, err
	}

	mutateLockReleased = true
	t.mu.Unlock()

	key := dbName + "/" + tableName
	e.mu.Lock()
	e.catalog.LastModified[key] = txID
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return 0, err
	}
	e.mu.Unlock()

	// WAL: mark transaction committed so recovery replays rather than rolls back
	if e.wal != nil {
		if _, err := e.wal.AppendWithTx(txID, wal.OpCommit, nil); err != nil {
			return 0, fmt.Errorf("wal commit: %w", err)
		}
	}

	return affected, nil
}

func (e *PageStorageEngine) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	return e.mutateRows(dbName, tableName, indices, nil, true)
}

// TruncateTable removes all rows from a table without per-row WAL.
// Unlike DeleteRows which marks each tuple as dead, this resets the heap file
// entirely — much faster for large tables.
func (e *PageStorageEngine) TruncateTable(dbName, tableName string) error {
	e.mu.Lock()
	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()

	// Write WAL entry for crash recovery
	if e.wal != nil {
		payload := wal.WALTruncateTablePayload{DB: dbName, Table: tableName}
		if _, err := e.wal.Append(wal.OpTruncateTable, payload); err != nil {
			return fmt.Errorf("truncate: wal append: %w", err)
		}
	}

	// Invalidate all cached pages for this table
	e.bufPool.InvalidateTableForce(t.tableID)

	// Close the current heap file
	if err := t.heap.Close(); err != nil {
		return fmt.Errorf("truncate: close heap: %w", err)
	}

	// Remove all segment files and recreate a fresh heap
	tableDir := e.tablePath(dbName, tableName)
	entries, err := readDirFilenames(tableDir)
	if err != nil {
		return fmt.Errorf("truncate: read dir: %w", err)
	}
	for _, name := range entries {
		if strings.HasSuffix(name, ".heap") {
			if err := removeFile(filepath.Join(tableDir, name)); err != nil {
				return fmt.Errorf("truncate: remove segment: %w", err)
			}
		}
	}

	// Create a fresh heap file (same path, same tableID)
	hf, err := createFreshHeapFile(tableDir)
	if err != nil {
		return fmt.Errorf("truncate: create heap: %w", err)
	}
	t.heap = hf

	// Update catalog
	key := dbName + "/" + tableName
	e.mu.Lock()
	e.catalog.RowCounts[key] = 0
	e.catalog.LastModified[key] = e.nextTxLocked()
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("truncate: save catalog: %w", err)
	}
	e.mu.Unlock()

	return nil
}

// readDirFilenames returns just the filenames in a directory.
func readDirFilenames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}

// removeFile removes a single file.
func removeFile(path string) error {
	return os.Remove(path)
}

// createFreshHeapFile creates a new heap file with an empty segment 0.
func createFreshHeapFile(dir string) (*heap.HeapFile, error) {
	return heap.CreateHeapFile(dir)
}

// ── Чтение ────────────────────────────────────────────────────────────────

// readRows возвращает строки, видимые на момент asOf (0 = текущие версии).
func (e *PageStorageEngine) readRows(dbName, tableName string, asOf uint64) ([]Row, error) {
	t, err := e.getTableForRead(dbName, tableName)
	if err != nil {
		return nil, err
	}
	defer t.mu.RUnlock()

	rows := []Row{}
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if asOf == 0 {
			if deletedTx == 0 {
				if e.txMgr != nil && !e.txMgr.IsCommitted(createdTx) {
					return false, nil
				}
				rows = append(rows, row)
			}
			return false, nil
		}
		if createdTx <= asOf && (deletedTx == 0 || deletedTx > asOf) {
			rows = append(rows, row)
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (e *PageStorageEngine) SelectRows(dbName, tableName string) ([]Row, error) {
	return e.readRows(dbName, tableName, 0)
}

func (e *PageStorageEngine) ReadCurrentRows(dbName, tableName string) ([]Row, error) {
	return e.readRows(dbName, tableName, 0)
}

// ReadSampleRows читает не более limit строк из таблицы.
// Использует покаместный проход по страницам с остановкой при достижении лимита.
func (e *PageStorageEngine) ReadSampleRows(dbName, tableName string, limit int) ([]Row, error) {
	if limit <= 0 {
		return nil, nil
	}
	t, err := e.getTableForRead(dbName, tableName)
	if err != nil {
		return nil, err
	}
	defer t.mu.RUnlock()

	rows := make([]Row, 0, limit)
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx == 0 {
			if e.txMgr != nil && !e.txMgr.IsCommitted(createdTx) {
				return false, nil
			}
			rows = append(rows, row)
			if len(rows) >= limit {
				return true, nil // stop early
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (e *PageStorageEngine) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]Row, error) {
	return e.readRows(dbName, tableName, txID)
}

func (e *PageStorageEngine) ReadRowsByPositions(dbName, tableName string, positions []int) ([]Row, error) {
	if len(positions) == 0 {
		return nil, nil
	}

	e.mu.RLock()
	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		e.mu.RUnlock()
		return nil, err
	}

	posSet := make(map[int]bool, len(positions))
	for _, p := range positions {
		posSet[p] = true
	}

	result := make([]Row, 0, len(positions))
	rowIdx := 0

	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx != 0 {
			return false, nil
		}
		if posSet[rowIdx] {
			result = append(result, row)
			delete(posSet, rowIdx)
		}
		rowIdx++
		if len(posSet) == 0 {
			return true, nil
		}
		return false, nil
	})
	e.mu.RUnlock()

	if err != nil {
		return nil, err
	}
	return result, nil
}

func (e *PageStorageEngine) CountRows(dbName, tableName string) (int, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	key := dbName + "/" + tableName
	count, ok := e.catalog.RowCounts[key]
	if !ok {
		return 0, fmt.Errorf("table '%s' not found in database '%s'", tableName, dbName)
	}
	return count, nil
}

func (e *PageStorageEngine) TxIDAtTimestamp(dbName, ts string) (uint64, error) {
	target, err := parseTimestampFlexible(ts)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q: %w", ts, err)
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	var maxTx uint64
	for _, stamp := range e.catalog.TxTimes {
		if (stamp.Timestamp.Equal(target) || stamp.Timestamp.Before(target)) && stamp.TxID > maxTx {
			maxTx = stamp.TxID
		}
	}
	return maxTx, nil
}

func (e *PageStorageEngine) RowHistory(dbName, tableName string, pkValue interface{}) ([]VersionedRow, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		return nil, err
	}
	if len(t.schema.Columns) == 0 {
		return []VersionedRow{}, nil
	}

	pk, err := normalizeValue(pkValue, t.schema.Columns[0])
	if err != nil {
		return nil, err
	}

	history := []VersionedRow{}
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if len(row) > 0 && valuesEqual(row[0], pk) {
			history = append(history, VersionedRow{CreatedTx: createdTx, DeletedTx: deletedTx, Data: row})
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return history, nil
}
