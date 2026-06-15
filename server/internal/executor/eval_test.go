package executor

import (
	"strings"
	"testing"

	"vaultdb/internal/storage"
)

func mockSetupSession(t *testing.T) *Session {
	t.Helper()
	store := NewMockStorage()
	store.databases["testdb"] = true
	store.ensureDB("testdb")
	store.tables["testdb"]["t"] = &storage.TableSchema{
		Name: "t",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "val", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
			{Name: "alive", Type: "BOOL"},
		},
	}
	store.rows["testdb"]["t"] = []storage.Row{
		{int64(1), "alice", 9.5, true},
		{int64(2), "bob", 7.0, true},
		{int64(3), "carol", 8.0, false},
	}
	session := newTestSession(store)
	executeSQL(t, session, "USE testdb;")
	return session
}

func TestFnAbs(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT ABS(-5) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "5" {
		t.Fatalf("expected 5, got %s", res.Rows[0][0])
	}
}

func TestFnUpper(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT UPPER(val) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "ALICE" {
		t.Fatalf("expected ALICE, got %s", res.Rows[0][0])
	}
}

func TestFnLength(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LENGTH(val) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "5" {
		t.Fatalf("expected 5, got %s", res.Rows[0][0])
	}
}

func TestFnCoalesce(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT COALESCE(NULL, NULL, 'default') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "default" {
		t.Fatalf("expected default, got %s", res.Rows[0][0])
	}
}

func TestFnNullif(t *testing.T) {
	session := mockSetupSession(t)

	t.Run("equal_returns_null", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT NULLIF(id, 1) FROM t WHERE id = 1;")
		if res.Rows[0][0] != "" {
			t.Fatalf("expected NULL (empty string), got %s", res.Rows[0][0])
		}
	})

	t.Run("not_equal_returns_first", func(t *testing.T) {
		res := executeSQL(t, session, "SELECT NULLIF(id, 2) FROM t WHERE id = 1;")
		if res.Rows[0][0] != "1" {
			t.Fatalf("expected 1, got %s", res.Rows[0][0])
		}
	})
}

func TestFnSubstring(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT SUBSTRING(val, 2, 3) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "lic" {
		t.Fatalf("expected 'lic', got %s", res.Rows[0][0])
	}
}

func TestFnReplace(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT REPLACE('hello', 'l', 'r') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "herro" {
		t.Fatalf("expected 'herro', got %s", res.Rows[0][0])
	}
}

func TestFnNow(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT NOW() FROM t WHERE id = 1;")
	if res.Rows[0][0] == "" {
		t.Fatal("expected non-nil timestamp, got empty string")
	}
}

func TestFnTrim(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT TRIM('  hello  ') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "hello" {
		t.Fatalf("expected 'hello', got %s", res.Rows[0][0])
	}
}

func TestFnLpad(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LPAD('hi', 5, '*') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "***hi" {
		t.Fatalf("expected '***hi', got %s", res.Rows[0][0])
	}
}

func TestFnRpad(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT RPAD('hi', 5, '*') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "hi***" {
		t.Fatalf("expected 'hi***', got %s", res.Rows[0][0])
	}
}

func TestFnLpadEmpty(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LPAD('hi', 5, '') FROM t WHERE id = 1;")
	if len(res.Rows) == 0 || res.Rows[0][0] != "ERR" {
		t.Fatalf("expected 'ERR' for empty pad string, got %v", res.Rows)
	}
}

func TestFnPosition(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT POSITION('l', 'hello') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "3" {
		t.Fatalf("expected 3, got %s", res.Rows[0][0])
	}
}

func TestFnInitcap(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT INITCAP('hello world') FROM t WHERE id = 1;")
	if res.Rows[0][0] != "Hello World" {
		t.Fatalf("expected 'Hello World', got %s", res.Rows[0][0])
	}
}

func TestFnGreatest(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT GREATEST(1, 2, 3) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "3" {
		t.Fatalf("expected 3, got %s", res.Rows[0][0])
	}
}

func TestFnLeast(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT LEAST(1, 2, 3) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "1" {
		t.Fatalf("expected 1, got %s", res.Rows[0][0])
	}
}

