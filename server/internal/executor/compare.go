package executor

import (
	"fmt"

	"vaultdb/internal/storage"
)

// valuesEqual compares two storage.Value values.
// Uses type-safe comparison: first numbers, then strings, then bools,
// and only as a last resort falls back to fmt.Sprintf.
func valuesEqual(a, b storage.Value) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	// Numeric comparison
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	// String comparison
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as == bs
		}
	}
	// Bool comparison
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			return ab == bb
		}
	}
	// Int64 comparison
	if ai, ok := a.(int64); ok {
		if bi, ok := b.(int64); ok {
			return ai == bi
		}
	}
	// Float64 comparison
	if af, ok := a.(float64); ok {
		if bf, ok := b.(float64); ok {
			return af == bf
		}
	}
	// Fallback: string representation
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// rowsEqual compares two table rows element by element.
func rowsEqual(a, b storage.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !valuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
