package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/index"
	"vaultdb/internal/metrics"
	"vaultdb/internal/wal"
)

type FileStorageEngine struct {
	rootDir string

	tableLocks   map[string]*sync.RWMutex
	tableLocksMu sync.Mutex

	globalMu sync.RWMutex

	indexes   map[string]*index.IndexManager
	indexesMu sync.RWMutex

	// dataCache holds the parsed, coerced contents of each table's _data.json
	// keyed by tableLockKey(db, table). It removes the per-operation cost of
	// reading and JSON-decoding the entire table file on every read/insert/
	// lookup. Entry contents are mutated only while the caller holds the
	// per-table lock; the map itself is guarded by dataCacheMu.
	//
	// dataDirty marks tables whose cached contents have not yet been written
	// to disk. Disk writes are deferred to checkpoint time (flushDataDirty)
	// so a stream of single-row inserts costs O(rows) total instead of
	// O(rows^2) — the WAL is the durable record between checkpoints.
	dataCache   map[string]*tableDataDisk
	dataDirty   map[string]bool
	dataCacheMu sync.RWMutex

	// txLogCache caches each database's _tx_log.json so appends are O(1)
	// (tx IDs are monotonic, so entries stay sorted) instead of rewriting the
	// whole log on every mutation. txLogDirty mirrors dataDirty for the log.
	txLogCache map[string]*txLogDisk
	txLogDirty map[string]bool
	txLogMu    sync.Mutex

	// walGate serializes WAL truncation against in-flight WAL-logged ops.
	// Each mutation holds RLock across append→apply→mark-dirty; a checkpoint
	// takes Lock so that, once it runs, every appended record's effect is
	// marked dirty and can be flushed to disk before the WAL is truncated.
	walGate sync.RWMutex

	metrics            *metrics.Collector
	wal                *wal.WAL
	walErr             error
	opsSinceCheckpoint atomic.Int64
	checkpointInterval int
	fallbackTxID       atomic.Uint64
}

type databaseMeta struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type versionedRowDisk struct {
	CreatedTx uint64        `json:"_vdb_created_tx"`
	DeletedTx uint64        `json:"_vdb_deleted_tx"`
	Data      []interface{} `json:"data"`
}

type tableDataDisk struct {
	Version int                `json:"version"`
	NextSeq int                `json:"next_seq"`
	Rows    []versionedRowDisk `json:"rows"`
}

type legacyTableDataDisk struct {
	Rows   [][]interface{} `json:"rows"`
	NextID int             `json:"next_id"`
}

type txLogEntryDisk struct {
	TxID      uint64 `json:"tx_id"`
	Timestamp string `json:"timestamp"`
	Op        string `json:"op"`
	Table     string `json:"table"`
}

type txLogDisk struct {
	Entries []txLogEntryDisk `json:"entries"`
}

type walCreateDatabasePayload struct {
	Name string `json:"name"`
}

type walDropDatabasePayload struct {
	Name string `json:"name"`
}

type walCreateTablePayload struct {
	DB     string      `json:"db"`
	Schema TableSchema `json:"schema"`
}

type walDropTablePayload struct {
	DB    string `json:"db"`
	Table string `json:"table"`
}

type walInsertPayload struct {
	DB    string          `json:"db"`
	Table string          `json:"table"`
	Rows  [][]interface{} `json:"rows"`
	Ts    string          `json:"ts"`
}

type walUpdatePayload struct {
	DB      string                 `json:"db"`
	Table   string                 `json:"table"`
	Indices []int                  `json:"indices"`
	Updates map[string]interface{} `json:"updates"`
	Ts      string                 `json:"ts"`
}

type walDeletePayload struct {
	DB      string `json:"db"`
	Table   string `json:"table"`
	Indices []int  `json:"indices"`
	Ts      string `json:"ts"`
}

type walVacuumPayload struct {
	DB    string `json:"db"`
	Table string `json:"table"`
}

type walAlterTablePayload struct {
	DB         string       `json:"db"`
	Table      string       `json:"table"`
	Op         string       `json:"op"` // ADD_COLUMN, DROP_COLUMN, RENAME_COLUMN, RENAME_TABLE
	Column     ColumnSchema `json:"column,omitempty"`
	DefaultVal interface{}  `json:"default_val,omitempty"`
	OldName    string       `json:"old_name,omitempty"`
	NewName    string       `json:"new_name,omitempty"`
}

func (s *FileStorageEngine) AlterTableAddColumn(dbName, tableName string, col ColumnSchema, defaultVal Value) error {
	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	payload := walAlterTablePayload{
		DB:         dbName,
		Table:      tableName,
		Op:         "ADD_COLUMN",
		Column:     col,
		DefaultVal: defaultVal,
	}

	_, err := s.withWALGate(func() (int, error) {
		if _, err := s.appendWAL(wal.OpAlterTable, payload); err != nil {
			return 0, err
		}
		return 0, s.applyAlterTableAddColumnLocked(dbName, tableName, col, defaultVal)
	})
	return err
}

func (s *FileStorageEngine) applyAlterTableAddColumnLocked(dbName, tableName string, col ColumnSchema, defaultVal interface{}) error {
	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return err
	}

	for _, c := range schema.Columns {
		if strings.EqualFold(c.Name, col.Name) {
			return fmt.Errorf("column '%s' already exists in table '%s'", col.Name, tableName)
		}
	}

	schema.Columns = append(schema.Columns, col)
	if err := writeJSONAtomic(s.schemaPath(dbName, tableName), schema); err != nil {
		return err
	}

	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return err
	}

	// Add default value to all existing rows
	normalizedDefault, _ := normalizeValue(defaultVal, col)
	for i := range data.Rows {
		data.Rows[i].Data = append(data.Rows[i].Data, normalizedDefault)
	}

	return s.writeVersionedData(dbName, tableName, data)
}

func (s *FileStorageEngine) AlterTableDropColumn(dbName, tableName string, colName string) error {
	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	payload := walAlterTablePayload{
		DB:     dbName,
		Table:  tableName,
		Op:     "DROP_COLUMN",
		Column: ColumnSchema{Name: colName},
	}

	_, err := s.withWALGate(func() (int, error) {
		if _, err := s.appendWAL(wal.OpAlterTable, payload); err != nil {
			return 0, err
		}
		return 0, s.applyAlterTableDropColumnLocked(dbName, tableName, colName)
	})
	return err
}

