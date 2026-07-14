package storage

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/core/index"
	"vaultdb/internal/core/storage/heap"
	"vaultdb/internal/core/storage/page"
	"vaultdb/internal/core/wal"
)

// readAheadPages is the number of pages to prefetch during sequential scans.
// 16 pages = 128 KB — amortizes syscall overhead without excessive memory pressure.
const readAheadPages = 16

// ── Scanning ────────────────────────────────────────────────────────────────

// pageIDAt converts a global page number to PageID (segment + page).
// tableID uniquely identifies the table in the buffer pool.
func pageIDAt(tableID uint32, global uint32) page.PageID {
	return page.PageID{
		TableID:   tableID,
		SegmentNo: uint16(global / page.PagesPerSegment),
		PageNo:    global % page.PagesPerSegment,
	}
}

// tableIDFromPath computes a unique table ID from the path.
func tableIDFromPath(path string) uint32 {
	h := uint32(2166136261) // FNV-1a offset basis
	for i := 0; i < len(path); i++ {
		h ^= uint32(path[i])
		h *= 16777619 // FNV-1a prime
	}
	if h == 0 {
		h = 1 // avoid zero ID
	}
	return h
}

type tupleVisitor func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (stop bool, err error)

// scanTuples iterates all tuples of a table in page/slot order.
// During sequential scans, pages are prefetched ahead of time (read-ahead).
func (e *PageStorageEngine) scanTuples(t *pageTable, visit tupleVisitor) error {
	total, err := t.heap.PageCount()
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}

	// Kick off initial read-ahead for the first batch of pages.
	var prefetchWg sync.WaitGroup
	startPrefetch := func(from uint32) {
		count := readAheadPages
		if int(from)+count > int(total) {
			count = int(total) - int(from)
		}
		if count <= 0 {
			return
		}
		pids := make([]page.PageID, count)
		for i := 0; i < count; i++ {
			pids[i] = pageIDAt(t.tableID, from+uint32(i))
		}
		prefetchWg.Add(1)
		go func() {
			defer prefetchWg.Done()
			e.bufPool.PrefetchPages(pids, t.heap)
		}()
	}
	startPrefetch(0)

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

		// Trigger read-ahead for the next batch when we approach the edge
		// of the currently prefetched window.
		nextGlobal := g + 1
		if nextGlobal < total && nextGlobal%readAheadPages == 0 {
			startPrefetch(nextGlobal)
		}
	}
	prefetchWg.Wait()
	return nil
}

// ── Writing ─────────────────────────────────────────────────────────────────

// flushDirty flushes a dirty page to disk via heap file.

// appendTuplesLocked appends tuples to the end of the table; called under write lock.
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
			freshPage := false
			if !havePage {
				newPid, newPg, err := t.heap.AllocatePage(page.PageTypeHeap)
				if err != nil {
					return err
				}
				newPid.TableID = t.tableID
				e.bufPool.CachePage(newPid, newPg, t.heap, t.schema.Database, t.schema.Name)
				pid, pg, havePage = newPid, newPg, true
				freshPage = true
			}
			if _, err := pg.InsertTuple(tuple); err == nil {
				break
			}
			// Page is full — flush it and allocate a new one
			if err := flush(); err != nil {
				return err
			}
			havePage = false

			if freshPage {
				return fmt.Errorf("tuple too large to fit on a page (%d bytes > %d usable)",
					len(tuple), page.PageSize-page.PageHeaderSize-page.ItemPointerSize)
			}
		}
	}
	return flush()
}

