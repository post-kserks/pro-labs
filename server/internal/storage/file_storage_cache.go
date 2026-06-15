package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

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

func (s *FileStorageEngine) cacheStore(key string, data *tableDataDisk) {
	s.dataCacheMu.Lock()
	s.dataCache[key] = data
	s.dataCacheMu.Unlock()
}

func (s *FileStorageEngine) cacheEvict(dbName, tableName string) {
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.Lock()
	delete(s.dataCache, key)
	delete(s.dataDirty, key)
	s.dataCacheMu.Unlock()
}

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
		if err := writeDataJSONAtomic(s.dataPath(dbName, tableName), data); err != nil {
				s.dataCacheMu.Lock()
				s.dataDirty[key] = true
				s.dataCacheMu.Unlock()
				return fmt.Errorf("flush table data %q: %w", key, err)
			}
	}
	return nil
}

func (s *FileStorageEngine) flushTable(dbName, tableName string) error {
	key := tableLockKey(dbName, tableName)
	s.dataCacheMu.Lock()
	data := s.dataCache[key]
	delete(s.dataDirty, key)
	s.dataCacheMu.Unlock()
	if data == nil {
		return nil
	}
	if err := writeDataJSONAtomic(s.dataPath(dbName, tableName), data); err != nil {
		s.dataCacheMu.Lock()
		s.dataDirty[key] = true
		s.dataCacheMu.Unlock()
		return err
	}
	return nil
}

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
