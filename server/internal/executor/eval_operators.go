package executor

import (
	"fmt"
	"math"
)

// compareValues сравнивает два значения по оператору.
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

// CompareValues возвращает -1 если a < b, 1 если a > b, 0 если a == b.
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

	return -1
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

// evalArithmetic выполняет арифметические операции.
func evalArithmetic(left, right interface{}, op string) (interface{}, error) {
	if left == nil || right == nil {
		return nil, nil
	}

	leftStr := valueToString(left)
	rightStr := valueToString(right)
	if isIntervalString(rightStr) && (op == "+" || op == "-") {
		return evalDateInterval(leftStr, rightStr, op)
	}
	if isIntervalString(leftStr) && (op == "+" || op == "-") {
		return evalDateInterval(rightStr, leftStr, op)
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

	_, lint := left.(int64)
	if !lint {
		_, lint = left.(int)
	}
	_, rint := right.(int64)
	if !rint {
		_, rint = right.(int)
	}

	if lint && rint && op != "/" {
		if res > float64(math.MaxInt64) || res < float64(math.MinInt64) {
			return nil, fmt.Errorf("value out of int64 range")
		}
		return int64(res), nil
	}

	return res, nil
}
