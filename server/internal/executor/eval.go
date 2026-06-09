package executor

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func evalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (bool, error) {
	if expr == nil {
		return true, nil
	}

	val, err := evalOperand(expr, row, schema, ctx)
	if err != nil {
		return false, err
	}

	if b, ok := val.(bool); ok {
		return b, nil
	}

	return false, fmt.Errorf("expression must return boolean, got %T", val)
}

func evalBinary(expr *parser.BinaryExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	left, err := evalOperand(expr.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	right, err := evalOperand(expr.Right, row, schema, ctx)
	if err != nil {
		return nil, err
	}

	switch expr.Operator {
	case "=", "!=", "<", ">", "<=", ">=":
		return compareValues(left, right, expr.Operator)
	case "+", "-", "*", "/":
		return evalArithmetic(left, right, expr.Operator)
	case "SEMANTIC_MATCH":
		return evalSemanticMatch(left, right)
	case "FTS_MATCH":
		return evalFtsMatch(left, right)
	default:
		return nil, fmt.Errorf("unsupported operator '%s'", expr.Operator)
	}
}

func evalFtsMatch(left, right interface{}) (bool, error) {
	text := strings.ToLower(valueToString(left))
	query := strings.ToLower(valueToString(right))

	// Simplified BM25 / TF-IDF
	queryTerms := strings.Fields(query)
	if len(queryTerms) == 0 {
		return true, nil
	}

	docTerms := strings.Fields(text)
	docFreq := make(map[string]int)
	for _, term := range docTerms {
		docFreq[term]++
	}

	score := 0.0
	for _, q := range queryTerms {
		if count, ok := docFreq[q]; ok {
			// TF part
			score += float64(count) / float64(len(docTerms))
		}
	}

	return score > 0.1, nil // Threshold for "match"
}

func evalSemanticMatch(left, right interface{}) (bool, error) {
	v1, err := toVector(left)
	if err != nil {
		v1 = mockEmbed(valueToString(left))
	}
	v2, err := toVector(right)
	if err != nil {
		v2 = mockEmbed(valueToString(right))
	}

	sim := cosineSimilarity(v1, v2)
	return sim > 0.7, nil
}

func toVector(val interface{}) ([]float64, error) {
	switch v := val.(type) {
	case []float64:
		return v, nil
	case []interface{}:
		res := make([]float64, len(v))
		for i, x := range v {
			if f, ok := toFloat(x); ok {
				res[i] = f
			}
		}
		return res, nil
	case string:
		var res []float64
		if err := json.Unmarshal([]byte(v), &res); err == nil {
			return res, nil
		}
	}
	return nil, fmt.Errorf("cannot convert %T to VECTOR", val)
}

func cosineSimilarity(v1, v2 []float64) float64 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0
	}
	var dot, n1, n2 float64
	for i := range v1 {
		dot += v1[i] * v2[i]
		n1 += v1[i] * v1[i]
		n2 += v2[i] * v2[i]
	}
	if n1 == 0 || n2 == 0 {
		return 0
	}
	return dot / (math.Sqrt(n1) * math.Sqrt(n2))
}

func mockEmbed(text string) []float64 {
	text = strings.ToLower(text)
	res := make([]float64, 8)
	
	// Influence vector by keywords
	if strings.Contains(text, "database") || strings.Contains(text, "sql") || strings.Contains(text, "storage") {
		res[0] = 1.0
	}
	if strings.Contains(text, "ai") || strings.Contains(text, "artificial") || strings.Contains(text, "intelligence") || strings.Contains(text, "neural") || strings.Contains(text, "network") {
		res[4] = 1.0
	}

	// Add some hash-based noise but keep it small
	var h uint32
	for _, b := range []byte(text) {
		h = h*31 + uint32(b)
	}
	for i := range res {
		res[i] += math.Sin(float64(h)+float64(i)) * 0.1
	}
	return res
}