func (s *FileStorageEngine) applyAlterTableDropColumnLocked(dbName, tableName string, colName string) error {
	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return err
	}

	colIdx := -1
	for i, c := range schema.Columns {
		if strings.EqualFold(c.Name, colName) {
			colIdx = i
			break
		}
	}

	if colIdx == -1 {
		return fmt.Errorf("column '%s' not found in table '%s'", colName, tableName)
	}

	if len(schema.Columns) <= 1 {
		return fmt.Errorf("cannot drop the last column of table '%s'", tableName)
	}

	schema.Columns = append(schema.Columns[:colIdx], schema.Columns[colIdx+1:]...)
	if err := writeJSONAtomic(s.schemaPath(dbName, tableName), schema); err != nil {
		return err
	}

	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return err
	}

	for i := range data.Rows {
		row := data.Rows[i].Data
		data.Rows[i].Data = append(row[:colIdx], row[colIdx+1:]...)
	}

	if err := s.writeVersionedData(dbName, tableName, data); err != nil {
		return err
	}

	// Dropping a column invalidates the index on it and shifts every index on
	// a later column left by one position.
	if mgr := s.getOrCreateIndexManager(dbName, tableName); mgr != nil {
		changed := false
		rows := s.diskToIndexableRows(data)
		for _, idx := range mgr.All() {
			switch {
			case idx.ColIndex() == colIdx:
				mgr.Remove(idx.Name())
				changed = true
			case idx.ColIndex() > colIdx:
				mgr.Remove(idx.Name())
				shifted := index.New(idx.Name(), idx.Column(), idx.ColIndex()-1)
				shifted.Rebuild(rows)
				mgr.Add(shifted)
				changed = true
			}
		}
		if changed {
			if err := s.saveIndexesMetadata(dbName, tableName, mgr); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *FileStorageEngine) AlterTableRenameColumn(dbName, tableName, oldName, newName string) error {
	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	payload := walAlterTablePayload{
		DB:      dbName,
		Table:   tableName,
		Op:      "RENAME_COLUMN",
		OldName: oldName,
		NewName: newName,
	}

	_, err := s.withWALGate(func() (int, error) {
		if _, err := s.appendWAL(wal.OpAlterTable, payload); err != nil {
			return 0, err
		}
		return 0, s.applyAlterTableRenameColumnLocked(dbName, tableName, oldName, newName)
	})
	return err
}

func (s *FileStorageEngine) applyAlterTableRenameColumnLocked(dbName, tableName, oldName, newName string) error {
	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return err
	}

	found := false
	for i, c := range schema.Columns {
		if strings.EqualFold(c.Name, oldName) {
			schema.Columns[i].Name = newName
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("column '%s' not found in table '%s'", oldName, tableName)
	}

	if err := writeJSONAtomic(s.schemaPath(dbName, tableName), schema); err != nil {
		return err
	}

	// Keep index metadata pointing at the renamed column.
	if mgr := s.getOrCreateIndexManager(dbName, tableName); mgr != nil {
		changed := false
		for _, idx := range mgr.All() {
			if strings.EqualFold(idx.Column(), oldName) {
				idx.SetColumn(newName)
				changed = true
			}
		}
		if changed {
			if err := s.saveIndexesMetadata(dbName, tableName, mgr); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *FileStorageEngine) AlterTableRenameTable(dbName, oldName, newName string) error {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	payload := walAlterTablePayload{
		DB:      dbName,
		Op:      "RENAME_TABLE",
		OldName: oldName,
		NewName: newName,
	}

	_, err := s.withWALGate(func() (int, error) {
		if _, err := s.appendWAL(wal.OpAlterTable, payload); err != nil {
			return 0, err
		}
		return 0, s.applyAlterTableRenameTableLocked(dbName, oldName, newName)
	})
	return err
}

func (s *FileStorageEngine) applyAlterTableRenameTableLocked(dbName, oldName, newName string) error {
	oldPath := s.tableDir(dbName, oldName)
	newPath := s.tableDir(dbName, newName)

	if !dirExists(oldPath) {
		return fmt.Errorf("table '%s' does not exist", oldName)
	}
	if dirExists(newPath) {
		return fmt.Errorf("table '%s' already exists", newName)
	}

	// Flush dirty data before renaming to avoid data loss
	if err := s.flushTable(dbName, oldName); err != nil {
		return fmt.Errorf("flush table before rename: %w", err)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename table directory: %w", err)
	}

	// Update schema metadata
	schema, err := s.readSchema(dbName, newName)
	if err == nil {
		schema.Name = newName
		_ = writeJSONAtomic(s.schemaPath(dbName, newName), schema)
	}

	// Update caches and locks
	s.cacheEvict(dbName, oldName)
	s.tableLocksMu.Lock()
	delete(s.tableLocks, tableLockKey(dbName, oldName))
	s.tableLocksMu.Unlock()

	return nil
}

func NewFileStorageEngine(rootDir string, m *metrics.Collector) *FileStorageEngine {
	s := &FileStorageEngine{
		rootDir:            rootDir,
		tableLocks:         make(map[string]*sync.RWMutex),
		indexes:            make(map[string]*index.IndexManager),
		dataCache:          make(map[string]*tableDataDisk),
		dataDirty:          make(map[string]bool),
		txLogCache:         make(map[string]*txLogDisk),
		txLogDirty:         make(map[string]bool),
		metrics:            m,
		checkpointInterval: 100,
	}

	_ = os.MkdirAll(s.databasesDir(), 0o755)
	walPath := filepath.Join(rootDir, "wal", "vaultdb.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		s.walErr = err
		slog.Warn("WAL unavailable, running without crash recovery", "error", err)
		return s
	}

	s.wal = w
	if err := s.recoverFromWAL(); err != nil {
		s.walErr = err
		slog.Warn("WAL recovery failed, continuing", "error", err)
	}

	return s
}

func (s *FileStorageEngine) CreateDatabase(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("database name cannot be empty")
	}

	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	path := s.dbDir(name)
	if dirExists(path) {
		return fmt.Errorf("database '%s' already exists", name)
	}

	if _, err := s.appendWAL(wal.OpCreateDatabase, walCreateDatabasePayload{Name: name}); err != nil {
		return err
	}
	if err := s.createDatabaseInternal(name); err != nil {
		return err
	}

	s.maybeCheckpoint()
	return nil
}

func (s *FileStorageEngine) DropDatabase(name string) error {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	path := s.dbDir(name)
	if !dirExists(path) {
		return fmt.Errorf("database '%s' does not exist", name)
	}

	if _, err := s.appendWAL(wal.OpDropDatabase, walDropDatabasePayload{Name: name}); err != nil {
		return err
	}
	if err := s.dropDatabaseInternal(name); err != nil {
		return err
	}

	s.maybeCheckpoint()
	return nil
}

func (s *FileStorageEngine) DatabaseExists(name string) bool {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	return dirExists(s.dbDir(name))
}

func (s *FileStorageEngine) ListDatabases() ([]string, error) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()

	entries, err := os.ReadDir(s.databasesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read databases directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *FileStorageEngine) ListTables(dbName string) ([]TableInfo, error) {
	s.globalMu.RLock()
	dbPath := s.dbDir(dbName)
	if !dirExists(dbPath) {
		s.globalMu.RUnlock()
		return nil, fmt.Errorf("database '%s' does not exist", dbName)
	}

	entries, err := os.ReadDir(dbPath)
	s.globalMu.RUnlock()
	if err != nil {
		return nil, fmt.Errorf("read database '%s' directory: %w", dbName, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	tables := make([]TableInfo, 0, len(names))
	for _, name := range names {
		count, err := s.CountRows(dbName, name)
		if err != nil {
			return nil, err
		}
		schema, err := s.GetTableSchema(dbName, name)
		if err != nil {
			return nil, err
		}
		tables = append(tables, TableInfo{
			Name:      name,
			RowCount:  count,
			CreatedAt: schema.CreatedAt,
		})
	}
	return tables, nil
}

func (s *FileStorageEngine) CreateTable(dbName string, schema TableSchema) error {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	if !dirExists(s.dbDir(dbName)) {
		return fmt.Errorf("database '%s' does not exist", dbName)
	}
	if strings.TrimSpace(schema.Name) == "" {
		return fmt.Errorf("table name cannot be empty")
	}

	tablePath := s.tableDir(dbName, schema.Name)
	if dirExists(tablePath) {
		return fmt.Errorf("table '%s' already exists", schema.Name)
	}

	schema.Database = dbName
	if schema.CreatedAt.IsZero() {
		schema.CreatedAt = time.Now().UTC()
	}

	if _, err := s.appendWAL(wal.OpCreateTable, walCreateTablePayload{DB: dbName, Schema: schema}); err != nil {
		return err
	}
	if err := s.createTableInternal(dbName, schema); err != nil {
		return err
	}

	s.maybeCheckpoint()
	return nil
}

func (s *FileStorageEngine) DropTable(dbName, tableName string) error {
	s.globalMu.Lock()
	defer s.globalMu.Unlock()

	if !dirExists(s.dbDir(dbName)) {
		return fmt.Errorf("database '%s' does not exist", dbName)
	}
	path := s.tableDir(dbName, tableName)
	if !dirExists(path) {
		return fmt.Errorf("table '%s' does not exist", tableName)
	}

	if _, err := s.appendWAL(wal.OpDropTable, walDropTablePayload{DB: dbName, Table: tableName}); err != nil {
		return err
	}
	if err := s.dropTableInternal(dbName, tableName); err != nil {
		return err
	}

	s.maybeCheckpoint()
	return nil
}

func (s *FileStorageEngine) TableExists(dbName, tableName string) bool {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	return dirExists(s.tableDir(dbName, tableName))
}

func (s *FileStorageEngine) GetTableSchema(dbName, tableName string) (*TableSchema, error) {
	s.globalMu.RLock()
	defer s.globalMu.RUnlock()
	return s.readSchema(dbName, tableName)
}

func (s *FileStorageEngine) InsertRows(dbName, tableName string, rows []Row) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return 0, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return 0, err
	}

	normalizedRows := make([][]interface{}, 0, len(rows))
	for _, row := range rows {
		if len(row) != len(schema.Columns) {
			return 0, fmt.Errorf("invalid row width for table '%s': expected %d values, got %d", tableName, len(schema.Columns), len(row))
		}
		normalized := make([]interface{}, len(row))
		for i, value := range row {
			n, err := normalizeValue(value, schema.Columns[i])
			if err != nil {
				return 0, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
			}
			normalized[i] = n
		}
		normalizedRows = append(normalizedRows, normalized)
	}

	ts := time.Now().UTC()
	payload := walInsertPayload{
		DB:    dbName,
		Table: tableName,
		Rows:  normalizedRows,
		Ts:    ts.Format(time.RFC3339Nano),
	}
	affected, err := s.withWALGate(func() (int, error) {
		txID, err := s.appendWAL(wal.OpInsert, payload)
		if err != nil {
			return 0, err
		}
		affected, err := s.applyInsertLocked(dbName, tableName, data, normalizedRows, txID, false)
		if err != nil {
			return 0, err
		}
		if err := s.writeVersionedData(dbName, tableName, data); err != nil {
			return 0, err
		}
		if err := s.appendTxLog(dbName, TxLogEntry{
			TxID:      txID,
			Timestamp: ts,
			Op:        "INSERT",
			Table:     tableName,
		}); err != nil {
			return 0, err
		}
		return affected, nil
	})
	if err != nil {
		return 0, err
	}

	s.maybeCheckpoint()
	return affected, nil
}

// withWALGate runs fn while holding walGate.RLock, the section spanning a
// mutation's WAL append, in-memory apply, and mark-dirty. Holding RLock across
// these steps lets a concurrent checkpoint (walGate.Lock) know that every
// appended WAL record's effect is already cached and dirty before it flushes
// and truncates the log.
func (s *FileStorageEngine) withWALGate(fn func() (int, error)) (int, error) {
	s.walGate.RLock()
	defer s.walGate.RUnlock()
	return fn()
}

func (s *FileStorageEngine) SelectRows(dbName, tableName string) ([]Row, error) {
	return s.ReadCurrentRows(dbName, tableName)
}

func (s *FileStorageEngine) ReadCurrentRows(dbName, tableName string) ([]Row, error) {
	lock := s.getTableLock(dbName, tableName)
	lock.RLock()
	defer lock.RUnlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return nil, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(data.Rows))
	for _, vrow := range data.Rows {
		if vrow.DeletedTx != 0 {
			continue
		}
		rows = append(rows, interfaceSliceToRow(vrow.Data))
	}
	return rows, nil
}

func (s *FileStorageEngine) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]Row, error) {
	lock := s.getTableLock(dbName, tableName)
	lock.RLock()
	defer lock.RUnlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return nil, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(data.Rows))
	for _, vrow := range data.Rows {
		createdBefore := vrow.CreatedTx <= txID
		notDeletedYet := vrow.DeletedTx == 0 || vrow.DeletedTx > txID
		if !createdBefore || !notDeletedYet {
			continue
		}
		rows = append(rows, interfaceSliceToRow(vrow.Data))
	}
	return rows, nil
}

