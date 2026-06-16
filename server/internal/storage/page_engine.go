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
	bufPool *BufferPool

	indexes   map[string]*index.IndexManager // "db/table" → index manager
	indexesMu sync.RWMutex
}

type pageTable struct {
	heap    *heap.HeapFile
	schema  *TableSchema
	tableID uint32
}

type pageTxStamp struct {
	TxID      uint64    `json:"tx_id"`
	Timestamp time.Time `json:"ts"`
}

type pageCatalog struct {
	CurrentTxID  uint64            `json:"current_tx_id"`
	LastModified map[string]uint64 `json:"last_modified"`
	RowCounts    map[string]int    `json:"row_counts"`
	TxTimes      []pageTxStamp     `json:"tx_times"`
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

	e := &PageStorageEngine{
		rootDir: root,
		tables:  make(map[string]*pageTable),
		catalog: pageCatalog{
			LastModified: make(map[string]uint64),
			RowCounts:    make(map[string]int),
		},
		wal:     w,
		txMgr:   txMgr,
		bufPool: NewBufferPool(defaultBufferPoolCapacity),
		indexes: make(map[string]*index.IndexManager),
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
	}
	return e, nil
}

// RecoverFromWAL воспроизводит WAL при старте page engine.
// Три фазы: Analysis → Redo → Undo (как в PostgreSQL ARIES).
func (e *PageStorageEngine) RecoverFromWAL() error {
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

	// Фаза 2: Redo — воспроизводим ВСЕ записи (и committed, и in-progress)
	if err := e.redoPhase(); err != nil {
		return fmt.Errorf("wal redo: %w", err)
	}

	// Фаза 3: Undo — откатываем незакоммиченные транзакции
	if err := e.undoPhase(inProgress); err != nil {
		return fmt.Errorf("wal undo: %w", err)
	}

	// Очищаем WAL после успешного recovery
	if err := e.wal.Checkpoint(); err != nil {
		return fmt.Errorf("wal checkpoint after recovery: %w", err)
	}

	slog.Info("WAL recovery: complete",
		"replayed", len(committed),
		"rolled_back", len(inProgress))

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
		e.wal.Append(wal.OpAbort, nil)
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
	pid := page.PageID{SegmentNo: p.SegmentNo, PageNo: p.PageNo}
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

	// Шаг 1: fsync WAL
	lsn, err := e.wal.Flush()
	if err != nil {
		return fmt.Errorf("checkpoint: wal flush: %w", err)
	}

	// Шаг 2: сбрасываем dirty pages из buffer pool
	e.mu.Lock()
	if e.bufPool != nil {
		for _, t := range e.tables {
			if err := e.bufPool.FlushDirtyPagesUpToLSN(lsn, t.heap); err != nil {
				e.mu.Unlock()
				return fmt.Errorf("checkpoint: flush dirty pages: %w", err)
			}
		}
	}

	// Шаг 3: сохраняем каталог (содержит актуальные RowCounts)
	if err := e.saveCatalogLocked(); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("checkpoint: save catalog: %w", err)
	}

	// Шаг 4: записать checkpoint в WAL (inside lock to prevent races)
	if _, err := e.wal.Append(wal.OpCheckpoint, wal.CheckpointPayload{LSN: lsn}); err != nil {
		e.mu.Unlock()
		return fmt.Errorf("checkpoint: write record: %w", err)
	}
	e.mu.Unlock()

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
func (e *PageStorageEngine) getPage(pid page.PageID, hf *heap.HeapFile) (*page.Page, error) {
	pg, _, err := e.bufPool.FetchPage(pid, hf)
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
	return e.catalog.CurrentTxID
}

// ── Кодирование кортежей ──────────────────────────────────────────────────

