package storage

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/index"
	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

// PageStorageEngine — реализация StorageEngine поверх бинарного страничного
// хранилища (internal/storage/page + internal/storage/heap), включается через
// storage.engine: page в vaultdb.yaml.
//
// Формат кортежа: [0:8] created_tx LE, [8:16] deleted_tx LE, [16:] JSON строки.
// Заголовок фиксированного размера позволяет помечать версию удалённой
// записью 8 байт на месте, без перемещения кортежа — на этом строится
// версионность (time travel) и vacuum.
//
// Вторичные индексы поддерживаются (Hash, BTree, GIN, GiST).
type PageStorageEngine struct {
	mu      sync.RWMutex
	rootDir string

	tables  map[string]*pageTable // "db/table" → открытая таблица
	catalog pageCatalog

	wal     *wal.WAL
	txMgr   *txmanager.Manager
	bufPool  *BufferPool
	pageLock *PageLockManager

	indexes   map[string]*index.IndexManager // "db/table" → index manager
	indexesMu sync.RWMutex
}

type pageTable struct {
	heap    *heap.HeapFile
	schema  *TableSchema
	tableID uint32
	mu      sync.RWMutex // per-table lock
}

type pageTxStamp struct {
	TxID      uint64    `json:"tx_id"`
	Timestamp time.Time `json:"ts"`
}

type pageCatalog struct {
	CurrentTxID   uint64            `json:"current_tx_id"`
	LastModified  map[string]uint64 `json:"last_modified"`
	RowCounts     map[string]int    `json:"row_counts"`
	TxTimes       []pageTxStamp     `json:"tx_times"`
	CheckpointLSN uint64            `json:"checkpoint_lsn"`
}

const (
	pageTupleHeaderSize = 16
	maxTxTimesEntries   = 10000
	keepTxTimesEntries  = 5000
)

// NewPageStorageEngine открывает (или создаёт) страничное хранилище в
// <dataDir>/pagedb.
func NewPageStorageEngine(dataDir string, w *wal.WAL, txMgr *txmanager.Manager) (*PageStorageEngine, error) {
	root := filepath.Join(dataDir, "pagedb")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	bufPool := NewBufferPool(defaultBufferPoolCapacity)
	if w != nil {
		bufPool.SetWAL(w)
	}

	e := &PageStorageEngine{
		rootDir:  root,
		tables:   make(map[string]*pageTable),
		catalog: pageCatalog{
			LastModified: make(map[string]uint64),
			RowCounts:    make(map[string]int),
		},
		wal:      w,
		txMgr:    txMgr,
		bufPool:  bufPool,
		pageLock: NewPageLockManager(),
		indexes:  make(map[string]*index.IndexManager),
	}

	catalogPath := e.catalogPath()
	if data, err := os.ReadFile(catalogPath); err == nil {
		if err := json.Unmarshal(data, &e.catalog); err != nil {
			return nil, fmt.Errorf("page engine: corrupt catalog %s: %w", catalogPath, err)
		}
		if e.catalog.LastModified == nil {
			e.catalog.LastModified = make(map[string]uint64)
		}
		if e.catalog.RowCounts == nil {
			e.catalog.RowCounts = make(map[string]int)
		}
		if txMgr != nil && e.catalog.CurrentTxID > 0 {
			txMgr.EnsureCounterAtLeast(e.catalog.CurrentTxID + 1)
		}
	}
	return e, nil
}