func evalArithmetic(left, right interface{}, op string) (interface{}, error) {
	if left == nil || right == nil {
		return nil, nil
	}

	lf, lok := toFloat(left)
	rf, rok := toFloat(right)
	if !lok || !rok {
		return nil, fmt.Errorf("arithmetic requires numeric operands, got %T and %T", left, right)
	}

	var res float64
	switch op {
	case "+":
		res = lf + rf
	case "-":
		res = lf - rf
	case "*":
		res = lf * rf
	case "/":
		if rf == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		res = lf / rf
	}

	// If both were integers, try to return integer
	_, lint := left.(int64)
	if !lint {
		_, lint = left.(int)
	}
	_, rint := right.(int64)
	if !rint {
		_, rint = right.(int)
	}

	if lint && rint && op != "/" {
		return int64(res), nil
	}

	return res, nil
}

func evalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	if expr == nil {
		return nil, nil
	}
	switch e := expr.(type) {
	case parser.Value:
		return parserValueToRaw(e), nil
	case *parser.Value:
		return parserValueToRaw(*e), nil
	case *parser.ColumnRef:
		return resolveColumn(row, schema, e.Name)
	case *parser.BinaryExpr:
		return evalBinary(e, row, schema, ctx)
	case *parser.AndExpr:
		left, err := evalExpr(e.Left, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(e.Right, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return left && right, nil
	case *parser.OrExpr:
		left, err := evalExpr(e.Left, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(e.Right, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return left || right, nil
	case *parser.NotExpr:
		val, err := evalExpr(e.Expr, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		return !val, nil
	case *parser.SubqueryExpr:
		return executeSubquery(e, row, schema, ctx)
	case *parser.InExpr:
		return evalInExpr(e, row, schema, ctx)
	case *parser.WindowFunctionExpr:
		return resolveColumn(row, schema, "window_func")
	case *parser.FunctionCall:
		return evalFunctionCall(e, row, schema, ctx)
	case *parser.CastExpr:
		return evalCast(e, row, schema, ctx)
	case *parser.CaseExpr:
		return evalCase(e, row, schema, ctx)
	case *parser.JsonPathExpr:
		return evalJsonPath(e, row, schema, ctx)
	case *parser.AggregateExpr:
		return nil, fmt.Errorf("aggregate function %s() not allowed here", e.Name)
	default:
		return nil, fmt.Errorf("invalid operand type %T", expr)
	}
}

func evalFunctionCall(fn *parser.FunctionCall, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	args := make([]interface{}, len(fn.Args))
	for i, arg := range fn.Args {
		val, err := evalOperand(arg, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		args[i] = val
	}

	name := strings.ToUpper(fn.Name)
	switch name {
	case "NOW":
		return time.Now().UTC().Format(time.RFC3339), nil
	case "UPPER":
		if len(args) != 1 {
			return nil, fmt.Errorf("UPPER requires 1 argument")
		}
		if s, ok := args[0].(string); ok {
			return strings.ToUpper(s), nil
		}
		return nil, fmt.Errorf("UPPER requires string argument")
	case "LOWER":
		if len(args) != 1 {
			return nil, fmt.Errorf("LOWER requires 1 argument")
		}
		if s, ok := args[0].(string); ok {
			return strings.ToLower(s), nil
		}
		return nil, fmt.Errorf("LOWER requires string argument")
	case "CONCAT":
		var sb strings.Builder
		for _, arg := range args {
			sb.WriteString(valueToString(arg))
		}
		return sb.String(), nil
	case "ABS":
		if len(args) != 1 {
			return nil, fmt.Errorf("ABS requires 1 argument")
		}
		if f, ok := toFloat(args[0]); ok {
			return math.Abs(f), nil
		}
		return nil, fmt.Errorf("ABS requires numeric argument")
	case "CEIL", "CEILING":
		if len(args) != 1 {
			return nil, fmt.Errorf("%s requires 1 argument", name)
		}
		if f, ok := toFloat(args[0]); ok {
			return math.Ceil(f), nil
		}
		return nil, fmt.Errorf("%s requires numeric argument", name)
	case "FLOOR":
		if len(args) != 1 {
			return nil, fmt.Errorf("FLOOR requires 1 argument")
		}
		if f, ok := toFloat(args[0]); ok {
			return math.Floor(f), nil
		}
		return nil, fmt.Errorf("FLOOR requires numeric argument")
	case "ROUND":
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("ROUND requires 1 or 2 arguments")
		}
		f, ok := toFloat(args[0])
		if !ok {
			return nil, fmt.Errorf("ROUND requires numeric argument")
		}
		places := 0.0
		if len(args) == 2 {
			if p, ok := toFloat(args[1]); ok {
				places = p
			}
		}
		shift := math.Pow(10, places)
		return math.Round(f*shift) / shift, nil
	case "COALESCE":
		for _, arg := range args {
			if arg != nil {
				return arg, nil
			}
		}
		return nil, nil
	case "AI_EMBED":
		if len(args) != 1 {
			return nil, fmt.Errorf("AI_EMBED requires 1 argument")
		}
		text := valueToString(args[0])
		return mockEmbed(text), nil
	default:
		return nil, fmt.Errorf("unknown function: %s", name)
	}
}

func executeSubquery(sub *parser.SubqueryExpr, outerRow storage.Row, outerSchema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	// For correlated subqueries, we would need to pass outerRow/outerSchema.
	// For now, let's implement simple non-correlated subqueries.
	cmd, err := CommandFactory(sub.Query)
	if err != nil {
		return nil, err
	}

	res, err := cmd.Execute(ctx)
	if err != nil {
		return nil, err
	}

	if len(res.Rows) == 0 {
		return nil, nil
	}
	if len(res.Rows) > 1 {
		return nil, fmt.Errorf("scalar subquery returned more than one row")
	}
	if len(res.Rows[0]) != 1 {
		return nil, fmt.Errorf("scalar subquery returned more than one column")
	}

	val := res.Rows[0][0]
	// Try to parse back to numeric type if possible to avoid type mismatches in comparison
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f, nil
	}

	return val, nil
}

func evalInExpr(e *parser.InExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	leftVal, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}

	// Check if Right has a subquery
	if len(e.Right) == 1 {
		if sub, ok := e.Right[0].(*parser.SubqueryExpr); ok {
			cmd, err := CommandFactory(sub.Query)
			if err != nil {
				return nil, err
			}
			res, err := cmd.Execute(ctx)
			if err != nil {
				return nil, err
			}
			
			found := false
			leftStr := valueToString(leftVal)
			for _, row := range res.Rows {
				if len(row) > 0 && row[0] == leftStr {
					found = true
					break
				}
			}
			if e.Not {
				return !found, nil
			}
			return found, nil
		}
	}

	// Normal IN (val1, val2, ...)
	found := false
	for _, rightExpr := range e.Right {
		rightVal, err := evalOperand(rightExpr, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if CompareValues(leftVal, rightVal) == 0 {
			found = true
			break
		}
	}

	if e.Not {
		return !found, nil
	}
	return found, nil
}

func resolveColumn(row storage.Row, schema *storage.TableSchema, name string) (interface{}, error) {
	for i, column := range schema.Columns {
		// Try exact match (including potential Table.Column in schema)
		if strings.EqualFold(column.Name, name) {
			if i < len(row) {
				return row[i], nil
			}
		}

		// Try matching just the column part if name is unqualified
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

func compareValues(left, right interface{}, op string) (bool, error) {
	if left == nil || right == nil {
		switch op {
		case "=":
			return left == nil && right == nil, nil
		case "!=":
			return !(left == nil && right == nil), nil
		default:
			return false, nil
		}
	}

	if lf, lok := toFloat(left); lok {
		rf, rok := toFloat(right)
		if !rok {
			return false, fmt.Errorf("type mismatch in comparison: %T %s %T", left, op, right)
		}
		return compareOrdered(lf, rf, op)
	}

	switch l := left.(type) {
	case string:
		r, ok := right.(string)
		if !ok {
			return false, fmt.Errorf("type mismatch in comparison: %T %s %T", left, op, right)
		}
		return compareOrdered(l, r, op)
	case bool:
		r, ok := right.(bool)
		if !ok {
			return false, fmt.Errorf("type mismatch in comparison: %T %s %T", left, op, right)
		}
		switch op {
		case "=":
			return l == r, nil
		case "!=":
			return l != r, nil
		default:
			return false, fmt.Errorf("operator '%s' is not supported for BOOL", op)
		}
	default:
		return false, fmt.Errorf("unsupported comparison type %T", left)
	}
}

// CompareValues returns -1 if a < b, 1 if a > b, and 0 if a == b.
// It handles mixed numeric types and NULLs (nil).
func CompareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			if af < bf {
				return -1
			}
			if af > bf {
				return 1
			}
			return 0
		}
	}

	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		}
	case bool:
		if bv, ok := b.(bool); ok {
			if av == bv {
				return 0
			}
			if !av {
				return -1
			}
			return 1
		}
	}

	return 0
}