func (e *PageStorageEngine) InsertRows(dbName, tableName string, rows []Row) (int, error) {
	// Get txID without global lock — atomic counter.
	txID := e.nextTxID()

	// Get table reference (releases e.mu)
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

	// Try to find a BTree index on the PK column for O(log n) lookups.
	// Falls back to full table scan when no index exists.
	var pkIndex index.Index
	existingPKs := make(map[interface{}]bool)
	if pkIdx >= 0 {
		pkCol := t.schema.Columns[pkIdx].Name
		pkIndex, _ = e.findIndexForColumn(dbName, tableName, pkCol)
		if pkIndex == nil {
			// No index — build O(1) lookup set via full scan (backward compatible)
			_ = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
				if deletedTx == 0 && pkIdx < len(row) {
					existingPKs[row[pkIdx]] = true
				}
				return false, nil
			})
		}
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
			if pkIndex != nil {
				// BTree index path: O(log n) lookup per row
				pkStr := fmt.Sprintf("%v", pkVal)
				if _, found := pkIndex.Lookup(pkStr); found {
					return 0, fmt.Errorf("duplicate primary key value: %v", pkVal)
				}
				// Also check within this batch (index won't have batch rows yet)
				if existingPKs[pkVal] {
					return 0, fmt.Errorf("duplicate primary key value: %v", pkVal)
				}
				existingPKs[pkVal] = true
			} else {
				// Fallback: O(1) map lookup from pre-built set
				if existingPKs[pkVal] {
					return 0, fmt.Errorf("duplicate primary key value: %v", pkVal)
				}
				existingPKs[pkVal] = true
			}
		}

		tuple, err := encodePageTuple(txID, 0, normalized)
		if err != nil {
			return 0, err
		}
		tuples = append(tuples, tuple)
	}

	// Cache page count — avoid syscall per tuple in batch inserts.
	cachedPageCount, err := t.heap.PageCount()
	if err != nil {
		return 0, err
	}

	for _, tuple := range tuples {
		var pid page.PageID
		var pg *page.Page
		havePage := false
		pageLocked := false

		if cachedPageCount > 0 {
			pid = pageIDAt(t.tableID, cachedPageCount-1)
			e.pageLock.LockPage(pid)
			pg, err = e.getPage(pid, t.heap, t.schema.Database, t.schema.Name)
			if err != nil {
				e.pageLock.UnlockPageWrite(pid)
				return 0, err
			}
			havePage = true
			pageLocked = true
		}

		for {
			freshPage := false
			if !havePage {
				newPid, newPg, err := t.heap.AllocatePage(page.PageTypeHeap)
				if err != nil {
					return 0, err
				}
				newPid.TableID = t.tableID
				e.bufPool.CachePage(newPid, newPg, t.heap, t.schema.Database, t.schema.Name)
				pid, pg, havePage = newPid, newPg, true
				cachedPageCount++
				freshPage = true
			}

			if !pageLocked {
				e.pageLock.LockPage(pid)
			}
			pageLocked = true
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

				e.bufPool.UnpinPageDirty(pid, lsn)
				e.pageLock.UnlockPageWrite(pid)
				pageLocked = false
				break
			}
			e.pageLock.UnlockPageWrite(pid)
			pageLocked = false

			if havePage {
				e.bufPool.UnpinPage(pid, false)
			}
			havePage = false

			if freshPage {
				return 0, fmt.Errorf("tuple too large to fit on a page (%d bytes > %d usable)",
					len(tuple), page.PageSize-page.PageHeaderSize-page.ItemPointerSize)
			}
		}
	}

	insertLockReleased = true
	t.mu.Unlock()

	key := dbName + "/" + tableName

	// Update per-table atomic counters (no lock needed).
	t.rowCount.Add(int64(len(rows)))
	t.lastTxID.Store(txID)

	// Sync catalog under e.mu for persistence.
	e.mu.Lock()
	e.catalog.CurrentTxID = txID
	e.catalog.LastModified[key] = txID
	e.catalog.RowCounts[key] = int(t.rowCount.Load())
	startPos := e.catalog.RowCounts[key] - len(rows)
	e.catalog.TxTimes = append(e.catalog.TxTimes, pageTxStamp{
		TxID:      txID,
		Timestamp: time.Now(),
	})
	if len(e.catalog.TxTimes) > maxTxTimesEntries {
		e.catalog.TxTimes = e.catalog.TxTimes[len(e.catalog.TxTimes)-keepTxTimesEntries:]
	}
	e.markCatalogDirty()
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

