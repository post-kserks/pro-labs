package storage

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var validIdentRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// ValidateObjectName checks a database object name for safety and correctness.
// Combines path traversal (storage) and syntax (executor) checks.
func ValidateObjectName(name string) error {
	if len(name) == 0 {
		return fmt.Errorf("object name cannot be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("object name too long (max 128): %s", name)
	}
	if !validIdentRe.MatchString(name) {
		return fmt.Errorf("invalid object name (only letters, digits, underscores): %s", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("object name contains invalid path separator: %s", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("object name contains invalid path traversal: %s", name)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("object name contains null byte: %s", name)
	}
	return nil
}

func validateObjectName(name string) error {
	return ValidateObjectName(name)
}

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
	case "TEXT", "VARCHAR", "BLOB":
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
			raw, err := DecodeJSON([]byte(v))
			if err == nil {
				if m, ok := raw.(map[string]interface{}); ok {
					return m, nil
				}
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
