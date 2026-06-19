package executor

import (
	"fmt"
	"math"
)

// ─── Math Functions ─────────────────────────────────────────────────────────

func fnMod(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("MOD requires 2 arguments")
	}
	a, ok1 := toFloat(args[0])
	b, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("MOD requires numeric arguments")
	}
	if b == 0 {
		return nil, fmt.Errorf("MOD: division by zero")
	}
	return a - (float64(int(a/b)) * b), nil
}

func fnPower(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("POWER requires 2 arguments")
	}
	base, ok1 := toFloat(args[0])
	exp, ok2 := toFloat(args[1])
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("POWER requires numeric arguments")
	}
	return math.Pow(base, exp), nil
}

func fnSqrt(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SQRT requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("SQRT requires numeric argument")
	}
	if v < 0 {
		return nil, fmt.Errorf("SQRT of negative number")
	}
	return math.Sqrt(v), nil
}

func fnLn(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LN requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LN requires numeric argument")
	}
	if v <= 0 {
		return nil, fmt.Errorf("LN of non-positive number")
	}
	return math.Log(v), nil
}

func fnLog(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("LOG requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("LOG requires numeric argument")
	}
	if v <= 0 {
		return nil, fmt.Errorf("LOG of non-positive number")
	}
	return math.Log10(v), nil
}

func fnExp(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("EXP requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("EXP requires numeric argument")
	}
	return math.Exp(v), nil
}

func fnSign(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("SIGN requires 1 argument")
	}
	v, ok := toFloat(args[0])
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

func fnAbs(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ABS requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ABS requires numeric argument")
	}
	return math.Abs(v), nil
}

func fnCeil(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("CEIL requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("CEIL requires numeric argument")
	}
	return math.Ceil(v), nil
}

func fnFloor(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("FLOOR requires 1 argument")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("FLOOR requires numeric argument")
	}
	return math.Floor(v), nil
}

func fnRound(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("ROUND requires 1 or 2 arguments")
	}
	v, ok := toFloat(args[0])
	if !ok {
		return nil, fmt.Errorf("ROUND requires numeric argument")
	}
	places := 0
	if len(args) == 2 {
		if p, ok := toInt64(args[1]); ok {
			places = int(p)
		}
	}
	if places == 0 {
		return math.Round(v), nil
	}
	factor := math.Pow(10, float64(places))
	return math.Round(v*factor) / factor, nil
}

func fnCoalesce(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	for _, arg := range args {
		if arg != nil {
			return arg, nil
		}
	}
	return nil, nil
}

func fnNullif(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("NULLIF requires 2 arguments")
	}
	if args[0] == nil || args[1] == nil {
		return args[0], nil
	}
	eq, _ := compareValues(args[0], args[1], "=")
	if eq {
		return nil, nil
	}
	return args[0], nil
}

func fnGreatest(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("GREATEST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		cmp, _ := compareValues(result, arg, "<")
		if cmp {
			result = arg
		}
	}
	return result, nil
}

func fnLeast(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("LEAST requires at least 1 argument")
	}
	result := args[0]
	for _, arg := range args[1:] {
		cmp, _ := compareValues(result, arg, ">")
		if cmp {
			result = arg
		}
	}
	return result, nil
}
