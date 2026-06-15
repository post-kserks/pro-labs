package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func requireCurrentDB(ctx *ExecutionContext) (string, error) {
	if ctx.CurrentDB == nil || strings.TrimSpace(*ctx.CurrentDB) == "" {
		return "", fmt.Errorf("no active database selected; use USE <database>; first")
	}
	return *ctx.CurrentDB, nil
}

func resolveDatabase(ctx *ExecutionContext, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return requireCurrentDB(ctx)
	}
	if !ctx.Storage.DatabaseExists(requested) {
		return "", fmt.Errorf("database '%s' does not exist", requested)
	}
	return requested, nil
}

func formatColumnType(column storage.ColumnSchema) string {
	if column.Type == "VARCHAR" && column.VarcharLen > 0 {
		return fmt.Sprintf("VARCHAR(%d)", column.VarcharLen)
	}
	return column.Type
}

func resolveProjection(schema *storage.TableSchema, requested []string) ([]int, []string, error) {
	if len(requested) == 0 {
		indices := make([]int, len(schema.Columns))
		columns := make([]string, len(schema.Columns))
		for i, col := range schema.Columns {
			indices[i] = i
			columns[i] = col.Name
		}
		return indices, columns, nil
	}

	columnIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = i
	}

	indices := make([]int, 0, len(requested))
	columns := make([]string, 0, len(requested))
	for _, name := range requested {
		idx, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, nil, fmt.Errorf("unknown column '%s'", name)
		}
		indices = append(indices, idx)
		columns = append(columns, schema.Columns[idx].Name)
	}

	return indices, columns, nil
}

func parserValueToColumnType(value parser.Value, col storage.ColumnSchema) (storage.Value, error) {
	var raw storage.Value
	switch value.Type {
	case "int":
		raw = value.IntVal
	case "float":
		raw = value.FltVal
	case "string":
		raw = value.StrVal
	case "bool":
		raw = value.BoolVal
	case "null":
		raw = nil
	default:
		return nil, fmt.Errorf("unsupported value type '%s'", value.Type)
	}

	converted, err := normalizeForColumn(raw, col)
	if err != nil {
		return nil, err
	}
	return converted, nil
}

func normalizeForColumn(value storage.Value, col storage.ColumnSchema) (storage.Value, error) {
	tmpSchema := storage.TableSchema{Columns: []storage.ColumnSchema{col}}
	row := storage.Row{value}
	coerced, err := coerceRowViaEval(row, &tmpSchema)
	if err != nil {
		return nil, err
	}
	return coerced[0], nil
}

// coerceRowViaEval keeps executor independent from storage internals while sharing conversion logic.
func coerceRowViaEval(row storage.Row, schema *storage.TableSchema) (storage.Row, error) {
	coerced := make(storage.Row, len(row))
	for i, raw := range row {
		value, err := coerceToColumn(raw, schema.Columns[i])
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
		}
		coerced[i] = value
	}
	return coerced, nil
}

func coerceToColumn(value storage.Value, column storage.ColumnSchema) (storage.Value, error) {
	if value == nil {
		return nil, nil
	}

	switch strings.ToUpper(column.Type) {
	case "INT":
		switch v := value.(type) {
		case int64:
			return v, nil
		case int:
			return int64(v), nil
		case float64:
			if float64(int64(v)) != v {
				return nil, fmt.Errorf("cannot cast FLOAT to INT without precision loss")
			}
			return int64(v), nil
		default:
			return nil, fmt.Errorf("expected INT-compatible value, got %T", value)
		}
	case "FLOAT":
		switch v := value.(type) {
		case float64:
			return v, nil
		case int64:
			return float64(v), nil
		case int:
			return float64(v), nil
		default:
			return nil, fmt.Errorf("expected FLOAT-compatible value, got %T", value)
		}
	case "BOOL":
		boolValue, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected BOOL value, got %T", value)
		}
		return boolValue, nil
	case "TEXT", "VARCHAR":
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected string value, got %T", value)
		}
		if column.Type == "VARCHAR" && column.VarcharLen > 0 && len([]rune(stringValue)) > column.VarcharLen {
			return nil, fmt.Errorf("VARCHAR(%d) overflow", column.VarcharLen)
		}
		return stringValue, nil
	case "VECTOR":
		vec, err := toVector(value)
		if err != nil {
			return nil, err
		}
		if column.VarcharLen > 0 && len(vec) != column.VarcharLen {
			return nil, fmt.Errorf("VECTOR(%d) dimension mismatch: got %d", column.VarcharLen, len(vec))
		}
		return vec, nil
	case "FLEXIBLE":
		// Can be a map or a raw JSON string
		switch v := value.(type) {
		case map[string]interface{}:
			return v, nil
		case string:
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(v), &m); err == nil {
				return m, nil
			}
			return v, nil // fallback to string if not JSON
		default:
			return valueToString(value), nil
		}
	case "DATE", "TIME", "TIMESTAMP", "DECIMAL":
		// For simplicity, store these as strings for now.
		return valueToString(value), nil
	case "JSONB", "JSON":
		return valueToString(value), nil
	default:
		return nil, fmt.Errorf("unsupported column type '%s'", column.Type)
	}
}

func valueToString(value interface{}) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func inferType(val interface{}) string {
	if val == nil {
		return "TEXT"
	}
	switch v := val.(type) {
	case int64, int:
		return "INT"
	case float64:
		return "FLOAT"
	case bool:
		return "BOOL"
	case []float64:
		return "VECTOR"
	case map[string]interface{}:
		return "FLEXIBLE"
	case string:
		// Try to see if it's JSON
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return "FLEXIBLE"
		}
		return "TEXT"
	default:
		return "TEXT"
	}
}