func (s *FileStorageEngine) ReadRowsByPositions(dbName, tableName string, positions []int) ([]Row, error) {
	lock := s.getTableLock(dbName, tableName)
	lock.RLock()
	defer lock.RUnlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return nil, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(positions))
	for _, pos := range positions {
		if pos < 0 || pos >= len(data.Rows) {
			continue
		}
		vrow := data.Rows[pos]
		if vrow.DeletedTx != 0 {
			continue
		}
		rows = append(rows, interfaceSliceToRow(vrow.Data))
	}
	return rows, nil
}

func (s *FileStorageEngine) CountRows(dbName, tableName string) (int, error) {
	rows, err := s.ReadCurrentRows(dbName, tableName)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func (s *FileStorageEngine) UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error) {
	if len(indices) == 0 {
		return 0, nil
	}

	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return 0, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return 0, err
	}

	normalizedUpdates, updatesForWAL, err := buildNormalizedUpdates(schema, updates)
	if err != nil {
		return 0, err
	}

	ts := time.Now().UTC()
	payload := walUpdatePayload{
		DB:      dbName,
		Table:   tableName,
		Indices: append([]int(nil), indices...),
		Updates: updatesForWAL,
		Ts:      ts.Format(time.RFC3339Nano),
	}
	affected, err := s.withWALGate(func() (int, error) {
		txID, err := s.appendWAL(wal.OpUpdate, payload)
		if err != nil {
			return 0, err
		}
		affected, err := s.applyUpdateLocked(dbName, tableName, data, schema, indices, normalizedUpdates, txID, false)
		if err != nil {
			return 0, err
		}
		if err := s.writeVersionedData(dbName, tableName, data); err != nil {
			return 0, err
		}
		if err := s.appendTxLog(dbName, TxLogEntry{
			TxID:      txID,
			Timestamp: ts,
			Op:        "UPDATE",
			Table:     tableName,
		}); err != nil {
			return 0, err
		}
		return affected, nil
	})
	if err != nil {
		return 0, err
	}

	s.maybeCheckpoint()
	return affected, nil
}

func (s *FileStorageEngine) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	if len(indices) == 0 {
		return 0, nil
	}

	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return 0, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return 0, err
	}

	ts := time.Now().UTC()
	payload := walDeletePayload{
		DB:      dbName,
		Table:   tableName,
		Indices: append([]int(nil), indices...),
		Ts:      ts.Format(time.RFC3339Nano),
	}
	affected, err := s.withWALGate(func() (int, error) {
		txID, err := s.appendWAL(wal.OpDelete, payload)
		if err != nil {
			return 0, err
		}
		affected, err := s.applyDeleteLocked(dbName, tableName, data, indices, txID, false)
		if err != nil {
			return 0, err
		}
		if err := s.writeVersionedData(dbName, tableName, data); err != nil {
			return 0, err
		}
		if err := s.appendTxLog(dbName, TxLogEntry{
			TxID:      txID,
			Timestamp: ts,
			Op:        "DELETE",
			Table:     tableName,
		}); err != nil {
			return 0, err
		}
		return affected, nil
	})
	if err != nil {
		return 0, err
	}

	s.maybeCheckpoint()
	return affected, nil
}

func (s *FileStorageEngine) TxIDAtTimestamp(dbName, ts string) (uint64, error) {
	log, err := s.readTxLog(dbName)
	if err != nil {
		return 0, err
	}

	target, err := parseTimestampFlexible(ts)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q: %w", ts, err)
	}

	var maxTx uint64
	s.txLogMu.Lock()
	for _, entry := range log.Entries {
		entryTs, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			continue
		}
		if (entryTs.Equal(target) || entryTs.Before(target)) && entry.TxID > maxTx {
			maxTx = entry.TxID
		}
	}
	s.txLogMu.Unlock()
	return maxTx, nil
}

func (s *FileStorageEngine) RowHistory(dbName, tableName string, pkValue interface{}) ([]VersionedRow, error) {
	lock := s.getTableLock(dbName, tableName)
	lock.RLock()
	defer lock.RUnlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return nil, err
	}
	if len(schema.Columns) == 0 {
		return []VersionedRow{}, nil
	}

	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return nil, err
	}

	pkNormalized, err := normalizeValue(pkValue, schema.Columns[0])
	if err != nil {
		return nil, fmt.Errorf("invalid key value: %w", err)
	}

	out := make([]VersionedRow, 0, 8)
	for _, row := range data.Rows {
		if len(row.Data) == 0 {
			continue
		}
		if !valuesEqual(row.Data[0], pkNormalized) {
			continue
		}
		out = append(out, VersionedRow{
			CreatedTx: row.CreatedTx,
			DeletedTx: row.DeletedTx,
			Data:      interfaceSliceToRow(row.Data),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedTx < out[j].CreatedTx })
	return out, nil
}

