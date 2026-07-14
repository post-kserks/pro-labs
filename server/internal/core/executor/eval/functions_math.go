package eval

import (
	"fmt"
	"math"
)

// FnMod computes modulo.
func FnMod(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("MOD requires 2 arguments")
	}
	a, ok1 := ToFloat(args[0])
	b, ok2 := ToFloat(args[1])
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("MOD requires numeric arguments")
	}
	if b == 0 {
		return nil, fmt.Errorf("MOD: division by zero")
	}
	return a - (float64(int(a/b)) * b), nil
}

// FnPower computes power.
func FnPower(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POWER requires 2 arguments")
	}
	base, ok1 := ToFloat(args[0])
	exp, ok2 := ToFloat(args[1])
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("POWER requires numeric arguments")
	}
	return math.Pow(base, exp), nil
}

// FnSqrt computes square root.
func FnSqrt(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SQRT requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SQRT requires numeric argument")
	}
	if v < 0 {
		return nil, fmt.Errorf("SQRT of negative number")
	}
	return math.Sqrt(v), nil
}

// FnLn computes natural logarithm.
func FnLn(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LN requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LN requires numeric argument")
	}
	if v <= 0 {
		return nil, fmt.Errorf("LN of non-positive number")
	}
	return math.Log(v), nil
}

// FnLog computes base-10 logarithm.
func FnLog(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LOG requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LOG requires numeric argument")
	}
	if v <= 0 {
		return nil, fmt.Errorf("LOG of non-positive number")
	}
	return math.Log10(v), nil
}

// FnExp computes e^x.
func FnExp(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("EXP requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("EXP requires numeric argument")
	}
	return math.Exp(v), nil
}

// FnSign returns the sign of a number.
func FnSign(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SIGN requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SIGN requires numeric argument")
	}
	if v > 0 {
		return int64(1), nil
	}
	if v < 0 {
		return int64(-1), nil
	}
	return int64(0), nil
}

// FnAbs computes absolute value.
func FnAbs(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ABS requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ABS requires numeric argument")
	}
	return math.Abs(v), nil
}

// FnCeil rounds up.
func FnCeil(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("CEIL requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("CEIL requires numeric argument")
	}
	return math.Ceil(v), nil
}

// FnFloor rounds down.
func FnFloor(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("FLOOR requires 1 argument")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("FLOOR requires numeric argument")
	}
	return math.Floor(v), nil
}

// FnRound rounds to specified places.
func FnRound(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("ROUND requires 1 or 2 arguments")
	}
	v, ok := ToFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ROUND requires numeric argument")
	}
	places := 0
	if len(args) == 2 {
		if p, ok := ToInt64(args[1]); ok {
			places = int(p)
		}
	}
	if places == 0 {
		return math.Round(v), nil
	}
	factor := math.Pow(10, float64(places))
	return math.Round(v*factor) / factor, nil
}

// FnCoalesce returns the first non-null argument.
func FnCoalesce(args []interface{}, _ interface{}) (interface{}, error) {
	for _, arg := range args {
		if arg != nil {
			return arg, nil
		}
	}
	return nil, nil
}

// FnNullif returns null if a == b.
func FnNullif(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("NULLIF requires 2 arguments")
	}
	if args[0] == nil || args[1] == nil {
		return args[0], nil
	}
	eq, _ := CompareValues(args[0], args[1], "=")
	if eq {
		return nil, nil
	}
	return args[0], nil
}

// FnGreatest returns the largest value.
func FnGreatest(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("GREATEST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		cmp, _ := CompareValues(result, arg, "<")
		if cmp {
			result = arg
		}
	}
	return result, nil
}

// FnLeast returns the smallest value.
func FnLeast(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("LEAST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		cmp, _ := CompareValues(result, arg, ">")
		if cmp {
			result = arg
		}
	}
	return result, nil
}
