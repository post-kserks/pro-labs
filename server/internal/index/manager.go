package index

import "sync"

// IndexManager хранит все индексы одной таблицы.
type IndexManager struct {
	mu      sync.RWMutex
	indexes map[string]*HashIndex // имя индекса → индекс
}

func NewManager() *IndexManager {
	return &IndexManager{
		indexes: make(map[string]*HashIndex),
	}
}

func (m *IndexManager) Add(idx *HashIndex) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexes[idx.name] = idx
}

func (m *IndexManager) Remove(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.indexes, name)
}

func (m *IndexManager) Has(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.indexes[name]
	return ok
}

// FindForColumn возвращает индекс для указанного столбца (если есть).
func (m *IndexManager) FindForColumn(column string) (*HashIndex, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, idx := range m.indexes {
		if idx.column == column {
			return idx, true
		}
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