func (s *FileStorageEngine) Vacuum(dbName, tableName string) (*VacuumStats, error) {
	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	start := time.Now()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return nil, err
	}

	dataPath := s.dataPath(dbName, tableName)
	statBefore, err := os.Stat(dataPath)
	if err != nil {
		return nil, fmt.Errorf("vacuum: stat: %w", err)
	}

	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return nil, fmt.Errorf("vacuum: load: %w", err)
	}

	rowsBefore := len(data.Rows)

	var liveRows []versionedRowDisk
	for _, row := range data.Rows {
		if row.DeletedTx == 0 {
			liveRows = append(liveRows, row)
		}
	}

	if _, err := s.withWALGate(func() (int, error) {
		data.Rows = liveRows
		if err := s.writeVersionedData(dbName, tableName, data); err != nil {
			return 0, fmt.Errorf("vacuum: write: %w", err)
		}
		if _, err := s.appendWAL(wal.OpVacuum, walVacuumPayload{
			DB:    dbName,
			Table: tableName,
		}); err != nil {
			return 0, err
		}
		return 0, nil
	}); err != nil {
		return nil, err
	}

	// Persist immediately so the reclaimed file size is reflected on disk
	// (VACUUM reports it) rather than waiting for the next checkpoint.
	if err := s.flushTable(dbName, tableName); err != nil {
		return nil, fmt.Errorf("vacuum: flush: %w", err)
	}

	// Rebuild indices after vacuum
	if mgr := s.getOrCreateIndexManager(dbName, tableName); mgr != nil {
		rows := s.diskToIndexableRows(data)
		for _, idx := range mgr.All() {
			idx.Rebuild(rows)
		}
	}

	statAfter, _ := os.Stat(dataPath)

	return &VacuumStats{
		TableName:      tableName,
		RowsBefore:     rowsBefore,
		RowsAfter:      len(liveRows),
		ReclaimedRows:  rowsBefore - len(liveRows),
		FileSizeBefore: statBefore.Size(),
		FileSizeAfter:  statAfter.Size(),
		DurationMs:     float64(time.Since(start).Microseconds()) / 1000.0,
	}, nil
}

func (s *FileStorageEngine) TableVersionStats(dbName, tableName string) (*TableVersionStats, error) {
	lock := s.getTableLock(dbName, tableName)
	lock.RLock()
	defer lock.RUnlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return nil, err
	}
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return nil, err
	}

	total := len(data.Rows)
	dead := 0
	for _, row := range data.Rows {
		if row.DeletedTx != 0 {
			dead++
		}
	}

	return &TableVersionStats{
		TotalRows: total,
		DeadRows:  dead,
	}, nil
}

func (s *FileStorageEngine) TableModifiedSince(db, table string, txID uint64) (bool, error) {
	log, err := s.readTxLog(db)
	if err != nil {
		return false, err
	}

	s.txLogMu.Lock()
	defer s.txLogMu.Unlock()
	for _, entry := range log.Entries {
		if strings.EqualFold(entry.Table, table) && entry.TxID > txID {
			return true, nil
		}
	}
	return false, nil
}

func (s *FileStorageEngine) CreateIndex(dbName, tableName, indexName, column string) error {
	lock := s.getTableLock(dbName, tableName)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(dbName, tableName)
	if err != nil {
		return err
	}

	colIdx := -1
	for i, col := range schema.Columns {
		if strings.EqualFold(col.Name, column) {
			colIdx = i
			break
		}
	}
	if colIdx == -1 {
		return fmt.Errorf("column '%s' not found in table '%s'", column, tableName)
	}

	mgr := s.getOrCreateIndexManager(dbName, tableName)
	if _, ok := mgr.FindForColumn(column); ok {
		// MVP: only one index per column
		return fmt.Errorf("index already exists for column '%s' in table '%s'", column, tableName)
	}

	idx := index.New(indexName, column, colIdx)
	data, err := s.readVersionedData(dbName, tableName, schema)
	if err != nil {
		return err
	}

	idx.Rebuild(s.diskToIndexableRows(data))
	mgr.Add(idx)

	return s.saveIndexesMetadata(dbName, tableName, mgr)
}

func (s *FileStorageEngine) DropIndex(dbName, indexName string) error {
	s.indexesMu.Lock()
	// Try memory first
	for key, mgr := range s.indexes {
		if !strings.HasPrefix(key, dbName+"/") {
			continue
		}
		if mgr.Has(indexName) {
			s.indexesMu.Unlock()
			tableName := strings.TrimPrefix(key, dbName+"/")
			lock := s.getTableLock(dbName, tableName)
			lock.Lock()
			defer lock.Unlock()

			mgr.Remove(indexName)
			return s.saveIndexesMetadata(dbName, tableName, mgr)
		}
	}
	s.indexesMu.Unlock()

	// Also check tables on disk that are not in memory yet
	tables, err := s.ListTables(dbName)
	if err == nil {
		for _, t := range tables {
			mgr := s.getOrCreateIndexManager(dbName, t.Name)
			if mgr.Has(indexName) {
				lock := s.getTableLock(dbName, t.Name)
				lock.Lock()
				defer lock.Unlock()

				mgr.Remove(indexName)
				return s.saveIndexesMetadata(dbName, t.Name, mgr)
			}
		}
	}

	return fmt.Errorf("index '%s' not found", indexName)
}

func (s *FileStorageEngine) ListIndexes(dbName, tableName string) ([]string, error) {
	mgr := s.getOrCreateIndexManager(dbName, tableName)
	indexes := mgr.All()
	names := make([]string, len(indexes))
	for i, idx := range indexes {
		names[i] = idx.Name()
	}
	return names, nil
}

func (s *FileStorageEngine) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	mgr := s.getOrCreateIndexManager(dbName, tableName)
	idx, ok := mgr.FindForColumn(column)
	if !ok {
		return "", false
	}
	return idx.Name(), true
}

func (s *FileStorageEngine) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	mgr := s.getOrCreateIndexManager(dbName, tableName)
	idx, ok := mgr.FindForColumn(column)
	if !ok {
		if s.metrics != nil {
			s.metrics.IncIndexMiss()
		}
		return nil, false
	}
	res, found := idx.Lookup(value)
	if s.metrics != nil {
		if found {
			s.metrics.IncIndexHit()
		} else {
			s.metrics.IncIndexMiss()
		}
	}
	return res, found
}

func (s *FileStorageEngine) getOrCreateIndexManager(db, table string) *index.IndexManager {
	key := tableLockKey(db, table)
	s.indexesMu.Lock()
	defer s.indexesMu.Unlock()
	if mgr, ok := s.indexes[key]; ok {
		return mgr
	}
	mgr := index.NewManager()
	s.indexes[key] = mgr
	// Try to load existing indexes metadata
	_ = s.loadIndexesMetadata(db, table, mgr)
	return mgr
}

type indexMeta struct {
	Name     string `json:"name"`
	Column   string `json:"column"`
	ColIndex int    `json:"col_index"`
}

type indexesMetadata struct {
	Indexes []indexMeta `json:"indexes"`
}

