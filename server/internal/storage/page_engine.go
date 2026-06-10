package storage

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
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
// Известные ограничения относительно JSON-движка:
//   - вторичные индексы не поддерживаются (CREATE INDEX возвращает ошибку);
//   - строка после сериализации должна помещаться в страницу 8 КБ.
type PageStorageEngine struct {
	mu      sync.RWMutex
	rootDir string

	tables  map[string]*pageTable // "db/table" → открытая таблица
	catalog pageCatalog
}

type pageTable struct {
	heap   *heap.HeapFile
	schema *TableSchema
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

const pageTupleHeaderSize = 16

// NewPageStorageEngine открывает (или создаёт) страничное хранилище в
// <dataDir>/pagedb.
func NewPageStorageEngine(dataDir string) (*PageStorageEngine, error) {
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
	e.mu.Lock()
	defer e.mu.Unlock()
	path := e.dbPath(name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("database '%s' already exists", name)
	}
	return os.MkdirAll(path, 0o755)
}

func (e *PageStorageEngine) DropDatabase(name string) error {
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
	e.tables[key] = &pageTable{heap: hf, schema: &schema}
	e.catalog.RowCounts[key] = 0
	return e.saveCatalogLocked()
}

func (e *PageStorageEngine) writeSchemaLocked(db, table string, schema *TableSchema) error {
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return err
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

	t := &pageTable{heap: hf, schema: &schema}
	if cache {
		e.tables[key] = t
	}
	return t, nil
}

func (e *PageStorageEngine) DropTable(dbName, tableName string) error {
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
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return nil, err
	}
	copied := *t.schema
	copied.Columns = append([]ColumnSchema(nil), t.schema.Columns...)
	return &copied, nil
}

// ── Сканирование ──────────────────────────────────────────────────────────

// pageIDAt переводит сквозной номер страницы в PageID (сегмент + страница).
func pageIDAt(global uint32) page.PageID {
	return page.PageID{
		SegmentNo: uint16(global / page.PagesPerSegment),
		PageNo:    global % page.PagesPerSegment,
	}
}

type tupleVisitor func(pid page.PageID, pg *page.Page, slot uint16, createdTx, deletedTx uint64, row Row) (stop bool, err error)

// scanTuples обходит все кортежи таблицы в порядке страниц/слотов.
func (e *PageStorageEngine) scanTuples(t *pageTable, visit tupleVisitor) error {
	total, err := t.heap.PageCount()
	if err != nil {
		return err
	}
	for g := uint32(0); g < total; g++ {
		pid := pageIDAt(g)
		var pg page.Page
		if err := t.heap.ReadPage(pid, &pg); err != nil {
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
				return err
			}
			stop, err := visit(pid, &pg, slot, createdTx, deletedTx, row)
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	}
	return nil
}

// ── Запись ────────────────────────────────────────────────────────────────

// appendTuplesLocked добавляет кортежи в конец таблицы; вызывается под write-локом.
func (e *PageStorageEngine) appendTuplesLocked(t *pageTable, tuples [][]byte) error {
	total, err := t.heap.PageCount()
	if err != nil {
		return err
	}

	var pid page.PageID
	var pg page.Page
	havePage := false
	if total > 0 {
		pid = pageIDAt(total - 1)
		if err := t.heap.ReadPage(pid, &pg); err != nil {
			return err
		}
		havePage = true
	}

	dirty := false
	flush := func() error {
		if havePage && dirty {
			if err := t.heap.WritePage(pid, &pg); err != nil {
				return err
			}
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
				pid, pg, havePage = newPid, *newPg, true
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

	if err := e.appendTuplesLocked(t, tuples); err != nil {
		return 0, err
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
			dirty = false
		}
		return nil
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
	}
	if err := e.saveCatalogLocked(); err != nil {
		return 0, err
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
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
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
	all, err := e.readRows(dbName, tableName, 0)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(positions))
	for _, p := range positions {
		if p >= 0 && p < len(all) {
			rows = append(rows, all[p])
		}
	}
	return rows, nil
}

func (e *PageStorageEngine) CountRows(dbName, tableName string) (int, error) {
	rows, err := e.readRows(dbName, tableName, 0)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
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
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
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
		if len(row) > 0 && reflect.DeepEqual(row[0], pk) {
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

	total, err := t.heap.PageCount()
	if err != nil {
		return nil, err
	}

	rowsBefore, rowsAfter := 0, 0
	for g := uint32(0); g < total; g++ {
		pid := pageIDAt(g)
		var pg page.Page
		if err := t.heap.ReadPage(pid, &pg); err != nil {
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
				return nil, err
			}
		}
		if err := t.heap.WritePage(pid, &pg); err != nil {
			return nil, err
		}
	}
	if err := t.heap.Sync(); err != nil {
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
	e.mu.Lock()
	defer e.mu.Unlock()

	t, err := e.getTableLocked(dbName, tableName, true)
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
func (e *PageStorageEngine) rewriteTable(db, table string, newSchema *TableSchema, transform func(Row) Row) error {
	t, err := e.getTableLocked(db, table, true)
	if err != nil {
		return err
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
	return e.rewriteTable(dbName, tableName, &newSchema, func(row Row) Row {
		return append(append(Row(nil), row...), normalizedDefault)
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
	newSchema := *t.schema
	newSchema.Columns = append([]ColumnSchema(nil), t.schema.Columns...)
	for i := range newSchema.Columns {
		if strings.EqualFold(newSchema.Columns[i].Name, oldName) {
			newSchema.Columns[i].Name = newName
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("column '%s' does not exist", oldName)
	}
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

// ── Индексы (не поддерживаются страничным движком) ────────────────────────

func (e *PageStorageEngine) CreateIndex(dbName, tableName, indexName, column string) error {
	return fmt.Errorf("page storage engine does not support secondary indexes yet; use storage.engine: json")
}

func (e *PageStorageEngine) DropIndex(dbName, indexName string) error {
	return fmt.Errorf("page storage engine does not support secondary indexes yet; use storage.engine: json")
}

func (e *PageStorageEngine) ListIndexes(dbName, tableName string) ([]string, error) {
	return []string{}, nil
}

func (e *PageStorageEngine) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	return "", false
}

func (e *PageStorageEngine) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	return nil, false
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
