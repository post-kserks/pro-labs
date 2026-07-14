package types

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/core/executor/eval"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

// ─── Type Formatting / Inference ────────────────────────────────────────────

// FormatColumnType returns the display type for a column (e.g. "VARCHAR(255)").
func FormatColumnType(column storage.ColumnSchema) string {
	if column.Type == "VARCHAR" && column.VarcharLen > 0 {
		return fmt.Sprintf("VARCHAR(%d)", column.VarcharLen)
	}
	return column.Type
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

// InferTypeFromExpr determines expression type from schema.
func InferTypeFromExpr(expr interface{}, schema *storage.TableSchema) string {
	if pe, ok := expr.(parser.Expression); ok {
		return eval.InferTypeFromExpr(pe, schema)
	}
	return "TEXT"
}

// ─── Value Comparison / Conversion ──────────────────────────────────────────

// ParserValueToRaw converts parser.Value to a raw Go value.
func ParserValueToRaw(value interface{}) interface{} {
	if pv, ok := value.(parser.Value); ok {
		return eval.ParserValueToRaw(pv)
	}
	return value
}

// EvalOperandRaw extracts raw value from parser expression.
func EvalOperandRaw(expr interface{}) interface{} {
	return expr
}

// RowsEqual compares two table rows element by element.
func RowsEqual(a, b storage.Row) bool {
	return eval.RowsEqual(a, b)
}

// ValuesEqual compares two storage.Value values.
func ValuesEqual(a, b storage.Value) bool {
	return eval.ValuesEqual(a, b)
}

// ValuesEqualCaseInsensitive compares two storage.Value values case-insensitively.
func ValuesEqualCaseInsensitive(a, b storage.Value) bool {
	return eval.ValuesEqualCaseInsensitive(a, b)
}

// CompareOrdered compares two ordered values.
func CompareOrdered[T ~float64 | ~string](left, right T, op string) (bool, error) {
	switch op {
	case "=":
		return left == right, nil
	case "!=":
		return left != right, nil
	case "<":
		return left < right, nil
	case ">":
		return left > right, nil
	case "<=":
		return left <= right, nil
	case ">=":
		return left >= right, nil
	default:
		return false, fmt.Errorf("unknown operator '%s'", op)
	}
}

// ─── Type Coercion ──────────────────────────────────────────────────────────

// NormalizeForColumn coerces a single value to match a column's type.
func NormalizeForColumn(value storage.Value, col storage.ColumnSchema) (storage.Value, error) {
	tmpSchema := storage.TableSchema{Columns: []storage.ColumnSchema{col}}
	row := storage.Row{value}
	coerced, err := CoerceRowViaEval(row, &tmpSchema)
	if err != nil {
		return nil, err
	}
	return coerced[0], nil
}

// CoerceRowViaEval coerces an entire row to match a schema.
func CoerceRowViaEval(row storage.Row, schema *storage.TableSchema) (storage.Row, error) {
	coerced := make(storage.Row, len(row))
	for i, raw := range row {
		value, err := CoerceToColumn(raw, schema.Columns[i])
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
		}
		coerced[i] = value
	}
	return coerced, nil
}

// CoerceToColumn converts a value to match a column's declared type.
func CoerceToColumn(value storage.Value, column storage.ColumnSchema) (storage.Value, error) {
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
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse string as INT: %q", v)
			}
			return parsed, nil
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
		case string:
			parsed, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("cannot parse string as FLOAT: %q", v)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("expected FLOAT-compatible value, got %T", value)
		}
	case "BOOL":
		boolValue, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("expected BOOL value, got %T", value)
		}
		return boolValue, nil
	case "TEXT", "VARCHAR", "BLOB":
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected string value, got %T", value)
		}
		if column.Type == "VARCHAR" && column.VarcharLen > 0 && len([]rune(stringValue)) > column.VarcharLen {
			return nil, fmt.Errorf("VARCHAR(%d) overflow", column.VarcharLen)
		}
		return stringValue, nil
	case "VECTOR":
		vec, err := eval.ToVector(value)
		if err != nil {
			return nil, err
		}
		if column.VarcharLen > 0 && len(vec) != column.VarcharLen {
			return nil, fmt.Errorf("VECTOR(%d) dimension mismatch: got %d", column.VarcharLen, len(vec))
		}
		return vec, nil
	case "FLEXIBLE":
		switch v := value.(type) {
		case map[string]interface{}:
			return v, nil
		case string:
			raw, err := storage.DecodeJSON([]byte(v))
			if err == nil {
				if m, ok := raw.(map[string]interface{}); ok {
					return m, nil
				}
			}
			return v, nil
		default:
			return ValueToString(value), nil
		}
	case "DATE", "TIME", "TIMESTAMP", "DECIMAL":
		return ValueToString(value), nil
	case "JSONB", "JSON":
		return ValueToString(value), nil
	default:
		return nil, fmt.Errorf("unsupported column type '%s'", column.Type)
	}
}

// EvalOperandFn evaluates a parser expression against a row.
var EvalOperandFn func(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error)