// RecoverFromWAL воспроизводит WAL при старте page engine.
// Три фазы: Analysis → Redo → Undo (как в PostgreSQL ARIES).
func (e *PageStorageEngine) RecoverFromWAL() error {
	// Clean up any incomplete ALTER TABLE rewrites before WAL replay
	e.recoverIncompleteRewrites()

	// Clean up any orphaned vacuum shadow directories
	e.recoverOrphanedVacuums()

	if e.wal == nil {
		return nil
	}

	// Фаза 1: Analysis — определяем какие транзакции закоммичены
	committed, inProgress, err := e.wal.AnalyzeTransactions()
	if err != nil {
		return fmt.Errorf("wal analysis: %w", err)
	}

	slog.Info("WAL recovery: analysis complete",
		"committed", len(committed),
		"in_progress", len(inProgress))

	// Ensure all tables on disk are opened so redo phase can find them.
	// With write-back buffer pool, data may only exist in WAL, not on disk.
	if err := e.ensureAllTablesOpen(); err != nil {
		slog.Warn("failed to discover tables before WAL recovery", "error", err)
	}

	// Фаза 2: Redo — воспроизводим ВСЕ записи (и committed, и in-progress)
	if err := e.redoPhase(); err != nil {
		return fmt.Errorf("wal redo: %w", err)
	}

	// Фаза 3: Undo — откатываем незакоммиченные транзакции
	if err := e.undoPhase(inProgress); err != nil {
		return fmt.Errorf("wal undo: %w", err)
	}

	// Сначала fsync все heap-файлы, чтобы данные были на диске
	e.mu.RLock()
	for _, t := range e.tables {
		if t.heap != nil {
			if err := t.heap.Sync(); err != nil {
				e.mu.RUnlock()
				return fmt.Errorf("heap sync during recovery: %w", err)
			}
		}
	}
	e.mu.RUnlock()

	// Recalculate catalog from actual table state to fix any inconsistencies
	e.recalculateCatalog()

	// Записываем checkpoint record в WAL, потом сохраняем catalog с LSN,
	// затем усекаем WAL — тот же порядок, что и в doCheckpoint.
	checkpointLSN, err := e.wal.WriteCheckpointRecord()
	if err != nil {
		return fmt.Errorf("checkpoint: write checkpoint record after recovery: %w", err)
	}

	e.mu.Lock()
	e.catalog.CheckpointLSN = checkpointLSN
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("checkpoint: save catalog after recovery: %w", err)
	}
	e.mu.Unlock()

	if err := e.wal.TruncateWAL(); err != nil {
		return fmt.Errorf("checkpoint: truncate wal after recovery: %w", err)
	}

	slog.Info("WAL recovery: complete",
		"replayed", len(committed),
		"rolled_back", len(inProgress))

	return nil
}

// recalculateCatalog rebuilds catalog row counts from actual table state.
// Called after WAL replay to fix any inconsistencies between catalog and heap.
func (e *PageStorageEngine) recalculateCatalog() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Ensure all tables on disk are opened in e.tables
	if err := e.ensureAllTablesOpen(); err != nil {
		slog.Warn("failed to discover tables for catalog recalculation", "error", err)
	}

	for key, t := range e.tables {
		count := 0
		if err := e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, _ Row) (bool, error) {
			if deletedTx == 0 {
				if e.txMgr != nil && !e.txMgr.IsCommitted(createdTx) {
					return false, nil
				}
				count++
			}
			return false, nil
		}); err != nil {
			slog.Warn("failed to recalculate catalog", "table", key, "error", err)
			continue
		}
		e.catalog.RowCounts[key] = count
	}

	if err := e.saveCatalogLocked(); err != nil {
		slog.Error("failed to save recalculated catalog", "error", err)
	}
}

// ensureAllTablesOpen discovers all tables on disk and opens them into e.tables.
func (e *PageStorageEngine) ensureAllTablesOpen() error {
	dbs, err := os.ReadDir(e.rootDir)
	if err != nil {
		return err
	}
	for _, dbEntry := range dbs {
		if !dbEntry.IsDir() || strings.HasPrefix(dbEntry.Name(), "_") {
			continue
		}
		dbName := dbEntry.Name()
		tables, err := os.ReadDir(e.dbPath(dbName))
		if err != nil {
			continue
		}
		for _, tblEntry := range tables {
			if !tblEntry.IsDir() {
				continue
			}
			tableName := tblEntry.Name()
			key := dbName + "/" + tableName
			if _, ok := e.tables[key]; ok {
				continue
			}
			if _, err := e.getTableLocked(dbName, tableName, true); err != nil {
				slog.Warn("failed to open table for catalog recalculation", "db", dbName, "table", tableName, "error", err)
			}
		}
	}
	return nil
}

