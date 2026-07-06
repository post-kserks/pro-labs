package storage

import "sync"

var rowPool = sync.Pool{
	New: func() interface{} {
		return make(Row, 0, 16)
	},
}

// GetRow retrieves a Row from the pool or creates a new one.
// The returned Row has length 0 and capacity >= 16.
func GetRow() Row {
	return rowPool.Get().(Row)[:0]
}

// PutRow returns a Row to the pool for reuse.
// Rows with excessive capacity (>256) are discarded to prevent pool bloat.
func PutRow(r Row) {
	if cap(r) > 256 {
		return
	}
	rowPool.Put(r[:0])
}

// GetRowWithLen retrieves a Row from the pool with the specified length.
func GetRowWithLen(n int) Row {
	r := GetRow()
	if cap(r) < n {
		return make(Row, n)
	}
	return r[:n]
}
