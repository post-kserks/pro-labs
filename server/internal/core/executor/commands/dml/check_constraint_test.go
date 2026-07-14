package dml_test

import (
	"testing"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/parser"
)

func TestCheckConstraintEnforcement(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE ages (id INT, age INT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE ages ADD CONSTRAINT chk_age CHECK (age > 0);")

	executor.ExecuteSQL(t, session, "INSERT INTO ages VALUES (1, 25);")

	executor.ExecuteSQLExpectError(t, session, "INSERT INTO ages VALUES (2, -1);")
}

func TestCheckConstraintBooleanEnforcement(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE users (id INT, age INT, role TEXT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE users ADD CONSTRAINT chk_age_range CHECK (age >= 0 AND age <= 150);")

	executor.ExecuteSQL(t, session, "INSERT INTO users VALUES (1, 25, 'admin');")
	executor.ExecuteSQL(t, session, "INSERT INTO users VALUES (2, 0, 'user');")
	executor.ExecuteSQL(t, session, "INSERT INTO users VALUES (3, 150, 'user');")

	executor.ExecuteSQLExpectError(t, session, "INSERT INTO users VALUES (4, -5, 'user');")
	executor.ExecuteSQLExpectError(t, session, "INSERT INTO users VALUES (5, 200, 'user');")
}

func TestCheckConstraintOrLogic(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE temps (id INT, temp INT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE temps ADD CONSTRAINT chk_temp CHECK (temp < -20 OR temp > 50);")

	executor.ExecuteSQL(t, session, "INSERT INTO temps VALUES (1, -25);")
	executor.ExecuteSQL(t, session, "INSERT INTO temps VALUES (2, 60);")

	executor.ExecuteSQLExpectError(t, session, "INSERT INTO temps VALUES (3, 0);")
}

func TestCheckConstraintNotLogic(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE flags (id INT, val INT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE flags ADD CONSTRAINT chk_not_zero CHECK (NOT (val = 0));")

	executor.ExecuteSQL(t, session, "INSERT INTO flags VALUES (1, 5);")
	executor.ExecuteSQL(t, session, "INSERT INTO flags VALUES (2, -1);")

	executor.ExecuteSQLExpectError(t, session, "INSERT INTO flags VALUES (3, 0);")
}

func TestCheckConstraintNestedLogic(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE people (id INT, age INT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE people ADD CONSTRAINT chk_age_group CHECK ((age >= 0 AND age < 18) OR (age >= 18 AND age < 65) OR age >= 65);")

	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (1, 10);")
	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (2, 30);")
	executor.ExecuteSQL(t, session, "INSERT INTO people VALUES (3, 70);")

	executor.ExecuteSQLExpectError(t, session, "INSERT INTO people VALUES (4, -1);")
}

func TestCheckConstraintOnUpdate(t *testing.T) {
	session := executor.SetupSession(t)
	executor.ExecuteSQL(t, session, "CREATE TABLE products (id INT, price INT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE products ADD CONSTRAINT chk_price CHECK (price > 0);")

	executor.ExecuteSQL(t, session, "INSERT INTO products VALUES (1, 100);")
	executor.ExecuteSQL(t, session, "INSERT INTO products VALUES (2, 50);")

	executor.ExecuteSQLExpectError(t, session, "UPDATE products SET price = -10 WHERE id = 1;")
	executor.ExecuteSQL(t, session, "UPDATE products SET price = 200 WHERE id = 1;")
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

func TestInsertSelectCheckConstraint(t *testing.T) {
	session := executor.SetupSession(t)

	// Create source table with data
	executor.ExecuteSQL(t, session, "CREATE TABLE src_products (id INT, name VARCHAR(100), price FLOAT);")
	executor.ExecuteSQL(t, session, "INSERT INTO src_products VALUES (1, 'Widget', 9.99);")
	executor.ExecuteSQL(t, session, "INSERT INTO src_products VALUES (2, 'Gadget', 19.99);")
	executor.ExecuteSQL(t, session, "INSERT INTO src_products VALUES (3, 'Thing', -5.00);")

	// Create destination table with CHECK constraint on price
	executor.ExecuteSQL(t, session, "CREATE TABLE dst_products (id INT, name VARCHAR(100), price FLOAT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE dst_products ADD CONSTRAINT chk_price CHECK (price > 0);")

	// INSERT ... SELECT should enforce CHECK constraint
	executor.ExecuteSQLExpectError(t, session, "INSERT INTO dst_products SELECT * FROM src_products;")

	// Verify no rows were inserted (all-or-nothing semantics)
	result := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM dst_products;")
	if result.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows, got %s", result.Rows[0][0])
	}
}

func TestInsertSelectCheckConstraintPass(t *testing.T) {
	session := executor.SetupSession(t)

	// Create source table with only valid prices
	executor.ExecuteSQL(t, session, "CREATE TABLE src_prices (id INT, val FLOAT);")
	executor.ExecuteSQL(t, session, "INSERT INTO src_prices VALUES (1, 5.00);")
	executor.ExecuteSQL(t, session, "INSERT INTO src_prices VALUES (2, 10.00);")

	// Create destination table with CHECK constraint
	executor.ExecuteSQL(t, session, "CREATE TABLE dst_prices (id INT, val FLOAT);")
	executor.ExecuteSQL(t, session, "ALTER TABLE dst_prices ADD CONSTRAINT chk_val CHECK (val > 0);")

	// All rows pass CHECK
	result := executor.ExecuteSQL(t, session, "INSERT INTO dst_prices SELECT * FROM src_prices;")
	if result.Affected != 2 {
		t.Fatalf("expected 2 affected rows, got %d", result.Affected)
	}

	// Verify data
	sel := executor.ExecuteSQL(t, session, "SELECT COUNT(*) FROM dst_prices;")
	if sel.Rows[0][0] != "2" {
		t.Fatalf("expected 2 rows in dst_prices, got %s", sel.Rows[0][0])
	}
}