func (e *PageStorageEngine) redoPhase() error {
	return e.wal.Replay(func(entry wal.Entry) error {
		switch entry.OpType {
		case wal.OpPageInsert:
			var p wal.WALPageInsertPayload
			if err := json.Unmarshal(entry.Payload, &p); err != nil {
				return err
			}
			return e.redoInsert(p)
		case wal.OpPageDelete, wal.OpPageUpdateXMax:
			var p wal.WALPageDeletePayload
			if err := json.Unmarshal(entry.Payload, &p); err != nil {
				return err
			}
			return e.redoDelete(p)
		case wal.OpSchemaWrite:
			var p wal.WALSchemaWritePayload
			if err := json.Unmarshal(entry.Payload, &p); err != nil {
				return err
			}
			return e.redoSchemaWrite(p)
		case wal.OpRewriteBegin:
			slog.Warn("WAL recovery: incomplete table rewrite detected (OpRewriteBegin without OpRewriteCommit)",
				"db", extractFieldFromPayload(entry.Payload, "db"),
				"table", extractFieldFromPayload(entry.Payload, "table"),
				"txid", entry.TxID)
		case wal.OpRewriteCommit, wal.OpRewriteData:
			// Rewrite already completed — nothing to redo (data was written to heap)
		case wal.OpTruncateTable:
			var p wal.WALTruncateTablePayload
			if err := json.Unmarshal(entry.Payload, &p); err != nil {
				return err
			}
			return e.redoTruncateTable(p)
		case wal.OpFullPageImage:
			var p wal.FullPageImagePayload
			if err := json.Unmarshal(entry.Payload, &p); err != nil {
				return err
			}
			return e.replayFullPageImage(p)
		}
		return nil // другие типы — пропускаем
	})
}

func (e *PageStorageEngine) undoPhase(inProgress map[uint64]bool) error {
	for xid := range inProgress {
		if err := e.wal.ReplayTransaction(xid, func(entry wal.Entry) error {
			switch entry.OpType {
			case wal.OpPageInsert:
				var p wal.WALPageInsertPayload
				if err := json.Unmarshal(entry.Payload, &p); err != nil {
					return err
				}
				return e.undoInsert(p, xid)
			case wal.OpPageDelete:
				var p wal.WALPageDeletePayload
				if err := json.Unmarshal(entry.Payload, &p); err != nil {
					return err
				}
				return e.undoDelete(p)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("undo transaction %d: %w", xid, err)
		}

		// Записать в WAL что транзакция откатилась
		e.wal.AppendWithTx(xid, wal.OpAbort, nil)
	}
	return nil
}

func (e *PageStorageEngine) redoInsert(p wal.WALPageInsertPayload) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := p.DB + "/" + p.Table
	t, ok := e.tables[key]
	if !ok {
		return nil
	}

	// Восстанавливаем tuple на страницу
	pid := page.PageID{TableID: t.tableID, SegmentNo: p.SegmentNo, PageNo: p.PageNo}
	var pg page.Page
	if err := t.heap.ReadPage(pid, &pg); err != nil {
		// Страница не существует — создаём новую
		newPid, newPg, err := t.heap.AllocatePage(page.PageTypeHeap)
		if err != nil {
			return err
		}
		// Fix TableID: AllocatePage не знает о tableID
		newPid.TableID = t.tableID
		pg = *newPg
		pid = newPid
	}

	if _, err := pg.InsertTuple(p.TupleData); err != nil {
		return err
	}

	if err := t.heap.WritePage(pid, &pg); err != nil {
		return err
	}

	// Обновляем каталог
	e.catalog.RowCounts[key]++
	return nil
}

func (e *PageStorageEngine) redoDelete(p wal.WALPageDeletePayload) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := p.DB + "/" + p.Table
	t, ok := e.tables[key]
	if !ok {
		return nil
	}

	// Помечаем tuple как удалённый (устанавливаем XMax)
	pid := page.PageID{TableID: t.tableID, SegmentNo: p.SegmentNo, PageNo: p.PageNo}
	var pg page.Page
	if err := t.heap.ReadPage(pid, &pg); err != nil {
		return err
	}

	tuple := pg.GetTuple(p.SlotNo)
	if tuple == nil {
		return nil
	}

	// Устанавливаем XMax (deleted_tx)
	binary.LittleEndian.PutUint64(tuple[8:16], p.XMax)

	if err := t.heap.WritePage(pid, &pg); err != nil {
		return err
	}

	// Обновляем каталог
	e.catalog.RowCounts[key]--
	if e.catalog.RowCounts[key] < 0 {
		e.catalog.RowCounts[key] = 0
	}
	return nil
}

