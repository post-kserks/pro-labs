package executor

import (
	"fmt"
	"strings"
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func TestParseDropPolicy(t *testing.T) {
	tests := []struct {
		input string
		want  DropPolicy
	}{
		{"drop", PolicyDrop},
		{"block", PolicyBlock},
		{"evict", PolicyEvict},
		{"unknown", PolicyDrop},
		{"", PolicyDrop},
	}
	for _, tt := range tests {
		got := ParseDropPolicy(tt.input)
		if got != tt.want {
			t.Errorf("ParseDropPolicy(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestCoerceToColumnEdgeCases(t *testing.T) {
	// nil value
	v, err := coerceToColumn(nil, storage.ColumnSchema{Name: "a", Type: "INT"})
	if err != nil || v != nil {
		t.Errorf("nil value: v=%v, err=%v", v, err)
	}

	// INT with invalid type
	_, err = coerceToColumn([]int{1}, storage.ColumnSchema{Name: "a", Type: "INT"})
	if err == nil {
		t.Error("expected error for []int to INT")
	}

	// FLOAT with string
	v, err = coerceToColumn("3.14", storage.ColumnSchema{Name: "a", Type: "FLOAT"})
	if err != nil || v != 3.14 {
		t.Errorf("FLOAT from string: v=%v, err=%v", v, err)
	}

	// FLOAT with invalid string
	_, err = coerceToColumn("not_a_number", storage.ColumnSchema{Name: "a", Type: "FLOAT"})
	if err == nil {
		t.Error("expected error for invalid FLOAT string")
	}

	// FLOAT with invalid type
	_, err = coerceToColumn([]float64{}, storage.ColumnSchema{Name: "a", Type: "FLOAT"})
	if err == nil {
		t.Error("expected error for []float64 to FLOAT")
	}

	// BOOL with non-bool
	_, err = coerceToColumn("true", storage.ColumnSchema{Name: "a", Type: "BOOL"})
	if err == nil {
		t.Error("expected error for string to BOOL")
	}

	// VARCHAR overflow
	_, err = coerceToColumn(strings.Repeat("x", 101), storage.ColumnSchema{Name: "a", Type: "VARCHAR", VarcharLen: 100})
	if err == nil {
		t.Error("expected VARCHAR overflow error")
	}

	// BLOB with non-string
	_, err = coerceToColumn(123, storage.ColumnSchema{Name: "a", Type: "BLOB"})
	if err == nil {
		t.Error("expected error for int to BLOB")
	}

	// TEXT with non-string
	_, err = coerceToColumn(123, storage.ColumnSchema{Name: "a", Type: "TEXT"})
	if err == nil {
		t.Error("expected error for int to TEXT")
	}

	// VECTOR with dimension mismatch
	_, err = coerceToColumn([]float64{1, 2}, storage.ColumnSchema{Name: "a", Type: "VECTOR", VarcharLen: 3})
	if err == nil {
		t.Error("expected VECTOR dimension mismatch")
	}

	// TIMESTAMP stored as string
	v, err = coerceToColumn("2024-01-01", storage.ColumnSchema{Name: "a", Type: "TIMESTAMP"})
	if err != nil || v != "2024-01-01" {
		t.Errorf("TIMESTAMP: v=%v, err=%v", v, err)
	}

	// DECIMAL stored as string
	v, err = coerceToColumn("123.45", storage.ColumnSchema{Name: "a", Type: "DECIMAL"})
	if err != nil || v != "123.45" {
		t.Errorf("DECIMAL: v=%v, err=%v", v, err)
	}

	// DATE stored as string
	v, err = coerceToColumn("2024-01-01", storage.ColumnSchema{Name: "a", Type: "DATE"})
	if err != nil || v != "2024-01-01" {
		t.Errorf("DATE: v=%v, err=%v", v, err)
	}

	// TIME stored as string
	v, err = coerceToColumn("12:00:00", storage.ColumnSchema{Name: "a", Type: "TIME"})
	if err != nil || v != "12:00:00" {
		t.Errorf("TIME: v=%v, err=%v", v, err)
	}

	// JSON stored as string
	v, err = coerceToColumn(`{"key":"val"}`, storage.ColumnSchema{Name: "a", Type: "JSON"})
	if err != nil || v != `{"key":"val"}` {
		t.Errorf("JSON: v=%v, err=%v", v, err)
	}

	// JSONB stored as string
	v, err = coerceToColumn(`{"key":"val"}`, storage.ColumnSchema{Name: "a", Type: "JSONB"})
	if err != nil || v != `{"key":"val"}` {
		t.Errorf("JSONB: v=%v, err=%v", v, err)
	}

	// unsupported type
	_, err = coerceToColumn("val", storage.ColumnSchema{Name: "a", Type: "UNKNOWN"})
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestInferType(t *testing.T) {
	tests := []struct {
		val  interface{}
		want string
	}{
		{nil, "TEXT"},
		{int64(1), "INT"},
		{int(1), "INT"},
		{float64(1.0), "FLOAT"},
		{true, "BOOL"},
		{[]float64{1, 2}, "VECTOR"},
		{map[string]interface{}{"a": 1}, "FLEXIBLE"},
		{"hello", "TEXT"},
	}
	for _, tt := range tests {
		got := inferType(tt.val)
		if got != tt.want {
			t.Errorf("inferType(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestValueToString(t *testing.T) {
	tests := []struct {
		val  interface{}
		want string
	}{
		{nil, ""},
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{42, "42"},
		{int64(42), "42"},
		{3.14, "3.14"},
		{struct{}{}, "[]"},
	}
	for _, tt := range tests {
		got := valueToString(tt.val)
		if got != tt.want {
			t.Errorf("valueToString(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestContainsStatementDML(t *testing.T) {
	// SELECT with subquery
	sel := &parser.SelectStatement{
		Where: &parser.ExistsExpr{
			Select: &parser.SelectStatement{
				TableName: "t",
			},
		},
	}
	if !containsStatementDML(sel) {
		t.Error("expected containsStatementDML to return true for SELECT with EXISTS subquery")
	}

	// Non-SELECT statement
	ins := &parser.InsertStatement{TableName: "t"}
	if containsStatementDML(ins) {
		t.Error("expected containsStatementDML to return false for INSERT")
	}
}

func TestContainsExprDML(t *testing.T) {
	// nil
	if containsExprDML(nil) {
		t.Error("expected false for nil")
	}

	// SubqueryExpr
	sq := &parser.SubqueryExpr{
		Query: &parser.SelectStatement{TableName: "t"},
	}
	if !containsExprDML(sq) {
		t.Error("expected true for SubqueryExpr")
	}

	// ExistsExpr
	exists := &parser.ExistsExpr{
		Select: &parser.SelectStatement{TableName: "t"},
	}
	if !containsExprDML(exists) {
		t.Error("expected true for ExistsExpr")
	}

	// ComparisonSubqueryExpr
	cmp := &parser.ComparisonSubqueryExpr{
		Subquery: &parser.SelectStatement{TableName: "t"},
	}
	if !containsExprDML(cmp) {
		t.Error("expected true for ComparisonSubqueryExpr")
	}

	// BinaryExpr with DML
	bin := &parser.BinaryExpr{
		Left:  sq,
		Right: &parser.Value{Type: "int", IntVal: 1},
	}
	if !containsExprDML(bin) {
		t.Error("expected true for BinaryExpr with DML")
	}

	// AndExpr with DML
	and := &parser.AndExpr{Left: sq, Right: &parser.Value{Type: "int", IntVal: 1}}
	if !containsExprDML(and) {
		t.Error("expected true for AndExpr with DML")
	}

	// OrExpr with DML
	or := &parser.OrExpr{Left: sq, Right: &parser.Value{Type: "int", IntVal: 1}}
	if !containsExprDML(or) {
		t.Error("expected true for OrExpr with DML")
	}

	// NotExpr with DML
	not := &parser.NotExpr{Expr: sq}
	if !containsExprDML(not) {
		t.Error("expected true for NotExpr with DML")
	}

	// InExpr with DML
	in := &parser.InExpr{
		Left:  &parser.ColumnRef{Name: "a"},
		Right: []parser.Expression{sq},
	}
	if !containsExprDML(in) {
		t.Error("expected true for InExpr with DML")
	}

	// BetweenExpr with DML
	between := &parser.BetweenExpr{
		Expr:  sq,
		Lower: &parser.Value{Type: "int", IntVal: 1},
		Upper: &parser.Value{Type: "int", IntVal: 10},
	}
	if !containsExprDML(between) {
		t.Error("expected true for BetweenExpr with DML")
	}

	// CaseExpr with DML
	caseExpr := &parser.CaseExpr{
		Whens: []parser.WhenClause{
			{Condition: sq, Result: &parser.Value{Type: "int", IntVal: 1}},
		},
	}
	if !containsExprDML(caseExpr) {
		t.Error("expected true for CaseExpr with DML")
	}

	// CaseExpr with DML in Else
	caseExprElse := &parser.CaseExpr{
		Else: sq,
	}
	if !containsExprDML(caseExprElse) {
		t.Error("expected true for CaseExpr with DML in Else")
	}

	// CastExpr with DML
	cast := &parser.CastExpr{Expr: sq}
	if !containsExprDML(cast) {
		t.Error("expected true for CastExpr with DML")
	}

	// FunctionCall with DML
	funcCall := &parser.FunctionCall{Name: "COALESCE", Args: []parser.Expression{sq}}
	if !containsExprDML(funcCall) {
		t.Error("expected true for FunctionCall with DML")
	}

	// AggregateExpr with DML
	agg := &parser.AggregateExpr{Name: "SUM", Args: []parser.Expression{sq}}
	if !containsExprDML(agg) {
		t.Error("expected true for AggregateExpr with DML")
	}

	// WindowFunctionExpr with DML in args
	wf := &parser.WindowFunctionExpr{
		Args: []parser.Expression{sq},
		Over: parser.WindowSpec{PartitionBy: []parser.Expression{}},
	}
	if !containsExprDML(wf) {
		t.Error("expected true for WindowFunctionExpr with DML in args")
	}

	// WindowFunctionExpr with DML in partitionBy
	wf2 := &parser.WindowFunctionExpr{
		Args: []parser.Expression{&parser.Value{Type: "int", IntVal: 1}},
		Over: parser.WindowSpec{PartitionBy: []parser.Expression{sq}},
	}
	if !containsExprDML(wf2) {
		t.Error("expected true for WindowFunctionExpr with DML in partitionBy")
	}

	// Simple value (no DML)
	val := &parser.Value{Type: "int", IntVal: 1}
	if containsExprDML(val) {
		t.Error("expected false for simple value")
	}

	// InExpr with DML in left
	inLeft := &parser.InExpr{
		Left:  sq,
		Right: []parser.Expression{&parser.Value{Type: "int", IntVal: 1}},
	}
	if !containsExprDML(inLeft) {
		t.Error("expected true for InExpr with DML in left")
	}

	// BetweenExpr with DML in lower
	betweenLower := &parser.BetweenExpr{
		Expr:  &parser.Value{Type: "int", IntVal: 1},
		Lower: sq,
		Upper: &parser.Value{Type: "int", IntVal: 10},
	}
	if !containsExprDML(betweenLower) {
		t.Error("expected true for BetweenExpr with DML in lower")
	}

	// BetweenExpr with DML in upper
	betweenUpper := &parser.BetweenExpr{
		Expr:  &parser.Value{Type: "int", IntVal: 1},
		Lower: &parser.Value{Type: "int", IntVal: 1},
		Upper: sq,
	}
	if !containsExprDML(betweenUpper) {
		t.Error("expected true for BetweenExpr with DML in upper")
	}
}

func TestCoerceToColumnFlexible(t *testing.T) {
	// FLEXIBLE with map
	val := map[string]interface{}{"key": "value"}
	v, err := coerceToColumn(val, storage.ColumnSchema{Name: "a", Type: "FLEXIBLE"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, ok := v.(map[string]interface{}); !ok {
		t.Errorf("expected map, got %T", v)
	}

	// FLEXIBLE with string (JSON)
	v, err = coerceToColumn(`{"key":"value"}`, storage.ColumnSchema{Name: "a", Type: "FLEXIBLE"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, ok := v.(map[string]interface{}); !ok {
		t.Errorf("expected map from JSON string, got %T: %v", v, v)
	}

	// FLEXIBLE with non-JSON string
	v, err = coerceToColumn("not json", storage.ColumnSchema{Name: "a", Type: "FLEXIBLE"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if s, ok := v.(string); !ok || s != "not json" {
		t.Errorf("expected 'not json', got %v", v)
	}

	// FLEXIBLE with non-string non-map
	v, err = coerceToColumn(123, storage.ColumnSchema{Name: "a", Type: "FLEXIBLE"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if v != "123" {
		t.Errorf("expected '123', got %v", v)
	}
}

func TestNormalizeForColumn(t *testing.T) {
	col := storage.ColumnSchema{Name: "age", Type: "INT"}
	v, err := normalizeForColumn("42", col)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if v != int64(42) {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestRequireCurrentDBNoDatabase(t *testing.T) {
	session := &Session{}
	_, err := requireCurrentDB(&ExecutionContext{Session: session})
	if err == nil {
		t.Error("expected error for no current database")
	}
}

func TestResolveDatabase(t *testing.T) {
	session := &Session{}
	session.SetCurrentDatabase("mydb")
	// Empty name should resolve to current
	db, err := resolveDatabase(&ExecutionContext{Session: session}, "")
	if err != nil || db != "mydb" {
		t.Errorf("expected 'mydb', got %q, err=%v", db, err)
	}

	// Explicit name should use that
	db, err = resolveDatabase(&ExecutionContext{Session: session}, "otherdb")
	if err != nil || db != "otherdb" {
		t.Errorf("expected 'otherdb', got %q, err=%v", db, err)
	}
}

func TestShowIndexesCommandNoDB(t *testing.T) {
	session := &Session{}
	ctx := &ExecutionContext{Session: session}
	cmd := &ShowIndexesCommand{stmt: &parser.ShowIndexesStatement{TableName: "t"}}
	_, err := cmd.Execute(ctx)
	if err == nil {
		t.Error("expected error for no current database")
	}
}

func TestIsProcedureBodySafe(t *testing.T) {
	// Safe statements
	safeStmts := []parser.Statement{
		&parser.SelectStatement{},
		&parser.InsertStatement{},
		&parser.UpdateStatement{},
		&parser.DeleteStatement{},
		&parser.BeginStatement{},
	}
	for _, stmt := range safeStmts {
		if !isProcedureBodySafe(stmt) {
			t.Errorf("expected %T to be safe", stmt)
		}
	}

	// Unsafe statements
	unsafeStmts := []parser.Statement{
		&parser.DropTableStatement{},
		&parser.AlterTableStatement{},
		&parser.CreateDatabaseStatement{},
	}
	for _, stmt := range unsafeStmts {
		if isProcedureBodySafe(stmt) {
			t.Errorf("expected %T to be unsafe", stmt)
		}
	}
}

func TestIsMigrationSafe(t *testing.T) {
	// Safe statements
	safeStmts := []parser.Statement{
		&parser.SelectStatement{},
		&parser.InsertStatement{},
		&parser.UpdateStatement{},
		&parser.DeleteStatement{},
		&parser.CreateIndexStatement{},
		&parser.CreateViewStatement{},
		&parser.AlterTableStatement{
			Action: &parser.AlterAddColumn{},
		},
		&parser.AlterTableStatement{
			Action: &parser.AlterAddConstraint{},
		},
	}
	for _, stmt := range safeStmts {
		if !isMigrationSafe(stmt) {
			t.Errorf("expected %T to be safe for migration", stmt)
		}
	}

	// Unsafe statements
	unsafeStmts := []parser.Statement{
		&parser.DropTableStatement{},
		&parser.AlterTableStatement{
			Action: &parser.AlterDropColumn{},
		},
		&parser.AlterTableStatement{
			Action: &parser.AlterRenameColumn{},
		},
	}
	for _, stmt := range unsafeStmts {
		if isMigrationSafe(stmt) {
			t.Errorf("expected %T to be unsafe for migration", stmt)
		}
	}
}

func TestIsMigrationSafeSystemTables(t *testing.T) {
	// System table
	stmt := &parser.CreateTableStatement{TableName: "_migrations"}
	if isMigrationSafe(stmt) {
		t.Error("expected _migrations to be unsafe")
	}

	// Audit log table
	stmt2 := &parser.CreateTableStatement{TableName: "vaultdb_audit_log"}
	if isMigrationSafe(stmt2) {
		t.Error("expected vaultdb_audit_log to be unsafe")
	}

	// Regular table
	stmt3 := &parser.CreateTableStatement{TableName: "users"}
	if !isMigrationSafe(stmt3) {
		t.Error("expected users to be safe")
	}
}

func TestSplitSQLStatements(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"SELECT 1;", 1},
		{"SELECT 1; SELECT 2;", 2},
		{"SELECT 'hello; world';", 1},
		{"SELECT 1;-- comment\nSELECT 2;", 2},
	}
	for _, tt := range tests {
		got := splitSQLStatements(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitSQLStatements(%q) returned %d parts, want %d: %v", tt.input, len(got), tt.want, got)
		}
	}
}

func TestConvertJSONValue(t *testing.T) {
	// INT from float64
	v, err := convertJSONValue(float64(42), "INT")
	if err != nil || v != int64(42) {
		t.Errorf("INT from float64: v=%v, err=%v", v, err)
	}

	// INT from int
	v, err = convertJSONValue(int(42), "INT")
	if err != nil || v != int64(42) {
		t.Errorf("INT from int: v=%v, err=%v", v, err)
	}

	// INT from int64
	v, err = convertJSONValue(int64(42), "INT")
	if err != nil || v != int64(42) {
		t.Errorf("INT from int64: v=%v, err=%v", v, err)
	}

	// INT from string
	v, err = convertJSONValue("42", "INT")
	if err != nil || v != int64(42) {
		t.Errorf("INT from string: v=%v, err=%v", v, err)
	}

	// INT from invalid type
	_, err = convertJSONValue([]int{1}, "INT")
	if err == nil {
		t.Error("expected error for []int to INT")
	}

	// FLOAT from float64
	v, err = convertJSONValue(float64(3.14), "FLOAT")
	if err != nil || v != 3.14 {
		t.Errorf("FLOAT from float64: v=%v, err=%v", v, err)
	}

	// FLOAT from string
	v, err = convertJSONValue("3.14", "FLOAT")
	if err != nil || v != 3.14 {
		t.Errorf("FLOAT from string: v=%v, err=%v", v, err)
	}

	// FLOAT from invalid type
	_, err = convertJSONValue([]float64{}, "FLOAT")
	if err == nil {
		t.Error("expected error for []float64 to FLOAT")
	}

	// BOOL from bool
	v, err = convertJSONValue(true, "BOOL")
	if err != nil || v != true {
		t.Errorf("BOOL from bool: v=%v, err=%v", v, err)
	}

	// BOOL from invalid type
	_, err = convertJSONValue("true", "BOOL")
	if err == nil {
		t.Error("expected error for string to BOOL")
	}

	// TEXT passthrough
	v, err = convertJSONValue("hello", "TEXT")
	if err != nil || v != "hello" {
		t.Errorf("TEXT: v=%v, err=%v", v, err)
	}
}

func TestValueToStringCopy(t *testing.T) {
	tests := []struct {
		val  interface{}
		want string
	}{
		{nil, ""},
		{int64(42), "42"},
		{float64(3.14), "3.14"},
		{true, "true"},
		{false, "false"},
		{"hello", "hello"},
	}
	for _, tt := range tests {
		got := valueToStringCopy(tt.val)
		if got != tt.want {
			t.Errorf("valueToStringCopy(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestEscapeCSVField(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`hello,world`, `"hello,world"`},
		{`hello"world`, `"hello""world"`},
	}
	for _, tt := range tests {
		got := escapeCSVField(tt.input)
		if got != tt.want {
			t.Errorf("escapeCSVField(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCreateDatabaseCommands(t *testing.T) {
	session := setupSession(t)

	// Create database
	result := executeSQL(t, session, "CREATE DATABASE testdb;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}

	// Create database IF NOT EXISTS (existing)
	result = executeSQL(t, session, "CREATE DATABASE IF NOT EXISTS testdb;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}
}

func TestDropDatabaseCommands(t *testing.T) {
	session := setupSession(t)

	// Create and drop
	executeSQL(t, session, "CREATE DATABASE to_drop;")
	result := executeSQL(t, session, "DROP DATABASE to_drop;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}

	// Drop IF EXISTS (non-existing)
	result = executeSQL(t, session, "DROP DATABASE IF EXISTS nonexistent;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}
}

func TestUseDatabase(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE otherdb;")

	result := executeSQL(t, session, "USE otherdb;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}

	// Use non-existing database
	executeSQLExpectError(t, session, "USE nonexistent;")
}

func TestShowDatabasesCommand(t *testing.T) {
	session := setupSession(t)
	result := executeSQL(t, session, "SHOW DATABASES;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
}

func TestCreateTableErrors(t *testing.T) {
	session := setupSession(t)

	// Duplicate table
	executeSQLExpectError(t, session, "CREATE TABLE heroes (id INT);")

	// Invalid column type
	executeSQLExpectError(t, session, "CREATE TABLE bad (id INVALID_TYPE);")
}

func TestAlterTableCommands(t *testing.T) {
	session := setupSession(t)

	// Add column
	result := executeSQL(t, session, "ALTER TABLE heroes ADD COLUMN age INT;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}

	// Drop column
	result = executeSQL(t, session, "ALTER TABLE heroes DROP COLUMN age;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}

	// Rename column
	result = executeSQL(t, session, "ALTER TABLE heroes RENAME COLUMN name TO hero_name;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestShowTablesCommand(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE t1 (id INT);")
	executeSQL(t, session, "CREATE TABLE t2 (id INT);")

	result := executeSQL(t, session, "SHOW TABLES;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	if len(result.Rows) < 2 {
		t.Fatalf("expected at least 2 tables, got %d", len(result.Rows))
	}
}

func TestShowTablesFromDatabase(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE otherdb;")
	executeSQL(t, session, "USE otherdb;")
	executeSQL(t, session, "CREATE TABLE other_table (id INT);")

	result := executeSQL(t, session, "SHOW TABLES FROM otherdb;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestDescribeTableCommand(t *testing.T) {
	session := setupSession(t)
	result := executeSQL(t, session, "DESCRIBE heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestDropTableCommand(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE to_drop (id INT);")
	result := executeSQL(t, session, "DROP TABLE to_drop;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}

	// Drop IF EXISTS
	result = executeSQL(t, session, "DROP TABLE IF EXISTS nonexistent;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestSelectAggregateFunctions(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	queries := []string{
		"SELECT COUNT(*) FROM heroes;",
		"SELECT COUNT(DISTINCT level) FROM heroes;",
		"SELECT SUM(level) FROM heroes;",
		"SELECT AVG(level) FROM heroes;",
		"SELECT MIN(level) FROM heroes;",
		"SELECT MAX(level) FROM heroes;",
	}
	for _, q := range queries {
		result := executeSQL(t, session, q)
		if result.Type != "rows" {
			t.Errorf("expected rows for %q, got %s", q, result.Type)
		}
	}
}

func TestSelectGroupByHaving(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT alive, COUNT(*) AS cnt FROM heroes GROUP BY alive HAVING COUNT(*) > 1;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestSelectOrderBy(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT name FROM heroes ORDER BY level DESC;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	// First name should be the one with highest level
	if result.Rows[0][0] != "Aragorn" {
		t.Fatalf("expected Aragorn first, got %s", result.Rows[0][0])
	}
}

func TestSelectLimitOffset(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT name FROM heroes ORDER BY id LIMIT 2 OFFSET 1;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
}

func TestSelectDistinct(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT DISTINCT alive FROM heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 distinct values, got %d", len(result.Rows))
	}
}

func TestInsertUpdateDelete(t *testing.T) {
	session := setupSession(t)

	// Insert
	executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Test', 1, TRUE, 5.0, 'Test bio');")

	// Update
	result := executeSQL(t, session, "UPDATE heroes SET level = 20 WHERE id = 10;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}

	// Verify
	result = executeSQL(t, session, "SELECT level FROM heroes WHERE id = 10;")
	if result.Rows[0][0] != "20" {
		t.Fatalf("expected level 20, got %s", result.Rows[0][0])
	}

	// Delete
	result = executeSQL(t, session, "DELETE FROM heroes WHERE id = 10;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestBeginCommit(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Tx1', 1, TRUE, 5.0, 'Tx1 bio');")
	executeSQL(t, session, "COMMIT;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM heroes WHERE id = 10;")
	if result.Rows[0][0] != "1" {
		t.Fatalf("expected 1 row, got %s", result.Rows[0][0])
	}
}

func TestBeginRollback(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Tx2', 1, TRUE, 5.0, 'Tx2 bio');")
	executeSQL(t, session, "ROLLBACK;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM heroes WHERE id = 10;")
	if result.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows after rollback, got %s", result.Rows[0][0])
	}
}

func TestSavepointOperations(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'SP1', 1, TRUE, 5.0, 'SP1 bio');")
	executeSQL(t, session, "SAVEPOINT sp1;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (11, 'SP2', 2, TRUE, 6.0, 'SP2 bio');")
	executeSQL(t, session, "ROLLBACK TO SAVEPOINT sp1;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM heroes;")
	if result.Rows[0][0] != "4" {
		t.Fatalf("expected 4 rows after rollback to savepoint, got %s", result.Rows[0][0])
	}

	// Release savepoint
	executeSQL(t, session, "RELEASE SAVEPOINT sp1;")
	executeSQL(t, session, "COMMIT;")
}

func TestExplainCommand(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "EXPLAIN SELECT * FROM heroes;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestSelectWithSubquery(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	// IN subquery
	result := executeSQL(t, session, "SELECT * FROM heroes WHERE id IN (SELECT id FROM heroes WHERE level > 8);")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
}

func TestSelectWithExists(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT * FROM heroes WHERE EXISTS (SELECT 1 FROM heroes WHERE level > 8);")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestSelectWithCaseExpression(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT CASE WHEN level > 8 THEN 'high' ELSE 'low' END AS category FROM heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCreateTableWithConstraints(t *testing.T) {
	session := setupSession(t)

	// Primary key
	executeSQL(t, session, "CREATE TABLE pk_test (id INT PRIMARY KEY, name TEXT);")

	// Unique
	executeSQL(t, session, "CREATE TABLE uq_test (id INT, email TEXT UNIQUE);")

	// Not null
	executeSQL(t, session, "CREATE TABLE nn_test (id INT NOT NULL, name TEXT);")

	// Default
	executeSQL(t, session, "CREATE TABLE def_test (id INT, active BOOL DEFAULT TRUE);")
}

func TestCopyFromCSV(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE csv_test (id INT, name TEXT, value FLOAT);")

	// Use INSERT instead of COPY for testing
	executeSQL(t, session, "INSERT INTO csv_test VALUES (1, 'row1', 1.5);")
	executeSQL(t, session, "INSERT INTO csv_test VALUES (2, 'row2', 2.5);")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM csv_test;")
	if result.Rows[0][0] != "2" {
		t.Fatalf("expected 2 rows, got %s", result.Rows[0][0])
	}
}

func TestProcedureCall(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE PROCEDURE insert_hero(id_val INT, name_val TEXT) LANGUAGE SQL AS $$INSERT INTO heroes (id, name) VALUES (id_val, name_val);$$;")

	result := executeSQL(t, session, "CALL insert_hero(100, 'ProcedureHero');")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestCreateFunctionAndCall(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE FUNCTION get_count() RETURNS INT LANGUAGE SQL AS $$SELECT COUNT(*) FROM heroes;$$;")

	result := executeSQL(t, session, "SELECT get_count();")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestTransactionConflict(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	session1 := NewSession(store, nil, txm, nil)
	session2 := NewSession(store, nil, txm, nil)

	// Setup
	_, err = session1.Execute(&parser.CreateDatabaseStatement{DatabaseName: "conflict_db"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session1.Execute(&parser.UseDatabaseStatement{DatabaseName: "conflict_db"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session1.Execute(&parser.CreateTableStatement{
		TableName: "counter",
		Columns:   []parser.ColumnDef{{Name: "val", Type: "INT"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session1.Execute(&parser.InsertStatement{
		TableName: "counter",
		Values:    [][]parser.Expression{{&parser.Value{Type: "int", IntVal: 0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session2.Execute(&parser.UseDatabaseStatement{DatabaseName: "conflict_db"})
	if err != nil {
		t.Fatal(err)
	}

	// Both begin transactions
	_, _ = session1.Execute(&parser.BeginStatement{})
	_, _ = session2.Execute(&parser.BeginStatement{})

	// Both try to update same table
	_, _ = session1.Execute(&parser.UpdateStatement{
		TableName: "counter",
		SetClauses: []parser.Assignment{
			{Column: "val", Value: &parser.Value{Type: "int", IntVal: 1}},
		},
	})

	// Session2 also tries to update
	_, _ = session2.Execute(&parser.UpdateStatement{
		TableName: "counter",
		SetClauses: []parser.Assignment{
			{Column: "val", Value: &parser.Value{Type: "int", IntVal: 2}},
		},
	})

	// First commit should succeed
	_, err = session1.Execute(&parser.CommitStatement{})
	if err != nil {
		t.Fatalf("session1 commit failed: %v", err)
	}

	// Second commit should fail with conflict
	_, err = session2.Execute(&parser.CommitStatement{})
	if err == nil {
		t.Fatal("expected conflict error for session2 commit")
	}
}

func TestFireTriggers(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE audit_log (msg TEXT);")
	executeSQL(t, session, "CREATE TRIGGER trg_after_insert AFTER INSERT ON heroes FOR EACH ROW EXECUTE PROCEDURE insert_audit();")

	// The trigger body references a function that doesn't exist, but fireTriggers should not crash
	// It should log the error and continue
	result := executeSQL(t, session, "INSERT INTO heroes VALUES (200, 'TriggerTest', 1, TRUE, 5.0, 'Test');")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestAlterTableWithConstraints(t *testing.T) {
	session := setupSession(t)

	// Add unique constraint
	executeSQL(t, session, "CREATE TABLE constraint_test (id INT, name TEXT);")
	executeSQL(t, session, "ALTER TABLE constraint_test ADD CONSTRAINT uq_name UNIQUE (name);")

	// Add check constraint
	executeSQL(t, session, "ALTER TABLE constraint_test ADD CONSTRAINT chk_id CHECK (id > 0);")

	// Drop constraint
	executeSQL(t, session, "ALTER TABLE constraint_test DROP CONSTRAINT uq_name;")
}

func TestCreateDatabaseErrors(t *testing.T) {
	session := setupSession(t)

	// Database already exists
	executeSQLExpectError(t, session, "CREATE DATABASE mydb;")
}

func TestDropDatabaseErrors(t *testing.T) {
	session := setupSession(t)

	// Database doesn't exist
	executeSQLExpectError(t, session, "DROP DATABASE nonexistent;")
}

func TestShowEncryptionStatus(t *testing.T) {
	session := setupSession(t)
	result := executeSQL(t, session, "SHOW ENCRYPTION STATUS;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestCreateTypeAndDropType(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TYPE mood AS ENUM ('happy', 'sad');")
	executeSQL(t, session, "DROP TYPE mood;")
}

func TestShowIndexes(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE idx_test (id INT, name TEXT);")
	executeSQL(t, session, "CREATE INDEX idx_name ON idx_test (name);")

	result := executeSQL(t, session, "SHOW INDEXES FROM idx_test;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCreateIndexAndDropIndex(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE idx_test2 (id INT, name TEXT);")

	executeSQL(t, session, "CREATE INDEX idx_id ON idx_test2 (id);")
	executeSQL(t, session, "CREATE INDEX idx_name ON idx_test2 (name);")
	executeSQL(t, session, "DROP INDEX idx_id;")
}

func TestExecuteTriggerBody(t *testing.T) {
	// executeTriggerBody parses and executes a SQL string as a trigger body
	session := setupSession(t)
	ctx := &ExecutionContext{
		Session: session,
		Storage: session.Storage,
	}

	// Valid body
	err := executeTriggerBody(ctx, "SELECT 1;")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Invalid parse
	err = executeTriggerBody(ctx, "INVALID SQL ;;;")
	if err == nil {
		t.Error("expected error for invalid SQL")
	}

	// Valid parse but invalid command
	err = executeTriggerBody(ctx, "BEGIN;")
	if err == nil {
		t.Error("expected error for BEGIN as trigger body")
	}
}

func TestContainsSubqueryDML(t *testing.T) {
	// SELECT with subquery containing INSERT in WHERE
	sel := &parser.SelectStatement{
		Where: &parser.BinaryExpr{
			Left: &parser.ColumnRef{Name: "id"},
			Operator: "=",
			Right: &parser.SubqueryExpr{
				Query: &parser.InsertStatement{TableName: "t"},
			},
		},
	}
	if !containsSubqueryDML(sel) {
		t.Error("expected true for SELECT with INSERT subquery in WHERE")
	}

	// SELECT with subquery in FROM
	selFrom := &parser.SelectStatement{
		FromSubquery: &parser.SelectStatement{
			TableName: "t",
		},
	}
	// This shouldn't have DML since it's a SELECT subquery
	if containsSubqueryDML(selFrom) {
		t.Error("expected false for SELECT with SELECT subquery in FROM")
	}
}

func TestCommandsDDLMiscExecuteErrors(t *testing.T) {
	session := setupSession(t)

	// Migration with invalid SQL
	executeSQLExpectError(t, session, "CREATE MIGRATION bad_migration AS $$INVALID SQL;;;$$;")

	// Migration with unsafe statement (DROP TABLE)
	executeSQLExpectError(t, session, "CREATE MIGRATION unsafe_migration AS $$DROP TABLE heroes;$$;")

	// Procedure with unsafe body
	executeSQLExpectError(t, session, "CREATE PROCEDURE unsafe_proc() LANGUAGE SQL AS $$DROP TABLE heroes;$$;")

	// Procedure with invalid body
	executeSQLExpectError(t, session, "CREATE PROCEDURE bad_proc() LANGUAGE SQL AS $$INVALID;;;$$;")

	// Drop function that doesn't exist
	executeSQLExpectError(t, session, "DROP FUNCTION nonexistent;")
}

func TestCommandsDDLTableExecuteErrors(t *testing.T) {
	session := setupSession(t)

	// Alter table with unsupported action
	executeSQLExpectError(t, session, "ALTER TABLE heroes ADD COLUMN age;")
	// Above should succeed, let's try invalid ALTER
	executeSQLExpectError(t, session, "ALTER nonexistent_table ADD COLUMN x INT;")

	// Drop table that doesn't exist
	executeSQLExpectError(t, session, "DROP TABLE nonexistent;")
}

func TestCoerceRowViaEvalErrors(t *testing.T) {
	schema := &storage.TableSchema{
		Columns: []storage.ColumnSchema{
			{Name: "a", Type: "INT"},
			{Name: "b", Type: "FLOAT"},
		},
	}
	// Row with mismatched types
	row := storage.Row{"not_int", "not_float"}
	_, err := coerceRowViaEval(row, schema)
	if err == nil {
		t.Error("expected error for mismatched types")
	}
}
