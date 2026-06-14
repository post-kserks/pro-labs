package index

import (
	"strings"
	"sync"
)

// Index — интерфейс для всех типов индексов.
type Index interface {
	Name() string
	Column() string
	ColIndex() int
	Type() string
	SetColumn(column string)
	Lookup(value string) ([]int, bool)
	Insert(value string, rowPos int)
	Delete(rowPos int)
	Rebuild(rows []IndexableRow)
}

// NewByType создаёт индекс по типу.
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
	default:
		return New(name, column, colIndex)
	}
}

// IndexManager хранит все индексы одной таблицы.
type IndexManager struct {
	mu      sync.RWMutex
	indexes map[string]Index // имя индекса → индекс
	// byColumn даёт O(1) поиск индекса по столбцу (ключ — имя столбца
	// в нижнем регистре, так как поиск регистронезависимый).
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

// removeFromColumn удаляет индекс из byColumn; вызывается под write-локом.
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

// RenameColumn переименовывает индексируемый столбец индекса и обновляет
// byColumn-карту (использовать вместо прямого вызова HashIndex.SetColumn).
func (m *IndexManager) RenameColumn(indexName, newColumn string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx, ok := m.indexes[indexName]
	if !ok {
		return
	}
	m.removeFromColumn(idx)
	idx.SetColumn(newColumn)
	key := columnKey(newColumn)
	m.byColumn[key] = append(m.byColumn[key], idx)
}

func (m *IndexManager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.indexes[name]
	return ok
}

// FindForColumn возвращает индекс для указанного столбца (если есть) за O(1).
func (m *IndexManager) FindForColumn(column string) (Index, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if idxs, ok := m.byColumn[columnKey(column)]; ok && len(idxs) > 0 {
		return idxs[0], true
	}
	return nil, false
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

// RangeSearch выполняет range query по индексу (если это B-tree).
func (m *IndexManager) RangeSearch(column, low, high string) ([]int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	idxs, ok := m.byColumn[columnKey(column)]
	if !ok || len(idxs) == 0 {
		return nil, false
	}

	// Ищем B-tree индекс
	for _, idx := range idxs {
		if btree, ok := idx.(*BTreeIndex); ok {
			return btree.Range(low, high), true
		}
	}

	return nil, false
}

// FullTextSearch выполняет полнотекстовый поиск через GIN индекс.
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

// RangeSearchGiST выполняет range query через GiST индекс.
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

// OverlapSearchGiST выполняет overlap query через GiST индекс.
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

// SearchJSONBContains ищет JSONB строки, содержащие указанные ключи/значения.
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

// SearchJSONBHasKey ищет JSONB строки, содержащие указанный ключ.
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