func encodePageTuple(createdTx, deletedTx uint64, row Row) ([]byte, error) {
	payload, err := json.Marshal(row)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, pageTupleHeaderSize+len(payload))
	binary.LittleEndian.PutUint64(buf[0:8], createdTx)
	binary.LittleEndian.PutUint64(buf[8:16], deletedTx)
	copy(buf[pageTupleHeaderSize:], payload)
	if len(buf) > page.MaxTupleLength {
		return nil, fmt.Errorf("page engine: row of %d bytes does not fit a page (max %d)", len(buf), page.MaxTupleLength)
	}
	return buf, nil
}

func decodePageTuple(tuple []byte, schema *TableSchema) (createdTx, deletedTx uint64, row Row, err error) {
	if len(tuple) < pageTupleHeaderSize {
		return 0, 0, nil, fmt.Errorf("page engine: tuple too short (%d bytes)", len(tuple))
	}
	createdTx = binary.LittleEndian.Uint64(tuple[0:8])
	deletedTx = binary.LittleEndian.Uint64(tuple[8:16])

	var raw []interface{}
	if err := json.Unmarshal(tuple[pageTupleHeaderSize:], &raw); err != nil {
		return 0, 0, nil, err
	}

	row = make(Row, len(schema.Columns))
	for i := range schema.Columns {
		if i >= len(raw) {
			row[i] = nil
			continue
		}
		normalized, nerr := normalizeValue(raw[i], schema.Columns[i])
		if nerr != nil {
			// Тип столбца мог измениться после ALTER — отдаём как есть
			row[i] = raw[i]
			continue
		}
		row[i] = normalized
	}
	return createdTx, deletedTx, row, nil
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
			_ = t.heap.Close()
			delete(e.tables, key)
			delete(e.catalog.LastModified, key)
			delete(e.catalog.RowCounts, key)
		}
	}
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
		_ = t.heap.Close()
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
func (e *PageStorageEngine) flushDirty(dirty bool, dirtyPid page.PageID, dirtyPg *page.Page, t *pageTable) error {
	if dirty {
		if err := t.heap.WritePage(dirtyPid, dirtyPg); err != nil {
			return err
		}
		dirty = false
	}
	return nil
}

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
		pid = pageIDAt(t.tableID, total - 1)
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
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return 0, err
	}

	txID := e.nextTxLocked()
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
		pid page.PageID
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
			pid = pageIDAt(t.tableID, total - 1)
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
						DB:         dbName,
						Table:      tableName,
						SegmentNo:  pid.SegmentNo,
						PageNo:     pid.PageNo,
						SlotNo:     slot,
						XID:        txID,
						TupleData:  tuple,
					}
					if _, err := e.wal.AppendWithTx(txID, wal.OpPageInsert, payload); err != nil {
						return 0, fmt.Errorf("wal insert: %w", err)
					}
				}

				// Запоминаем позицию для catalog
				insertedTuples = append(insertedTuples, struct {
					pid page.PageID
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

	if err := t.heap.Sync(); err != nil {
		return 0, err
	}

	key := dbName + "/" + tableName
	e.catalog.LastModified[key] = txID
	e.catalog.RowCounts[key] += len(rows)
	if err := e.saveCatalogLocked(); err != nil {
		return 0, err
	}

	e.updateIndexesOnInsert(dbName, tableName, rows, e.catalog.RowCounts[key]-len(rows))

	return len(rows), nil
}

// mutateRows помечает версии удалёнными и (для UPDATE) добавляет новые версии.
func (e *PageStorageEngine) mutateRows(dbName, tableName string, indices []int, updates map[string]Value, isDelete bool) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return 0, err
	}

	wanted := make(map[int]bool, len(indices))
	for _, i := range indices {
		wanted[i] = true
	}

	colIndex := make(map[string]int, len(t.schema.Columns))
	for i, col := range t.schema.Columns {
		colIndex[strings.ToLower(col.Name)] = i
	}

	txID := e.nextTxLocked()
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
				DB:         dbName,
				Table:      tableName,
				SegmentNo:  ps.pid.SegmentNo,
				PageNo:     ps.pid.PageNo,
				SlotNo:     ps.slot,
				XMax:       txID,
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
	e.mu.RLock()
	defer e.mu.RUnlock()

	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		return nil, err
	}

	rows := []Row{}
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if asOf == 0 {
			if deletedTx == 0 {
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

	// Атомарная замена: удаляем старый heap и переименовываем shadow
	originalPath := e.tablePath(dbName, tableName)
	if err := t.heap.Close(); err != nil {
		os.Remove(shadowPath)
		return nil, err
	}
	if err := os.RemoveAll(originalPath); err != nil {
		os.Remove(shadowPath)
		return nil, err
	}
	if err := os.Rename(shadowPath, originalPath); err != nil {
		os.Remove(shadowPath)
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

// ── ALTER TABLE ───────────────────────────────────────────────────────────

// rewriteTable перезаписывает все живые строки таблицы функцией transform
// (используется ADD/DROP COLUMN, когда меняется арность строк).
// Перед началом и после завершения эмитятся WAL-записи OpRewriteBegin/OpRewriteCommit.
func (e *PageStorageEngine) rewriteTable(db, table string, newSchema *TableSchema, transform func(Row) Row) error {
	t, err := e.getTableLocked(db, table, true)
	if err != nil {
		return err
	}

	// Emit WAL rewrite begin
	if e.wal != nil {
		rewritePayload := wal.WALRewritePayload{DB: db, Table: table}
		if _, err := e.wal.Append(wal.OpRewriteBegin, rewritePayload); err != nil {
			return err
		}
	}

	rows := []Row{}
	err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, _, deletedTx uint64, row Row) (bool, error) {
		if deletedTx == 0 {
			rows = append(rows, transform(row))
		}
		return false, nil
	})
	if err != nil {
		return err
	}

	// Полная перезапись heap-файла: история версий при ALTER не сохраняется
	if err := t.heap.Close(); err != nil {
		return err
	}
	path := e.tablePath(db, table)
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".heap") {
			if err := os.Remove(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	hf, err := heap.CreateHeapFile(path)
	if err != nil {
		return err
	}
	t.heap = hf
	t.schema = newSchema

	// Invalidate all cached pages for this table (old heap file is gone)
	e.bufPool.InvalidateTable(t.tableID)

	txID := e.nextTxLocked()
	tuples := make([][]byte, 0, len(rows))
	for _, row := range rows {
		tuple, err := encodePageTuple(txID, 0, row)
		if err != nil {
			return err
		}
		tuples = append(tuples, tuple)
	}
	if err := e.appendTuplesLocked(t, tuples); err != nil {
		return err
	}
	if err := t.heap.Sync(); err != nil {
		return err
	}

	if err := e.writeSchemaLocked(db, table, newSchema); err != nil {
		return err
	}

	// Emit WAL rewrite commit
	if e.wal != nil {
		rewritePayload := wal.WALRewritePayload{DB: db, Table: table}
		if _, err := e.wal.Append(wal.OpRewriteCommit, rewritePayload); err != nil {
			return err
		}
	}

	e.catalog.LastModified[db+"/"+table] = txID
	return e.saveCatalogLocked()
}

func (e *PageStorageEngine) AlterTableAddColumn(dbName, tableName string, col ColumnSchema, defaultVal Value) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return err
	}
	for _, existing := range t.schema.Columns {
		if strings.EqualFold(existing.Name, col.Name) {
			return fmt.Errorf("column '%s' already exists", col.Name)
		}
	}

	var normalizedDefault Value
	if defaultVal != nil {
		normalizedDefault, err = normalizeValue(defaultVal, col)
		if err != nil {
			return err
		}
	}

	newSchema := *t.schema
	newSchema.Columns = append(append([]ColumnSchema(nil), t.schema.Columns...), col)
	defaultValCopy := normalizedDefault
	return e.rewriteTable(dbName, tableName, &newSchema, func(row Row) Row {
		return append(append(Row(nil), row...), defaultValCopy)
	})
}

