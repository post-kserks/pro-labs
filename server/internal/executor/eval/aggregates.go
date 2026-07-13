package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Aggregator accumulates values and returns a result.
type Aggregator interface {
	Add(key, value interface{})
	Result() interface{}
}

type countAgg struct {
	count    int64
	distinct bool
	seen     map[string]bool
}

func (a *countAgg) Add(_, v interface{}) {
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

type sumAgg struct {
	sum     float64
	hasVal  bool
	allInts bool
}

func (a *sumAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if f, ok := ToFloat(v); ok {
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

type avgAgg struct {
	sum   float64
	count int64
}

func (a *avgAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if f, ok := ToFloat(v); ok {
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

type minAgg struct {
	min interface{}
}

func (a *minAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if a.min == nil || CompareOrdering(v, a.min) < 0 {
		a.min = v
	}
}

func (a *minAgg) Result() interface{} {
	return a.min
}

type maxAgg struct {
	max interface{}
}

func (a *maxAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if a.max == nil || CompareOrdering(v, a.max) > 0 {
		a.max = v
	}
}

func (a *maxAgg) Result() interface{} {
	return a.max
}

type stringAgg struct {
	delimiter string
	distinct  bool
	seen      map[string]bool
	values    []string
}

func (a *stringAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	s := ValueToString(v)
	if a.distinct {
		if a.seen == nil {
			a.seen = make(map[string]bool)
		}
		if a.seen[s] {
			return
		}
		a.seen[s] = true
	}
	a.values = append(a.values, s)
}

func (a *stringAgg) Result() interface{} {
	return strings.Join(a.values, a.delimiter)
}

type boolAndAgg struct {
	hasVal bool
	result bool
}

func (a *boolAndAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	b, ok := v.(bool)
	if !ok {
		return
	}
	if !a.hasVal {
		a.result = b
		a.hasVal = true
	} else {
		a.result = a.result && b
	}
}

func (a *boolAndAgg) Result() interface{} {
	if !a.hasVal {
		return nil
	}
	return a.result
}

type boolOrAgg struct {
	hasVal bool
	result bool
}

func (a *boolOrAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	b, ok := v.(bool)
	if !ok {
		return
	}
	if !a.hasVal {
		a.result = b
		a.hasVal = true
	} else {
		a.result = a.result || b
	}
}

func (a *boolOrAgg) Result() interface{} {
	if !a.hasVal {
		return nil
	}
	return a.result
}

type stddevAgg struct {
	n    int64
	mean float64
	m2   float64
}

func (a *stddevAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if f, ok := ToFloat(v); ok {
		a.n++
		delta := f - a.mean
		a.mean += delta / float64(a.n)
		delta2 := f - a.mean
		a.m2 += delta * delta2
	}
}

func (a *stddevAgg) Result() interface{} {
	if a.n < 2 {
		if a.n == 0 {
			return nil
		}
		return 0.0
	}
	variance := a.m2 / float64(a.n-1)
	return math.Sqrt(variance)
}

type varianceAgg struct {
	n    int64
	mean float64
	m2   float64
}

func (a *varianceAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if f, ok := ToFloat(v); ok {
		a.n++
		delta := f - a.mean
		a.mean += delta / float64(a.n)
		delta2 := f - a.mean
		a.m2 += delta * delta2
	}
}

func (a *varianceAgg) Result() interface{} {
	if a.n < 2 {
		if a.n == 0 {
			return nil
		}
		return 0.0
	}
	return a.m2 / float64(a.n-1)
}

type jsonObjectAgg struct {
	keys   []string
	values []interface{}
}

func (a *jsonObjectAgg) Add(key, val interface{}) {
	k := fmt.Sprintf("%v", key)
	a.keys = append(a.keys, k)
	a.values = append(a.values, val)
}

func (a *jsonObjectAgg) Result() interface{} {
	if len(a.keys) == 0 {
		return "{}"
	}
	obj := make(map[string]interface{})
	for i, k := range a.keys {
		if i < len(a.values) {
			obj[k] = a.values[i]
		}
	}
	data, _ := json.Marshal(obj)
	return string(data)
}

// NewAggregator is a factory for aggregators.
func NewAggregator(name string, distinct bool, args ...interface{}) Aggregator {
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
	case "STRING_AGG":
		delim := ","
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				delim = s
			}
		}
		return &stringAgg{delimiter: delim, distinct: distinct}
	case "BOOL_AND":
		return &boolAndAgg{}
	case "BOOL_OR":
		return &boolOrAgg{}
	case "STDDEV", "STDDEV_SAMP":
		return &stddevAgg{}
	case "VARIANCE", "VAR_SAMP":
		return &varianceAgg{}
	case "JSON_OBJECT_AGG":
		return &jsonObjectAgg{}
	default:
		return nil
	}
}
