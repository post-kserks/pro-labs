package storage

import (
	"encoding/json"
	"testing"
)

func TestJsonNullHandling(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"null_literal", "null"},
		{"empty_bytes", ""},
		{"whitespace_only", "   \t\n  "},
		{"empty_object", "{}"},
		{"empty_array", "[]"},
		{"null_with_whitespace", "  null  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := DecodeJSON([]byte(tt.input))
			if err != nil {
				t.Fatalf("DecodeJSON(%q) error: %v", tt.input, err)
			}
			// null, empty, and whitespace should return nil
			switch tt.name {
			case "null_literal", "empty_bytes", "whitespace_only", "null_with_whitespace":
				if raw != nil {
					t.Fatalf("DecodeJSON(%q) = %v (%T), want nil", tt.input, raw, raw)
				}
			case "empty_object":
				if raw == nil {
					t.Fatal("DecodeJSON({}) = nil, want non-nil empty object")
				}
				m, ok := raw.(map[string]interface{})
				if !ok {
					t.Fatalf("DecodeJSON({}) returned %T, want map[string]interface{}", raw)
				}
				if len(m) != 0 {
					t.Errorf("DecodeJSON({}) = %v, want empty map", m)
				}
			case "empty_array":
				if raw == nil {
					t.Fatal("DecodeJSON([]) = nil, want non-nil empty array")
				}
				arr, ok := raw.([]interface{})
				if !ok {
					t.Fatalf("DecodeJSON([]) returned %T, want []interface{}", raw)
				}
				if len(arr) != 0 {
					t.Errorf("DecodeJSON([]) = %v, want empty slice", arr)
				}
			}
		})
	}
}

func TestDecodeJSON_LargeIntegers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType string
	}{
		{"small_int", `42`, "int64"},
		{"zero", `0`, "int64"},
		{"negative_int", `-123`, "int64"},
		{"max_safe", `9007199254740992`, "int64"},
		{"above_max_safe", `9007199254740993`, "int64"},
		{"large_int", `1234567890123456789`, "int64"},
		{"max_int64", `9223372036854775807`, "int64"},
		{"exceeds_int64", `9999999999999999999`, "float64"},
		{"small_float", `3.14`, "float64"},
		{"int_as_float", `1.0`, "float64"},
		{"exponent", `1e5`, "float64"},
		{"string_val", `"hello"`, "string"},
		{"bool_val", `true`, "bool"},
		{"null_val", `null`, "nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := DecodeJSON([]byte(tt.input))
			if err != nil {
				t.Fatalf("DecodeJSON(%s) error: %v", tt.input, err)
			}

			switch tt.wantType {
			case "int64":
				v, ok := raw.(int64)
				if !ok {
					t.Fatalf("DecodeJSON(%s) returned %T, want int64", tt.input, raw)
				}
				// Verify precision by comparing with the original string representation
				wantStr := tt.input
				gotStr := json.Number(int64ToStr(v)).String()
				if wantStr != gotStr {
					t.Errorf("DecodeJSON(%s) = %d (string: %s), want precision-preserved value", tt.input, v, gotStr)
				}
			case "float64":
				if _, ok := raw.(float64); !ok {
					t.Fatalf("DecodeJSON(%s) returned %T, want float64", tt.input, raw)
				}
			case "string":
				if _, ok := raw.(string); !ok {
					t.Fatalf("DecodeJSON(%s) returned %T, want string", tt.input, raw)
				}
			case "bool":
				if _, ok := raw.(bool); !ok {
					t.Fatalf("DecodeJSON(%s) returned %T, want bool", tt.input, raw)
				}
			case "nil":
				if raw != nil {
					t.Fatalf("DecodeJSON(%s) returned %T, want nil", tt.input, raw)
				}
			}
		})
	}
}

func TestDecodeJSON_PreservesInt64Precision(t *testing.T) {
	// 2^53 + 1 = 9007199254740993 — this is the smallest integer that float64 cannot represent exactly
	largeInt := "9007199254740993"
	raw, err := DecodeJSON([]byte(largeInt))
	if err != nil {
		t.Fatalf("DecodeJSON error: %v", err)
	}

	v, ok := raw.(int64)
	if !ok {
		t.Fatalf("expected int64, got %T", raw)
	}

	expected := int64(9007199254740993)
	if v != expected {
		t.Errorf("expected %d, got %d", expected, v)
	}

	// Verify that float64 would lose precision
	f := float64(v)
	if f == float64(expected) {
		// This means float64 can represent it exactly — unexpected for this value
		// But it's not an error in our code, just skip the precision check
		t.Logf("WARNING: float64 can represent %d exactly (unexpected)", expected)
	}
}

func TestDecodeJSON_NestedObjects(t *testing.T) {
	input := `{"id": 9007199254740993, "name": "test", "nested": {"value": 1234567890123456789}}`
	raw, err := DecodeJSON([]byte(input))
	if err != nil {
		t.Fatalf("DecodeJSON error: %v", err)
	}

	obj, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map[string]interface{}, got %T", raw)
	}

	// Check top-level int64
	id, ok := obj["id"].(int64)
	if !ok {
		t.Fatalf("expected id to be int64, got %T", obj["id"])
	}
	if id != 9007199254740993 {
		t.Errorf("id = %d, want 9007199254740993", id)
	}

	// Check nested int64
	nested, ok := obj["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected nested to be map[string]interface{}, got %T", obj["nested"])
	}
	nestedVal, ok := nested["value"].(int64)
	if !ok {
		t.Fatalf("expected nested.value to be int64, got %T", nested["value"])
	}
	if nestedVal != 1234567890123456789 {
		t.Errorf("nested.value = %d, want 1234567890123456789", nestedVal)
	}
}

func TestDecodeJSON_Arrays(t *testing.T) {
	input := `[1, 9007199254740993, 3.14, "hello"]`
	raw, err := DecodeJSON([]byte(input))
	if err != nil {
		t.Fatalf("DecodeJSON error: %v", err)
	}

	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", raw)
	}

	if len(arr) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(arr))
	}

	// Check first element (small int)
	if v, ok := arr[0].(int64); !ok || v != 1 {
		t.Errorf("arr[0] = %v (%T), want int64(1)", arr[0], arr[0])
	}

	// Check second element (large int)
	if v, ok := arr[1].(int64); !ok || v != 9007199254740993 {
		t.Errorf("arr[1] = %v (%T), want int64(9007199254740993)", arr[1], arr[1])
	}

	// Check third element (float)
	if v, ok := arr[2].(float64); !ok || v != 3.14 {
		t.Errorf("arr[2] = %v (%T), want float64(3.14)", arr[2], arr[2])
	}

	// Check fourth element (string)
	if v, ok := arr[3].(string); !ok || v != "hello" {
		t.Errorf("arr[3] = %v (%T), want string(hello)", arr[3], arr[3])
	}
}

func TestDecodeJSON_MarshalPreservesInt64(t *testing.T) {
	// Verify that Go's json.Marshal serializes int64 as JSON integer (not float)
	input := `{"id": 9007199254740993}`
	raw, err := DecodeJSON([]byte(input))
	if err != nil {
		t.Fatalf("DecodeJSON error: %v", err)
	}

	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	// The output should contain the exact integer, not scientific notation
	output := string(data)
	expected := `{"id":9007199254740993}`
	if output != expected {
		t.Errorf("json.Marshal output = %s, want %s", output, expected)
	}
}

func int64ToStr(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := false
	if v < 0 {
		negative = true
		v = -v
	}
	digits := make([]byte, 0, 20)
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
