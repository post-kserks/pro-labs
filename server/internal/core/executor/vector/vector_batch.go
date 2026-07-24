package vector

import (
	"errors"
)

const BatchSize = 1024

// RecordBatch represents a columnar batch of data for vectorized processing.
type RecordBatch struct {
	Count uint32
	// Columns stores data column-by-column rather than row-by-row
	Int64Cols   map[int][]int64
	Float64Cols map[int][]float64
	StringCols  map[int][]string
	BoolCols    map[int][]bool

	// Selection vector for filtering without moving data
	Selection []uint32
	Selected  uint32
}

func NewRecordBatch() *RecordBatch {
	return &RecordBatch{
		Count:       0,
		Int64Cols:   make(map[int][]int64),
		Float64Cols: make(map[int][]float64),
		StringCols:  make(map[int][]string),
		BoolCols:    make(map[int][]bool),
		Selection:   make([]uint32, BatchSize),
		Selected:    0,
	}
}

// AddInt64Column initializes a column
func (b *RecordBatch) AddInt64Column(colIdx int) {
	b.Int64Cols[colIdx] = make([]int64, BatchSize)
}

// FilterInt64GreaterThan applies a vectorized filter (SIMD-friendly layout).
func (b *RecordBatch) FilterInt64GreaterThan(colIdx int, val int64) error {
	col, ok := b.Int64Cols[colIdx]
	if !ok {
		return errors.New("column not found")
	}

	var newSelected uint32 = 0

	// If it's the first filter, populate selection vector directly
	if b.Selected == 0 {
		for i := uint32(0); i < b.Count; i++ {
			if col[i] > val {
				b.Selection[newSelected] = i
				newSelected++
			}
		}
	} else {
		// Filter based on already selected rows
		for i := uint32(0); i < b.Selected; i++ {
			idx := b.Selection[i]
			if col[idx] > val {
				b.Selection[newSelected] = idx
				newSelected++
			}
		}
	}

	b.Selected = newSelected
	return nil
}

// FilterInt64Equals applies a vectorized equality filter.
func (b *RecordBatch) FilterInt64Equals(colIdx int, val int64) error {
	col, ok := b.Int64Cols[colIdx]
	if !ok {
		return errors.New("column not found")
	}

	var newSelected uint32 = 0

	if b.Selected == 0 {
		for i := uint32(0); i < b.Count; i++ {
			if col[i] == val {
				b.Selection[newSelected] = i
				newSelected++
			}
		}
	} else {
		for i := uint32(0); i < b.Selected; i++ {
			idx := b.Selection[i]
			if col[idx] == val {
				b.Selection[newSelected] = idx
				newSelected++
			}
		}
	}

	b.Selected = newSelected
	return nil
}
