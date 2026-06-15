package executor

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

// countAgg handles COUNT(*) and COUNT(col).
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

// sumAgg handles SUM(col).
type sumAgg struct {
	sum     float64
	hasVal  bool
	allInts bool
}

func (a *sumAgg) Add(_, v interface{}) {
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

func (a *avgAgg) Add(_, v interface{}) {
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

func (a *minAgg) Add(_, v interface{}) {
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

func (a *maxAgg) Add(_, v interface{}) {
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

// stringAgg handles STRING_AGG(col, delimiter).
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
	s := valueToString(v)
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

// boolAndAgg handles BOOL_AND(col) — все значения true?
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

// boolOrAgg handles BOOL_OR(col) — хотя бы одно значение true?
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

// stddevAgg handles STDDEV(col).
type stddevAgg struct {
	values []float64
}

func (a *stddevAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if f, ok := toFloat(v); ok {
		a.values = append(a.values, f)
	}
}

func (a *stddevAgg) Result() interface{} {
	n := len(a.values)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return 0.0
	}
	// Вычисляем среднее
	var sum float64
	for _, v := range a.values {
		sum += v
	}
	mean := sum / float64(n)

	// Вычисляем дисперсию
	var variance float64
	for _, v := range a.values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(n - 1) // sample variance

	return math.Sqrt(variance)
}

// varianceAgg handles VARIANCE(col).
type varianceAgg struct {
	values []float64
}

func (a *varianceAgg) Add(_, v interface{}) {
	if v == nil {
		return
	}
	if f, ok := toFloat(v); ok {
		a.values = append(a.values, f)
	}
}

func (a *varianceAgg) Result() interface{} {
	n := len(a.values)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return 0.0
	}
	var sum float64
	for _, v := range a.values {
		sum += v
	}
	mean := sum / float64(n)

	var variance float64
	for _, v := range a.values {
		diff := v - mean
		variance += diff * diff
	}
	return variance / float64(n-1) // sample variance
}

// jsonObjectAgg собирает ключи и значения в JSON объект.
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
	case "STRING_AGG":
		return &stringAgg{delimiter: ",", distinct: distinct}
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
