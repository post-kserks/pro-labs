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

	"vaultdb/internal/wal"
)

type FileStorageEngine struct {
	rootDir string

	tableLocks   map[string]*sync.RWMutex
	tableLocksMu sync.Mutex

	globalMu sync.RWMutex

	wal                *wal.WAL
	walErr             error
	opsSinceCheckpoint int
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

func NewFileStorageEngine(rootDir string) *FileStorageEngine {
	s := &FileStorageEngine{
		rootDir:            rootDir,
		tableLocks:         make(map[string]*sync.RWMutex),
		checkpointInterval: 100,
		opsSinceCheckpoint: 0,
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
	if len(schema.Columns) == 0 {
		return fmt.Errorf("table '%s' must have at least one column", schema.Name)
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
	txID, err := s.appendWAL(wal.OpInsert, payload)
	if err != nil {
		return 0, err
	}

	affected, err := s.applyInsertLocked(data, normalizedRows, txID, false)
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

	s.maybeCheckpoint()
	return affected, nil
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
	txID, err := s.appendWAL(wal.OpUpdate, payload)
	if err != nil {
		return 0, err
	}

	affected, err := s.applyUpdateLocked(data, schema, indices, normalizedUpdates, txID, false)
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
	txID, err := s.appendWAL(wal.OpDelete, payload)
	if err != nil {
		return 0, err
	}

	affected, err := s.applyDeleteLocked(data, indices, txID, false)
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
	for _, entry := range log.Entries {
		entryTs, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			continue
		}
		if (entryTs.Equal(target) || entryTs.Before(target)) && entry.TxID > maxTx {
			maxTx = entry.TxID
		}
	}
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

func (s *FileStorageEngine) FinalCheckpoint() error {
	if s.wal == nil {
		return nil
	}
	return s.wal.Checkpoint()
}

func (s *FileStorageEngine) Close() error {
	if s.wal == nil {
		return nil
	}
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

	default:
		return fmt.Errorf("unknown WAL op: 0x%02X", entry.OpType)
	}
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

	affected, err := s.applyInsertLocked(data, p.Rows, txID, true)
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
	affected, err := s.applyUpdateLocked(data, schema, p.Indices, normalizedUpdates, txID, true)
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

	affected, err := s.applyDeleteLocked(data, p.Indices, txID, true)
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

func (s *FileStorageEngine) applyInsertLocked(data *tableDataDisk, rows [][]interface{}, txID uint64, idempotent bool) (int, error) {
	if idempotent {
		for _, row := range data.Rows {
			if row.CreatedTx == txID {
				return 0, nil
			}
		}
	}

	for _, row := range rows {
		data.Rows = append(data.Rows, versionedRowDisk{
			CreatedTx: txID,
			DeletedTx: 0,
			Data:      append([]interface{}(nil), row...),
		})
	}

	if data.NextSeq <= 0 {
		data.NextSeq = 1
	}
	data.NextSeq += len(rows)
	return len(rows), nil
}

func (s *FileStorageEngine) applyUpdateLocked(
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

	for _, physicalIdx := range targets {
		old := &data.Rows[physicalIdx]
		old.DeletedTx = txID

		newData := append([]interface{}(nil), old.Data...)
		for colIdx, value := range updates {
			if colIdx < 0 || colIdx >= len(schema.Columns) {
				return 0, fmt.Errorf("column index %d out of range", colIdx)
			}
			newData[colIdx] = value
		}

		data.Rows = append(data.Rows, versionedRowDisk{
			CreatedTx: txID,
			DeletedTx: 0,
			Data:      newData,
		})
	}

	return len(targets), nil
}

func (s *FileStorageEngine) applyDeleteLocked(data *tableDataDisk, indices []int, txID uint64, idempotent bool) (int, error) {
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

	for _, physicalIdx := range targets {
		data.Rows[physicalIdx].DeletedTx = txID
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

	s.tableLocksMu.Lock()
	delete(s.tableLocks, tableLockKey(dbName, tableName))
	s.tableLocksMu.Unlock()
	return nil
}

func (s *FileStorageEngine) appendWAL(opType byte, payload interface{}) (uint64, error) {
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

	s.opsSinceCheckpoint++
	if s.opsSinceCheckpoint < s.checkpointInterval {
		return
	}

	if err := s.wal.Checkpoint(); err != nil {
		slog.Warn("WAL checkpoint failed", "error", err)
		return
	}
	s.opsSinceCheckpoint = 0
}

func (s *FileStorageEngine) readTxLog(dbName string) (*txLogDisk, error) {
	path := s.txLogPath(dbName)
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &txLogDisk{Entries: []txLogEntryDisk{}}, nil
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
	return &log, nil
}

func (s *FileStorageEngine) appendTxLog(dbName string, entry TxLogEntry) error {
	log, err := s.readTxLog(dbName)
	if err != nil {
		return err
	}

	for _, existing := range log.Entries {
		if existing.TxID == entry.TxID {
			return nil
		}
	}

	log.Entries = append(log.Entries, txLogEntryDisk{
		TxID:      entry.TxID,
		Timestamp: entry.Timestamp.UTC().Format(time.RFC3339Nano),
		Op:        entry.Op,
		Table:     entry.Table,
	})
	sort.Slice(log.Entries, func(i, j int) bool { return log.Entries[i].TxID < log.Entries[j].TxID })

	return writeJSONAtomic(s.txLogPath(dbName), log)
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
	return converted, nil
}

func (s *FileStorageEngine) writeVersionedData(dbName, tableName string, data *tableDataDisk) error {
	if data.Version == 0 {
		data.Version = 2
	}
	if data.NextSeq <= 0 {
		data.NextSeq = 1
	}
	if err := writeJSONAtomic(s.dataPath(dbName, tableName), data); err != nil {
		return fmt.Errorf("write table data: %w", err)
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
