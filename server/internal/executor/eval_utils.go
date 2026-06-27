package executor

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// resolveColumn находит значение столбца по имени в строке данных.
func resolveColumn(row storage.Row, schema *storage.TableSchema, name string) (interface{}, error) {
	for i, column := range schema.Columns {
		if strings.EqualFold(column.Name, name) {
			if i < len(row) {
				return row[i], nil
			}
		}
		if !strings.Contains(name, ".") {
			parts := strings.Split(column.Name, ".")
			if len(parts) == 2 && strings.EqualFold(parts[1], name) {
				if i < len(row) {
					return row[i], nil
				}
			}
		}
	}
	return nil, fmt.Errorf("unknown column '%s'", name)
}

// inferTypeFromExpr определяет тип выражения по схеме.
func inferTypeFromExpr(expr parser.Expression, schema *storage.TableSchema) string {
	if expr == nil {
		return "TEXT"
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		if schema != nil {
			for _, col := range schema.Columns {
				if strings.EqualFold(col.Name, e.Name) {
					return col.Type
				}
				parts := strings.Split(col.Name, ".")
				if len(parts) == 2 && strings.EqualFold(parts[1], e.Name) {
					return col.Type
				}
			}
		}
	case *parser.AggregateExpr:
		switch strings.ToUpper(e.Name) {
		case "COUNT":
			return "INT"
		case "AVG":
			return "FLOAT"
		case "SUM":
			return "FLOAT"
		}
	case *parser.FunctionCall:
		switch strings.ToUpper(e.Name) {
		case "COSINE_SIMILARITY":
			return "FLOAT"
		}
	case parser.Value:
		return inferType(parserValueToRaw(e))
	case *parser.Value:
		return inferType(parserValueToRaw(*e))
	}
	return "TEXT"
}

// parserValueToRaw преобразует parser.Value в raw Go value.
func parserValueToRaw(value parser.Value) interface{} {
	switch value.Type {
	case "int":
		return value.IntVal
	case "float":
		return value.FltVal
	case "string":
		return value.StrVal
	case "bool":
		return value.BoolVal
	case "null":
		return nil
	default:
		return nil
	}
}

// toFloat преобразует значение в float64.
func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		if math.IsNaN(v) {
			return 0, false
		}
		return v, true
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// toInt64 преобразует значение в int64.
func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v > float64(math.MaxInt64) || v < float64(math.MinInt64) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

// initcap делает первую букву каждого слова заглавной.
func initcap(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		runes := []rune(strings.ToLower(w))
		if r := runes[0]; r >= 'a' && r <= 'z' {
			runes[0] = r - ('a' - 'A')
		}
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}
