package storage

import (
	"encoding/binary"
	"fmt"
	"strings"

	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

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
		pg, err := e.getPage(pid, t.heap)
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
		pg, err = e.getPage(pid, t.heap)
		if err != nil {
			return err
		}
		havePage = true
	}

	dirty := false
	flush := func() error {
		if havePage && dirty {
			if err := t.heap.WritePage(pid, pg); err != nil {
				return err
			}
			e.bufPool.InvalidatePage(pid)
			dirty = false
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
				pid, pg, havePage = newPid, newPg, true
			}
			if _, err := pg.InsertTuple(tuple); err == nil {
				dirty = true
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
		tuple, err := encodePageTuple(txID, 0, normalized)
		if err != nil {
			return 0, err
		}
		tuples = append(tuples, tuple)
	}

	// Сначала вставляем tuples, затем пишем WAL с реальными позициями
	// Это важно для recovery — WAL должен содержать точные позиции
	insertedTuples := make([]struct {
		pid  page.PageID
		slot uint16
	}, 0, len(tuples))

	for _, tuple := range tuples {
		// Находим или выделяем страницу через buffer pool
		total, err := t.heap.PageCount()
		if err != nil {
			return 0, err
		}

		var pid page.PageID
		var pg *page.Page
		havePage := false

		if total > 0 {
			pid = pageIDAt(t.tableID, total-1)
			pg, err = e.getPage(pid, t.heap)
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
				pid, pg, havePage = newPid, newPg, true
			}

			slot, err := pg.InsertTuple(tuple)
			if err == nil {
				// Успешно вставили — записываем в WAL
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
					if _, err := e.wal.AppendWithTx(txID, wal.OpPageInsert, payload); err != nil {
						return 0, fmt.Errorf("wal insert: %w", err)
					}
				}

				// Запоминаем позицию для catalog
				insertedTuples = append(insertedTuples, struct {
					pid  page.PageID
					slot uint16
				}{pid, slot})

				// Сбрасываем страницу на диск и помечаем как dirty
				if err := t.heap.WritePage(pid, pg); err != nil {
					return 0, err
				}
				e.bufPool.UnpinPage(pid, true)
				break
			}

			// Страница полна — отпиним старую и выделяем новую
			if havePage {
				e.bufPool.UnpinPage(pid, false)
			}
			if err := t.heap.Sync(); err != nil {
				return 0, err
			}
			havePage = false
		}
	}
	// Отпускаем t.mu перед обновлением каталога
	t.mu.Unlock()

	if err := t.heap.Sync(); err != nil {
		return 0, err
	}

	// Обновляем каталог под e.mu
	key := dbName + "/" + tableName
	e.mu.Lock()
	e.catalog.LastModified[key] = txID
	e.catalog.RowCounts[key] += len(rows)
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return 0, err
	}
	e.mu.Unlock()

	e.updateIndexesOnInsert(dbName, tableName, rows, e.catalog.RowCounts[key]-len(rows))

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
	defer t.mu.Unlock()

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
	var dirtyPg *page.Page
	dirty := false
	flushDirty := func() error {
		if dirty {
			if err := t.heap.WritePage(dirtyPid, dirtyPg); err != nil {
				return err
			}
			e.bufPool.InvalidatePage(dirtyPid)
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
		dirtyPid, dirtyPg, dirty = pid, pg, true

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

	key := dbName + "/" + tableName
	e.catalog.LastModified[key] = txID
	if isDelete {
		e.catalog.RowCounts[key] -= affected
		if e.catalog.RowCounts[key] < 0 {
			e.catalog.RowCounts[key] = 0
		}
	}
	if err := e.saveCatalogLocked(); err != nil {
		return 0, err
	}

	if isDelete && affected > 0 {
		e.updateIndexesOnDelete(dbName, tableName, indices)
	}

	return affected, nil
}

func (e *PageStorageEngine) UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error) {
	return e.mutateRows(dbName, tableName, indices, updates, false)
}

func (e *PageStorageEngine) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	return e.mutateRows(dbName, tableName, indices, nil, true)
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