func (e *PageStorageEngine) AlterTableDropColumn(dbName, tableName string, colName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return err
	}
	drop := -1
	for i, existing := range t.schema.Columns {
		if strings.EqualFold(existing.Name, colName) {
			drop = i
			break
		}
	}
	if drop < 0 {
		return fmt.Errorf("column '%s' does not exist", colName)
	}

	// Update indexes: remove index on dropped column
	key := dbName + "/" + tableName
	e.indexesMu.RLock()
	if mgr, ok := e.indexes[key]; ok {
		for _, idx := range mgr.All() {
			if idx.ColIndex() == drop {
				mgr.Remove(idx.Name())
			}
		}
		e.saveIndexesMetadata(dbName, tableName, mgr)
	}
	e.indexesMu.RUnlock()

	newSchema := *t.schema
	newSchema.Columns = append([]ColumnSchema(nil), t.schema.Columns...)
	newSchema.Columns = append(newSchema.Columns[:drop], newSchema.Columns[drop+1:]...)
	return e.rewriteTable(dbName, tableName, &newSchema, func(row Row) Row {
		out := append(Row(nil), row...)
		if drop < len(out) {
			out = append(out[:drop], out[drop+1:]...)
		}
		return out
	})
}

func (e *PageStorageEngine) AlterTableRenameColumn(dbName, tableName, oldName, newName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return err
	}
	found := false
	colIdx := -1
	newSchema := *t.schema
	newSchema.Columns = append([]ColumnSchema(nil), t.schema.Columns...)
	for i := range newSchema.Columns {
		if strings.EqualFold(newSchema.Columns[i].Name, oldName) {
			newSchema.Columns[i].Name = newName
			colIdx = i
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("column '%s' does not exist", oldName)
	}

	// Update index column reference
	key := dbName + "/" + tableName
	e.indexesMu.RLock()
	if mgr, ok := e.indexes[key]; ok {
		for _, idx := range mgr.All() {
			if idx.ColIndex() == colIdx {
				mgr.RenameColumn(idx.Name(), newName)
				e.saveIndexesMetadata(dbName, tableName, mgr)
				break
			}
		}
	}
	e.indexesMu.RUnlock()

	t.schema = &newSchema
	return e.writeSchemaLocked(dbName, tableName, &newSchema)
}