func (e *PageStorageEngine) redoSchemaWrite(p wal.WALSchemaWritePayload) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return os.WriteFile(e.schemaPathFor(p.DB, p.Table), []byte(p.Schema), 0o644)
}

func (e *PageStorageEngine) redoTruncateTable(p wal.WALTruncateTablePayload) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := p.DB + "/" + p.Table
	t, ok := e.tables[key]
	if !ok {
		// Table not loaded yet; catalog recalc will fix row counts
		return nil
	}

	// Invalidate cached pages
	e.bufPool.InvalidateTableForce(t.tableID)

	// Close the heap
	if err := t.heap.Close(); err != nil {
		return fmt.Errorf("redo truncate: close heap: %w", err)
	}

	// Remove all segment files and recreate fresh heap
	tableDir := e.tablePath(p.DB, p.Table)
	entries, err := os.ReadDir(tableDir)
	if err != nil {
		return fmt.Errorf("redo truncate: read dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".heap") {
			if err := os.Remove(filepath.Join(tableDir, entry.Name())); err != nil {
				return fmt.Errorf("redo truncate: remove segment: %w", err)
			}
		}
	}

	hf, err := heap.CreateHeapFile(tableDir)
	if err != nil {
		return fmt.Errorf("redo truncate: create heap: %w", err)
	}
	t.heap = hf

	e.catalog.RowCounts[key] = 0
	return nil
}

func (e *PageStorageEngine) replayFullPageImage(p wal.FullPageImagePayload) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := p.DB + "/" + p.Table
	t, ok := e.tables[key]
	if !ok {
		path := e.tablePath(p.DB, p.Table)
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		hf, err := heap.OpenHeapFile(path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(e.schemaPathFor(p.DB, p.Table))
		if err != nil {
			_ = hf.Close()
			return nil
		}
		var schema TableSchema
		if err := json.Unmarshal(data, &schema); err != nil {
			_ = hf.Close()
			return nil
		}
		tid := tableIDFromPath(path)
		t = &pageTable{heap: hf, schema: &schema, tableID: tid}
		e.tables[key] = t
	}

	if len(p.PageData) != page.PageSize {
		return fmt.Errorf("full page image: invalid page data size %d (expected %d)", len(p.PageData), page.PageSize)
	}

	pid := page.PageID{TableID: t.tableID, SegmentNo: p.SegmentNo, PageNo: p.PageNo}
	var pg page.Page
	copy(pg[:], p.PageData)

	if err := t.heap.WritePage(pid, &pg); err != nil {
		return fmt.Errorf("full page image: write page %v: %w", pid, err)
	}

	slog.Info("WAL recovery: replayed full page image",
		"db", p.DB, "table", p.Table, "segment", p.SegmentNo, "page", p.PageNo)
	return nil
}

