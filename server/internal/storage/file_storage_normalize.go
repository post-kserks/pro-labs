package storage

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func normalizeValue(value interface{}, col ColumnSchema) (Value, error) {
	if value == nil {
		return nil, nil
	}

	switch col.Type {
	case "INT":
		intVal, ok := toInt64(value)
		if !ok {
			return nil, fmt.Errorf("expected INT, got %T", value)
		}
		return intVal, nil
	case "FLOAT":
		floatVal, ok := toFloat64(value)
		if !ok {
			return nil, fmt.Errorf("expected FLOAT, got %T", value)
		}
		return floatVal, nil
	case "BOOL":
		boolVal, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected BOOL, got %T", value)
		}
		return boolVal, nil
	case "TEXT", "VARCHAR":
		strVal, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected %s, got %T", col.Type, value)
		}
		if col.Type == "VARCHAR" && col.VarcharLen > 0 {
			if len([]rune(strVal)) > col.VarcharLen {
				return nil, fmt.Errorf("VARCHAR(%d) overflow", col.VarcharLen)
			}
		}
		return strVal, nil
	case "VECTOR":
		switch v := value.(type) {
		case []float64:
			return v, nil
		case []interface{}:
			res := make([]float64, len(v))
			for i, x := range v {
				switch f := x.(type) {
				case float64:
					res[i] = f
				case int:
					res[i] = float64(f)
				case int64:
					res[i] = float64(f)
				default:
					return nil, fmt.Errorf("VECTOR element must be numeric, got %T", x)
				}
			}
			return res, nil
		default:
			return nil, fmt.Errorf("expected VECTOR ([]float64), got %T", value)
		}
	case "FLEXIBLE":
		switch v := value.(type) {
		case map[string]interface{}:
			return v, nil
		case string:
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(v), &m); err == nil {
				return m, nil
			}
			return v, nil
		default:
			return value, nil
		}
	case "DATE", "TIME", "TIMESTAMP", "DECIMAL":
		return fmt.Sprintf("%v", value), nil
	case "JSONB", "JSON":
		return fmt.Sprintf("%v", value), nil
	default:
		return nil, fmt.Errorf("unsupported column type '%s'", col.Type)
	}
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case float32:
		f := float64(v)
		if math.Trunc(f) != f {
			return 0, false
		}
		return int64(f), true
	case float64:
		if math.Trunc(v) != v {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

func toFloat64(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func coerceRow(raw []interface{}, schema *TableSchema) (Row, error) {
	if len(raw) != len(schema.Columns) {
		return nil, fmt.Errorf("row width mismatch: expected %d, got %d", len(schema.Columns), len(raw))
	}
	row := make(Row, len(raw))
	for i, cell := range raw {
		v, err := normalizeValue(cell, schema.Columns[i])
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
		}
		row[i] = v
	}
	return row, nil
}

func rowToInterfaceSlice(row Row) []interface{} {
	out := make([]interface{}, len(row))
	for i, v := range row {
		out[i] = v
	}
	return out
}

func interfaceSliceToRow(values []interface{}) Row {
	out := make(Row, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

func valuesEqual(left, right interface{}) bool {
	if left == nil || right == nil {
		return left == right
	}

	if lf, ok := toFloat64(left); ok {
		if rf, ok := toFloat64(right); ok {
			return lf == rf
		}
	}

	switch lv := left.(type) {
	case string:
		rv, ok := right.(string)
		return ok && lv == rv
	case bool:
		rv, ok := right.(bool)
		return ok && lv == rv
	case int64:
		rv, ok := toInt64(right)
		return ok && lv == rv
	default:
		return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right)
	}
}

func writeJSONAtomic(path string, payload interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	bytes, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, bytes, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func writeDataJSONAtomic(path string, payload interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, bytes, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func parseTimestampFlexible(ts string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format")
}

func parsePayloadTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Now().UTC()
	}
	return t.UTC()
}
