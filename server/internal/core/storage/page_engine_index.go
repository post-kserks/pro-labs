package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/core/index"
	"vaultdb/internal/core/storage/page"
)

// ── Indexes ─────────────────────────────────────────────────────────────────

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
		Unique bool   `json:"unique,omitempty"`
	}, 0, len(indexes))
	for _, idx := range indexes {
		meta = append(meta, struct {
			Name   string `json:"name"`
			Column string `json:"column"`
			ColIdx int    `json:"col_idx"`
			Type   string `json:"type"`
			Unique bool   `json:"unique,omitempty"`
		}{
			Name:   idx.Name(),
			Column: idx.Column(),
			ColIdx: idx.ColIndex(),
			Type:   idx.Type(),
			Unique: idx.IsUnique(),
		})
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(e.indexMetadataPath(dbName, tableName), data, 0o600)
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

func (e *PageStorageEngine) CreateIndexUnique(dbName, tableName, indexName, column, indexType string) error {
	err := e.CreateIndex(dbName, tableName, indexName, column, indexType)
	if err != nil {
		return err
	}
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	if idx, ok := mgr.FindForColumn(column); ok && idx.Name() == indexName {
		idx.SetUnique(true)
		return e.saveIndexesMetadata(dbName, tableName, mgr)
	}
	return nil
}

func (e *PageStorageEngine) CreateIndexMultiUnique(dbName, tableName, indexName string, columns []string) error {
	err := e.CreateIndexMulti(dbName, tableName, indexName, columns)
	if err != nil {
		return err
	}
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	idxs, ok := mgr.FindForColumnMultiple(columns[0])
	if ok {
		for _, idx := range idxs {
			if idx.Name() == indexName {
				idx.SetUnique(true)
				return e.saveIndexesMetadata(dbName, tableName, mgr)
			}
		}
	}
	return nil
}

func (e *PageStorageEngine) CreateIndex(dbName, tableName, indexName, column, indexType string) error {
	e.mu.RLock()
	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		e.mu.RUnlock()
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
		e.mu.RUnlock()
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
	e.mu.RUnlock()

	mgr := e.getOrCreateIndexManager(dbName, tableName)
	if _, ok := mgr.FindForColumn(column); ok {
		return fmt.Errorf("index already exists for column '%s' in table '%s'", column, tableName)
	}

	var idx index.Index
	switch strings.ToUpper(indexType) {
	case "GIN":
		idx = index.NewGINIndex(indexName, column, colIdx)
	case "GIST":
		idx = index.NewGiSTIndex(indexName, column, colIdx)
	case "HASH":
		idx = index.New(indexName, column, colIdx)
	case "BTREE", "":
		// Default: fall back to name-prefix convention when indexType is empty
		if indexType == "" {
			if strings.HasPrefix(indexName, "gin_") {
				idx = index.NewGINIndex(indexName, column, colIdx)
			} else if strings.HasPrefix(indexName, "gin_jsonb_") {
				idx = index.NewGINJSONBIndex(indexName, column, colIdx)
			} else if strings.HasPrefix(indexName, "gist_") {
				idx = index.NewGiSTIndex(indexName, column, colIdx)
			} else {
				idx = index.NewBTreeIndex(indexName, column, colIdx)
			}
		} else {
			idx = index.NewBTreeIndex(indexName, column, colIdx)
		}
	default:
		return fmt.Errorf("unsupported index type: %s", indexType)
	}

	idx.Rebuild(e.rowsToIndexable(rows))
	mgr.Add(idx)

	return e.saveIndexesMetadata(dbName, tableName, mgr)
}

func (e *PageStorageEngine) CreateIndexMulti(dbName, tableName, indexName string, columns []string) error {
	e.mu.RLock()
	t, err := e.getTableLocked(dbName, tableName, false)
	if err != nil {
		e.mu.RUnlock()
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
			e.mu.RUnlock()
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
	e.mu.RUnlock()

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

// GetIndex returns the index object for the given index name.
func (e *PageStorageEngine) GetIndex(dbName, tableName, indexName string) (index.Index, bool) {
	e.indexesMu.RLock()
	key := dbName + "/" + tableName
	mgr, ok := e.indexes[key]
	e.indexesMu.RUnlock()
	if !ok {
		return nil, false
	}
	idxs := mgr.All()
	for _, idx := range idxs {
		if idx.Name() == indexName {
			return idx, true
		}
	}
	return nil, false
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

func (e *PageStorageEngine) updateIndexesOnUpdate(dbName, tableName string, indices []int, newRows []Row) {
	mgr := e.getOrCreateIndexManager(dbName, tableName)
	for _, idx := range mgr.All() {
		colIdx := idx.ColIndex()
		for i, row := range newRows {
			if i < len(indices) && colIdx < len(row) {
				pos := indices[i]
				key := fmt.Sprintf("%v", row[colIdx])
				idx.Insert(key, pos)
			}
		}
	}
}