func extractFieldFromPayload(payload []byte, field string) string {
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	if v, ok := m[field]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (e *PageStorageEngine) undoInsert(p wal.WALPageInsertPayload, xid uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := p.DB + "/" + p.Table
	t, ok := e.tables[key]
	if !ok {
		return nil
	}

	// Undo INSERT = пометить tuple как dead (XMax = xid)
	pid := page.PageID{TableID: t.tableID, SegmentNo: p.SegmentNo, PageNo: p.PageNo}
	var pg page.Page
	if err := t.heap.ReadPage(pid, &pg); err != nil {
		return err
	}

	tuple := pg.GetTuple(p.SlotNo)
	if tuple == nil {
		return nil
	}

	// Устанавливаем XMax = xid (помечаем как удалённый)
	binary.LittleEndian.PutUint64(tuple[8:16], xid)

	if err := t.heap.WritePage(pid, &pg); err != nil {
		return err
	}

	// Обновляем каталог
	e.catalog.RowCounts[key]--
	if e.catalog.RowCounts[key] < 0 {
		e.catalog.RowCounts[key] = 0
	}
	return nil
}

func (e *PageStorageEngine) undoDelete(p wal.WALPageDeletePayload) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := p.DB + "/" + p.Table
	t, ok := e.tables[key]
	if !ok {
		return nil
	}

	// Undo DELETE = снять XMax (восстановить tuple)
	pid := page.PageID{TableID: t.tableID, SegmentNo: p.SegmentNo, PageNo: p.PageNo}
	var pg page.Page
	if err := t.heap.ReadPage(pid, &pg); err != nil {
		return err
	}

	tuple := pg.GetTuple(p.SlotNo)
	if tuple == nil {
		return nil
	}

	// Обнуляем XMax (восстанавливаем tuple)
	binary.LittleEndian.PutUint64(tuple[8:16], 0)

	if err := t.heap.WritePage(pid, &pg); err != nil {
		return err
	}

	// Обновляем каталог
	e.catalog.RowCounts[key]++
	return nil
}

// CheckpointLoop запускается в фоновой горутине и периодически:
// 1. Сбрасывает WAL на диск (fsync)
// 2. После успешного WAL fsync — сбрасывает dirty pages из buffer pool
// 3. Записывает checkpoint запись в WAL
func (e *PageStorageEngine) CheckpointLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Финальный checkpoint при shutdown
			e.doCheckpoint()
			return
		case <-ticker.C:
			if err := e.doCheckpoint(); err != nil {
				slog.Error("checkpoint failed", "error", err)
			}
		}
	}
}

func (e *PageStorageEngine) doCheckpoint() error {
	if e.wal == nil {
		return nil
	}

	// Шаг 1: fsync WAL — получаем текущий LSN
	lsn, err := e.wal.Flush()
	if err != nil {
		return fmt.Errorf("checkpoint: wal flush: %w", err)
	}

	// Шаг 2: сбрасываем dirty pages из buffer pool
	e.mu.Lock()
	if e.bufPool != nil {
		if err := e.bufPool.FlushDirtyPagesUpToLSN(lsn); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("checkpoint: flush dirty pages: %w", err)
		}
	}
	e.mu.Unlock()

	// Шаг 3: записываем checkpoint record в WAL (до сохранения каталога).
	// Это гарантирует, что при crash между шагом 3 и 4 recovery
	// сможет найти checkpoint record в WAL.
	// ВАЖНО: mu не удерживается — нет deadlock WAL↔PageEngine:
	// doCheckpoint: wal.mu (step 3) → mu (step 4), recovery: wal.mu → mu.
	checkpointLSN, err := e.wal.WriteCheckpointRecord()
	if err != nil {
		return fmt.Errorf("checkpoint: write checkpoint record: %w", err)
	}

	// Шаг 4: сохраняем каталог с CheckpointLSN.
	// Теперь recovery может определить checkpoint LSN из каталога.
	e.mu.Lock()
	e.catalog.CheckpointLSN = checkpointLSN
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("checkpoint: save catalog: %w", err)
	}
	e.mu.Unlock()

	// Шаг 5: усекаем WAL — все dirty pages сброшены, catalog сохранён
	if err := e.wal.TruncateWAL(); err != nil {
		return fmt.Errorf("checkpoint: truncate wal: %w", err)
	}

	return nil
}

