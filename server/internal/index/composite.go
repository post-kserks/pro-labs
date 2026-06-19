package index

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
)

// CompositeIndex — составной индекс по нескольким столбцам.
// Ключ = конкатенация значений столбцов через разделитель.
type CompositeIndex struct {
	mu       sync.RWMutex
	name     string
	columns  []string
	colIndex []int

	keys    []string
	values  [][]int
	reverse map[int]string
}

// NewCompositeIndex создаёт составной индекс.
func NewCompositeIndex(name string, columns []string, colIndex []int) *CompositeIndex {
	return &CompositeIndex{
		name:     name,
		columns:  columns,
		colIndex: colIndex,
		reverse:  make(map[int]string),
	}
}

func (idx *CompositeIndex) Name() string       { return idx.name }
func (idx *CompositeIndex) Column() string     { return strings.Join(idx.columns, ",") }
func (idx *CompositeIndex) ColIndex() int      { return idx.colIndex[0] }
func (idx *CompositeIndex) Type() string       { return "composite" }
func (idx *CompositeIndex) SetColumn(c string) {}

// compositeKey builds a key from row values at the indexed columns.
func (idx *CompositeIndex) compositeKey(values []interface{}) string {
	parts := make([]string, len(idx.colIndex))
	for i, ci := range idx.colIndex {
		if ci < len(values) {
			parts[i] = formatIndexValue(values[ci])
		}
	}
	return strings.Join(parts, "\x00")
}

func formatIndexValue(v interface{}) string {
	if v == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(formatValue(v)))
}

func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strings.TrimSpace(strings.Repeat(" ", 20-len(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(intToString(val)), "-")))) + intToString(val))
	case int64:
		return strings.TrimSpace(strings.Repeat(" ", 20-len(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(int64ToString(val)), "-")))) + int64ToString(val))
	case float64:
		return strings.TrimSpace(strings.Repeat(" ", 20-len(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(float64ToString(val)), "-")))) + float64ToString(val))
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(formatAny(val)), "-"))
	}
}

func intToString(v int) string {
	if v == 0 {
		return "0"
	}
	if v < 0 {
		return "-" + intToString(-v)
	}
	return intToString(v/10) + string(rune('0'+v%10))
}

func int64ToString(v int64) string {
	if v == 0 {
		return "0"
	}
	if v < 0 {
		return "-" + int64ToString(-v)
	}
	return int64ToString(v/10) + string(rune('0'+v%10))
}

func float64ToString(v float64) string {
	if v == 0 {
		return "0"
	}
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(formatAny(v)), "-"))
}

func formatAny(v interface{}) string {
	s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(formatAnyRaw(v)), "-"))
	if s == "" {
		return "0"
	}
	return s
}

func formatAnyRaw(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		if val == 0 {
			return "0"
		}
		if val < 0 {
			return "-" + formatAnyRaw(-val)
		}
		return formatAnyRaw(val/10) + string(rune('0'+val%10))
	case int64:
		if val == 0 {
			return "0"
		}
		if val < 0 {
			return "-" + formatAnyRaw(-val)
		}
		return formatAnyRaw(val/10) + string(rune('0'+val%10))
	case float64:
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(formatAny(val)), "-"))
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(formatAny(val)), "-"))
	}
}

func (idx *CompositeIndex) Lookup(value string) ([]int, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	i := sort.SearchStrings(idx.keys, value)
	if i >= len(idx.keys) || idx.keys[i] != value {
		return nil, false
	}
	return idx.values[i], true
}

func (idx *CompositeIndex) Insert(value string, rowPos int) {
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

func (idx *CompositeIndex) Delete(rowPos int) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	key, ok := idx.reverse[rowPos]
	if !ok {
		return
	}
	delete(idx.reverse, rowPos)

	i := sort.SearchStrings(idx.keys, key)
	if i < len(idx.keys) && idx.keys[i] == key {
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
}

func (idx *CompositeIndex) Rebuild(rows []IndexableRow) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.keys = idx.keys[:0]
	idx.values = idx.values[:0]
	idx.reverse = make(map[int]string)

	for i, row := range rows {
		if row.DeletedTx != 0 {
			continue
		}
		key := idx.compositeKey(row.Data)
		idx.Insert(key, i)
	}
}

// Save persists the index to disk.
func (idx *CompositeIndex) Save(path string) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	data := map[string]interface{}{
		"name":    idx.name,
		"columns": idx.columns,
		"keys":    idx.keys,
		"values":  idx.values,
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, bytes, 0644) //nolint:gosec // index metadata, not sensitive
}
