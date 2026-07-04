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

	// Index-only scan: stored columns per row position
	storedCols map[int]map[string]interface{}
}

// NewBTreeIndex создаёт новый B-tree индекс.
func NewBTreeIndex(name, column string, colIndex int) *BTreeIndex {
	return &BTreeIndex{
		name:       name,
		column:     column,
		colIndex:   colIndex,
		keys:       make([]string, 0),
		values:     make([][]int, 0),
		reverse:    make(map[int]string),
		storedCols: make(map[int]map[string]interface{}),
	}
}

func (idx *BTreeIndex) Type() string   { return "btree" }
func (idx *BTreeIndex) Name() string   { return idx.name }
func (idx *BTreeIndex) Column() string { return idx.column }
func (idx *BTreeIndex) ColIndex() int  { return idx.colIndex }

// Columns returns the column names this index covers (for index-only scan).
func (idx *BTreeIndex) Columns() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if len(idx.storedCols) == 0 {
		return nil
	}
	// Collect all column names from stored data
	colSet := make(map[string]bool)
	for _, cols := range idx.storedCols {
		for col := range cols {
			colSet[col] = true
		}
		break // one sample is enough for column names
	}
	cols := make([]string, 0, len(colSet))
	for col := range colSet {
		cols = append(cols, col)
	}
	return cols
}

// HasStoredColumns returns true if this index stores column data for index-only scans.
func (idx *BTreeIndex) HasStoredColumns() bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.storedCols) > 0
}

// StoreColumns stores column data for a row position (for index-only scan).
func (idx *BTreeIndex) StoreColumns(rowPos int, columns map[string]interface{}) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.storedCols[rowPos] = columns
}

// StoreColumnsBatch stores column data for multiple row positions.
func (idx *BTreeIndex) StoreColumnsBatch(batch map[int]map[string]interface{}) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for pos, cols := range batch {
		idx.storedCols[pos] = cols
	}
}

// GetStoredColumns returns stored columns for a row position.
func (idx *BTreeIndex) GetStoredColumns(rowPos int) (map[string]interface{}, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	cols, ok := idx.storedCols[rowPos]
	return cols, ok
}

// LookupWithColumns returns positions and stored columns for an exact key match.
func (idx *BTreeIndex) LookupWithColumns(value string) ([]int, map[int]map[string]interface{}, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	i := sort.SearchStrings(idx.keys, value)
	if i < len(idx.keys) && idx.keys[i] == value {
		rowPositions := make([]int, len(idx.values[i]))
		copy(rowPositions, idx.values[i])

		resultCols := make(map[int]map[string]interface{}, len(rowPositions))
		for _, pos := range rowPositions {
			if cols, ok := idx.storedCols[pos]; ok {
				resultCols[pos] = cols
			}
		}
		return rowPositions, resultCols, true
	}
	return nil, nil, false
}

// RangeWithColumns returns positions and stored columns in [low, high] range.
func (idx *BTreeIndex) RangeWithColumns(low, high string) ([]int, map[int]map[string]interface{}) {
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
	resultCols := make(map[int]map[string]interface{})
	for i := start; i < end; i++ {
		for _, pos := range idx.values[i] {
			result = append(result, pos)
			if cols, ok := idx.storedCols[pos]; ok {
				resultCols[pos] = cols
			}
		}
	}
	return result, resultCols
}

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

// InsertWithColumns добавляет позицию строки с сохранёнными столбцами.
func (idx *BTreeIndex) InsertWithColumns(value string, rowPos int, columns map[string]interface{}) {
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
	if columns != nil {
		idx.storedCols[rowPos] = columns
	}
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
	delete(idx.storedCols, rowPos)

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
	Name       string                           `json:"name"`
	Column     string                           `json:"column"`
	ColIndex   int                              `json:"col_index"`
	Keys       []string                         `json:"keys"`
	Values     [][]int                          `json:"values"`
	Reverse    map[int]string                   `json:"reverse"`
	StoredCols map[int]map[string]interface{}   `json:"stored_cols,omitempty"`
}

// Save сохраняет B-tree индекс в JSON файл.
func (idx *BTreeIndex) Save(path string) error {
	idx.mu.RLock()
	data := btreeIndexData{
		Name:       idx.name,
		Column:     idx.column,
		ColIndex:   idx.colIndex,
		Keys:       idx.keys,
		Values:     idx.values,
		Reverse:    idx.reverse,
		StoredCols: idx.storedCols,
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

	storedCols := data.StoredCols
	if storedCols == nil {
		storedCols = make(map[int]map[string]interface{})
	}

	return &BTreeIndex{
		name:       data.Name,
		column:     data.Column,
		colIndex:   data.ColIndex,
		keys:       data.Keys,
		values:     data.Values,
		reverse:    data.Reverse,
		storedCols: storedCols,
	}, nil
}
