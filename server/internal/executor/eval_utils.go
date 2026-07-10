package executor

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// buildColumnIndex creates a lowercased column-name → position map from the schema.
// The map is stored in ExecutionContext.ColumnIndex and used by resolveColumn for O(1) lookups.
func buildColumnIndex(schema *storage.TableSchema) map[string]int {
	idx := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		idx[strings.ToLower(col.Name)] = i
	}
	return idx
}

// ensureColumnIndex lazily builds or refreshes ctx.ColumnIndex when the schema changes.
func ensureColumnIndex(ctx *ExecutionContext, schema *storage.TableSchema) {
	if ctx == nil || schema == nil {
		return
	}
	ctx.ColumnIndex = buildColumnIndex(schema)
}

// resolveColumn finds a column value by name in a data row.
// When columnIndex is non-nil, unqualified lookups use the O(1) cache first,
// falling back to linear scan only for qualified (table.column) names.
func resolveColumn(row storage.Row, schema *storage.TableSchema, name string, columnIndex map[string]int) (interface{}, error) {
	if schema == nil {
		return nil, fmt.Errorf("column %q: no schema available", name)
	}
	// Fast path: use cached index for unqualified names.
	if columnIndex != nil && !strings.Contains(name, ".") {
		if idx, ok := columnIndex[strings.ToLower(name)]; ok && idx < len(row) {
			return row[idx], nil
		}
	}

	// Slow path: linear scan (qualified names or cache miss).
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

// inferTypeFromExpr determines expression type from schema.
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

// parserValueToRaw converts parser.Value to a raw Go value.
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

// toFloat converts a value to float64.
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

// toInt64 converts a value to int64.
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

// initcap capitalizes the first letter of each word.
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