func (e *PageStorageEngine) catalogPath() string {
	return filepath.Join(e.rootDir, "_catalog.json")
}

func (e *PageStorageEngine) dbPath(db string) string {
	return filepath.Join(e.rootDir, db)
}

func (e *PageStorageEngine) tablePath(db, table string) string {
	return filepath.Join(e.rootDir, db, table)
}

func (e *PageStorageEngine) schemaPathFor(db, table string) string {
	return filepath.Join(e.tablePath(db, table), "_schema.json")
}

// getPage загружает страницу из buffer pool или с диска.
func (e *PageStorageEngine) getPage(pid page.PageID, hf *heap.HeapFile, db, table string) (*page.Page, error) {
	pg, _, err := e.bufPool.FetchPage(pid, hf, db, table)
	if err != nil {
		return nil, err
	}
	return pg, nil
}

// unpinPage освобождает страницу в buffer pool.
func (e *PageStorageEngine) unpinPage(pid page.PageID, dirty bool) {
	e.bufPool.UnpinPage(pid, dirty)
}

// saveCatalogLocked сохраняет каталог; вызывается под write-локом.
func (e *PageStorageEngine) saveCatalogLocked() error {
	data, err := json.MarshalIndent(&e.catalog, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.catalogPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, e.catalogPath())
}

// nextTxLocked выделяет новый txID и фиксирует его время (для AS OF).
func (e *PageStorageEngine) nextTxLocked() uint64 {
	e.catalog.CurrentTxID++
	e.catalog.TxTimes = append(e.catalog.TxTimes, pageTxStamp{
		TxID:      e.catalog.CurrentTxID,
		Timestamp: time.Now(),
	})
	if len(e.catalog.TxTimes) > maxTxTimesEntries {
		e.catalog.TxTimes = e.catalog.TxTimes[len(e.catalog.TxTimes)-keepTxTimesEntries:]
	}
	if e.txMgr != nil {
		e.txMgr.EnsureCounterAtLeast(e.catalog.CurrentTxID + 1)
	}
	return e.catalog.CurrentTxID
}

// ── Кодирование кортежей ──────────────────────────────────────────────────

func encodePageTuple(createdTx, deletedTx uint64, row Row) ([]byte, error) {
	return encodeBinaryTuple(createdTx, deletedTx, row)
}

func decodePageTuple(tuple []byte, schema *TableSchema) (createdTx, deletedTx uint64, row Row, err error) {
	return decodeBinaryTuple(tuple, schema)
}

// ── Базы данных ───────────────────────────────────────────────────────────

func (e *PageStorageEngine) CreateDatabase(name string) error {
	if err := validateObjectName(name); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	path := e.dbPath(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("database '%s' already exists", name)
	}
	return os.MkdirAll(path, 0o755)
}

func (e *PageStorageEngine) DropDatabase(name string) error {
	if err := validateObjectName(name); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := os.Stat(e.dbPath(name)); err != nil {
		return fmt.Errorf("database '%s' does not exist", name)
	}
	prefix := name + "/"
	for key, t := range e.tables {
		if strings.HasPrefix(key, prefix) {
			e.bufPool.InvalidateTableForce(t.tableID)
			if err := t.heap.Close(); err != nil {
				slog.Warn("failed to close heap during drop database", "key", key, "error", err)
			}
			delete(e.tables, key)
			delete(e.catalog.LastModified, key)
			delete(e.catalog.RowCounts, key)
		}
	}
	e.indexesMu.Lock()
	for key := range e.indexes {
		if strings.HasPrefix(key, prefix) {
			delete(e.indexes, key)
		}
	}
	e.indexesMu.Unlock()
	if err := os.RemoveAll(e.dbPath(name)); err != nil {
		return err
	}
	return e.saveCatalogLocked()
}

