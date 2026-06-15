package storage

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"vaultdb/internal/index"
	"vaultdb/internal/wal"
)

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

	normalizedDefault, err := normalizeValue(defaultVal, col)
	if err != nil {
		return err
	}
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

	if mgr := s.getOrCreateIndexManager(dbName, tableName); mgr != nil {
		changed := false
		for _, idx := range mgr.All() {
			if strings.EqualFold(idx.Column(), oldName) {
				mgr.RenameColumn(idx.Name(), newName)
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

	if err := s.flushTable(dbName, oldName); err != nil {
		return fmt.Errorf("flush table before rename: %w", err)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename table directory: %w", err)
	}

	schema, err := s.readSchema(dbName, newName)
	if err == nil {
		schema.Name = newName
		if err := writeJSONAtomic(s.schemaPath(dbName, newName), schema); err != nil {
			slog.Warn("applyAlterTableRenameTableLocked: write schema", "error", err)
		}
	}

	s.cacheEvict(dbName, oldName)
	s.tableLocksMu.Lock()
	delete(s.tableLocks, tableLockKey(dbName, oldName))
	s.tableLocksMu.Unlock()

	return nil
}
