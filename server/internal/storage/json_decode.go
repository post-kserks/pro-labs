package storage

import (
	"bytes"
	"encoding/json"
)

// DecodeJSON decodes JSON bytes preserving int64 precision for integer values.
// Unlike json.Unmarshal into interface{}, which converts all JSON numbers to float64,
// this function uses json.Number internally and converts integer-like numbers to int64.
func DecodeJSON(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return convertJSONNumbers(raw), nil
}

func convertJSONNumbers(v interface{}) interface{} {
	switch val := v.(type) {
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
		if f, err := val.Float64(); err == nil {
			return f
		}
		return val.String()
	case map[string]interface{}:
		for k, inner := range val {
			val[k] = convertJSONNumbers(inner)
		}
		return val
	case []interface{}:
		for i, inner := range val {
			val[i] = convertJSONNumbers(inner)
		}
		return val
	default:
		return v
	}
}
