package index

import (
	"encoding/json"
	"os"
	"sort"
	"sync"

	gbtree "github.com/google/btree"
)

type btreeEntry struct {
	key       string
	positions []int
}

func (e *btreeEntry) Less(than gbtree.Item) bool {
	return e.key < than.(*btreeEntry).key
}

// BTreeIndex — B-tree index for range queries and ordering.
type BTreeIndex struct {
	tree     *gbtree.BTree
	mu       sync.RWMutex
	name     string
	column   string
	colIndex int

	// Reverse mapping: позиция строки → ключ
	reverse map[int]string

	// Index-only scan: stored columns per row position
	storedCols map[int]map[string]interface{}
}

// NewBTreeIndex создаёт новый B-tree индекс.
func NewBTreeIndex(name, column string, colIndex int) *BTreeIndex {
	return &BTreeIndex{
		tree:       gbtree.New(128),
		name:       name,
		column:     column,
		colIndex:   colIndex,
		reverse:    make(map[int]string),
		storedCols: make(map[int]map[string]interface{}),
	}
}

func (idx *BTreeIndex) Type() string   { return "btree" }
func (idx *BTreeIndex) Name() string   { return idx.name }
func (idx *BTreeIndex) Column() string { return idx.column }
func (idx *BTreeIndex) ColIndex() int  { return idx.colIndex }

func (idx *BTreeIndex) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.tree.Len()
}

// Columns returns the column names this index covers (for index-only scan).
func (idx *BTreeIndex) Columns() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if len(idx.storedCols) == 0 {
		return nil
	}
	colSet := make(map[string]bool)
	for _, cols := range idx.storedCols {
		for col := range cols {
			colSet[col] = true
		}
		break
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

	entry := idx.tree.Get(&btreeEntry{key: value})
	if entry == nil {
		return nil, nil, false
	}
	e := entry.(*btreeEntry)
	rowPositions := make([]int, len(e.positions))
	copy(rowPositions, e.positions)

	resultCols := make(map[int]map[string]interface{}, len(rowPositions))
	for _, pos := range rowPositions {
		if cols, ok := idx.storedCols[pos]; ok {
			resultCols[pos] = cols
		}
	}
	return rowPositions, resultCols, true
}

// RangeWithColumns returns positions and stored columns in [low, high] range.
func (idx *BTreeIndex) RangeWithColumns(low, high string) ([]int, map[int]map[string]interface{}) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var result []int
	resultCols := make(map[int]map[string]interface{})

	idx.tree.AscendGreaterOrEqual(&btreeEntry{key: low}, func(item gbtree.Item) bool {
		e := item.(*btreeEntry)
		if e.key > high {
			return false
		}
		for _, pos := range e.positions {
			result = append(result, pos)
			if cols, ok := idx.storedCols[pos]; ok {
				resultCols[pos] = cols
			}
		}
		return true
	})
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

	entry := idx.tree.Get(&btreeEntry{key: value})
	if entry == nil {
		return nil, false
	}
	e := entry.(*btreeEntry)
	result := make([]int, len(e.positions))
	copy(result, e.positions)
	return result, true
}

// Range ищет все ключи в диапазоне [low, high].
func (idx *BTreeIndex) Range(low, high string) []int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var result []int
	idx.tree.AscendGreaterOrEqual(&btreeEntry{key: low}, func(item gbtree.Item) bool {
		e := item.(*btreeEntry)
		if e.key > high {
			return false
		}
		result = append(result, e.positions...)
		return true
	})
	return result
}

// Insert добавляет позицию строки в индекс.
func (idx *BTreeIndex) Insert(value string, rowPos int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entry := idx.tree.Get(&btreeEntry{key: value})
	if entry != nil {
		entry.(*btreeEntry).positions = append(entry.(*btreeEntry).positions, rowPos)
	} else {
		idx.tree.ReplaceOrInsert(&btreeEntry{key: value, positions: []int{rowPos}})
	}
	idx.reverse[rowPos] = value
}

// InsertWithColumns добавляет позицию строки с сохранёнными столбцами.
func (idx *BTreeIndex) InsertWithColumns(value string, rowPos int, columns map[string]interface{}) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	entry := idx.tree.Get(&btreeEntry{key: value})
	if entry != nil {
		entry.(*btreeEntry).positions = append(entry.(*btreeEntry).positions, rowPos)
	} else {
		idx.tree.ReplaceOrInsert(&btreeEntry{key: value, positions: []int{rowPos}})
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

	entry := idx.tree.Get(&btreeEntry{key: key})
	if entry == nil {
		return
	}
	e := entry.(*btreeEntry)
	for j, pos := range e.positions {
		if pos == rowPos {
			e.positions = append(e.positions[:j], e.positions[j+1:]...)
			break
		}
	}
	if len(e.positions) == 0 {
		idx.tree.Delete(entry)
	}
}

// Rebuild пересоздаёт индекс из строк таблицы.
func (idx *BTreeIndex) Rebuild(rows []IndexableRow) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.tree = gbtree.New(128)
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
		existing := idx.tree.Get(&btreeEntry{key: e.key})
		if existing != nil {
			existing.(*btreeEntry).positions = append(existing.(*btreeEntry).positions, e.pos)
		} else {
			idx.tree.ReplaceOrInsert(&btreeEntry{key: e.key, positions: []int{e.pos}})
		}
		idx.reverse[e.pos] = e.key
	}
}

// btreeIndexData — структура для сериализации B-tree индекса.
type btreeIndexData struct {
	Name       string                         `json:"name"`
	Column     string                         `json:"column"`
	ColIndex   int                            `json:"col_index"`
	Keys       []string                       `json:"keys"`
	Values     [][]int                        `json:"values"`
	Reverse    map[int]string                 `json:"reverse"`
	StoredCols map[int]map[string]interface{} `json:"stored_cols,omitempty"`
}

// Save сохраняет B-tree индекс в JSON файл.
func (idx *BTreeIndex) Save(path string) error {
	idx.mu.RLock()

	var keys []string
	var values [][]int
	idx.tree.Ascend(func(item gbtree.Item) bool {
		e := item.(*btreeEntry)
		keys = append(keys, e.key)
		posCopy := make([]int, len(e.positions))
		copy(posCopy, e.positions)
		values = append(values, posCopy)
		return true
	})

	data := btreeIndexData{
		Name:       idx.name,
		Column:     idx.column,
		ColIndex:   idx.colIndex,
		Keys:       keys,
		Values:     values,
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

	tree := gbtree.New(128)
	for i, key := range data.Keys {
		if i < len(data.Values) {
			tree.ReplaceOrInsert(&btreeEntry{key: key, positions: data.Values[i]})
		}
	}

	return &BTreeIndex{
		tree:       tree,
		name:       data.Name,
		column:     data.Column,
		colIndex:   data.ColIndex,
		reverse:    data.Reverse,
		storedCols: storedCols,
	}, nil
}
