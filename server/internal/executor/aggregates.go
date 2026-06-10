package executor

import (
	"fmt"
	"strings"
)

// Aggregator accumulates values and returns a result.
type Aggregator interface {
	Add(value interface{})
	Result() interface{}
}

// countAgg handles COUNT(*) and COUNT(col).
type countAgg struct {
	count    int64
	distinct bool
	seen     map[string]bool
}

func (a *countAgg) Add(v interface{}) {
	if v == nil {
		return
	}
	if a.distinct {
		key := fmt.Sprintf("%v", v)
		if a.seen[key] {
			return
		}
		a.seen[key] = true
	}
	a.count++
}

func (a *countAgg) Result() interface{} {
	return a.count
}

// sumAgg handles SUM(col).
type sumAgg struct {
	sum     float64
	hasVal  bool
	allInts bool
}

func (a *sumAgg) Add(v interface{}) {
	if v == nil {
		return
	}
	if f, ok := toFloat(v); ok {
		if !a.hasVal {
			a.allInts = true
		}
		switch v.(type) {
		case int, int64:
		default:
			a.allInts = false
		}
		a.sum += f
		a.hasVal = true
	}
}

func (a *sumAgg) Result() interface{} {
	if !a.hasVal {
		return nil
	}
	if a.allInts {
		return int64(a.sum)
	}
	return a.sum
}

// avgAgg handles AVG(col).
type avgAgg struct {
	sum   float64
	count int64
}

func (a *avgAgg) Add(v interface{}) {
	if v == nil {
		return
	}
	if f, ok := toFloat(v); ok {
		a.sum += f
		a.count++
	}
}

func (a *avgAgg) Result() interface{} {
	if a.count == 0 {
		return nil
	}
	return a.sum / float64(a.count)
}

// minAgg handles MIN(col).
type minAgg struct {
	min interface{}
}

func (a *minAgg) Add(v interface{}) {
	if v == nil {
		return
	}
	if a.min == nil || CompareValues(v, a.min) < 0 {
		a.min = v
	}
}

func (a *minAgg) Result() interface{} {
	return a.min
}

// maxAgg handles MAX(col).
type maxAgg struct {
	max interface{}
}

func (a *maxAgg) Add(v interface{}) {
	if v == nil {
		return
	}
	if a.max == nil || CompareValues(v, a.max) > 0 {
		a.max = v
	}
}

func (a *maxAgg) Result() interface{} {
	return a.max
}

// NewAggregator is a factory for aggregators.
func NewAggregator(name string, distinct bool) Aggregator {
	switch strings.ToUpper(name) {
	case "COUNT":
		return &countAgg{distinct: distinct, seen: make(map[string]bool)}
	case "SUM":
		return &sumAgg{}
	case "AVG":
		return &avgAgg{}
	case "MIN":
		return &minAgg{}
	case "MAX":
		return &maxAgg{}
	default:
		return nil
	}
}