func (s *FileStorageEngine) loadIndexesMetadata(db, table string, mgr *index.IndexManager) error {
	path := filepath.Join(s.tableDir(db, table), ".indexes.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var meta indexesMetadata
	if err := json.Unmarshal(bytes, &meta); err != nil {
		return err
	}

	schema, _ := s.readSchema(db, table)
	data, _ := s.readVersionedData(db, table, schema)
	rows := s.diskToIndexableRows(data)

	for _, m := range meta.Indexes {
		idx := index.New(m.Name, m.Column, m.ColIndex)
		idx.Rebuild(rows)
		mgr.Add(idx)
	}
	return nil
}

func (s *FileStorageEngine) saveIndexesMetadata(db, table string, mgr *index.IndexManager) error {
	var meta indexesMetadata
	for _, idx := range mgr.All() {
		meta.Indexes = append(meta.Indexes, indexMeta{
			Name:     idx.Name(),
			Column:   idx.Column(),
			ColIndex: idx.ColIndex(),
		})
	}

	path := filepath.Join(s.tableDir(db, table), ".indexes.json")
	return writeJSONAtomic(path, meta)
}

func (s *FileStorageEngine) diskToIndexableRows(data *tableDataDisk) []index.IndexableRow {
	res := make([]index.IndexableRow, len(data.Rows))
	for i, r := range data.Rows {
		res[i] = index.IndexableRow{
			DeletedTx: r.DeletedTx,
			Data:      r.Data,
		}
	}
	return res
}

func (s *FileStorageEngine) CurrentTxID() uint64 {
	if s.wal != nil {
		return s.wal.CurrentTxID()
	}
	return s.fallbackTxID.Load()
}

func (s *FileStorageEngine) FinalCheckpoint() error {
	if s.wal == nil {
		return nil
	}
	s.walGate.Lock()
	defer s.walGate.Unlock()
	if err := s.flushDataDirty(); err != nil {
		return err
	}
	if err := s.flushTxLogDirty(); err != nil {
		return err
	}
	return s.wal.Checkpoint()
}

func (s *FileStorageEngine) Close() error {
	if s.wal == nil {
		return nil
	}
	// Persist any deferred writes before closing. The WAL is left intact (not
	// truncated), so anything not yet flushed is still recoverable on restart.
	s.walGate.Lock()
	if err := s.flushDataDirty(); err != nil {
		slog.Warn("flush data on close failed", "error", err)
	}
	if err := s.flushTxLogDirty(); err != nil {
		slog.Warn("flush tx log on close failed", "error", err)
	}
	s.walGate.Unlock()
	return s.wal.Close()
}

func (s *FileStorageEngine) recoverFromWAL() error {
	if s.wal == nil {
		return nil
	}

	entries, err := s.wal.Recover()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		slog.Info("WAL: nothing to recover")
		return nil
	}

	slog.Info("WAL recovery started", "entries", len(entries))
	replayed := 0
	for _, entry := range entries {
		if err := s.replayWALEntry(entry); err != nil {
			slog.Warn("WAL replay error", "tx_id", entry.TxID, "op_type", entry.OpType, "error", err)
			continue
		}
		replayed++
	}

	// Replay only marks tables/tx logs dirty in the cache; flush them to disk
	// before truncating the WAL, otherwise recovered data would be lost.
	if err := s.flushDataDirty(); err != nil {
		slog.Warn("flush data after recovery failed", "error", err)
		return nil
	}
	if err := s.flushTxLogDirty(); err != nil {
		slog.Warn("flush tx log after recovery failed", "error", err)
		return nil
	}
	_ = s.wal.Checkpoint()
	slog.Info("WAL recovery complete", "total_entries", len(entries), "replayed", replayed)
	return nil
}

func (s *FileStorageEngine) replayWALEntry(entry wal.Entry) error {
	switch entry.OpType {
	case wal.OpCreateDatabase:
		var p walCreateDatabasePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.createDatabaseInternal(p.Name)

	case wal.OpDropDatabase:
		var p walDropDatabasePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.dropDatabaseInternal(p.Name)

	case wal.OpCreateTable:
		var p walCreateTablePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.createTableInternal(p.DB, p.Schema)

	case wal.OpDropTable:
		var p walDropTablePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.dropTableInternal(p.DB, p.Table)

	case wal.OpInsert:
		var p walInsertPayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayInsert(entry.TxID, p)

	case wal.OpUpdate:
		var p walUpdatePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayUpdate(entry.TxID, p)

	case wal.OpDelete:
		var p walDeletePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayDelete(entry.TxID, p)

	case wal.OpVacuum:
		var p walVacuumPayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayVacuum(p)

	case wal.OpAlterTable:
		var p walAlterTablePayload
		if err := json.Unmarshal(entry.Payload, &p); err != nil {
			return err
		}
		return s.replayAlterTable(p)

	default:
		return fmt.Errorf("unknown WAL op: 0x%02X", entry.OpType)
	}
}

func (s *FileStorageEngine) replayAlterTable(p walAlterTablePayload) error {
	switch p.Op {
	case "ADD_COLUMN":
		lock := s.getTableLock(p.DB, p.Table)
		lock.Lock()
		defer lock.Unlock()
		return s.applyAlterTableAddColumnLocked(p.DB, p.Table, p.Column, p.DefaultVal)
	case "DROP_COLUMN":
		lock := s.getTableLock(p.DB, p.Table)
		lock.Lock()
		defer lock.Unlock()
		return s.applyAlterTableDropColumnLocked(p.DB, p.Table, p.Column.Name)
	case "RENAME_COLUMN":
		lock := s.getTableLock(p.DB, p.Table)
		lock.Lock()
		defer lock.Unlock()
		return s.applyAlterTableRenameColumnLocked(p.DB, p.Table, p.OldName, p.NewName)
	case "RENAME_TABLE":
		s.globalMu.Lock()
		defer s.globalMu.Unlock()
		return s.applyAlterTableRenameTableLocked(p.DB, p.OldName, p.NewName)
	default:
		return fmt.Errorf("unknown ALTER TABLE op: %s", p.Op)
	}
}

func (s *FileStorageEngine) replayVacuum(p walVacuumPayload) error {
	_, err := s.Vacuum(p.DB, p.Table)
	return err
}

func (s *FileStorageEngine) replayInsert(txID uint64, p walInsertPayload) error {
	lock := s.getTableLock(p.DB, p.Table)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(p.DB, p.Table)
	if err != nil {
		return err
	}
	data, err := s.readVersionedData(p.DB, p.Table, schema)
	if err != nil {
		return err
	}

	for _, row := range p.Rows {
		if len(row) != len(schema.Columns) {
			return fmt.Errorf("replay insert width mismatch for table '%s'", p.Table)
		}
	}

	affected, err := s.applyInsertLocked(p.DB, p.Table, data, p.Rows, txID, true)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if err := s.writeVersionedData(p.DB, p.Table, data); err != nil {
		return err
	}
	ts := parsePayloadTimestamp(p.Ts)
	return s.appendTxLog(p.DB, TxLogEntry{TxID: txID, Timestamp: ts, Op: "INSERT", Table: p.Table})
}

func (s *FileStorageEngine) replayUpdate(txID uint64, p walUpdatePayload) error {
	lock := s.getTableLock(p.DB, p.Table)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(p.DB, p.Table)
	if err != nil {
		return err
	}
	data, err := s.readVersionedData(p.DB, p.Table, schema)
	if err != nil {
		return err
	}

	normalizedUpdates, _, err := buildNormalizedUpdatesFromInterfaces(schema, p.Updates)
	if err != nil {
		return err
	}
	affected, err := s.applyUpdateLocked(p.DB, p.Table, data, schema, p.Indices, normalizedUpdates, txID, true)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if err := s.writeVersionedData(p.DB, p.Table, data); err != nil {
		return err
	}
	ts := parsePayloadTimestamp(p.Ts)
	return s.appendTxLog(p.DB, TxLogEntry{TxID: txID, Timestamp: ts, Op: "UPDATE", Table: p.Table})
}

func (s *FileStorageEngine) replayDelete(txID uint64, p walDeletePayload) error {
	lock := s.getTableLock(p.DB, p.Table)
	lock.Lock()
	defer lock.Unlock()

	schema, err := s.readSchema(p.DB, p.Table)
	if err != nil {
		return err
	}
	data, err := s.readVersionedData(p.DB, p.Table, schema)
	if err != nil {
		return err
	}

	affected, err := s.applyDeleteLocked(p.DB, p.Table, data, p.Indices, txID, true)
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil
	}
	if err := s.writeVersionedData(p.DB, p.Table, data); err != nil {
		return err
	}
	ts := parsePayloadTimestamp(p.Ts)
	return s.appendTxLog(p.DB, TxLogEntry{TxID: txID, Timestamp: ts, Op: "DELETE", Table: p.Table})
}