func (e *PageStorageEngine) AlterTableRenameTable(dbName, oldName, newName string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	oldKey := dbName + "/" + oldName
	newKey := dbName + "/" + newName
	if t, ok := e.tables[oldKey]; ok {
		_ = t.heap.Close()
		delete(e.tables, oldKey)
	}
	if _, err := os.Stat(e.tablePath(dbName, newName)); err == nil {
		return fmt.Errorf("table '%s' already exists", newName)
	}
	if err := os.Rename(e.tablePath(dbName, oldName), e.tablePath(dbName, newName)); err != nil {
		return err
	}

	// Обновляем имя в схеме
	t, err := e.getTableLocked(dbName, newName, true)
	if err != nil {
		return err
	}
	newSchema := *t.schema
	newSchema.Name = newName
	t.schema = &newSchema
	if err := e.writeSchemaLocked(dbName, newName, &newSchema); err != nil {
		return err
	}

	e.catalog.LastModified[newKey] = e.catalog.LastModified[oldKey]
	e.catalog.RowCounts[newKey] = e.catalog.RowCounts[oldKey]
	delete(e.catalog.LastModified, oldKey)
	delete(e.catalog.RowCounts, oldKey)
	return e.saveCatalogLocked()
}

// ── Индексы ────────────────────────────────────────────────────────────────

