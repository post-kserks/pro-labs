package eval

import (
	"fmt"
	"math"
)

// toFloatFast converts int, int64, float64 to float64 without string parsing or allocations.
func toFloatFast(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int64:
		return float64(v), true
	case int:
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

// CompareValues compares two values using an operator.
func CompareValues(left, right interface{}, op string) (bool, error) {
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

	if l, ok := left.(int64); ok {
		if r, ok := right.(int64); ok {
			return compareInt64(l, r, op), nil
		}
	}
	if l, ok := left.(string); ok {
		if r, ok := right.(string); ok {
			return compareString(l, r, op), nil
		}
	}

	// Numeric Fast-Path
	if lf, lok := toFloatFast(left); lok {
		if rf, rok := toFloatFast(right); rok {
			return compareOrdered(lf, rf, op)
		}
	}

	if lf, lok := ToFloat(left); lok {
		rf, rok := ToFloat(right)
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

// CompareOrdering returns -1 if a < b, 1 if a > b, 0 if a == b.
func CompareOrdering(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	// Numeric Fast-Path
	switch av := a.(type) {
	case int64:
		switch bv := b.(type) {
		case int64:
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		case int:
			b64 := int64(bv)
			if av < b64 {
				return -1
			}
			if av > b64 {
				return 1
			}
			return 0
		case float64:
			if math.IsNaN(bv) {
				break
			}
			af := float64(av)
			if af < bv {
				return -1
			}
			if af > bv {
				return 1
			}
			return 0
		}
	case int:
		switch bv := b.(type) {
		case int:
			if av < bv {
				return -1
			}
			if av > bv {
				return 1
			}
			return 0
		case int64:
			a64 := int64(av)
			if a64 < bv {
				return -1
			}
			if a64 > bv {
				return 1
			}
			return 0
		case float64:
			if math.IsNaN(bv) {
				break
			}
			af := float64(av)
			if af < bv {
				return -1
			}
			if af > bv {
				return 1
			}
			return 0
		}
	case float64:
		if !math.IsNaN(av) {
			switch bv := b.(type) {
			case float64:
				if !math.IsNaN(bv) {
					if av < bv {
						return -1
					}
					if av > bv {
						return 1
					}
					return 0
				}
			case int64:
				bf := float64(bv)
				if av < bf {
					return -1
				}
				if av > bf {
					return 1
				}
				return 0
			case int:
				bf := float64(bv)
				if av < bf {
					return -1
				}
				if av > bf {
					return 1
				}
				return 0
			}
		}
	}

	if af, aok := ToFloat(a); aok {
		if bf, bok := ToFloat(b); bok {
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

func compareInt64(l, r int64, op string) bool {
	switch op {
	case "=":
		return l == r
	case "!=":
		return l != r
	case "<":
		return l < r
	case ">":
		return l > r
	case "<=":
		return l <= r
	case ">=":
		return l >= r
	default:
		return false
	}
}

func compareString(l, r string, op string) bool {
	switch op {
	case "=":
		return l == r
	case "!=":
		return l != r
	case "<":
		return l < r
	case ">":
		return l > r
	case "<=":
		return l <= r
	case ">=":
		return l >= r
	default:
		return false
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

// EvalArithmetic performs arithmetic operations.
func EvalArithmetic(left, right interface{}, op string) (interface{}, error) {
	if left == nil || right == nil {
		return nil, nil
	}

	// Numeric Fast-Path
	if lf, lok := toFloatFast(left); lok {
		if rf, rok := toFloatFast(right); rok {
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
			default:
				return nil, fmt.Errorf("unknown operator '%s'", op)
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
	}

	leftStr := ValueToString(left)
	rightStr := ValueToString(right)
	if IsIntervalString(rightStr) && (op == "+" || op == "-") {
		return EvalDateInterval(leftStr, rightStr, op)
	}
	if IsIntervalString(leftStr) && (op == "+" || op == "-") {
		return EvalDateInterval(rightStr, leftStr, op)
	}

	lf, lok := ToFloat(left)
	rf, rok := ToFloat(right)
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
