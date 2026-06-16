package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

	dataCache   map[string]*tableDataDisk
	dataDirty   map[string]bool
	dataCacheMu sync.RWMutex

	txLogCache map[string]*txLogDisk
	txLogDirty map[string]bool
	txLogMu    sync.Mutex

	walGate sync.RWMutex

	metrics            *metrics.Collector
	wal                *wal.WAL
	walErr             error
	opsSinceCheckpoint atomic.Int64
	checkpointInterval int
	fallbackTxID       atomic.Uint64
}

func validateObjectName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("object name cannot be empty")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("object name contains invalid path separator: %s", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("object name contains invalid path traversal: %s", name)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("object name contains null byte: %s", name)
	}
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
		slog.Error("WAL unavailable — crash recovery disabled. "+
			"Server will continue but data may be lost on power failure.",
			"error", err, "path", walPath)
		return s
	}

	s.wal = w
	if err := s.recoverFromWAL(); err != nil {
		s.walErr = err
		slog.Error("WAL recovery failed — data may be inconsistent",
			"error", err, "path", walPath)
	}

	return s
}

func (s *FileStorageEngine) CreateDatabase(name string) error {
	if err := validateObjectName(name); err != nil {
		return err
	}
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
	if err := validateObjectName(name); err != nil {
		return err
	}

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
	defer s.globalMu.RUnlock()

	dbPath := s.dbDir(dbName)
	if !dirExists(dbPath) {
		return nil, fmt.Errorf("database '%s' does not exist", dbName)
	}

	entries, err := os.ReadDir(dbPath)
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
	if err := validateObjectName(dbName); err != nil {
		return err
	}
	if err := validateObjectName(schema.Name); err != nil {
		return err
	}

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
	if err := validateObjectName(dbName); err != nil {
		return err
	}
	if err := validateObjectName(tableName); err != nil {
		return err
	}

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

	refReader := func(dbName, tableName string) ([]Row, error) {
		refSchema, err := s.readSchema(dbName, tableName)
		if err != nil {
			return nil, err
		}
		refData, err := s.readVersionedData(dbName, tableName, refSchema)
		if err != nil {
			return nil, err
		}
		rows := make([]Row, 0, len(refData.Rows))
		for _, vr := range refData.Rows {
			if vr.DeletedTx == 0 {
				row := make(Row, len(vr.Data))
				for i, v := range vr.Data {
					row[i] = v
				}
				rows = append(rows, row)
			}
		}
		return rows, nil
	}
	if err := validateConstraintsRaw(schema, normalizedRows, existingRowsFromData(data), refReader); err != nil {
		return 0, err
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
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.RLock()
	cached := s.dataCache[key]
	s.dataCacheMu.RUnlock()

	if cached != nil {
		count := 0
		for _, row := range cached.Rows {
			if row.DeletedTx == 0 {
				count++
			}
		}
		return count, nil
	}

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

	if err := s.flushTable(dbName, tableName); err != nil {
		return nil, fmt.Errorf("vacuum: flush: %w", err)
	}

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
		idx = index.New(indexName, column, colIdx)
	}

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
	_ = s.loadIndexesMetadata(db, table, mgr)
	return mgr
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

	schema, err := s.readSchema(db, table)
	if err != nil {
		slog.Warn("loadIndexesMetadata: read schema", "db", db, "table", table, "error", err)
	}
	data, err := s.readVersionedData(db, table, schema)
	if err != nil {
		slog.Warn("loadIndexesMetadata: read data", "db", db, "table", table, "error", err)
		return nil
	}
	rows := s.diskToIndexableRows(data)

	for _, m := range meta.Indexes {
		idx := index.NewByType(m.Name, m.Column, m.ColIndex, m.Type)
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
			Type:     idx.Type(),
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
	s.walGate.Lock()
	defer s.walGate.Unlock()
	var firstErr error
	if err := s.flushDataDirty(); err != nil {
		firstErr = err
	}
	if err := s.flushTxLogDirty(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.wal.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
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

	s.indexesMu.Lock()
	prefix := name + "/"
	for key := range s.indexes {
		if strings.HasPrefix(key, prefix) {
			delete(s.indexes, key)
		}
	}
	s.indexesMu.Unlock()

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
	if err := writeDataJSONAtomic(s.dataPath(dbName, schema.Name), tableDataDisk{
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
		return
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
		s.txLogMu.Lock()
		defer s.txLogMu.Unlock()
		if errors.Is(err, os.ErrNotExist) {
			log := &txLogDisk{Entries: []txLogEntryDisk{}}
			s.txLogCache[dbName] = log
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
	defer s.txLogMu.Unlock()
	result := &log
	if cached := s.txLogCache[dbName]; cached != nil {
		result = cached
	} else {
		s.txLogCache[dbName] = result
	}
	return result, nil
}

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
