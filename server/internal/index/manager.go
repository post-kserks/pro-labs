package index

import (
	"strings"
	"sync"
)

// Index — interface for all index types.
type Index interface {
	Name() string
	Column() string
	ColIndex() int
	Type() string
	RenameColumn(old, new string)
	Lookup(value string) ([]int, bool)
	Insert(value string, rowPos int)
	Delete(rowPos int)
	Rebuild(rows []IndexableRow)
	// Index-only scan support
	Columns() []string      // returns column names this index covers
	HasStoredColumns() bool // true if index stores column data
	GetStoredColumns(rowPos int) (map[string]interface{}, bool)
}

// NewByType creates an index by type.
func NewByType(name, column string, colIndex int, indexType string) Index {
	switch indexType {
	case "btree":
		return NewBTreeIndex(name, column, colIndex)
	case "gin":
		return NewGINIndex(name, column, colIndex)
	case "gin_jsonb":
		return NewGINJSONBIndex(name, column, colIndex)
	case "gist":
		return NewGiSTIndex(name, column, colIndex)
	case "composite":
		return NewCompositeIndex(name, []string{column}, []int{colIndex})
	default:
		return New(name, column, colIndex)
	}
}

// NewCompositeByType creates a composite index.
func NewCompositeByType(name string, columns []string, colIndices []int, indexType string) Index {
	switch indexType {
	case "btree":
		return NewCompositeIndex(name, columns, colIndices)
	default:
		return NewCompositeIndex(name, columns, colIndices)
	}
}

// IndexManager stores all indexes for a single table.
type IndexManager struct {
	mu      sync.RWMutex
	indexes map[string]Index // index name → index
	// byColumn provides O(1) index lookup by column (key — lowercase column name
	// since the lookup is case-insensitive).
	byColumn map[string][]Index
}

func NewManager() *IndexManager {
	return &IndexManager{
		indexes:  make(map[string]Index),
		byColumn: make(map[string][]Index),
	}
}

func columnKey(column string) string {
	return strings.ToLower(column)
}

func (m *IndexManager) Add(idx Index) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.indexes[idx.Name()]; ok {
		m.removeFromColumn(old)
	}
	m.indexes[idx.Name()] = idx
	key := columnKey(idx.Column())
	m.byColumn[key] = append(m.byColumn[key], idx)
}

func (m *IndexManager) Remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, ok := m.indexes[name]
	if !ok {
		return
	}
	delete(m.indexes, name)
	m.removeFromColumn(idx)
}

// removeFromColumn removes an index from byColumn; called under write lock.
func (m *IndexManager) removeFromColumn(idx Index) {
	key := columnKey(idx.Column())
	col := m.byColumn[key]
	for i, v := range col {
		if v.Name() == idx.Name() {
			m.byColumn[key] = append(col[:i], col[i+1:]...)
			break
		}
	}
	if len(m.byColumn[key]) == 0 {
		delete(m.byColumn, key)
	}
}

// RenameColumn renames the indexed column of the index and updates
// the byColumn map.
func (m *IndexManager) RenameColumn(indexName, oldColumn, newColumn string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, ok := m.indexes[indexName]
	if !ok {
		return
	}
	m.removeFromColumn(idx)
	idx.RenameColumn(oldColumn, newColumn)
	key := columnKey(newColumn)
	m.byColumn[key] = append(m.byColumn[key], idx)
}

func (m *IndexManager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.indexes[name]
	return ok
}

// FindForColumn returns an index for the given column (if any) in O(1).
func (m *IndexManager) FindForColumn(column string) (Index, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if idxs, ok := m.byColumn[columnKey(column)]; ok && len(idxs) > 0 {
		return idxs[0], true
	}
	return nil, false
}

// FindForColumnMultiple returns all indexes for the given column.
func (m *IndexManager) FindForColumnMultiple(column string) ([]Index, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}
	result := make([]Index, len(idxs))
	copy(result, idxs)
	return result, true
}

func (m *IndexManager) All() []Index {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Index, 0, len(m.indexes))
	for _, idx := range m.indexes {
		result = append(result, idx)
	}
	return result
}

// RangeSearch performs a range query on an index (if it's a B-tree).
func (m *IndexManager) RangeSearch(column, low, high string) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	// Look for a B-tree index
	for _, idx := range idxs {
		if btree, ok := idx.(*BTreeIndex); ok {
			return btree.Range(low, high), true
		}
	}

	return nil, false
}

// FullTextSearch выполняет полноtextовый поиск через GIN индекс.
func (m *IndexManager) FullTextSearch(column, query string) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	for _, idx := range idxs {
		if gin, ok := idx.(*GINIndex); ok {
			return gin.Search(query), true
		}
	}

	return nil, false
}

// RangeSearchGiST performs a range query via a GiST index.
func (m *IndexManager) RangeSearchGiST(column string, min, max float64) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	for _, idx := range idxs {
		if gist, ok := idx.(*GiSTIndex); ok {
			return gist.SearchRange(min, max), true
		}
	}

	return nil, false
}

// OverlapSearchGiST performs an overlap query via a GiST index.
func (m *IndexManager) OverlapSearchGiST(column string, min, max float64) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	for _, idx := range idxs {
		if gist, ok := idx.(*GiSTIndex); ok {
			return gist.SearchOverlap(min, max), true
		}
	}

	return nil, false
}

// SearchJSONBContains searches for JSONB rows containing the specified keys/values.
func (m *IndexManager) SearchJSONBContains(column, query string) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	for _, idx := range idxs {
		if gin, ok := idx.(*GINIndex); ok {
			return gin.SearchJSONBContains(query), true
		}
	}

	return nil, false
}

// SearchJSONBHasKey searches for JSONB rows containing the specified key.
func (m *IndexManager) SearchJSONBHasKey(column, key string) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	for _, idx := range idxs {
		if gin, ok := idx.(*GINIndex); ok {
			return gin.SearchJSONBHasKey(key), true
		}
	}

	return nil, false
}
