package executor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─── Array Functions ────────────────────────────────────────────────────────

func fnArrayAppend(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_APPEND requires 2 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	arr = append(arr, args[1])
	data, _ := json.Marshal(arr)
	return string(data), nil
}

func fnArrayLength(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ARRAY_LENGTH requires 1 argument")
	}
	return int64(len(parseJSONArray(valueToString(args[0])))), nil
}

func fnArrayContains(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_CONTAINS requires 2 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	valStr := valueToString(args[1])
	for _, v := range arr {
		if valueToString(v) == valStr {
			return true, nil
		}
	}
	return false, nil
}

func fnArrayToString(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_TO_STRING requires 2 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	delim := valueToString(args[1])
	parts := make([]string, len(arr))
	for i, v := range arr {
		parts[i] = valueToString(v)
	}
	return strings.Join(parts, delim), nil
}

func fnArraySlice(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("ARRAY_SLICE requires 2 or 3 arguments")
	}
	arr := parseJSONArray(valueToString(args[0]))
	start, _ := toInt64(args[1])
	end := int64(len(arr))
	if len(args) == 3 {
		end, _ = toInt64(args[2])
	}
	if start < 0 {
		start = 0
	}
	if end > int64(len(arr)) {
		end = int64(len(arr))
	}
	if start >= end {
		return "[]", nil
	}
	sliced := arr[start:end]
	data, _ := json.Marshal(sliced)
	return string(data), nil
}
