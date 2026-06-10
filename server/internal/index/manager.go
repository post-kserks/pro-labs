package index

import (
	"strings"
	"sync"
)

// IndexManager хранит все индексы одной таблицы.
type IndexManager struct {
	mu      sync.RWMutex
	indexes map[string]*HashIndex // имя индекса → индекс
	// byColumn даёт O(1) поиск индекса по столбцу (ключ — имя столбца
	// в нижнем регистре, так как поиск регистронезависимый).
	byColumn map[string][]*HashIndex
}

func NewManager() *IndexManager {
	return &IndexManager{
		indexes:  make(map[string]*HashIndex),
		byColumn: make(map[string][]*HashIndex),
	}
}

func columnKey(column string) string {
	return strings.ToLower(column)
}

func (m *IndexManager) Add(idx *HashIndex) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.indexes[idx.name]; ok {
		m.removeFromColumn(old)
	}
	m.indexes[idx.name] = idx
	key := columnKey(idx.column)
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
func (m *IndexManager) removeFromColumn(idx *HashIndex) {
	key := columnKey(idx.column)
	col := m.byColumn[key]
	for i, v := range col {
		if v.name == idx.name {
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
func (m *IndexManager) FindForColumn(column string) (*HashIndex, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if idxs, ok := m.byColumn[columnKey(column)]; ok && len(idxs) > 0 {
		return idxs[0], true
	}
	return nil, false
}

func (m *IndexManager) All() []*HashIndex {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*HashIndex, 0, len(m.indexes))
	for _, idx := range m.indexes {
		result = append(result, idx)
	}
	return result
}
