package executor

import (
	"fmt"
	"math"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func evalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema) (bool, error) {
	switch e := expr.(type) {
	case nil:
		return true, nil
	case *parser.BinaryExpr:
		return evalBinary(e, row, schema)
	case *parser.AndExpr:
		left, err := evalExpr(e.Left, row, schema)
		if err != nil || !left {
			return left, err
		}
		return evalExpr(e.Right, row, schema)
	case *parser.OrExpr:
		left, err := evalExpr(e.Left, row, schema)
		if err != nil {
			return false, err
		}
		if left {
			return true, nil
		}
		return evalExpr(e.Right, row, schema)
	case *parser.NotExpr:
		value, err := evalExpr(e.Expr, row, schema)
		if err != nil {
			return false, err
		}
		return !value, nil
	case parser.Value:
		if e.Type != "bool" {
			return false, fmt.Errorf("WHERE literal '%s' must be boolean", e.Type)
		}
		return e.BoolVal, nil
	case *parser.ColumnRef:
		value, err := resolveColumn(row, schema, e.Name)
		if err != nil {
			return false, err
		}
		boolValue, ok := value.(bool)
		if !ok {
			return false, fmt.Errorf("column '%s' is not BOOL and cannot be used as condition", e.Name)
		}
		return boolValue, nil
	default:
		return false, fmt.Errorf("unsupported expression type %T", expr)
	}
}

func evalBinary(expr *parser.BinaryExpr, row storage.Row, schema *storage.TableSchema) (bool, error) {
	left, err := evalOperand(expr.Left, row, schema)
	if err != nil {
		return false, err
	}
	right, err := evalOperand(expr.Right, row, schema)
	if err != nil {
		return false, err
	}

	return compareValues(left, right, expr.Operator)
}

func evalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema) (interface{}, error) {
	switch e := expr.(type) {
	case parser.Value:
		return parserValueToRaw(e), nil
	case *parser.ColumnRef:
		return resolveColumn(row, schema, e.Name)
	case *parser.BinaryExpr, *parser.AndExpr, *parser.OrExpr, *parser.NotExpr:
		result, err := evalExpr(e, row, schema)
		if err != nil {
			return nil, err
		}
		return result, nil
	default:
		return nil, fmt.Errorf("invalid operand type %T", expr)
	}
}

func resolveColumn(row storage.Row, schema *storage.TableSchema, name string) (interface{}, error) {
	for i, column := range schema.Columns {
		if strings.EqualFold(column.Name, name) {
			if i >= len(row) {
				return nil, fmt.Errorf("column '%s' index is out of row bounds", name)
			}
			return row[i], nil
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
