package eval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FnArrayAppend appends a value to an array.
func FnArrayAppend(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_APPEND requires 2 arguments")
	}
	arr := ParseJSONArray(ValueToString(args[0]))
	arr = append(arr, args[1])
	data, _ := json.Marshal(arr)
	return string(data), nil
}

// FnArrayLength returns the length of an array.
func FnArrayLength(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("ARRAY_LENGTH requires 1 argument")
	}
	return int64(len(ParseJSONArray(ValueToString(args[0])))), nil
}

// FnArrayContains checks if an array contains a value.
func FnArrayContains(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_CONTAINS requires 2 arguments")
	}
	arr := ParseJSONArray(ValueToString(args[0]))
	valStr := ValueToString(args[1])
	for _, v := range arr {
		if ValueToString(v) == valStr {
			return true, nil
		}
	}
	return false, nil
}

// FnArrayToString joins array elements with a delimiter.
func FnArrayToString(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ARRAY_TO_STRING requires 2 arguments")
	}
	arr := ParseJSONArray(ValueToString(args[0]))
	delim := ValueToString(args[1])
	parts := make([]string, len(arr))
	for i, v := range arr {
		parts[i] = ValueToString(v)
	}
	return strings.Join(parts, delim), nil
}

// FnArraySlice returns a slice of an array.
func FnArraySlice(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, fmt.Errorf("ARRAY_SLICE requires 2 or 3 arguments")
	}
	arr := ParseJSONArray(ValueToString(args[0]))
	start, _ := ToInt64(args[1])
	end := int64(len(arr))
	if len(args) == 3 {
		end, _ = ToInt64(args[2])
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
