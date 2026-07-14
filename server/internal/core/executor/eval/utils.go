package eval

import (
	"context"
	crypto_rand "crypto/rand"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

// ─── Context interfaces ─────────────────────────────────────────────────────

// EmbedderProvider is implemented by execution contexts that provide an AI embedder.
type EmbedderProvider interface {
	GetEmbedder() ai.Embedder
	GetGoContext() context.Context
}

// ColumnIndexProvider is implemented by execution contexts that cache column indexes.
type ColumnIndexProvider interface {
	SetColumnIndex(idx map[string]int)
}

// ─── Type Conversion ────────────────────────────────────────────────────────

// ToFloat converts a value to float64.
func ToFloat(value interface{}) (float64, bool) {
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

// ToInt64 converts a value to int64.
func ToInt64(value interface{}) (int64, bool) {
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

// ValueToString converts any value to its string representation.
func ValueToString(value interface{}) string {
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
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// InferType determines the SQL type of a Go value.
func InferType(val interface{}) string {
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
		raw, err := storage.DecodeJSON([]byte(v))
		if err == nil {
			if _, ok := raw.(map[string]interface{}); ok {
				return "FLEXIBLE"
			}
		}
		return "TEXT"
	default:
		return "TEXT"
	}
}

// Initcap capitalizes the first letter of each word.
func Initcap(s string) string {
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

// ─── Column Resolution ──────────────────────────────────────────────────────

// BuildColumnIndex creates a lowercased column-name → position map from the schema.
func BuildColumnIndex(schema *storage.TableSchema) map[string]int {
	idx := make(map[string]int, len(schema.Columns)*2)
	for i, col := range schema.Columns {
		lower := strings.ToLower(col.Name)
		idx[lower] = i
	}
	for i, col := range schema.Columns {
		lower := strings.ToLower(col.Name)
		if dot := strings.IndexByte(lower, '.'); dot >= 0 && dot < len(lower)-1 {
			short := lower[dot+1:]
			if _, exists := idx[short]; !exists {
				idx[short] = i
			}
		}
	}
	return idx
}

// ResolveColumn finds a column value by name in a data row.
func ResolveColumn(row storage.Row, schema *storage.TableSchema, name string, columnIndex map[string]int) (interface{}, error) {
	if schema == nil {
		return nil, fmt.Errorf("column %q: no schema available", name)
	}
	if columnIndex != nil {
		if idx, ok := columnIndex[strings.ToLower(name)]; ok && idx < len(row) {
			return row[idx], nil
		}
	}

	for i, column := range schema.Columns {
		if strings.EqualFold(column.Name, name) {
			if i < len(row) {
				return row[i], nil
			}
		}
		if !strings.Contains(name, ".") {
			if dot := strings.IndexByte(column.Name, '.'); dot >= 0 && dot < len(column.Name)-1 {
				if strings.EqualFold(column.Name[dot+1:], name) {
					if i < len(row) {
						return row[i], nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("unknown column '%s'", name)
}

// ─── Parser Helpers ─────────────────────────────────────────────────────────

// ParserValueToRaw converts parser.Value to a raw Go value.
func ParserValueToRaw(value parser.Value) interface{} {
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

// InferTypeFromExpr determines expression type from schema.
func InferTypeFromExpr(expr parser.Expression, schema *storage.TableSchema) string {
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
	case *parser.WindowExpr:
		return "INT"
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
		return InferType(ParserValueToRaw(e))
	case *parser.Value:
		return InferType(ParserValueToRaw(*e))
	}
	return "TEXT"
}

// ─── JSON / UUID Helpers ────────────────────────────────────────────────────

// ParseJSONArray parses a JSON array.
func ParseJSONArray(s string) []interface{} {
	raw, err := storage.DecodeJSON([]byte(s))
	if err != nil {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	return arr
}

// GenerateUUID generates a UUID v4.
func GenerateUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := crypto_rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate UUID: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ─── Embedding ──────────────────────────────────────────────────────────────

const embeddingTimeout = 10 * time.Second

// EmbedText calls the configured embedding provider with a timeout.
func EmbedText(embedder ai.Embedder, baseCtx context.Context, text string) ([]float64, error) {
	if embedder == nil {
		embedder = ai.NoopEmbedder{}
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	embedCtx, cancel := context.WithTimeout(baseCtx, embeddingTimeout)
	defer cancel()
	return embedder.Embed(embedCtx, text)
}

// ─── String Conversion ──────────────────────────────────────────────────────

// ConvertStringValue converts a string to a typed value based on column schema.
func ConvertStringValue(s string, col storage.ColumnSchema) (storage.Value, error) {
	switch strings.ToUpper(col.Type) {
	case "INT":
		parsed, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot parse string as INT: %q", s)
		}
		return parsed, nil
	case "FLOAT":
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot parse string as FLOAT: %q", s)
		}
		return parsed, nil
	case "BOOL":
		upper := strings.ToUpper(s)
		if upper == "TRUE" || upper == "1" {
			return true, nil
		}
		if upper == "FALSE" || upper == "0" {
			return false, nil
		}
		return nil, fmt.Errorf("cannot parse string as BOOL: %q", s)
	default:
		return s, nil
	}
}
