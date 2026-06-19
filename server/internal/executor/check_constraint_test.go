package executor

import (
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func TestCheckConstraintBooleanLogic(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		row     storage.Row
		schema  *storage.TableSchema
		want    bool
		wantErr bool
	}{
		{
			name: "simple comparison true",
			expr: "age > 0",
			row:  storage.Row{int64(5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "simple comparison false",
			expr: "age > 0",
			row:  storage.Row{int64(-1)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: false,
		},
		{
			name: "AND logic true",
			expr: "age > 0 AND age < 100",
			row:  storage.Row{int64(50)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "AND logic false left",
			expr: "age > 0 AND age < 100",
			row:  storage.Row{int64(-5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: false,
		},
		{
			name: "AND logic false right",
			expr: "age > 0 AND age < 100",
			row:  storage.Row{int64(150)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: false,
		},
		{
			name: "OR logic true left",
			expr: "age < 0 OR age > 100",
			row:  storage.Row{int64(-5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "OR logic true right",
			expr: "age < 0 OR age > 100",
			row:  storage.Row{int64(150)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "OR logic false",
			expr: "age < 0 OR age > 100",
			row:  storage.Row{int64(50)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: false,
		},
		{
			name: "NOT logic true",
			expr: "NOT (age < 0)",
			row:  storage.Row{int64(5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "NOT logic false",
			expr: "NOT (age > 0)",
			row:  storage.Row{int64(5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: false,
		},
		{
			name: "nested AND OR",
			expr: "(age > 0 AND age < 18) OR age >= 65",
			row:  storage.Row{int64(5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "nested AND OR middle excluded",
			expr: "(age > 0 AND age < 18) OR age >= 65",
			row:  storage.Row{int64(30)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: false,
		},
		{
			name: "nested AND OR senior",
			expr: "(age > 0 AND age < 18) OR age >= 65",
			row:  storage.Row{int64(70)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "equality check",
			expr: "status = 1",
			row:  storage.Row{int64(1)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "status", Type: "INT"}},
			},
			want: true,
		},
		{
			name: "string comparison",
			expr: "name = 'alice'",
			row:  storage.Row{"alice"},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "name", Type: "TEXT"}},
			},
			want: true,
		},
		{
			name: "empty expression",
			expr: "",
			row:  storage.Row{int64(5)},
			schema: &storage.TableSchema{
				Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}},
			},
			want: true,
		},
		{
			name:    "invalid expression",
			expr:    ">>>",
			row:     storage.Row{int64(5)},
			schema:  &storage.TableSchema{Columns: []storage.ColumnSchema{{Name: "age", Type: "INT"}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateCheckExpr(tt.expr, tt.row, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("evaluateCheckExpr() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("evaluateCheckExpr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckConstraintEnforcement(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE ages (id INT, age INT);")
	executeSQL(t, session, "ALTER TABLE ages ADD CONSTRAINT chk_age CHECK (age > 0);")

	executeSQL(t, session, "INSERT INTO ages VALUES (1, 25);")

	executeSQLExpectError(t, session, "INSERT INTO ages VALUES (2, -1);")
}

func TestCheckConstraintBooleanEnforcement(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE users (id INT, age INT, role TEXT);")
	executeSQL(t, session, "ALTER TABLE users ADD CONSTRAINT chk_age_range CHECK (age >= 0 AND age <= 150);")

	executeSQL(t, session, "INSERT INTO users VALUES (1, 25, 'admin');")
	executeSQL(t, session, "INSERT INTO users VALUES (2, 0, 'user');")
	executeSQL(t, session, "INSERT INTO users VALUES (3, 150, 'user');")

	executeSQLExpectError(t, session, "INSERT INTO users VALUES (4, -5, 'user');")
	executeSQLExpectError(t, session, "INSERT INTO users VALUES (5, 200, 'user');")
}

func TestCheckConstraintOrLogic(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE temps (id INT, temp INT);")
	executeSQL(t, session, "ALTER TABLE temps ADD CONSTRAINT chk_temp CHECK (temp < -20 OR temp > 50);")

	executeSQL(t, session, "INSERT INTO temps VALUES (1, -25);")
	executeSQL(t, session, "INSERT INTO temps VALUES (2, 60);")

	executeSQLExpectError(t, session, "INSERT INTO temps VALUES (3, 0);")
}

func TestCheckConstraintNotLogic(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE flags (id INT, val INT);")
	executeSQL(t, session, "ALTER TABLE flags ADD CONSTRAINT chk_not_zero CHECK (NOT (val = 0));")

	executeSQL(t, session, "INSERT INTO flags VALUES (1, 5);")
	executeSQL(t, session, "INSERT INTO flags VALUES (2, -1);")

	executeSQLExpectError(t, session, "INSERT INTO flags VALUES (3, 0);")
}

func TestCheckConstraintNestedLogic(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE people (id INT, age INT);")
	executeSQL(t, session, "ALTER TABLE people ADD CONSTRAINT chk_age_group CHECK ((age >= 0 AND age < 18) OR (age >= 18 AND age < 65) OR age >= 65);")

	executeSQL(t, session, "INSERT INTO people VALUES (1, 10);")
	executeSQL(t, session, "INSERT INTO people VALUES (2, 30);")
	executeSQL(t, session, "INSERT INTO people VALUES (3, 70);")

	executeSQLExpectError(t, session, "INSERT INTO people VALUES (4, -1);")
}

func TestCheckConstraintOnUpdate(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE products (id INT, price INT);")
	executeSQL(t, session, "ALTER TABLE products ADD CONSTRAINT chk_price CHECK (price > 0);")

	executeSQL(t, session, "INSERT INTO products VALUES (1, 100);")
	executeSQL(t, session, "INSERT INTO products VALUES (2, 50);")

	executeSQLExpectError(t, session, "UPDATE products SET price = -10 WHERE id = 1;")
	executeSQL(t, session, "UPDATE products SET price = 200 WHERE id = 1;")
}

func TestParseExpression(t *testing.T) {
	tests := []struct {
		input   string
		wantNil bool
		wantErr bool
	}{
		{"age > 0", false, false},
		{"age > 0 AND age < 100", false, false},
		{"NOT (age < 0)", false, false},
		{"(a > 0 AND b < 10) OR c >= 5", false, false},
		{"", true, true},
		{">>>", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			expr, err := parser.ParseExpression(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseExpression() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.wantNil && expr != nil {
				t.Errorf("expected nil expression, got %v", expr)
			}
			if err == nil && !tt.wantNil && expr == nil {
				t.Error("expected non-nil expression, got nil")
			}
		})
	}
}

func TestFormatExpressionRoundTrip(t *testing.T) {
	tests := []string{
		"age > 0",
		"age > 0 AND age < 100",
		"NOT (age < 0)",
		"(a > 0 AND b < 10) OR c >= 5",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			expr, err := parser.ParseExpression(input)
			if err != nil {
				t.Fatalf("ParseExpression() error = %v", err)
			}
			formatted := parser.FormatExpression(expr)
			expr2, err := parser.ParseExpression(formatted)
			if err != nil {
				t.Fatalf("second ParseExpression() error = %v (formatted: %s)", err, formatted)
			}
			result := parser.FormatExpression(expr2)
			if formatted != result {
				t.Errorf("round-trip mismatch: %q -> %q -> %q", input, formatted, result)
			}
		})
	}
}