func (e *PageStorageEngine) getOrCreateIndexManager(db, table string) *index.IndexManager {
	key := db + "/" + table
	e.indexesMu.Lock()
	defer e.indexesMu.Unlock()
	mgr, ok := e.indexes[key]
	if !ok {
		mgr = index.NewManager()
		e.indexes[key] = mgr
	}
	return mgr
}

func (e *PageStorageEngine) indexMetadataPath(dbName, tableName string) string {
	return filepath.Join(e.rootDir, dbName, tableName, ".indexes.json")
}

func (e *PageStorageEngine) loadIndexesMetadata(dbName, tableName string, mgr *index.IndexManager) {
	path := e.indexMetadataPath(dbName, tableName)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var meta []struct {
		Name    string `json:"name"`
		Column  string `json:"column"`
		ColIdx  int    `json:"col_idx"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return
	}
	for _, m := range meta {
		idx := index.NewByType(m.Name, m.Column, m.ColIdx, m.Type)
		mgr.Add(idx)
	}
}

func (e *PageStorageEngine) saveIndexesMetadata(dbName, tableName string, mgr *index.IndexManager) error {
	indexes := mgr.All()
	meta := make([]struct {
		Name    string `json:"name"`
		Column  string `json:"column"`
		ColIdx  int    `json:"col_idx"`
		Type    string `json:"type"`
	}, 0, len(indexes))
	for _, idx := range indexes {
		meta = append(meta, struct {
			Name    string `json:"name"`
			Column  string `json:"column"`
			ColIdx  int    `json:"col_idx"`
			Type    string `json:"type"`
		}{
			Name:    idx.Name(),
			Column:  idx.Column(),
			ColIdx:  idx.ColIndex(),
			Type:    idx.Type(),
		})
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.indexMetadataPath(dbName, tableName), data, 0o644)
}

func (e *PageStorageEngine) rowsToIndexable(rows []Row) []index.IndexableRow {
	result := make([]index.IndexableRow, len(rows))
	for i, row := range rows {
		data := make([]interface{}, len(row))
		for j, v := range row {
			data[j] = v
		}
		result[i] = index.IndexableRow{Data: data}
	}
	return result
}

func (e *PageStorageEngine) CreateIndex(dbName, tableName, indexName, column string) error {
	e.mu.Lock()
	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}

	colIdx := -1
	for i, col := range t.schema.Columns {
		if strings.EqualFold(col.Name, column) {
			colIdx = i
			break
		}
	}
	if colIdx == -1 {
		e.mu.Unlock()
		return fmt.Errorf("column '%s' not found in table '%s'", column, tableName)
	}

	// Scan rows directly under existing lock (readRows would deadlock)
	var rows []Row
	e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx == 0 {
			rows = append(rows, row)
		}
		return false, nil
	})
	e.mu.Unlock()

	mgr := e.getOrCreateIndexManager(dbName, tableName)
	if _, ok := mgr.FindForColumn(column); ok {
		return fmt.Errorf("index already exists for column '%s' in table '%s'", column, tableName)
	}

	var idx index.Index
	if strings.HasPrefix(indexName, "gin_") {
		idx = index.NewGINIndex(indexName, column, colIdx)
	} else if strings.HasPrefix(indexName, "gin_jsonb_") {
		idx = index.NewGINJSONBIndex(indexName, column, colIdx)
	} else if strings.HasPrefix(indexName, "gist_") {
		idx = index.NewGiSTIndex(indexName, column, colIdx)
	} else {
		idx = index.NewBTreeIndex(indexName, column, colIdx)
	}

	idx.Rebuild(e.rowsToIndexable(rows))
	mgr.Add(idx)

	return e.saveIndexesMetadata(dbName, tableName, mgr)
}

func (e *PageStorageEngine) CreateIndexMulti(dbName, tableName, indexName string, columns []string) error {
	e.mu.Lock()
	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}

	var colIndices []int
	for _, column := range columns {
		colIdx := -1
		for i, col := range t.schema.Columns {
			if strings.EqualFold(col.Name, column) {
				colIdx = i
				break
			}
		}
		if colIdx == -1 {
			e.mu.Unlock()
			return fmt.Errorf("column '%s' not found in table '%s'", column, tableName)
		}
		colIndices = append(colIndices, colIdx)
	}

	var rows []Row
	e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
		if deletedTx == 0 {
			rows = append(rows, row)
		}
		return false, nil
	})
	e.mu.Unlock()

	mgr := e.getOrCreateIndexManager(dbName, tableName)
	idx := index.NewCompositeIndex(indexName, columns, colIndices)
	idx.Rebuild(e.rowsToIndexable(rows))
	mgr.Add(idx)

	return e.saveIndexesMetadata(dbName, tableName, mgr)
}

func (e *PageStorageEngine) DropIndex(dbName, indexName string) error {
	e.indexesMu.Lock()
	for key, mgr := range e.indexes {
		if !strings.HasPrefix(key, dbName+"/") {
			continue
		}
		if mgr.Has(indexName) {
			tableName := strings.TrimPrefix(key, dbName+"/")
			mgr.Remove(indexName)
			err := e.saveIndexesMetadata(dbName, tableName, mgr)
			e.indexesMu.Unlock()
			return err
		}
	}
	e.indexesMu.Unlock()
	return fmt.Errorf("index '%s' not found", indexName)
}

func (e *PageStorageEngine) ListIndexes(dbName, tableName string) ([]string, error) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	indexes := mgr.All()
	names := make([]string, len(indexes))
	for i, idx := range indexes {
		names[i] = idx.Name()
	}
	return names, nil
}

func (e *PageStorageEngine) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	idx, ok := mgr.FindForColumn(column)
	if !ok {
		return "", false
	}
	return idx.Name(), true
}

func (e *PageStorageEngine) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	idx, ok := mgr.FindForColumn(column)
	if !ok {
		return nil, false
	}
	return idx.Lookup(value)
}

func (e *PageStorageEngine) IndexRangeLookup(dbName, tableName, column, low, high string) ([]int, bool) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	idxs, ok := mgr.FindForColumnMultiple(column)
	if !ok || len(idxs) == 0 {
		return nil, false
	}
	for _, idx := range idxs {
		if btree, ok := idx.(*index.BTreeIndex); ok {
			return btree.Range(low, high), true
		}
	}
	return nil, false
}

func (e *PageStorageEngine) IndexFTSLookup(dbName, tableName, column, query string) ([]int, bool) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	idxs, ok := mgr.FindForColumnMultiple(column)
	if !ok || len(idxs) == 0 {
		return nil, false
	}
	for _, idx := range idxs {
		if gin, ok := idx.(*index.GINIndex); ok {
			return gin.Search(query), true
		}
	}
	return nil, false
}

func (e *PageStorageEngine) updateIndexesOnInsert(dbName, tableName string, rows []Row, startPos int) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	for _, idx := range mgr.All() {
		colIdx := idx.ColIndex()
		for i, row := range rows {
			if colIdx < len(row) {
				idx.Insert(fmt.Sprintf("%v", row[colIdx]), startPos+i)
			}
		}
	}
}

func (e *PageStorageEngine) updateIndexesOnDelete(dbName, tableName string, rowPositions []int) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	for _, idx := range mgr.All() {
		for _, pos := range rowPositions {
			idx.Delete(pos)
		}
	}
}

// ── Жизненный цикл ────────────────────────────────────────────────────────

func (e *PageStorageEngine) FinalCheckpoint() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range e.tables {
		if err := t.heap.Sync(); err != nil {
			return err
		}
	}
	return e.saveCatalogLocked()
}

func (e *PageStorageEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var firstErr error
	for key, t := range e.tables {
		if err := t.heap.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(e.tables, key)
	}
	if err := e.saveCatalogLocked(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
