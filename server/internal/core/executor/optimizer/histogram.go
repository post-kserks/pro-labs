package optimizer

import (
	"fmt"
	"reflect"
	"sort"
)

// MCVItem represents a Most Common Value and its relative frequency.
type MCVItem struct {
	Value     interface{}
	Frequency float64
}

// ComputeMCVAndHistogram computes Most Common Values (MCV) and equi-depth histogram boundaries
// from a sample of column values.
func ComputeMCVAndHistogram(values []interface{}, maxMCV int, maxBuckets int, totalRows int) (mcv []MCVItem, histogram []interface{}) {
	mcv = make([]MCVItem, 0)
	histogram = make([]interface{}, 0)

	if len(values) == 0 {
		return mcv, histogram
	}

	// Count frequency of every non-nil distinct value using fast map lookup for comparable types
	type valueGroup struct {
		val   interface{}
		count int
	}
	groupsMap := make(map[interface{}]*valueGroup)
	var groups []*valueGroup

	for _, v := range values {
		if v == nil {
			continue
		}
		// Check if type is comparable before indexing groupsMap
		if reflect.TypeOf(v).Comparable() {
			if g, ok := groupsMap[v]; ok {
				g.count++
				continue
			}
			g := &valueGroup{val: v, count: 1}
			groupsMap[v] = g
			groups = append(groups, g)
			continue
		}

		// Fallback for non-comparable types (slices, maps, etc.)
		found := false
		for _, g := range groups {
			if compareValues(g.val, v) == 0 {
				g.count++
				found = true
				break
			}
		}
		if !found {
			groups = append(groups, &valueGroup{val: v, count: 1})
		}
	}

	if len(groups) == 0 {
		return mcv, histogram
	}

	// Sort groups by count descending, break ties by value ascending
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].count == groups[j].count {
			return compareValues(groups[i].val, groups[j].val) < 0
		}
		return groups[i].count > groups[j].count
	})

	// Pick up to maxMCV most frequent values whose frequency fraction is >= 0.05
	nTotal := float64(len(values))
	mcvSet := make(map[int]bool) // index in groups -> true if added to MCV

	for i, g := range groups {
		if len(mcv) >= maxMCV {
			break
		}
		freq := float64(g.count) / nTotal
		if freq >= 0.05 {
			mcv = append(mcv, MCVItem{
				Value:     g.val,
				Frequency: freq,
			})
			mcvSet[i] = true
		}
	}

	// Collect remaining non-MCV values directly from groups
	remVals := make([]interface{}, 0, len(values))
	for i, g := range groups {
		if mcvSet[i] {
			continue
		}
		for c := 0; c < g.count; c++ {
			remVals = append(remVals, g.val)
		}
	}

	if len(remVals) == 0 {
		return mcv, histogram
	}

	// Sort remaining values in ascending order supporting int/float64/string comparison
	sort.Slice(remVals, func(i, j int) bool {
		return compareValues(remVals[i], remVals[j]) < 0
	})

	// Partition into up to maxBuckets equi-depth histogram boundaries
	numBuckets := maxBuckets
	if len(remVals) < numBuckets {
		numBuckets = len(remVals)
	}
	if numBuckets <= 0 {
		return mcv, histogram
	}

	step := float64(len(remVals)) / float64(numBuckets)
	for i := 0; i < numBuckets; i++ {
		idx := int(float64(i) * step)
		if idx >= len(remVals) {
			idx = len(remVals) - 1
		}
		histogram = append(histogram, remVals[idx])
	}

	return mcv, histogram
}

// compareValues compares two values supporting int, float64, string, etc.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Try numeric comparison if both are numeric or convertible to float64
	fa, okA := toFloat64(a)
	fb, okB := toFloat64(b)
	if okA && okB {
		if fa < fb {
			return -1
		}
		if fa > fb {
			return 1
		}
		return 0
	}

	// String comparison
	sa, isStrA := a.(string)
	sb, isStrB := b.(string)
	if isStrA && isStrB {
		if sa < sb {
			return -1
		}
		if sa > sb {
			return 1
		}
		return 0
	}

	// Fallback to string representation for mixed/unhashable types
	strA := fmt.Sprintf("%v", a)
	strB := fmt.Sprintf("%v", b)
	if strA < strB {
		return -1
	}
	if strA > strB {
		return 1
	}
	return 0
}

func toFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return float64(x), true
	}
	return 0, false
}
