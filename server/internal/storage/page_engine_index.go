package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/index"
	"vaultdb/internal/storage/page"
)

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


func (e *PageStorageEngine) saveIndexesMetadata(dbName, tableName string, mgr *index.IndexManager) error {
	indexes := mgr.All()
	meta := make([]struct {
		Name   string `json:"name"`
		Column string `json:"column"`
		ColIdx int    `json:"col_idx"`
		Type   string `json:"type"`
	}, 0, len(indexes))
	for _, idx := range indexes {
		meta = append(meta, struct {
			Name   string `json:"name"`
			Column string `json:"column"`
			ColIdx int    `json:"col_idx"`
			Type   string `json:"type"`
		}{
			Name:   idx.Name(),
			Column: idx.Column(),
			ColIdx: idx.ColIndex(),
			Type:   idx.Type(),
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

// findIndexForColumn returns the Index object for the given column, or nil if none exists.
// Unlike FindIndexForColumn, it does not create an IndexManager if none exists.
func (e *PageStorageEngine) findIndexForColumn(dbName, tableName, column string) (index.Index, bool) {
	e.indexesMu.RLock()
	key := dbName + "/" + tableName
	mgr, ok := e.indexes[key]
	e.indexesMu.RUnlock()
	if !ok {
		return nil, false
	}
	return mgr.FindForColumn(column)
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