func compareOrdered[T ~float64 | ~string](left, right T, op string) (bool, error) {
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
	default:
		return 0, false
	}
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func evalCast(e *parser.CastExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	val, err := evalOperand(e.Expr, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}

	switch strings.ToUpper(e.TargetType) {
	case "INT":
		if i, ok := toInt64(val); ok {
			return i, nil
		}
		if f, ok := toFloat(val); ok {
			return int64(f), nil
		}
		if s, ok := val.(string); ok {
			if i, err := strconv.ParseInt(s, 10, 64); err == nil {
				return i, nil
			}
		}
	case "FLOAT":
		if f, ok := toFloat(val); ok {
			return f, nil
		}
		if s, ok := val.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, nil
			}
		}
	case "TEXT", "VARCHAR":
		return valueToString(val), nil
	case "BOOL":
		if b, ok := val.(bool); ok {
			return b, nil
		}
		s := strings.ToUpper(valueToString(val))
		if s == "TRUE" || s == "1" {
			return true, nil
		}
		if s == "FALSE" || s == "0" {
			return false, nil
		}
	}

	return nil, fmt.Errorf("cannot cast %T to %s", val, e.TargetType)
}

func evalCase(e *parser.CaseExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	var baseVal interface{}
	var err error
	if e.Base != nil {
		baseVal, err = evalOperand(e.Base, row, schema, ctx)
		if err != nil {
			return nil, err
		}
	}

	for _, when := range e.Whens {
		if baseVal != nil {
			// CASE base WHEN val THEN ...
			whenVal, err := evalOperand(when.Condition, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if CompareValues(baseVal, whenVal) == 0 {
				return evalOperand(when.Result, row, schema, ctx)
			}
		} else {
			// CASE WHEN cond THEN ...
			match, err := evalExpr(when.Condition, row, schema, ctx)
			if err != nil {
				return nil, err
			}
			if match {
				return evalOperand(when.Result, row, schema, ctx)
			}
		}
	}

	if e.Else != nil {
		return evalOperand(e.Else, row, schema, ctx)
	}

	return nil, nil
}

func evalJsonPath(e *parser.JsonPathExpr, row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) (interface{}, error) {
	left, err := evalOperand(e.Left, row, schema, ctx)
	if err != nil {
		return nil, err
	}
	if left == nil {
		return nil, nil
	}

	var data map[string]interface{}
	switch v := left.(type) {
	case map[string]interface{}:
		data = v
	case string:
		if err := json.Unmarshal([]byte(v), &data); err != nil {
			return nil, nil
		}
	default:
		return nil, nil
	}

	val, ok := data[e.Path]
	if !ok {
		return nil, nil
	}

	if e.Op == "->>" {
		return valueToString(val), nil
	}
	return val, nil
}
