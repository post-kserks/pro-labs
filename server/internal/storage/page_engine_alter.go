package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/storage/heap"
	"vaultdb/internal/storage/page"
	"vaultdb/internal/wal"
)

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