func (s *FileStorageEngine) applyInsertLocked(db, table string, data *tableDataDisk, rows [][]interface{}, txID uint64, idempotent bool) (int, error) {
	if idempotent {
		for _, row := range data.Rows {
			if row.CreatedTx == txID {
				return 0, nil
			}
		}
	}

	startPos := len(data.Rows)
	for _, row := range rows {
		data.Rows = append(data.Rows, versionedRowDisk{
			CreatedTx: txID,
			DeletedTx: 0,
			Data:      append([]interface{}(nil), row...),
		})
	}

	// Update indices
	if mgr := s.getOrCreateIndexManager(db, table); mgr != nil {
		for i, row := range rows {
			for _, idx := range mgr.All() {
				if idx.ColIndex() < len(row) {
					key := index.ValueToIndexKey(row[idx.ColIndex()])
					idx.Insert(key, startPos+i)
				}
			}
		}
	}

	if data.NextSeq <= 0 {
		data.NextSeq = 1
	}
	data.NextSeq += len(rows)
	return len(rows), nil
}

func (s *FileStorageEngine) applyUpdateLocked(
	db, table string,
	data *tableDataDisk,
	schema *TableSchema,
	indices []int,
	updates map[int]interface{},
	txID uint64,
	idempotent bool,
) (int, error) {
	if idempotent {
		for _, row := range data.Rows {
			if row.CreatedTx == txID {
				return 0, nil
			}
		}
	}

	targets, err := resolveCurrentRowIndices(data, indices)
	if err != nil {
		return 0, err
	}

	mgr := s.getOrCreateIndexManager(db, table)

	for _, physicalIdx := range targets {
		old := &data.Rows[physicalIdx]
		old.DeletedTx = txID

		// Remove from index
		if mgr != nil {
			for _, idx := range mgr.All() {
				idx.Delete(physicalIdx)
			}
		}

		newData := append([]interface{}(nil), old.Data...)
		for colIdx, value := range updates {
			if colIdx < 0 || colIdx >= len(schema.Columns) {
				return 0, fmt.Errorf("column index %d out of range", colIdx)
			}
			newData[colIdx] = value
		}

		newPhysicalIdx := len(data.Rows)
		data.Rows = append(data.Rows, versionedRowDisk{
			CreatedTx: txID,
			DeletedTx: 0,
			Data:      newData,
		})

		// Add to index
		if mgr != nil {
			for _, idx := range mgr.All() {
				if idx.ColIndex() < len(newData) {
					key := index.ValueToIndexKey(newData[idx.ColIndex()])
					idx.Insert(key, newPhysicalIdx)
				}
			}
		}
	}

	return len(targets), nil
}

func (s *FileStorageEngine) applyDeleteLocked(db, table string, data *tableDataDisk, indices []int, txID uint64, idempotent bool) (int, error) {
	if idempotent {
		for _, row := range data.Rows {
			if row.DeletedTx == txID {
				return 0, nil
			}
		}
	}

	targets, err := resolveCurrentRowIndices(data, indices)
	if err != nil {
		return 0, err
	}

	mgr := s.getOrCreateIndexManager(db, table)

	for _, physicalIdx := range targets {
		data.Rows[physicalIdx].DeletedTx = txID
		// Remove from index
		if mgr != nil {
			for _, idx := range mgr.All() {
				idx.Delete(physicalIdx)
			}
		}
	}

	return len(targets), nil
}

func resolveCurrentRowIndices(data *tableDataDisk, indices []int) ([]int, error) {
	if len(indices) == 0 {
		return nil, nil
	}

	uniqueRequested := make(map[int]struct{}, len(indices))
	for _, idx := range indices {
		if idx < 0 {
			return nil, fmt.Errorf("row index %d out of range", idx)
		}
		uniqueRequested[idx] = struct{}{}
	}

	targets := make([]int, 0, len(uniqueRequested))
	currentPos := 0
	for physicalIdx, row := range data.Rows {
		if row.DeletedTx != 0 {
			continue
		}
		if _, ok := uniqueRequested[currentPos]; ok {
			targets = append(targets, physicalIdx)
		}
		currentPos++
	}

	if len(targets) != len(uniqueRequested) {
		return nil, fmt.Errorf("row index out of range")
	}
	sort.Ints(targets)
	return targets, nil
}

func buildNormalizedUpdates(schema *TableSchema, updates map[string]Value) (map[int]interface{}, map[string]interface{}, error) {
	columnIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = i
	}

	byIndex := make(map[int]interface{}, len(updates))
	byName := make(map[string]interface{}, len(updates))
	for colName, value := range updates {
		idx, ok := columnIndex[strings.ToLower(colName)]
		if !ok {
			return nil, nil, fmt.Errorf("unknown column '%s'", colName)
		}
		normalized, err := normalizeValue(value, schema.Columns[idx])
		if err != nil {
			return nil, nil, fmt.Errorf("column '%s': %w", schema.Columns[idx].Name, err)
		}
		byIndex[idx] = normalized
		byName[schema.Columns[idx].Name] = normalized
	}
	return byIndex, byName, nil
}

func buildNormalizedUpdatesFromInterfaces(schema *TableSchema, updates map[string]interface{}) (map[int]interface{}, map[string]interface{}, error) {
	tmp := make(map[string]Value, len(updates))
	for k, v := range updates {
		tmp[k] = v
	}
	return buildNormalizedUpdates(schema, tmp)
}

func (s *FileStorageEngine) createDatabaseInternal(name string) error {
	path := s.dbDir(name)
	if dirExists(path) {
		return nil
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}

	meta := databaseMeta{Name: name, CreatedAt: time.Now().UTC()}
	if err := writeJSONAtomic(filepath.Join(path, "_meta.json"), meta); err != nil {
		return fmt.Errorf("write database metadata: %w", err)
	}
	return nil
}

func (s *FileStorageEngine) dropDatabaseInternal(name string) error {
	path := s.dbDir(name)
	if !dirExists(path) {
		return nil
	}

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("drop database '%s': %w", name, err)
	}

	s.cacheEvictDatabase(name)
	s.tableLocksMu.Lock()
	for key := range s.tableLocks {
		if strings.HasPrefix(key, name+"/") {
			delete(s.tableLocks, key)
		}
	}
	s.tableLocksMu.Unlock()

	return nil
}

func (s *FileStorageEngine) createTableInternal(dbName string, schema TableSchema) error {
	tablePath := s.tableDir(dbName, schema.Name)
	if dirExists(tablePath) {
		return nil
	}

	if err := os.MkdirAll(tablePath, 0o755); err != nil {
		return fmt.Errorf("create table directory: %w", err)
	}

	schema.Database = dbName
	if schema.CreatedAt.IsZero() {
		schema.CreatedAt = time.Now().UTC()
	}

	if err := writeJSONAtomic(s.schemaPath(dbName, schema.Name), schema); err != nil {
		return fmt.Errorf("write schema: %w", err)
	}
	if err := writeJSONAtomic(s.dataPath(dbName, schema.Name), tableDataDisk{
		Version: 2,
		NextSeq: 1,
		Rows:    []versionedRowDisk{},
	}); err != nil {
		return fmt.Errorf("write table data: %w", err)
	}

	s.getTableLock(dbName, schema.Name)
	return nil
}

func (s *FileStorageEngine) dropTableInternal(dbName, tableName string) error {
	path := s.tableDir(dbName, tableName)
	if !dirExists(path) {
		return nil
	}

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("drop table '%s': %w", tableName, err)
	}

	s.cacheEvict(dbName, tableName)
	s.tableLocksMu.Lock()
	delete(s.tableLocks, tableLockKey(dbName, tableName))
	s.tableLocksMu.Unlock()
	return nil
}

func (s *FileStorageEngine) appendWAL(opType byte, payload interface{}) (uint64, error) {
	if s.metrics != nil {
		s.metrics.IncWALEntries()
	}
	if s.wal != nil {
		txID, err := s.wal.Append(opType, payload)
		if err != nil {
			return 0, fmt.Errorf("wal append: %w", err)
		}
		return txID, nil
	}
	return s.fallbackTxID.Add(1), nil
}