func (e *PageStorageEngine) DatabaseExists(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	info, err := os.Stat(e.dbPath(name))
	return err == nil && info.IsDir()
}

func (e *PageStorageEngine) ListDatabases() ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	entries, err := os.ReadDir(e.rootDir)
	if err != nil {
		return nil, err
	}
	var dbs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dbs = append(dbs, entry.Name())
		}
	}
	sort.Strings(dbs)
	return dbs, nil
}

// ── Таблицы ───────────────────────────────────────────────────────────────

func (e *PageStorageEngine) CreateTable(dbName string, schema TableSchema) error {
	if err := validateObjectName(dbName); err != nil {
		return err
	}
	if err := validateObjectName(schema.Name); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, err := os.Stat(e.dbPath(dbName)); err != nil {
		return fmt.Errorf("database '%s' does not exist", dbName)
	}
	path := e.tablePath(dbName, schema.Name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("table '%s' already exists", schema.Name)
	}

	hf, err := heap.CreateHeapFile(path)
	if err != nil {
		return err
	}

	schema.Database = dbName
	if schema.CreatedAt.IsZero() {
		schema.CreatedAt = time.Now()
	}
	if err := e.writeSchemaLocked(dbName, schema.Name, &schema); err != nil {
		_ = hf.Close()
		return err
	}

	key := dbName + "/" + schema.Name
	tid := tableIDFromPath(path)
	e.bufPool.InvalidateTable(tid)
	e.tables[key] = &pageTable{heap: hf, schema: &schema, tableID: tid}
	e.catalog.RowCounts[key] = 0

	// Auto-create BTree index on PRIMARY KEY columns
	for i, col := range schema.Columns {
		if col.PrimaryKey {
			mgr := e.getOrCreateIndexManager(dbName, schema.Name)
			idxName := fmt.Sprintf("pk_%s_%s", schema.Name, col.Name)
			idx := index.NewBTreeIndex(idxName, col.Name, i)
			mgr.Add(idx)
			if err := e.saveIndexesMetadata(dbName, schema.Name, mgr); err != nil {
				return err
			}
		}
	}

	return e.saveCatalogLocked()
}

// writeSchemaLocked записывает JSON-схему на диск. Перед записью эмитится
// WAL-запись OpSchemaWrite, чтобы при recovery можно было перезаписать схему.
func (e *PageStorageEngine) writeSchemaLocked(db, table string, schema *TableSchema) error {
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return err
	}
	if e.wal != nil {
		payload := wal.WALSchemaWritePayload{
			DB:     db,
			Table:  table,
			Schema: string(data),
		}
		if _, err := e.wal.Append(wal.OpSchemaWrite, payload); err != nil {
			return err
		}
	}
	return os.WriteFile(e.schemaPathFor(db, table), data, 0o644)
}

// getTableForLock возвращает таблицу с захватом per-table мьютекса.
// write=true — полный Lock (для записи), write=false — RLock (для чтения).
// Caller должен вызвать t.mu.RUnlock() или t.mu.Unlock() когда закончит.
func (e *PageStorageEngine) getTableForLock(db, table string, write bool) (*pageTable, error) {
	t, err := e.getOrCreateTable(db, table)
	if err != nil {
		return nil, err
	}
	if write {
		t.mu.Lock()
	} else {
		t.mu.RLock()
	}
	return t, nil
}

func (e *PageStorageEngine) getTableForRead(db, table string) (*pageTable, error) {
	return e.getTableForLock(db, table, false)
}

func (e *PageStorageEngine) getTableForWrite(db, table string) (*pageTable, error) {
	return e.getTableForLock(db, table, true)
}