func TestEvalArithmeticDivByZero(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT 1 / 0 FROM t WHERE id = 1;")
	if len(res.Rows) == 0 || res.Rows[0][0] != "ERR" {
		t.Fatalf("expected 'ERR' for division by zero, got %v", res.Rows)
	}
}

func TestEvalComparisonSubquery(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT val FROM t WHERE id = (SELECT MAX(id) FROM t);")
	if len(res.Rows) != 1 || res.Rows[0][0] != "carol" {
		t.Fatalf("expected [carol], got %v", res.Rows)
	}
}

func TestEvalCastInt(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT CAST('123' AS INT) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "123" {
		t.Fatalf("expected 123, got %s", res.Rows[0][0])
	}
}

func TestEvalCastFloat(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT CAST('3.14' AS FLOAT) FROM t WHERE id = 1;")
	if res.Rows[0][0] != "3.14" {
		t.Fatalf("expected 3.14, got %s", res.Rows[0][0])
	}
}

func TestEvalCaseWhen(t *testing.T) {
	session := mockSetupSession(t)
	res := executeSQL(t, session, "SELECT CASE WHEN 1 > 0 THEN 'yes' ELSE 'no' END FROM t WHERE id = 1;")
	if res.Rows[0][0] != "yes" {
		t.Fatalf("expected 'yes', got %s", res.Rows[0][0])
	}
}

func TestSanitizeObjectName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", true},
		{"dotdot", "..", true},
		{"slash", "/", true},
		{"valid", "my_table", false},
		{"valid_upper", "TABLE1", false},
		{"valid_mixed", "My_Table_123", false},
		{"too_long", strings.Repeat("a", 129), true},
		{"invalid_chars", "table name", true},
		{"invalid_special", "table@name", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := sanitizeObjectName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sanitizeObjectName(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestResolveProjection(t *testing.T) {
	schema := &storage.TableSchema{
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
			{Name: "score", Type: "FLOAT"},
		},
	}

	t.Run("empty_returns_all", func(t *testing.T) {
		indices, columns, err := resolveProjection(schema, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 3 || len(columns) != 3 {
			t.Fatalf("expected 3 columns, got %d", len(indices))
		}
		if columns[0] != "id" || columns[1] != "name" || columns[2] != "score" {
			t.Fatalf("unexpected columns: %v", columns)
		}
	})

	t.Run("known_columns", func(t *testing.T) {
		indices, columns, err := resolveProjection(schema, []string{"name", "score"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(indices) != 2 {
			t.Fatalf("expected 2 columns, got %d", len(indices))
		}
		if columns[0] != "name" || columns[1] != "score" {
			t.Fatalf("unexpected columns: %v", columns)
		}
	})

	t.Run("unknown_column", func(t *testing.T) {
		_, _, err := resolveProjection(schema, []string{"nonexistent"})
		if err == nil {
			t.Fatal("expected error for unknown column")
		}
		if !strings.Contains(err.Error(), "unknown column") {
			t.Fatalf("expected 'unknown column' in error, got: %v", err)
		}
	})
}

func TestInferType(t *testing.T) {
	tests := []struct {
		name  string
		val   interface{}
		want  string
	}{
		{"int64", int64(42), "INT"},
		{"int", int(42), "INT"},
		{"float64", float64(3.14), "FLOAT"},
		{"bool_true", true, "BOOL"},
		{"bool_false", false, "BOOL"},
		{"string", "hello", "TEXT"},
		{"nil", nil, "TEXT"},
		{"map", map[string]interface{}{"k": "v"}, "FLEXIBLE"},
		{"json_string", `{"key":"val"}`, "FLEXIBLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferType(tt.val)
			if got != tt.want {
				t.Fatalf("inferType(%v) = %s, want %s", tt.val, got, tt.want)
			}
		})
	}
}

func TestValueToString(t *testing.T) {
	tests := []struct {
		name string
		val  interface{}
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"int", int(42), "42"},
		{"int64", int64(42), "42"},
		{"float64", float64(3.14), "3.14"},
		{"bool_true", true, "true"},
		{"bool_false", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueToString(tt.val)
			if got != tt.want {
				t.Fatalf("valueToString(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}


