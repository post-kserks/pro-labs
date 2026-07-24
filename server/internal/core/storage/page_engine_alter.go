package storage

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/core/storage/heap"
	"vaultdb/internal/core/storage/page"
	"vaultdb/internal/core/wal"
)

// ── ALTER TABLE RECOVERY ──────────────────────────────────────────────────

// recoverRewrite cleans up an incomplete ALTER TABLE rewrite for a specific table.
// If a .rewrite.tmp directory exists for the given db/table, it means the rewrite
// was interrupted before the atomic rename completed. The original table data is
// still intact, so we simply remove the stale temp directory.
func (e *PageStorageEngine) recoverRewrite(db, table string) error {
	originalPath := e.tablePath(db, table)
	tmpPath := originalPath + ".rewrite.tmp"

	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		return nil // No incomplete rewrite
	}

	// Incomplete rewrite found — clean up temp directory
	slog.Warn("recovering from incomplete ALTER TABLE rewrite",
		"db", db, "table", table)
	os.RemoveAll(tmpPath)
	return nil
}

// recoverIncompleteRewrites scans the pagedb root directory for any leftover
// .rewrite.tmp directories and removes them. Called during startup to ensure
// the storage engine is in a consistent state after a crash.
func (e *PageStorageEngine) recoverIncompleteRewrites() {
	dbs, err := os.ReadDir(e.rootDir)
	if err != nil {
		return
	}
	for _, dbEntry := range dbs {
		if !dbEntry.IsDir() {
			continue
		}
		dbName := dbEntry.Name()
		dbDir := filepath.Join(e.rootDir, dbName)
		tables, err := os.ReadDir(dbDir)
		if err != nil {
			continue
		}
		for _, tblEntry := range tables {
			if !tblEntry.IsDir() {
				continue
			}
			name := tblEntry.Name()
			if strings.HasSuffix(name, ".rewrite.tmp") {
				tableName := strings.TrimSuffix(name, ".rewrite.tmp")
				if err := e.recoverRewrite(dbName, tableName); err != nil {
					slog.Error("failed to recover rewrite",
						"db", dbName, "table", tableName, "error", err)
				}
			}
		}
	}
}

// ── ALTER TABLE ───────────────────────────────────────────────────────────

// rewriteTable rewrites all live rows of a table using the transform function
// (used by ADD/DROP COLUMN when row arity changes).
// WAL entries OpRewriteBegin/OpRewriteCommit are emitted before start and after completion.
// A safe approach is used: data is written to a temporary directory,
// then atomically replaces the original.
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

	// Evict old pages from the buffer pool so that new pages written to the
	// temp heap (which share the same tableID and therefore the same pageIDs)
	// don't collide with stale cache entries from the original heap.
	e.bufPool.InvalidateTable(t.tableID)

	// Write new data to a temporary directory first (crash-safe approach)
	originalPath := e.tablePath(db, table)
	tmpPath := originalPath + ".rewrite.tmp"
	if err := os.RemoveAll(tmpPath); err != nil && !os.IsNotExist(err) {
		return err
	}

	hf, err := heap.CreateHeapFile(tmpPath)
	if err != nil {
		return err
	}

	txID := e.nextTxLocked()
	tuples := make([][]byte, 0, len(rows))
	for _, row := range rows {
		tuple, err := encodePageTuple(txID, 0, row)
		if err != nil {
			hf.Close()
			os.RemoveAll(tmpPath)
			return err
		}
		tuples = append(tuples, tuple)
	}

	// Create a temporary pageTable for writing
	tmpTable := &pageTable{heap: hf, schema: newSchema, tableID: t.tableID}
	if _, err := e.appendTuplesLocked(tmpTable, tuples, e.nextTxID()); err != nil {
		hf.Close()
		os.RemoveAll(tmpPath)
		return err
	}
	if err := hf.Sync(); err != nil {
		hf.Close()
		os.RemoveAll(tmpPath)
		return err
	}

	// Flush all dirty pages for the temporary heap to disk before closing.
	if err := e.bufPool.FlushAll(); err != nil {
		hf.Close()
		os.RemoveAll(tmpPath)
		return err
	}
	if err := hf.Close(); err != nil {
		os.RemoveAll(tmpPath)
		return err
	}

	// Write schema to temp directory
	tmpSchemaPath := filepath.Join(tmpPath, "_schema.json")
	schemaData, err := json.MarshalIndent(newSchema, "", "  ")
	if err != nil {
		os.RemoveAll(tmpPath)
		return err
	}
	if err := os.WriteFile(tmpSchemaPath, schemaData, 0o600); err != nil {
		os.RemoveAll(tmpPath)
		return err
	}

	// Emit WAL rewrite commit BEFORE atomic rename
	if e.wal != nil {
		rewritePayload := wal.WALRewritePayload{DB: db, Table: table}
		if _, err := e.wal.Append(wal.OpRewriteCommit, rewritePayload); err != nil {
			os.RemoveAll(tmpPath)
			return err
		}
	}

	// Atomically replace: close old heap, rename temp to original
	if err := t.heap.Close(); err != nil {
		os.RemoveAll(tmpPath)
		return err
	}
	e.bufPool.InvalidateTable(t.tableID)

	if err := os.RemoveAll(originalPath); err != nil && !os.IsNotExist(err) {
		os.RemoveAll(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, originalPath); err != nil {
		os.RemoveAll(tmpPath)
		return err
	}

	// Reopen the heap file at the original path
	hf, err = heap.OpenHeapFile(originalPath)
	if err != nil {
		return err
	}
	t.heap = hf
	t.schema = newSchema
	t.invalidatePosDirectory()

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
				mgr.RenameColumn(idx.Name(), oldName, newName)
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

	// Update the name in the schema
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

func (e *PageStorageEngine) SetTableRLS(dbName, tableName string, enabled bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return err
	}
	t.schema.RLSEnabled = enabled
	return e.writeSchemaLocked(dbName, tableName, t.schema)
}

func (e *PageStorageEngine) AddPolicy(dbName, tableName string, policy RLSPolicy) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.getTableLocked(dbName, tableName, true)
	if err != nil {
		return err
	}
	t.schema.Policies = append(t.schema.Policies, policy)
	return e.writeSchemaLocked(dbName, tableName, t.schema)
}