// getOrCreateTable возвращает таблицу из кэша или открывает с диска.
// Не берёт per-table lock — это ответственность вызывающего.
func (e *PageStorageEngine) getOrCreateTable(db, table string) (*pageTable, error) {
	key := db + "/" + table
	path := e.tablePath(db, table)

	// Быстрый путь: таблица уже в кэше
	e.mu.RLock()
	t, ok := e.tables[key]
	if ok {
		e.mu.RUnlock()
		return t, nil
	}
	e.mu.RUnlock()

	// Медленный путь: открываем и кэшируем
	e.mu.Lock()
	t, ok = e.tables[key]
	if !ok {
		if _, err := os.Stat(path); err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("table '%s' does not exist", table)
		}
		hf, err := heap.OpenHeapFile(path)
		if err != nil {
			e.mu.Unlock()
			return nil, err
		}
		data, err := os.ReadFile(e.schemaPathFor(db, table))
		if err != nil {
			_ = hf.Close()
			e.mu.Unlock()
			return nil, err
		}
		var schema TableSchema
		if err := json.Unmarshal(data, &schema); err != nil {
			_ = hf.Close()
			e.mu.Unlock()
			return nil, err
		}
		tid := tableIDFromPath(path)
		t = &pageTable{heap: hf, schema: &schema, tableID: tid}
		e.tables[key] = t
	}
	e.mu.Unlock()
	return t, nil
}

// getTableLocked открывает таблицу (лениво) и кэширует её.
// Вызывается под любым из локов e.mu; модификация e.tables безопасна только
// под write-локом, поэтому readOnly-путь не кэширует при RLock.
func (e *PageStorageEngine) getTableLocked(db, table string, cache bool) (*pageTable, error) {
	key := db + "/" + table
	if t, ok := e.tables[key]; ok {
		return t, nil
	}

	path := e.tablePath(db, table)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("table '%s' does not exist", table)
	}

	hf, err := heap.OpenHeapFile(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(e.schemaPathFor(db, table))
	if err != nil {
		_ = hf.Close()
		return nil, err
	}
	var schema TableSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		_ = hf.Close()
		return nil, err
	}

	tid := tableIDFromPath(path)
	t := &pageTable{heap: hf, schema: &schema, tableID: tid}
	if cache {
		e.tables[key] = t
	}
	return t, nil
}

func (e *PageStorageEngine) DropTable(dbName, tableName string) error {
	if err := validateObjectName(dbName); err != nil {
		return err
	}
	if err := validateObjectName(tableName); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	key := dbName + "/" + tableName
	if t, ok := e.tables[key]; ok {
		e.bufPool.InvalidateTableForce(t.tableID)
		if err := t.heap.Close(); err != nil {
			slog.Warn("failed to close heap during drop table", "key", key, "error", err)
		}
		delete(e.tables, key)
	}
	path := e.tablePath(dbName, tableName)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	delete(e.catalog.LastModified, key)
	delete(e.catalog.RowCounts, key)
	return e.saveCatalogLocked()
}

func (e *PageStorageEngine) TableExists(dbName, tableName string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if _, ok := e.tables[dbName+"/"+tableName]; ok {
		return true
	}
	info, err := os.Stat(e.tablePath(dbName, tableName))
	return err == nil && info.IsDir()
}

func (e *PageStorageEngine) ListTables(dbName string) ([]TableInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	entries, err := os.ReadDir(e.dbPath(dbName))
	if err != nil {
		return nil, fmt.Errorf("database '%s' does not exist", dbName)
	}

	var infos []TableInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		info := TableInfo{Name: name, RowCount: e.catalog.RowCounts[dbName+"/"+name]}
		if data, err := os.ReadFile(e.schemaPathFor(dbName, name)); err == nil {
			var schema TableSchema
			if json.Unmarshal(data, &schema) == nil {
				info.CreatedAt = schema.CreatedAt
			}
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

func (e *PageStorageEngine) GetTableSchema(dbName, tableName string) (*TableSchema, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		return nil, err
	}
	copied := *t.schema
	copied.Columns = append([]ColumnSchema(nil), t.schema.Columns...)
	return &copied, nil
}
