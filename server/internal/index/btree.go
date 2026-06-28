package index

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
)

// BTreeIndex — B-tree индекс для range queries и ordering.
// Реализация на основе отсортированного slice (постепенно будет заменена
// на полный B-tree с disk persistence).
type BTreeIndex struct {
	mu       sync.RWMutex
	name     string
	column   string
	colIndex int

	// Отсортированные ключи
	keys []string
	// Позиции строк для каждого ключа
	values [][]int

	// Обратный маппинг: позиция строки → ключ
	reverse map[int]string
}

// NewBTreeIndex создаёт новый B-tree индекс.
func NewBTreeIndex(name, column string, colIndex int) *BTreeIndex {
	return &BTreeIndex{
		name:     name,
		column:   column,
		colIndex: colIndex,
		keys:     make([]string, 0),
		values:   make([][]int, 0),
		reverse:  make(map[int]string),
	}
}

func (idx *BTreeIndex) Type() string   { return "btree" }
func (idx *BTreeIndex) Name() string   { return idx.name }
func (idx *BTreeIndex) Column() string { return idx.column }
func (idx *BTreeIndex) ColIndex() int  { return idx.colIndex }

// RenameColumn переименовывает столбец индекса.
func (idx *BTreeIndex) RenameColumn(old, new string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.column = new
}

// Lookup ищет точное значение ключа.
func (idx *BTreeIndex) Lookup(value string) ([]int, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	i := sort.SearchStrings(idx.keys, value)
	if i < len(idx.keys) && idx.keys[i] == value {
		result := make([]int, len(idx.values[i]))
		copy(result, idx.values[i])
		return result, true
	}
	return nil, false
}

// Range ищет все ключи в диапазоне [low, high].
func (idx *BTreeIndex) Range(low, high string) []int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	start := 0
	if low != "" {
		start = sort.SearchStrings(idx.keys, low)
	}
	end := len(idx.keys)
	if high != "" {
		end = sort.SearchStrings(idx.keys, high)
		if end < len(idx.keys) && idx.keys[end] <= high {
			end++
		}
	}

	var result []int
	for i := start; i < end; i++ {
		result = append(result, idx.values[i]...)
	}
	return result
}

// Insert добавляет позицию строки в индекс.
func (idx *BTreeIndex) Insert(value string, rowPos int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	i := sort.SearchStrings(idx.keys, value)
	if i < len(idx.keys) && idx.keys[i] == value {
		idx.values[i] = append(idx.values[i], rowPos)
	} else {
		idx.keys = append(idx.keys, "")
		copy(idx.keys[i+1:], idx.keys[i:])
		idx.keys[i] = value

		idx.values = append(idx.values, nil)
		copy(idx.values[i+1:], idx.values[i:])
		idx.values[i] = []int{rowPos}
	}
	idx.reverse[rowPos] = value
}

// Delete удаляет позицию строки из индекса.
func (idx *BTreeIndex) Delete(rowPos int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	key, ok := idx.reverse[rowPos]
	if !ok {
		return
	}
	delete(idx.reverse, rowPos)

	i := sort.SearchStrings(idx.keys, key)
	if i >= len(idx.keys) || idx.keys[i] != key {
		return
	}

	for j, pos := range idx.values[i] {
		if pos == rowPos {
			idx.values[i] = append(idx.values[i][:j], idx.values[i][j+1:]...)
			break
		}
	}

	if len(idx.values[i]) == 0 {
		idx.keys = append(idx.keys[:i], idx.keys[i+1:]...)
		idx.values = append(idx.values[:i], idx.values[i+1:]...)
	}
}

// Rebuild пересоздаёт индекс из строк таблицы.
func (idx *BTreeIndex) Rebuild(rows []IndexableRow) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.keys = make([]string, 0)
	idx.values = make([][]int, 0)
	idx.reverse = make(map[int]string)

	type entry struct {
		key string
		pos int
	}

	var entries []entry
	for pos, row := range rows {
		if row.DeletedTx != 0 {
			continue
		}
		if idx.colIndex >= len(row.Data) {
			continue
		}
		key := ValueToIndexKey(row.Data[idx.colIndex])
		entries = append(entries, entry{key: key, pos: pos})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].key < entries[j].key
	})

	for _, e := range entries {
		if len(idx.keys) > 0 && idx.keys[len(idx.keys)-1] == e.key {
			idx.values[len(idx.values)-1] = append(idx.values[len(idx.values)-1], e.pos)
		} else {
			idx.keys = append(idx.keys, e.key)
			idx.values = append(idx.values, []int{e.pos})
		}
		idx.reverse[e.pos] = e.key
	}
}

// btreeIndexData — структура для сериализации B-tree индекса.
type btreeIndexData struct {
	Name     string         `json:"name"`
	Column   string         `json:"column"`
	ColIndex int            `json:"col_index"`
	Keys     []string       `json:"keys"`
	Values   [][]int        `json:"values"`
	Reverse  map[int]string `json:"reverse"`
}

// Save сохраняет B-tree индекс в JSON файл.
func (idx *BTreeIndex) Save(path string) error {
	idx.mu.RLock()
	data := btreeIndexData{
		Name:     idx.name,
		Column:   idx.column,
		ColIndex: idx.colIndex,
		Keys:     idx.keys,
		Values:   idx.values,
		Reverse:  idx.reverse,
	}
	idx.mu.RUnlock()

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, jsonData, 0644) //nolint:gosec // index metadata, not sensitive
}

// LoadBTreeIndex загружает B-tree индекс из JSON файла.
func LoadBTreeIndex(path string) (*BTreeIndex, error) {
	jsonData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var data btreeIndexData
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil, err
	}

	return &BTreeIndex{
		name:     data.Name,
		column:   data.Column,
		colIndex: data.ColIndex,
		keys:     data.Keys,
		values:   data.Values,
		reverse:  data.Reverse,
	}, nil
}
