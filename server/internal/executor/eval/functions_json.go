package eval

import (
	"context"
	"encoding/json"
	"fmt"

	"vaultdb/internal/ai"
	"vaultdb/internal/storage"
)

// FnJsonObject creates a JSON object from key-value pairs.
func FnJsonObject(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSON_OBJECT requires even number of arguments")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := ValueToString(args[i])
		obj[key] = args[i+1]
	}
	data, _ := json.Marshal(obj)
	return string(data), nil
}

// FnJsonArray creates a JSON array.
func FnJsonArray(args []interface{}, _ interface{}) (interface{}, error) {
	data, _ := json.Marshal(args)
	return string(data), nil
}

// FnJsonExtract extracts a value from JSON.
func FnJsonExtract(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSON_EXTRACT requires at least 2 arguments")
	}
	leftStr := ValueToString(args[0])
	data, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSON_EXTRACT: not valid JSON")
	}

	for i := 1; i < len(args); i++ {
		key := ValueToString(args[i])
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

// FnJsonbBuildObject builds a JSONB object.
func FnJsonbBuildObject(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSONB_BUILD_OBJECT requires even number of arguments")
	}
	obj := make(map[string]interface{})
	for i := 0; i < len(args); i += 2 {
		key := ValueToString(args[i])
		obj[key] = args[i+1]
	}
	data, _ := json.Marshal(obj)
	return string(data), nil
}

// FnJsonbBuildArray builds a JSONB array.
func FnJsonbBuildArray(args []interface{}, _ interface{}) (interface{}, error) {
	data, _ := json.Marshal(args)
	return string(data), nil
}

// FnJsonbArrayElements extracts elements from a JSONB array.
func FnJsonbArrayElements(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_ARRAY_ELEMENTS requires 1 argument")
	}
	s := ValueToString(args[0])
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

// FnJsonbTypeof returns the type of a JSONB value.
func FnJsonbTypeof(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("JSONB_TYPEOF requires 1 argument")
	}
	s := ValueToString(args[0])
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

// FnJsonbSet sets a value in a JSONB object.
func FnJsonbSet(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("JSONB_SET requires 3 arguments: target, path, new_value")
	}
	targetStr := ValueToString(args[0])
	path := ValueToString(args[1])
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

// FnJsonbExtractPath extracts a value by path from JSONB.
func FnJsonbExtractPath(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH requires at least 2 arguments")
	}
	leftStr := ValueToString(args[0])
	data, err := storage.DecodeJSON([]byte(leftStr))
	if err != nil {
		return nil, fmt.Errorf("JSONB_EXTRACT_PATH: not valid JSON")
	}

	for i := 1; i < len(args); i++ {
		key := ValueToString(args[i])
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

// FnAiEmbed generates an embedding vector.
func FnAiEmbed(args []interface{}, ctx interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("AI_EMBED requires 1 argument")
	}
	text := ValueToString(args[0])
	var embedder ai.Embedder
	var goCtx context.Context
	if ep, ok := ctx.(EmbedderProvider); ok {
		embedder = ep.GetEmbedder()
		goCtx = ep.GetGoContext()
	}
	vec, err := EmbedText(embedder, goCtx, text)
	if err != nil {
		return nil, fmt.Errorf("AI_EMBED: %w", err)
	}
	return vec, nil
}

// FnUuid generates a UUID.
func FnUuid(_ []interface{}, _ interface{}) (interface{}, error) {
	return GenerateUUID()
}

// FnInterval returns the interval string.
func FnInterval(args []interface{}, _ interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("INTERVAL requires 1 argument")
	}
	return ValueToString(args[0]), nil
}