// maybeCheckpoint flushes deferred writes and truncates the WAL once enough
// operations have accumulated. It must be called outside the walGate RLock
// section (the caller's per-table lock may still be held — flushing does not
// acquire per-table locks). Taking walGate.Lock guarantees no mutation is
// mid-flight, so every appended WAL record's effect is already marked dirty and
// is persisted by the flush before the WAL is truncated.
func (s *FileStorageEngine) maybeCheckpoint() {
	if s.wal == nil {
		return
	}

	if s.opsSinceCheckpoint.Add(1) < int64(s.checkpointInterval) {
		return
	}

	s.walGate.Lock()
	defer s.walGate.Unlock()

	if s.opsSinceCheckpoint.Load() < int64(s.checkpointInterval) {
		return // another goroutine already checkpointed
	}

	if err := s.flushDataDirty(); err != nil {
		slog.Warn("flush data before checkpoint failed", "error", err)
		return
	}
	if err := s.flushTxLogDirty(); err != nil {
		slog.Warn("flush tx log before checkpoint failed", "error", err)
		return
	}
	if err := s.wal.Checkpoint(); err != nil {
		slog.Warn("WAL checkpoint failed", "error", err)
		return
	}
	if s.metrics != nil {
		s.metrics.IncCheckpoints()
	}
	s.opsSinceCheckpoint.Store(0)
}

// readTxLog returns the database's tx log, loading and caching it from disk on
// the first access. The returned pointer is shared with the cache, so callers
// that iterate Entries must hold txLogMu (see TxIDAtTimestamp / TableModifiedSince).
func (s *FileStorageEngine) readTxLog(dbName string) (*txLogDisk, error) {
	s.txLogMu.Lock()
	if cached := s.txLogCache[dbName]; cached != nil {
		s.txLogMu.Unlock()
		return cached, nil
	}
	s.txLogMu.Unlock()

	path := s.txLogPath(dbName)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log := &txLogDisk{Entries: []txLogEntryDisk{}}
			s.txLogMu.Lock()
			if cached := s.txLogCache[dbName]; cached != nil {
				log = cached
			} else {
				s.txLogCache[dbName] = log
			}
			s.txLogMu.Unlock()
			return log, nil
		}
		return nil, fmt.Errorf("read tx log: %w", err)
	}

	var log txLogDisk
	if err := json.Unmarshal(bytes, &log); err != nil {
		return nil, fmt.Errorf("decode tx log: %w", err)
	}
	if log.Entries == nil {
		log.Entries = []txLogEntryDisk{}
	}

	s.txLogMu.Lock()
	result := &log
	if cached := s.txLogCache[dbName]; cached != nil {
		result = cached
	} else {
		s.txLogCache[dbName] = result
	}
	s.txLogMu.Unlock()
	return result, nil
}

// appendTxLog adds an entry to the cached tx log and marks it dirty. The disk
// write is deferred to the next checkpoint (flushTxLogDirty). Tx IDs are
// monotonic so the common path is an O(1) append that keeps Entries sorted; a
// linear de-dup scan runs only for out-of-order ids (WAL replay).
func (s *FileStorageEngine) appendTxLog(dbName string, entry TxLogEntry) error {
	log, err := s.readTxLog(dbName)
	if err != nil {
		return err
	}

	s.txLogMu.Lock()
	defer s.txLogMu.Unlock()

	if n := len(log.Entries); n > 0 && entry.TxID <= log.Entries[n-1].TxID {
		for _, existing := range log.Entries {
			if existing.TxID == entry.TxID {
				return nil
			}
		}
	}

	log.Entries = append(log.Entries, txLogEntryDisk{
		TxID:      entry.TxID,
		Timestamp: entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Op:        entry.Op,
		Table:     entry.Table,
	})
	if n := len(log.Entries); n >= 2 && log.Entries[n-1].TxID < log.Entries[n-2].TxID {
		sort.Slice(log.Entries, func(i, j int) bool { return log.Entries[i].TxID < log.Entries[j].TxID })
	}
	s.txLogDirty[dbName] = true
	return nil
}

func (s *FileStorageEngine) databasesDir() string {
	return filepath.Join(s.rootDir, "databases")
}

func (s *FileStorageEngine) dbDir(dbName string) string {
	return filepath.Join(s.databasesDir(), dbName)
}

func (s *FileStorageEngine) tableDir(dbName, tableName string) string {
	return filepath.Join(s.dbDir(dbName), tableName)
}

func (s *FileStorageEngine) schemaPath(dbName, tableName string) string {
	return filepath.Join(s.tableDir(dbName, tableName), "_schema.json")
}

func (s *FileStorageEngine) dataPath(dbName, tableName string) string {
	return filepath.Join(s.tableDir(dbName, tableName), "_data.json")
}

func (s *FileStorageEngine) txLogPath(dbName string) string {
	return filepath.Join(s.dbDir(dbName), "_tx_log.json")
}

func (s *FileStorageEngine) getTableLock(dbName, tableName string) *sync.RWMutex {
	key := tableLockKey(dbName, tableName)
	s.tableLocksMu.Lock()
	defer s.tableLocksMu.Unlock()
	if lock, ok := s.tableLocks[key]; ok {
		return lock
	}
	lock := &sync.RWMutex{}
	s.tableLocks[key] = lock
	return lock
}

func (s *FileStorageEngine) readSchema(dbName, tableName string) (*TableSchema, error) {
	path := s.schemaPath(dbName, tableName)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("table '%s' does not exist", tableName)
		}
		return nil, fmt.Errorf("read schema for table '%s': %w", tableName, err)
	}

	var schema TableSchema
	if err := json.Unmarshal(bytes, &schema); err != nil {
		return nil, fmt.Errorf("decode schema for table '%s': %w", tableName, err)
	}
	return &schema, nil
}

func (s *FileStorageEngine) readVersionedData(dbName, tableName string, schema *TableSchema) (*tableDataDisk, error) {
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.RLock()
	cached := s.dataCache[key]
	s.dataCacheMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	path := s.dataPath(dbName, tableName)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("table '%s' data does not exist", tableName)
		}
		return nil, fmt.Errorf("read data for table '%s': %w", tableName, err)
	}

	var data tableDataDisk
	if err := json.Unmarshal(bytes, &data); err == nil {
		if data.Version == 0 {
			data.Version = 2
		}
		for i, row := range data.Rows {
			coerced, err := coerceRow(row.Data, schema)
			if err != nil {
				return nil, fmt.Errorf("coerce row %d in table '%s': %w", i, tableName, err)
			}
			data.Rows[i].Data = rowToInterfaceSlice(coerced)
		}
		s.cacheStore(key, &data)
		return &data, nil
	}

	var legacy legacyTableDataDisk
	if err := json.Unmarshal(bytes, &legacy); err != nil {
		return nil, fmt.Errorf("decode data for table '%s': %w", tableName, err)
	}

	converted := &tableDataDisk{
		Version: 2,
		NextSeq: legacy.NextID,
		Rows:    make([]versionedRowDisk, 0, len(legacy.Rows)),
	}
	for i, row := range legacy.Rows {
		coerced, err := coerceRow(row, schema)
		if err != nil {
			return nil, fmt.Errorf("coerce legacy row %d in table '%s': %w", i, tableName, err)
		}
		converted.Rows = append(converted.Rows, versionedRowDisk{
			CreatedTx: 1,
			DeletedTx: 0,
			Data:      rowToInterfaceSlice(coerced),
		})
	}
	if converted.NextSeq <= 0 {
		converted.NextSeq = len(converted.Rows) + 1
	}
	s.cacheStore(key, converted)
	return converted, nil
}

// cacheStore records the table's parsed data under the given lock key.
// Callers hold the per-table lock, so the entry contents stay consistent.
func (s *FileStorageEngine) cacheStore(key string, data *tableDataDisk) {
	s.dataCacheMu.Lock()
	s.dataCache[key] = data
	s.dataCacheMu.Unlock()
}