// EvalExprFn evaluates a parser expression and returns a boolean result.
var EvalExprFn func(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error)

// EvaluateCheckExprFn evaluates a CHECK constraint expression string against a row.
var EvaluateCheckExprFn func(exprStr string, row storage.Row, schema *storage.TableSchema) (bool, error)

// EvalOperand evaluates a parser expression against a row.
func EvalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	return EvalOperandFn(expr, row, schema, ctx)
}

// EvalExpr evaluates a parser expression and returns a boolean result.
func EvalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	return EvalExprFn(expr, row, schema, ctx)
}

// EvaluateCheckExpr evaluates a CHECK constraint expression string against a row.
func EvaluateCheckExpr(exprStr string, row storage.Row, schema *storage.TableSchema) (bool, error) {
	return EvaluateCheckExprFn(exprStr, row, schema)
}

// ─── DML Detection in Expressions ──────────────────────────────────────────

// ContainsSubqueryDML recursively walks a SELECT statement's expressions and
// subqueries to detect any non-SELECT (INSERT/UPDATE/DELETE) DML.
func ContainsSubqueryDML(sel *parser.SelectStatement) bool {
	// Walk CTEs
	for _, cte := range sel.CTEs {
		if ContainsStatementDML(cte.Query) {
			return true
		}
	}
	// Walk FROM subquery
	if sel.FromSubquery != nil {
		if ContainsSubqueryDML(sel.FromSubquery) {
			return true
		}
	}
	// Walk JOINs
	for _, j := range sel.Joins {
		if ContainsExprDML(j.Condition) {
			return true
		}
	}
	// Walk column expressions
	for _, col := range sel.Columns {
		if ContainsExprDML(col.Expr) {
			return true
		}
	}
	// Walk WHERE, HAVING
	if ContainsExprDML(sel.Where) {
		return true
	}
	if ContainsExprDML(sel.Having) {
		return true
	}
	// Walk GROUP BY, ORDER BY expressions
	for _, e := range sel.GroupBy {
		if ContainsExprDML(e) {
			return true
		}
	}
	for _, o := range sel.OrderBy {
		if ContainsExprDML(o.Expr) {
			return true
		}
	}
	return false
}

// ContainsStatementDML checks if a Statement contains DML subqueries.
func ContainsStatementDML(stmt parser.Statement) bool {
	if sel, ok := stmt.(*parser.SelectStatement); ok {
		return ContainsSubqueryDML(sel)
	}
	return false // non-SELECT statements as CTE body are fine here
}

// ContainsExprDML checks if an Expression tree contains a subquery with DML.
func ContainsExprDML(expr parser.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.SubqueryExpr:
		if sel, ok := e.Query.(*parser.SelectStatement); ok {
			return ContainsSubqueryDML(sel)
		}
		return true // non-SELECT subquery is DML
	case *parser.ExistsExpr:
		if sel, ok := e.Select.(*parser.SelectStatement); ok {
			return ContainsSubqueryDML(sel)
		}
		return true
	case *parser.ComparisonSubqueryExpr:
		if sel, ok := e.Subquery.(*parser.SelectStatement); ok {
			return ContainsSubqueryDML(sel)
		}
		return true
	case *parser.BinaryExpr:
		return ContainsExprDML(e.Left) || ContainsExprDML(e.Right)
	case *parser.AndExpr:
		return ContainsExprDML(e.Left) || ContainsExprDML(e.Right)
	case *parser.OrExpr:
		return ContainsExprDML(e.Left) || ContainsExprDML(e.Right)
	case *parser.NotExpr:
		return ContainsExprDML(e.Expr)
	case *parser.InExpr:
		if ContainsExprDML(e.Left) {
			return true
		}
		for _, r := range e.Right {
			if ContainsExprDML(r) {
				return true
			}
		}
		return false
	case *parser.BetweenExpr:
		return ContainsExprDML(e.Expr) || ContainsExprDML(e.Lower) || ContainsExprDML(e.Upper)
	case *parser.CaseExpr:
		if e.Base != nil && ContainsExprDML(e.Base) {
			return true
		}
		for _, w := range e.Whens {
			if ContainsExprDML(w.Condition) || ContainsExprDML(w.Result) {
				return true
			}
		}
		return e.Else != nil && ContainsExprDML(e.Else)
	case *parser.CastExpr:
		return ContainsExprDML(e.Expr)
	case *parser.FunctionCall:
		for _, a := range e.Args {
			if ContainsExprDML(a) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		for _, a := range e.Args {
			if ContainsExprDML(a) {
				return true
			}
		}
		return false
	case *parser.WindowFunctionExpr:
		for _, a := range e.Args {
			if ContainsExprDML(a) {
				return true
			}
		}
		for _, p := range e.Over.PartitionBy {
			if ContainsExprDML(p) {
				return true
			}
		}
		return false
	case *parser.WindowExpr:
		for _, p := range e.PartitionBy {
			if ContainsExprDML(p) {
				return true
			}
		}
		for _, o := range e.OrderBy {
			if ContainsExprDML(o.Expr) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
