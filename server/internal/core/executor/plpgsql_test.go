package executor

import (
	"testing"
)

func TestPLPGSQLSumFunction(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION my_sum(a INT, b INT) RETURNS INT AS $$ BEGIN RETURN a + b; END; $$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT my_sum(2, 3);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "5" {
		t.Fatalf("expected 5, got %v", result.Rows)
	}
}

func TestPLPGSQLSumFunctionSimpleSyntax(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE FUNCTION my_sum2(x INT, y INT) RETURNS INT AS 'BEGIN RETURN x + y; END;' LANGUAGE plpgsql;")

	result := executeSQL(t, session, "SELECT my_sum2(10, 20);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "30" {
		t.Fatalf("expected 30, got %v", result.Rows)
	}
}

func TestPLPGSQLGreetFunction(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION greet(name TEXT) RETURNS TEXT AS $$ BEGIN RETURN 'Hello, ' || name; END; $$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT greet('world');")
	if len(result.Rows) != 1 || result.Rows[0][0] != "Hello, world" {
		t.Fatalf("expected 'Hello, world', got %v", result.Rows)
	}
}

func TestPLPGSQLVariableAssignment(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION double_value(x INT) RETURNS INT AS $$
	DECLARE
		result INT;
	BEGIN
		result := x * 2;
		RETURN result;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT double_value(7);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "14" {
		t.Fatalf("expected 14, got %v", result.Rows)
	}
}

func TestPLPGSQLReturnQuery(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE scores (id INT, name TEXT, score INT);")
	executeSQL(t, session, "INSERT INTO scores VALUES (1, 'Alice', 95);")
	executeSQL(t, session, "INSERT INTO scores VALUES (2, 'Bob', 87);")

	executeSQL(t, session, `CREATE FUNCTION top_scorers(min_score INT) RETURNS SETOF TEXT AS $$
	BEGIN
		RETURN QUERY SELECT name FROM scores WHERE score >= min_score ORDER BY name;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT top_scorers(90);")
	// RETURN QUERY returns a Result object; the SELECT wraps it in rows
	if len(result.Rows) != 1 || result.Rows[0][0] != "Alice" {
		t.Fatalf("expected Alice, got %v", result.Rows)
	}
}

func TestPLPGSQLNoArgs(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION get_constant() RETURNS INT AS $$
	BEGIN
		RETURN 42;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT get_constant();")
	if len(result.Rows) != 1 || result.Rows[0][0] != "42" {
		t.Fatalf("expected 42, got %v", result.Rows)
	}
}

func TestPLPGSQLExpression(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION calc(x INT) RETURNS INT AS $$
	BEGIN
		RETURN x * 3 + 1;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT calc(5);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "16" {
		t.Fatalf("expected 16, got %v", result.Rows)
	}
}

func TestPLPGSQLDeclareWithInit(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION keep_val(x INT) RETURNS INT AS $$
	DECLARE
		val INT;
	BEGIN
		val := x;
		RETURN val;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT keep_val(99);")
	if len(result.Rows) != 1 || result.Rows[0][0] != "99" {
		t.Fatalf("expected 99, got %v", result.Rows)
	}
}

func TestPLPGSQLCallFromINSERT(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE results (val INT);")
	executeSQL(t, session, `CREATE FUNCTION compute(n INT) RETURNS INT AS $$
	BEGIN
		RETURN n + 10;
	END;
	$$ LANGUAGE plpgsql;`)

	executeSQL(t, session, "INSERT INTO results VALUES (compute(5));")
	result := executeSQL(t, session, "SELECT * FROM results;")
	if len(result.Rows) != 1 || result.Rows[0][0] != "15" {
		t.Fatalf("expected 15, got %v", result.Rows)
	}
}

func TestPLPGSQLWithDeclareAndMultipleStatements(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION complex_calc(a INT, b INT) RETURNS INT AS $$
	DECLARE
		sum_val INT;
		product INT;
	BEGIN
		sum_val := a + b;
		product := a * b;
		RETURN sum_val + product;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT complex_calc(2, 3);")
	// sum_val = 5, product = 6, return 5 + 6 = 11
	if len(result.Rows) != 1 || result.Rows[0][0] != "11" {
		t.Fatalf("expected 11, got %v", result.Rows)
	}
}

func TestPLPGSQLInterpreterDirectly(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION direct_test(x INT, y INT) RETURNS INT AS $$ BEGIN RETURN x + y; END; $$ LANGUAGE plpgsql;`)

	result, err := executeUserDefinedFunction("mydb", "direct_test", []interface{}{int64(10), int64(20)}, &ExecutionContext{
		Storage: session.executor.storage,
		Session: session,
	})
	if err != nil {
		t.Fatalf("executeUserDefinedFunction failed: %v", err)
	}
	if result != int64(30) {
		t.Fatalf("expected 30, got %v", result)
	}
}