// cacheEvict drops a single table's cached data (used when the table is dropped).
func (s *FileStorageEngine) cacheEvict(dbName, tableName string) {
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.Lock()
	delete(s.dataCache, key)
	delete(s.dataDirty, key)
	s.dataCacheMu.Unlock()
}

// cacheEvictDatabase drops every cached table and the tx log belonging to a database.
func (s *FileStorageEngine) cacheEvictDatabase(dbName string) {
	prefix := dbName + "/"
	s.dataCacheMu.Lock()
	for key := range s.dataCache {
		if strings.HasPrefix(key, prefix) {
			delete(s.dataCache, key)
			delete(s.dataDirty, key)
		}
	}
	s.dataCacheMu.Unlock()

	s.txLogMu.Lock()
	delete(s.txLogCache, dbName)
	delete(s.txLogDirty, dbName)
	s.txLogMu.Unlock()
}

// writeVersionedData records a table's new contents in the cache and marks it
// dirty. The actual disk write is deferred to the next checkpoint
// (flushDataDirty); the WAL holds the durable record in the meantime. Callers
// run inside the per-table lock and the walGate RLock section.
func (s *FileStorageEngine) writeVersionedData(dbName, tableName string, data *tableDataDisk) error {
	if data.Version == 0 {
		data.Version = 2
	}
	if data.NextSeq <= 0 {
		data.NextSeq = 1
	}
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.Lock()
	s.dataCache[key] = data
	s.dataDirty[key] = true
	s.dataCacheMu.Unlock()
	return nil
}

// flushDataDirty writes every dirty table to disk and clears the dirty set.
// It must run with no concurrent table mutation in progress — either holding
// walGate.Lock (runtime checkpoint) or during single-threaded recovery — so the
// cached pointers it marshals are stable.
func (s *FileStorageEngine) flushDataDirty() error {
	s.dataCacheMu.Lock()
	pending := make(map[string]*tableDataDisk, len(s.dataDirty))
	for key := range s.dataDirty {
		if d := s.dataCache[key]; d != nil {
			pending[key] = d
		}
	}
	s.dataDirty = make(map[string]bool)
	s.dataCacheMu.Unlock()

	for key, data := range pending {
		dbName, tableName := splitLockKey(key)
		if err := writeJSONAtomic(s.dataPath(dbName, tableName), data); err != nil {
			s.dataCacheMu.Lock()
			s.dataDirty[key] = true // retry on the next checkpoint
			s.dataCacheMu.Unlock()
			return fmt.Errorf("flush table data %q: %w", key, err)
		}
	}
	return nil
}

// flushTable writes a single table's cached data to disk immediately and clears
// its dirty flag. Used where the on-disk file must be current right away (e.g.
// VACUUM reporting reclaimed file size). Caller holds the per-table lock.
func (s *FileStorageEngine) flushTable(dbName, tableName string) error {
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.Lock()
	data := s.dataCache[key]
	delete(s.dataDirty, key)
	s.dataCacheMu.Unlock()
	if data == nil {
		return nil
	}
	if err := writeJSONAtomic(s.dataPath(dbName, tableName), data); err != nil {
		s.dataCacheMu.Lock()
		s.dataDirty[key] = true
		s.dataCacheMu.Unlock()
		return err
	}
	return nil
}

// flushTxLogDirty writes every dirty tx log to disk and clears the dirty set.
// Same concurrency contract as flushDataDirty.
func (s *FileStorageEngine) flushTxLogDirty() error {
	s.txLogMu.Lock()
	pending := make(map[string]*txLogDisk, len(s.txLogDirty))
	for dbName := range s.txLogDirty {
		if l := s.txLogCache[dbName]; l != nil {
			pending[dbName] = l
		}
	}
	s.txLogDirty = make(map[string]bool)
	s.txLogMu.Unlock()

	for dbName, log := range pending {
		if err := writeJSONAtomic(s.txLogPath(dbName), log); err != nil {
			s.txLogMu.Lock()
			s.txLogDirty[dbName] = true
			s.txLogMu.Unlock()
			return fmt.Errorf("flush tx log %q: %w", dbName, err)
		}
	}
	return nil
}

func rowToInterfaceSlice(row Row) []interface{} {
	out := make([]interface{}, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}

func interfaceSliceToRow(values []interface{}) Row {
	out := make(Row, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func coerceRow(raw []interface{}, schema *TableSchema) (Row, error) {
	if len(raw) != len(schema.Columns) {
		return nil, fmt.Errorf("row width mismatch: expected %d, got %d", len(schema.Columns), len(raw))
	}
	row := make(Row, len(raw))
	for i, cell := range raw {
		v, err := coerceValue(cell, schema.Columns[i])
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
		}
		row[i] = v
	}
	return row, nil
}

func coerceValue(raw interface{}, col ColumnSchema) (Value, error) {
	return normalizeValue(raw, col)
}

func normalizeValue(value interface{}, col ColumnSchema) (Value, error) {
	if value == nil {
		return nil, nil
	}

	switch col.Type {
	case "INT":
		intVal, ok := toInt64(value)
		if !ok {
			return nil, fmt.Errorf("expected INT, got %T", value)
		}
		return intVal, nil
	case "FLOAT":
		floatVal, ok := toFloat64(value)
		if !ok {
			return nil, fmt.Errorf("expected FLOAT, got %T", value)
		}
		return floatVal, nil
	case "BOOL":
		boolVal, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected BOOL, got %T", value)
		}
		return boolVal, nil
	case "TEXT", "VARCHAR":
		strVal, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected %s, got %T", col.Type, value)
		}
		if col.Type == "VARCHAR" && col.VarcharLen > 0 {
			if len([]rune(strVal)) > col.VarcharLen {
				return nil, fmt.Errorf("VARCHAR(%d) overflow", col.VarcharLen)
			}
		}
		return strVal, nil
	case "VECTOR":
		// Vectors are stored as []float64 in the Row.
		// Conversion from other formats should have happened in the executor.
		switch v := value.(type) {
		case []float64:
			return v, nil
		case []interface{}:
			res := make([]float64, len(v))
			for i, x := range v {
				switch f := x.(type) {
				case float64:
					res[i] = f
				case int:
					res[i] = float64(f)
				case int64:
					res[i] = float64(f)
				default:
					return nil, fmt.Errorf("VECTOR element must be numeric, got %T", x)
				}
			}
			return res, nil
		default:
			return nil, fmt.Errorf("expected VECTOR ([]float64), got %T", value)
		}
	case "FLEXIBLE":
		switch v := value.(type) {
		case map[string]interface{}:
			return v, nil
		case string:
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(v), &m); err == nil {
				return m, nil
			}
			return v, nil
		default:
			return value, nil
		}
	case "DATE", "TIME", "TIMESTAMP", "DECIMAL":
		return fmt.Sprintf("%v", value), nil
	default:
		return nil, fmt.Errorf("unsupported column type '%s'", col.Type)
	}
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float32:
		f := float64(v)
		if math.Trunc(f) != f {
			return 0, false
		}
		return int64(f), true
	case float64:
		if math.Trunc(v) != v {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

func toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func parseTimestampFlexible(ts string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format")
}

func parsePayloadTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Now().UTC()
	}
	return t.UTC()
}

func valuesEqual(left, right interface{}) bool {
	if left == nil || right == nil {
		return left == right
	}

	if lf, ok := toFloat64(left); ok {
		if rf, ok := toFloat64(right); ok {
			return lf == rf
		}
	}

	switch lv := left.(type) {
	case string:
		rv, ok := right.(string)
		return ok && lv == rv
	case bool:
		rv, ok := right.(bool)
		return ok && lv == rv
	case int64:
		rv, ok := toInt64(right)
		return ok && lv == rv
	default:
		return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right)
	}
}

func writeJSONAtomic(path string, payload interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	bytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, bytes, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func tableLockKey(dbName, tableName string) string {
	return dbName + "/" + tableName
}

func splitLockKey(key string) (dbName, tableName string) {
	parts := strings.SplitN(key, "/", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}