// mutateRows marks versions as deleted and (for UPDATE) appends new versions.
func (e *PageStorageEngine) mutateRows(dbName, tableName string, indices []int, updates map[string]Value, isDelete bool) (int, error) {
	// Get txID without global lock — atomic counter.
	txID := e.nextTxID()

	useRowLocks := len(indices) <= 10

	var t *pageTable
	var err error
	if useRowLocks {
		// Small batch: lock individual rows instead of the whole table.
		t, err = e.getTableForRead(dbName, tableName)
		if err != nil {
			return 0, err
		}
		// Acquire row-level exclusive locks on each target row.
		for _, idx := range indices {
			if err = e.rowLocks.LockRowLegacy(dbName, tableName, uint64(idx), txID, LockExclusive); err != nil {
				t.mu.RUnlock()
				return 0, err
			}
		}
	} else {
		// Bulk operation: fall back to table-level write lock.
		t, err = e.getTableForWrite(dbName, tableName)
		if err != nil {
			return 0, err
		}
	}
	mutateLockReleased := false
	defer func() {
		if !mutateLockReleased {
			mutateLockReleased = true
			if useRowLocks {
				for _, idx := range indices {
					e.rowLocks.UnlockRow(dbName, tableName, uint64(idx), txID)
				}
				t.mu.RUnlock()
			} else {
				t.mu.Unlock()
			}
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
	var newRows []Row
	affected := 0
	pos := 0

	type locatedTuple struct {
		pid       page.PageID
		slot      uint16
		createdTx uint64
		row       Row
	}
	var located []locatedTuple

	// Single scan: collect physical slots AND row data simultaneously.
	e.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx != 0 {
			return false, nil
		}
		matched := wanted[pos]
		pos++
		if matched {
			located = append(located, locatedTuple{
				pid:       pid,
				slot:      slot,
				createdTx: createdTx,
				row:       append(Row(nil), row...), // copy row data
			})
		}
		return false, nil
	})

	// WAL recording using collected data.
	if e.wal != nil {
		for _, loc := range located {
			payload := wal.WALPageDeletePayload{
				DB:        dbName,
				Table:     tableName,
				SegmentNo: loc.pid.SegmentNo,
				PageNo:    loc.pid.PageNo,
				SlotNo:    loc.slot,
				XMax:      txID,
			}
			if _, err := e.wal.AppendWithTx(txID, wal.OpPageDelete, payload); err != nil {
				return 0, fmt.Errorf("wal delete: %w", err)
			}
		}
	}

	// Apply mutations using collected data.
	for _, loc := range located {
		pg, err := e.getPage(loc.pid, t.heap, dbName, tableName)
		if err != nil {
			return 0, err
		}

		// Mark old tuple as deleted.
		tuple := pg.GetTuple(loc.slot)
		binary.LittleEndian.PutUint64(tuple[8:16], txID)

		if !isDelete {
			newRow := append(Row(nil), loc.row...)
			for name, val := range updates {
				idx, ok := colIndex[strings.ToLower(name)]
				if !ok {
					e.bufPool.UnpinPageDirty(loc.pid, 0)
					return 0, fmt.Errorf("column '%s' does not exist", name)
				}
				n, err := normalizeValue(val, t.schema.Columns[idx])
				if err != nil {
					e.bufPool.UnpinPageDirty(loc.pid, 0)
					return 0, fmt.Errorf("column '%s': %w", name, err)
				}
				newRow[idx] = n
			}
			encoded, err := encodePageTuple(txID, 0, newRow)
			if err != nil {
				e.bufPool.UnpinPageDirty(loc.pid, 0)
				return 0, err
			}
			newVersions = append(newVersions, encoded)
			newRows = append(newRows, newRow)
		}

		affected++
		e.bufPool.UnpinPageDirty(loc.pid, 0)
	}

	if len(newVersions) > 0 {
		if err := e.appendTuplesLocked(t, newVersions); err != nil {
			return 0, err
		}
	}

	// Update indexes before releasing t.mu (they don't need e.mu)
	if affected > 0 {
		e.updateIndexesOnDelete(dbName, tableName, indices)
		if !isDelete && len(newRows) > 0 {
			e.updateIndexesOnInsert(dbName, tableName, newRows, pos-affected)
		}
	}

	// Release t.mu BEFORE e.mu to avoid deadlock:
	// t.mu → e.mu vs e.mu.RLock → t.mu
	mutateLockReleased = true
	if useRowLocks {
		for _, idx := range indices {
			e.rowLocks.UnlockRow(dbName, tableName, uint64(idx), txID)
		}
		t.mu.RUnlock()
	} else {
		t.mu.Unlock()
	}

	key := dbName + "/" + tableName

	// Update per-table atomic counters.
	t.lastTxID.Store(txID)
	if isDelete {
		t.rowCount.Add(-int64(affected))
		if t.rowCount.Load() < 0 {
			t.rowCount.Store(0)
		}
	}

	// Sync catalog under e.mu for persistence.
	e.mu.Lock()
	e.catalog.CurrentTxID = txID
	e.catalog.LastModified[key] = txID
	e.catalog.RowCounts[key] = int(t.rowCount.Load())
	e.catalog.TxTimes = append(e.catalog.TxTimes, pageTxStamp{
		TxID:      txID,
		Timestamp: time.Now(),
	})
	if len(e.catalog.TxTimes) > maxTxTimesEntries {
		e.catalog.TxTimes = e.catalog.TxTimes[len(e.catalog.TxTimes)-keepTxTimesEntries:]
	}
	e.markCatalogDirty()
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
	// Get txID without global lock — atomic counter.
	txID := e.nextTxID()

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
	var newRows []Row
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
			newRows = append(newRows, nv)
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

	// Update indexes before releasing t.mu (they don't need e.mu)
	if affected > 0 {
		e.updateIndexesOnDelete(dbName, tableName, indices)
		if len(newRows) > 0 {
			e.updateIndexesOnInsert(dbName, tableName, newRows, pos-affected)
		}
	}

	mutateLockReleased = true
	t.mu.Unlock()

	key := dbName + "/" + tableName

	// Update per-table atomic counter.
	t.lastTxID.Store(txID)

	// Sync catalog under e.mu for persistence.
	e.mu.Lock()
	e.catalog.CurrentTxID = txID
	e.catalog.LastModified[key] = txID
	e.catalog.TxTimes = append(e.catalog.TxTimes, pageTxStamp{
		TxID:      txID,
		Timestamp: time.Now(),
	})
	if len(e.catalog.TxTimes) > maxTxTimesEntries {
		e.catalog.TxTimes = e.catalog.TxTimes[len(e.catalog.TxTimes)-keepTxTimesEntries:]
	}
	e.markCatalogDirty()
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
	t.mu.Lock()
	e.mu.Unlock()

	// Write WAL entry for crash recovery
	if e.wal != nil {
		payload := wal.WALTruncateTablePayload{DB: dbName, Table: tableName}
		if _, err := e.wal.Append(wal.OpTruncateTable, payload); err != nil {
			t.mu.Unlock()
			return fmt.Errorf("truncate: wal append: %w", err)
		}
	}

	// Invalidate all cached pages for this table
	e.bufPool.InvalidateTableForce(t.tableID)

	// Close the current heap file
	if err := t.heap.Close(); err != nil {
		t.mu.Unlock()
		return fmt.Errorf("truncate: close heap: %w", err)
	}

	// Remove all segment files and recreate a fresh heap
	tableDir := e.tablePath(dbName, tableName)
	entries, err := readDirFilenames(tableDir)
	if err != nil {
		t.mu.Unlock()
		return fmt.Errorf("truncate: read dir: %w", err)
	}
	for _, name := range entries {
		if strings.HasSuffix(name, ".heap") {
			if err := removeFile(filepath.Join(tableDir, name)); err != nil {
				t.mu.Unlock()
				return fmt.Errorf("truncate: remove segment: %w", err)
			}
		}
	}

	// Create a fresh heap file (same path, same tableID)
	hf, err := createFreshHeapFile(tableDir)
	if err != nil {
		t.mu.Unlock()
		return fmt.Errorf("truncate: create heap: %w", err)
	}
	t.heap = hf

	// Release t.mu before acquiring e.mu for catalog update to avoid deadlock.
	// t.mu is no longer needed — heap is replaced and all page-level operations
	// use pageLock, not t.mu.
	t.mu.Unlock()

	// Update catalog
	key := dbName + "/" + tableName
	txID := e.nextTxID()
	t.rowCount.Store(0)
	t.lastTxID.Store(txID)
	e.mu.Lock()
	e.catalog.CurrentTxID = txID
	e.catalog.RowCounts[key] = 0
	e.catalog.LastModified[key] = txID
	e.catalog.TxTimes = append(e.catalog.TxTimes, pageTxStamp{
		TxID:      txID,
		Timestamp: time.Now(),
	})
	if len(e.catalog.TxTimes) > maxTxTimesEntries {
		e.catalog.TxTimes = e.catalog.TxTimes[len(e.catalog.TxTimes)-keepTxTimesEntries:]
	}
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

// ── Reading ─────────────────────────────────────────────────────────────────

// readRows returns rows visible as of a given txID (0 = current versions).
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

// ReadSampleRows reads up to limit rows from a table.
// Uses a page-by-page scan with early termination when the limit is reached.
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
	key := dbName + "/" + tableName

	// Try per-table atomic counter first (lock-free).
	e.mu.RLock()
	t, ok := e.tables[key]
	e.mu.RUnlock()
	if ok {
		return int(t.rowCount.Load()), nil
	}

	// Fallback to catalog.
	e.mu.RLock()
	count, ok := e.catalog.RowCounts[key]
	e.mu.RUnlock()
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

func (e *PageStorageEngine) AllRowHistory(dbName, tableName string) ([]VersionedRow, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		return nil, err
	}
	if len(t.schema.Columns) == 0 {
		return []VersionedRow{}, nil
	}

	history := []VersionedRow{}
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		history = append(history, VersionedRow{CreatedTx: createdTx, DeletedTx: deletedTx, Data: row})
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return history, nil
}
