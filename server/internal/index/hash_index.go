package index

import (
	"fmt"
	"sync"
)

// IndexableRow — минимальный набор данных о строке, нужный индексу.
type IndexableRow struct {
	DeletedTx uint64
	Data      []interface{}
}

// HashIndex — in-memory хэш-индекс на один столбец таблицы.
// Хранит маппинг: значение_столбца → []int (индексы строк в data-файле).
type HashIndex struct {
	mu       sync.RWMutex
	name     string
	column   string
	colIndex int // позиция столбца в схеме (0-based)

	// Основное хранилище: ключ → список позиций строк
	data map[string][]int

	// Обратный маппинг: позиция строки → ключ
	// Нужен для UPDATE (удалить старую запись из индекса)
	reverse map[int]string
}

func New(name, column string, colIndex int) *HashIndex {
	return &HashIndex{
		name:     name,
		column:   column,
		colIndex: colIndex,
		data:     make(map[string][]int),
		reverse:  make(map[int]string),
	}
}

func (idx *HashIndex) Type() string   { return "hash" }
func (idx *HashIndex) Name() string   { return idx.name }
func (idx *HashIndex) Column() string { return idx.column }
func (idx *HashIndex) ColIndex() int  { return idx.colIndex }

// SetColumn renames the indexed column (used by ALTER TABLE RENAME COLUMN).
// The data mapping is unchanged, only the column label moves.
func (idx *HashIndex) SetColumn(column string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.column = column
}

// Lookup возвращает индексы строк для заданного значения.
func (idx *HashIndex) Lookup(value string) ([]int, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	positions, ok := idx.data[value]
	if !ok {
		return nil, false
	}
	result := make([]int, len(positions))
	copy(result, positions)
	return result, true
}

// Insert добавляет маппинг значение → позиция строки.
func (idx *HashIndex) Insert(value string, rowPos int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.data[value] = append(idx.data[value], rowPos)
	idx.reverse[rowPos] = value
}

// Delete удаляет позицию строки из индекса.
func (idx *HashIndex) Delete(rowPos int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	key, ok := idx.reverse[rowPos]
	if !ok {
		return
	}
	delete(idx.reverse, rowPos)
	positions := idx.data[key]
	for i, p := range positions {
		if p == rowPos {
			idx.data[key] = append(positions[:i], positions[i+1:]...)
			break
		}
	}
	if len(idx.data[key]) == 0 {
		delete(idx.data, key)
	}
}

// Rebuild пересоздаёт индекс из актуальных строк таблицы.
func (idx *HashIndex) Rebuild(rows []IndexableRow) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.data = make(map[string][]int)
	idx.reverse = make(map[int]string)

	for pos, row := range rows {
		if row.DeletedTx != 0 {
			continue // устаревшая версия не индексируется
		}
		if idx.colIndex >= len(row.Data) {
			continue
		}
		key := ValueToIndexKey(row.Data[idx.colIndex])
		idx.data[key] = append(idx.data[key], pos)
		idx.reverse[pos] = key
	}
}

// ValueToIndexKey конвертирует любое значение в строковый ключ для хэш-таблицы.
func ValueToIndexKey(v interface{}) string {
	if v == nil {
		return "\x00NULL"
	}
	return fmt.Sprintf("%v", v)
}
