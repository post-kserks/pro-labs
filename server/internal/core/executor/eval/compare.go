package eval

import (
	"fmt"
	"strings"

	"vaultdb/internal/core/storage"
)

// ValuesEqual compares two storage.Value values.
func ValuesEqual(a, b storage.Value) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if af, aok := ToFloat(a); aok {
		if bf, bok := ToFloat(b); bok {
			return af == bf
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as == bs
		}
	}
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			return ab == bb
		}
	}
	if ai, ok := a.(int64); ok {
		if bi, ok := b.(int64); ok {
			return ai == bi
		}
	}
	if af, ok := a.(float64); ok {
		if bf, ok := b.(float64); ok {
			return af == bf
		}
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// ValuesEqualCaseInsensitive compares two storage.Value values case-insensitively for strings.
func ValuesEqualCaseInsensitive(a, b storage.Value) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return strings.EqualFold(as, bs)
		}
	}
	return ValuesEqual(a, b)
}

// RowsEqual compares two table rows element by element.
func RowsEqual(a, b storage.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !ValuesEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