func TestPLPGSQLStripBeginEnd(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"BEGIN RETURN 1; END;", "RETURN 1;"},
		{"BEGIN RETURN 1; END", "RETURN 1;"},
		{"RETURN 1;", "RETURN 1;"},
		{"  BEGIN  RETURN 1;  END;  ", "RETURN 1;"},
	}
	for _, tt := range tests {
		result := stripBeginEnd(tt.input)
		if result != tt.expected {
			t.Errorf("stripBeginEnd(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestPLPGSQLSplitStatements(t *testing.T) {
	stmts := splitPLPGSQLStatements("x := 1; RETURN x + 2;")
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
	if stmts[0] != "x := 1" {
		t.Errorf("first statement: got %q, want %q", stmts[0], "x := 1")
	}
	if stmts[1] != "RETURN x + 2" {
		t.Errorf("second statement: got %q, want %q", stmts[1], "RETURN x + 2")
	}
}

func TestPLPGSQLSplitStatementsWithBlock(t *testing.T) {
	stmts := splitPLPGSQLStatements("BEGIN x := 1; RETURN x; END;")
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement (the whole block), got %d: %v", len(stmts), stmts)
	}
	if stmts[0] != "BEGIN x := 1; RETURN x; END;" {
		t.Errorf("got %q", stmts[0])
	}
}

func TestPLPGSQLParseDeclarations(t *testing.T) {
	decl := "x INT;\ny TEXT;\nz BOOL;"
	vars := parseDeclarations(decl)
	if len(vars) != 3 {
		t.Fatalf("expected 3 vars, got %d: %v", len(vars), vars)
	}
	if vars[0] != "x" || vars[1] != "y" || vars[2] != "z" {
		t.Errorf("unexpected vars: %v", vars)
	}
}

func TestPLPGSQLBuildVarSchemaRow(t *testing.T) {
	vars := map[string]interface{}{
		"a": int64(1),
		"b": "hello",
		"c": float64(3.14),
	}
	schema, row := buildVarSchemaRow(vars)
	if len(schema.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(schema.Columns))
	}
	if schema.Columns[0].Name != "a" || schema.Columns[1].Name != "b" || schema.Columns[2].Name != "c" {
		t.Errorf("columns not sorted: %v", schema.Columns)
	}
	if row[0] != int64(1) || row[1] != "hello" || row[2] != float64(3.14) {
		t.Errorf("row values mismatch: %v", row)
	}
}

func TestPLPGSQLDollarQuotedBody(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, `CREATE FUNCTION concat_words(a TEXT, b TEXT) RETURNS TEXT AS $$
	BEGIN
		RETURN a || b;
	END;
	$$ LANGUAGE plpgsql;`)

	result := executeSQL(t, session, "SELECT concat_words('hello', 'world');")
	if len(result.Rows) != 1 || result.Rows[0][0] != "helloworld" {
		t.Fatalf("expected 'helloworld', got %v", result.Rows)
	}
}
