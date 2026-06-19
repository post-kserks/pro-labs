package executor

import (
	"encoding/json"
	"fmt"

	"vaultdb/internal/storage"
)

// ─── JSON Functions ─────────────────────────────────────────────────────────

func fnJsonObject(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSON_OBJECT requires even number of arguments")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := valueToString(args[i])
		obj[key] = args[i+1]
	}
	data, _ := json.Marshal(obj)
	return string(data), nil
}

func fnJsonArray(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	data, _ := json.Marshal(args)
	return string(data), nil
}

func fnJsonExtract(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSON_EXTRACT requires at least 2 arguments")
	}
	leftStr := valueToString(args[0])
	data, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON_EXTRACT: not valid JSON")
	}

	for i := 1; i < len(args); i++ {
		key := valueToString(args[i])
		switch v := data.(type) {
		case map[string]interface{}:
			data = v[key]
		case []interface{}:
			idx := 0
			if _, err := fmt.Sscanf(key, "%d", &idx); err == nil && idx < len(v) {
				data = v[idx]
			} else {
				data = nil
			}
		default:
			data = nil
		}
	}
	if data == nil {
		return nil, nil
	}
	result, _ := json.Marshal(data)
	return string(result), nil
}

func fnJsonbBuildObject(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSONB_BUILD_OBJECT requires even number of arguments")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := valueToString(args[i])
		obj[key] = args[i+1]
	}
	data, _ := json.Marshal(obj)
	return string(data), nil
}

func fnJsonbBuildArray(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	data, _ := json.Marshal(args)
	return string(data), nil
}

func fnJsonbArrayElements(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS requires 1 argument")
	}
	s := valueToString(args[0])
	raw, err := storage.DecodeJSON([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS: not a JSON array")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS: not a JSON array")
	}
	if len(arr) == 0 {
		return nil, nil
	}
	return arr[0], nil
}

func fnJsonbTypeof(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_TYPEOF requires 1 argument")
	}
	s := valueToString(args[0])
	v, err := storage.DecodeJSON([]byte(s))
	if err != nil {
		return "string", nil
	}
	switch v.(type) {
	case nil:
		return "null", nil
	case bool:
		return "boolean", nil
	case int64, float64:
		return "number", nil
	case string:
		return "string", nil
	case []interface{}:
		return "array", nil
	case map[string]interface{}:
		return "object", nil
	default:
		return "string", nil
	}
}

func fnJsonbSet(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("JSONB_SET requires 3 arguments: target, path, new_value")
	}
	targetStr := valueToString(args[0])
	path := valueToString(args[1])
	newVal := args[2]

	raw, err := storage.DecodeJSON([]byte(targetStr))
	if err != nil {
		return nil, fmt.Errorf("JSONB_SET: target is not JSON object")
	}
	data, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("JSONB_SET: target is not JSON object")
	}
	data[path] = newVal
	result, _ := json.Marshal(data)
	return string(result), nil
}

func fnJsonbExtractPath(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH requires at least 2 arguments")
	}
	leftStr := valueToString(args[0])
	data, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH: not valid JSON")
	}

	for i := 1; i < len(args); i++ {
		key := valueToString(args[i])
		switch v := data.(type) {
		case map[string]interface{}:
			data = v[key]
		case []interface{}:
			idx := 0
			if _, err := fmt.Sscanf(key, "%d", &idx); err == nil && idx < len(v) {
				data = v[idx]
			} else {
				data = nil
			}
		default:
			data = nil
		}
	}
	if data == nil {
		return nil, nil
	}
	result, _ := json.Marshal(data)
	return string(result), nil
}

// ─── Misc Functions ─────────────────────────────────────────────────────────

func fnAiEmbed(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("AI_EMBED requires 1 argument")
	}
	text := valueToString(args[0])
	vec, err := embedText(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("AI_EMBED: %w", err)
	}
	return vec, nil
}

func fnUuid(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	return generateUUID()
}

func fnInterval(args []interface{}, ctx *ExecutionContext) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INTERVAL requires 1 argument")
	}
	return valueToString(args[0]), nil
}
