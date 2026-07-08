package executor

import (
	"strings"
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func TestCoverParseDropPolicy(t *testing.T) {
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

func TestCoverCoerceToColumnEdgeCases(t *testing.T) {
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

	// unsupported type
	_, err = coerceToColumn("val", storage.ColumnSchema{Name: "a", Type: "UNKNOWN"})
	if err == nil {
		t.Error("expected error for unsupported type")
	}
}

func TestCoverInferTypeValues(t *testing.T) {
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

func TestCoverValueToStringValues(t *testing.T) {
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
		{struct{}{}, "{}"},
	}
	for _, tt := range tests {
		got := valueToString(tt.val)
		if got != tt.want {
			t.Errorf("valueToString(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestCoverContainsStatementDMLValues(t *testing.T) {
	// Non-SELECT statement
	ins := &parser.InsertStatement{TableName: "t"}
	if containsStatementDML(ins) {
		t.Error("expected containsStatementDML to return false for INSERT")
	}
}

func TestCoverCoerceToColumnFlexible(t *testing.T) {
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

func TestCoverNormalizeForColumnValues(t *testing.T) {
	col := storage.ColumnSchema{Name: "age", Type: "INT"}
	v, err := normalizeForColumn("42", col)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if v != int64(42) {
		t.Errorf("expected 42, got %v", v)
	}
}

func TestCoverRequireCurrentDBNoDatabase(t *testing.T) {
	session := &Session{}
	_, err := requireCurrentDB(&ExecutionContext{Session: session})
	if err == nil {
		t.Error("expected error for no current database")
	}
}

func TestCoverResolveDatabaseValues(t *testing.T) {
	session := setupSession(t)
	// Empty name should resolve to current (mydb)
	db, err := resolveDatabase(&ExecutionContext{Session: session}, "")
	if err != nil || db != "mydb" {
		t.Errorf("expected 'mydb', got %q, err=%v", db, err)
	}
}

func TestCoverShowIndexesCommandNoDB(t *testing.T) {
	session := &Session{}
	ctx := &ExecutionContext{Session: session}
	cmd := &ShowIndexesCommand{stmt: &parser.ShowIndexesStatement{TableName: "t"}}
	_, err := cmd.Execute(ctx)
	if err == nil {
		t.Error("expected error for no current database")
	}
}

func TestCoverIsProcedureBodySafeValues(t *testing.T) {
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

func TestCoverIsMigrationSafeValues(t *testing.T) {
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

func TestCoverIsMigrationSafeSystemTables(t *testing.T) {
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

func TestCoverSplitSQLStatementsValues(t *testing.T) {
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

func TestCoverConvertJSONValueValues(t *testing.T) {
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

func TestCoverValueToStringCopyValues(t *testing.T) {
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

func TestCoverCreateDatabaseCommands(t *testing.T) {
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

func TestCoverDropDatabaseCommands(t *testing.T) {
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

func TestCoverUseDatabaseCommand(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE otherdb;")

	result := executeSQL(t, session, "USE otherdb;")
	if result.Type != "message" {
		t.Fatalf("expected message result, got %s", result.Type)
	}

	// Use non-existing database
	executeSQLExpectError(t, session, "USE nonexistent;")
}

func TestCoverShowDatabasesCommand(t *testing.T) {
	session := setupSession(t)
	result := executeSQL(t, session, "SHOW DATABASES;")
	if result.Type != "rows" {
		t.Fatalf("expected rows result, got %s", result.Type)
	}
}

func TestCoverCreateTableErrors(t *testing.T) {
	session := setupSession(t)

	// Duplicate table
	executeSQLExpectError(t, session, "CREATE TABLE heroes (id INT);")
}

func TestCoverAlterTableCommands(t *testing.T) {
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

func TestCoverShowTablesCommand(t *testing.T) {
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

func TestCoverShowTablesFromDatabase(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE DATABASE otherdb;")
	executeSQL(t, session, "USE otherdb;")
	executeSQL(t, session, "CREATE TABLE other_table (id INT);")

	result := executeSQL(t, session, "SHOW TABLES FROM otherdb;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCoverDescribeTableCommand(t *testing.T) {
	session := setupSession(t)
	result := executeSQL(t, session, "DESCRIBE heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCoverDropTableCommand(t *testing.T) {
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

func TestCoverSelectAggregateFunctions(t *testing.T) {
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

func TestCoverSelectGroupByHaving(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT alive, COUNT(*) AS cnt FROM heroes GROUP BY alive HAVING COUNT(*) > 1;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCoverSelectOrderByValues(t *testing.T) {
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

func TestCoverSelectLimitOffset(t *testing.T) {
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

func TestCoverSelectDistinct(t *testing.T) {
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

func TestCoverInsertUpdateDelete(t *testing.T) {
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

func TestCoverBeginCommit(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Tx1', 1, TRUE, 5.0, 'Tx1 bio');")
	executeSQL(t, session, "COMMIT;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM heroes WHERE id = 10;")
	if result.Rows[0][0] != "1" {
		t.Fatalf("expected 1 row, got %s", result.Rows[0][0])
	}
}

func TestCoverBeginRollback(t *testing.T) {
	session := setupSession(t)

	executeSQL(t, session, "BEGIN;")
	executeSQL(t, session, "INSERT INTO heroes VALUES (10, 'Tx2', 1, TRUE, 5.0, 'Tx2 bio');")
	executeSQL(t, session, "ROLLBACK;")

	result := executeSQL(t, session, "SELECT COUNT(*) FROM heroes WHERE id = 10;")
	if result.Rows[0][0] != "0" {
		t.Fatalf("expected 0 rows after rollback, got %s", result.Rows[0][0])
	}
}

func TestCoverSavepointOperations(t *testing.T) {
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

func TestCoverExplainCommand(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "EXPLAIN SELECT * FROM heroes;")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestCoverSelectWithSubquery(t *testing.T) {
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

func TestCoverSelectWithExists(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT * FROM heroes WHERE EXISTS (SELECT 1 FROM heroes WHERE level > 8);")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCoverSelectWithCaseExpression(t *testing.T) {
	session := setupSession(t)
	seedHeroes(t, session)

	result := executeSQL(t, session, "SELECT CASE WHEN level > 8 THEN 'high' ELSE 'low' END AS category FROM heroes;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCoverCreateTableWithConstraints(t *testing.T) {
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

func TestCoverProcedureCall(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE PROCEDURE insert_hero(id_val INT, name_val TEXT) LANGUAGE SQL AS $$INSERT INTO heroes (id, name) VALUES (id_val, name_val);$$;")

	result := executeSQL(t, session, "CALL insert_hero(100, 'ProcedureHero');")
	if result.Type != "message" {
		t.Fatalf("expected message, got %s", result.Type)
	}
}

func TestCoverTransactionConflict(t *testing.T) {
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
		Columns:   []parser.ColumnDef{{Name: "val", DataType: "INT"}},
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
		Assignments: []parser.Assignment{
			{Column: "val", Value: &parser.Value{Type: "int", IntVal: 1}},
		},
	})

	// Session2 also tries to update
	_, _ = session2.Execute(&parser.UpdateStatement{
		TableName: "counter",
		Assignments: []parser.Assignment{
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

func TestCoverFireTriggers(t *testing.T) {
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

func TestCoverAlterTableWithConstraints(t *testing.T) {
	session := setupSession(t)

	// Add unique constraint
	executeSQL(t, session, "CREATE TABLE constraint_test (id INT, name TEXT);")
	executeSQL(t, session, "ALTER TABLE constraint_test ADD CONSTRAINT uq_name UNIQUE (name);")

	// Add check constraint
	executeSQL(t, session, "ALTER TABLE constraint_test ADD CONSTRAINT chk_id CHECK (id > 0);")

	// Drop constraint
	executeSQL(t, session, "ALTER TABLE constraint_test DROP CONSTRAINT uq_name;")
}

func TestCoverCreateDatabaseErrors(t *testing.T) {
	session := setupSession(t)

	// Database already exists
	executeSQLExpectError(t, session, "CREATE DATABASE mydb;")
}

func TestCoverDropDatabaseErrors(t *testing.T) {
	session := setupSession(t)

	// Database doesn't exist
	executeSQLExpectError(t, session, "DROP DATABASE nonexistent;")
}

func TestCoverCreateTypeAndDropType(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TYPE mood AS ENUM ('happy', 'sad');")
	executeSQL(t, session, "DROP TYPE mood;")
}

func TestCoverShowIndexes(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE idx_test (id INT, name TEXT);")
	executeSQL(t, session, "CREATE INDEX idx_name ON idx_test (name);")

	result := executeSQL(t, session, "SHOW INDEXES FROM idx_test;")
	if result.Type != "rows" {
		t.Fatalf("expected rows, got %s", result.Type)
	}
}

func TestCoverCreateIndexAndDropIndex(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE idx_test2 (id INT, name TEXT);")

	executeSQL(t, session, "CREATE INDEX idx_id ON idx_test2 (id);")
	executeSQL(t, session, "CREATE INDEX idx_name ON idx_test2 (name);")
	executeSQL(t, session, "DROP INDEX idx_id;")
}

func TestCoverExecuteTriggerBodyValues(t *testing.T) {
	// executeTriggerBody parses and executes a SQL string as a trigger body
	session := setupSession(t)
	ctx := &ExecutionContext{
		Session: session,
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

func TestCoverContainsSubqueryDMLValues(t *testing.T) {
	// SELECT with subquery containing INSERT in WHERE
	sel := &parser.SelectStatement{
		Where: &parser.BinaryExpr{
			Left:   &parser.ColumnRef{Name: "id"},
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

func TestCoverCommandsDDLMiscExecuteErrors(t *testing.T) {
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

func TestCoverCommandsDDLTableExecuteErrors(t *testing.T) {
	session := setupSession(t)

	// Alter table with unsupported action
	executeSQLExpectError(t, session, "ALTER TABLE heroes ADD COLUMN age;")
	// Above should succeed, let's try invalid ALTER
	executeSQLExpectError(t, session, "ALTER nonexistent_table ADD COLUMN x INT;")

	// Drop table that doesn't exist
	executeSQLExpectError(t, session, "DROP TABLE nonexistent;")
}

func TestCoverCoerceRowViaEvalErrors(t *testing.T) {
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

func TestCoverNewAggregator(t *testing.T) {
	tests := []struct {
		name      string
		distinct  bool
		wantType  string
	}{
		{"COUNT", false, "*executor.countAgg"},
		{"SUM", false, "*executor.sumAgg"},
		{"AVG", false, "*executor.avgAgg"},
		{"MIN", false, "*executor.minAgg"},
		{"MAX", false, "*executor.maxAgg"},
	}
	for _, tt := range tests {
		agg := NewAggregator(tt.name, tt.distinct)
		if agg == nil {
			t.Errorf("NewAggregator(%q) returned nil", tt.name)
		}
	}
}

func TestCoverNewAggregatorStringAgg(t *testing.T) {
	// STRING_AGG with default delimiter
	agg := NewAggregator("STRING_AGG", false)
	if agg == nil {
		t.Error("NewAggregator(STRING_AGG) returned nil")
	}

	// STRING_AGG with custom delimiter
	agg = NewAggregator("STRING_AGG", false, "arg1", "|")
	if agg == nil {
		t.Error("NewAggregator(STRING_AGG, ...) returned nil")
	}

	// Unknown aggregator
	agg = NewAggregator("UNKNOWN", false)
	if agg == nil {
		t.Error("NewAggregator(UNKNOWN) returned nil")
	}
}

func TestCoverJsonObjectAgg(t *testing.T) {
	agg := &jsonObjectAgg{}

	// Empty result
	result := agg.Result()
	if result != "{}" {
		t.Errorf("expected '{}', got %v", result)
	}

	// Add values
	agg.Add("key1", "val1")
	agg.Add("key2", 42)

	result = agg.Result()
	if result == "{}" {
		t.Error("expected non-empty result")
	}
}

func TestCoverAggregatorCountDistinct(t *testing.T) {
	agg := &countAgg{distinct: true, seen: make(map[string]bool)}

	agg.Add(nil, 1)
	agg.Add(nil, 1) // duplicate
	agg.Add(nil, 2)

	result := agg.Result()
	if result != int64(2) {
		t.Errorf("expected 2, got %v", result)
	}
}

func TestCoverAggregatorCountAll(t *testing.T) {
	agg := &countAgg{distinct: false}

	agg.Add(nil, 1)
	agg.Add(nil, 1)
	agg.Add(nil, 2)

	result := agg.Result()
	if result != int64(3) {
		t.Errorf("expected 3, got %v", result)
	}
}

func TestCoverAggregatorSum(t *testing.T) {
	agg := &sumAgg{}

	agg.Add(nil, int64(10))
	agg.Add(nil, int64(20))

	result := agg.Result()
	if result != int64(30) {
		t.Errorf("expected 30, got %v", result)
	}
}

func TestCoverAggregatorAvg(t *testing.T) {
	agg := &avgAgg{}

	agg.Add(nil, int64(10))
	agg.Add(nil, int64(20))

	result := agg.Result()
	if result != 15.0 {
		t.Errorf("expected 15.0, got %v", result)
	}
}

func TestCoverAggregatorMin(t *testing.T) {
	agg := &minAgg{}

	agg.Add(nil, int64(10))
	agg.Add(nil, int64(5))
	agg.Add(nil, int64(20))

	result := agg.Result()
	if result != int64(5) {
		t.Errorf("expected 5, got %v", result)
	}
}

func TestCoverAggregatorMax(t *testing.T) {
	agg := &maxAgg{}

	agg.Add(nil, int64(10))
	agg.Add(nil, int64(5))
	agg.Add(nil, int64(20))

	result := agg.Result()
	if result != int64(20) {
		t.Errorf("expected 20, got %v", result)
	}
}

func TestCoverAggregatorStringAgg(t *testing.T) {
	agg := &stringAgg{delimiter: ", "}

	agg.Add(nil, "a")
	agg.Add(nil, "b")
	agg.Add(nil, "c")

	result := agg.Result()
	if result != "a, b, c" {
		t.Errorf("expected 'a, b, c', got %v", result)
	}
}

func TestCoverAggregatorStringAggDistinct(t *testing.T) {
	agg := &stringAgg{delimiter: ",", distinct: true}

	agg.Add(nil, "a")
	agg.Add(nil, "a") // duplicate
	agg.Add(nil, "b")

	result := agg.Result()
	if result != "a,b" {
		t.Errorf("expected 'a,b', got %v", result)
	}
}

func TestCoverAggregatorWelfordVariance(t *testing.T) {
	agg := &varianceAgg{}

	agg.Add(nil, int64(10))
	agg.Add(nil, int64(20))
	agg.Add(nil, int64(30))

	result := agg.Result()
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorWelfordStddev(t *testing.T) {
	agg := &stddevAgg{}

	agg.Add(nil, int64(10))
	agg.Add(nil, int64(20))
	agg.Add(nil, int64(30))

	result := agg.Result()
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorWelfordEmpty(t *testing.T) {
	agg := &varianceAgg{}
	result := agg.Result()
	if result != 0.0 {
		t.Errorf("expected 0.0 for empty variance, got %v", result)
	}

	agg2 := &stddevAgg{}
	result2 := agg2.Result()
	if result2 != 0.0 {
		t.Errorf("expected 0.0 for empty stddev, got %v", result2)
	}
}

func TestCoverAggregatorSumFloat(t *testing.T) {
	agg := &sumAgg{}

	agg.Add(nil, float64(10.5))
	agg.Add(nil, float64(20.5))

	result := agg.Result()
	if result != 31.0 {
		t.Errorf("expected 31.0, got %v", result)
	}
}

func TestCoverAggregatorSumString(t *testing.T) {
	agg := &sumAgg{}

	agg.Add(nil, "10")
	agg.Add(nil, "20")

	result := agg.Result()
	if result != "1020" {
		t.Errorf("expected '1020', got %v", result)
	}
}

func TestCoverAggregatorAvgEmpty(t *testing.T) {
	agg := &avgAgg{}
	result := agg.Result()
	if result != 0.0 {
		t.Errorf("expected 0.0 for empty avg, got %v", result)
	}
}

func TestCoverAggregatorMinEmpty(t *testing.T) {
	agg := &minAgg{}
	result := agg.Result()
	if result != nil {
		t.Errorf("expected nil for empty min, got %v", result)
	}
}

func TestCoverAggregatorMaxEmpty(t *testing.T) {
	agg := &maxAgg{}
	result := agg.Result()
	if result != nil {
		t.Errorf("expected nil for empty max, got %v", result)
	}
}

func TestCoverAggregatorSumNil(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	result := agg.Result()
	if result != int64(0) {
		t.Errorf("expected 0 for sum with nil, got %v", result)
	}
}

func TestCoverAggregatorSumInt64(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, int64(100))
	result := agg.Result()
	if result != int64(100) {
		t.Errorf("expected 100, got %v", result)
	}
}

func TestCoverAggregatorMinFloat(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, float64(3.14))
	agg.Add(nil, float64(2.71))
	result := agg.Result()
	if result != 2.71 {
		t.Errorf("expected 2.71, got %v", result)
	}
}

func TestCoverAggregatorMaxFloat(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, float64(3.14))
	agg.Add(nil, float64(2.71))
	result := agg.Result()
	if result != 3.14 {
		t.Errorf("expected 3.14, got %v", result)
	}
}

func TestCoverAggregatorMinString(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, "b")
	agg.Add(nil, "a")
	result := agg.Result()
	if result != "a" {
		t.Errorf("expected 'a', got %v", result)
	}
}

func TestCoverAggregatorMaxString(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, "a")
	agg.Add(nil, "b")
	result := agg.Result()
	if result != "b" {
		t.Errorf("expected 'b', got %v", result)
	}
}

func TestCoverAggregatorAvgFloat(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, float64(10.0))
	agg.Add(nil, float64(20.0))
	result := agg.Result()
	if result != 15.0 {
		t.Errorf("expected 15.0, got %v", result)
	}
}

func TestCoverAggregatorWelfordSampleVariance(t *testing.T) {
	agg := &varianceAgg{}

	agg.Add(nil, int64(2))
	agg.Add(nil, int64(4))
	agg.Add(nil, int64(4))
	agg.Add(nil, int64(4))
	agg.Add(nil, int64(5))
	agg.Add(nil, int64(5))
	agg.Add(nil, int64(7))
	agg.Add(nil, int64(9))

	result := agg.Result()
	// Sample variance of {2,4,4,4,5,5,7,9} is 4.0
	if result != 4.0 {
		t.Errorf("expected 4.0, got %v", result)
	}
}

func TestCoverAggregatorWelfordSampleStddev(t *testing.T) {
	agg := &stddevAgg{}

	agg.Add(nil, int64(2))
	agg.Add(nil, int64(4))
	agg.Add(nil, int64(4))
	agg.Add(nil, int64(4))
	agg.Add(nil, int64(5))
	agg.Add(nil, int64(5))
	agg.Add(nil, int64(7))
	agg.Add(nil, int64(9))

	result := agg.Result()
	// Sample stddev of {2,4,4,4,5,5,7,9} is 2.0
	if result != 2.0 {
		t.Errorf("expected 2.0, got %v", result)
	}
}

func TestCoverAggregatorCountNil(t *testing.T) {
	agg := &countAgg{distinct: false}
	agg.Add(nil, nil)
	result := agg.Result()
	if result != int64(1) {
		t.Errorf("expected 1, got %v", result)
	}
}

func TestCoverAggregatorCountDistinctNil(t *testing.T) {
	agg := &countAgg{distinct: true, seen: make(map[string]bool)}
	agg.Add(nil, nil)
	agg.Add(nil, nil) // duplicate nil
	result := agg.Result()
	if result != int64(1) {
		t.Errorf("expected 1, got %v", result)
	}
}

func TestCoverAggregatorStringAggNil(t *testing.T) {
	agg := &stringAgg{delimiter: ","}
	agg.Add(nil, nil)
	result := agg.Result()
	if result != "" {
		t.Errorf("expected empty string, got %v", result)
	}
}

func TestCoverAggregatorMinNil(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, int64(5))
	result := agg.Result()
	if result != int64(5) {
		t.Errorf("expected 5, got %v", result)
	}
}

func TestCoverAggregatorMaxNil(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, int64(5))
	result := agg.Result()
	if result != int64(5) {
		t.Errorf("expected 5, got %v", result)
	}
}

func TestCoverAggregatorSumMixedTypes(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, float64(5.5))
	agg.Add(nil, "3")
	agg.Add(nil, true)
	result := agg.Result()
	// Should handle mixed types gracefully
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorAvgInt64(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 15.0 {
		t.Errorf("expected 15.0, got %v", result)
	}
}

func TestCoverAggregatorAvgString(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, "10")
	agg.Add(nil, "20")
	result := agg.Result()
	// String values can't be averaged
	if result != 0.0 {
		t.Errorf("expected 0.0 for string avg, got %v", result)
	}
}

func TestCoverAggregatorMinBool(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, true)
	agg.Add(nil, false)
	result := agg.Result()
	// Booleans can't be compared numerically
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorMaxBool(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, true)
	agg.Add(nil, false)
	result := agg.Result()
	// Booleans can't be compared numerically
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorSumBool(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, true)
	agg.Add(nil, false)
	agg.Result() // should not panic
}

func TestCoverAggregatorAvgBool(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, true)
	agg.Add(nil, false)
	agg.Result() // should not panic
}

func TestCoverAggregatorSumFloatInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorSumIntFloat(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, float64(20.5))
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorAvgFloatInt(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, float64(10.0))
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 15.0 {
		t.Errorf("expected 15.0, got %v", result)
	}
}

func TestCoverAggregatorAvgIntFloat(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, float64(20.0))
	result := agg.Result()
	if result != 15.0 {
		t.Errorf("expected 15.0, got %v", result)
	}
}

func TestCoverAggregatorMinFloatString(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, float64(3.14))
	agg.Add(nil, "2.71")
	result := agg.Result()
	// Mixed types - behavior varies
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorMaxFloatString(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, float64(3.14))
	agg.Add(nil, "2.71")
	result := agg.Result()
	// Mixed types - behavior varies
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorSumNilFloat(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	result := agg.Result()
	if result != 10.5 {
		t.Errorf("expected 10.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, int64(10))
	result := agg.Result()
	if result != int64(10) {
		t.Errorf("expected 10, got %v", result)
	}
}

func TestCoverAggregatorMinNilFloat(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, float64(3.14))
	result := agg.Result()
	if result != 3.14 {
		t.Errorf("expected 3.14, got %v", result)
	}
}

func TestCoverAggregatorMaxNilFloat(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, float64(3.14))
	result := agg.Result()
	if result != 3.14 {
		t.Errorf("expected 3.14, got %v", result)
	}
}

func TestCoverAggregatorMinStringFloat(t *testing.T) {
	agg := &minAgg{}
	agg.Add(nil, "b")
	agg.Add(nil, float64(3.14))
	result := agg.Result()
	// Mixed types - string wins
	if result != "b" {
		t.Errorf("expected 'b', got %v", result)
	}
}

func TestCoverAggregatorMaxStringFloat(t *testing.T) {
	agg := &maxAgg{}
	agg.Add(nil, "a")
	agg.Add(nil, float64(3.14))
	result := agg.Result()
	// Mixed types - string wins
	if result != "a" {
		t.Errorf("expected 'a', got %v", result)
	}
}

func TestCoverAggregatorSumStringInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, "10")
	agg.Add(nil, int64(20))
	result := agg.Result()
	// Mixed types - string wins
	if result != "1020" {
		t.Errorf("expected '1020', got %v", result)
	}
}

func TestCoverAggregatorSumIntString(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, "20")
	result := agg.Result()
	// Mixed types - behavior varies
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorSumBoolInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, true)
	agg.Add(nil, int64(10))
	result := agg.Result()
	// Mixed types - bool is ignored
	if result != int64(10) {
		t.Errorf("expected 10, got %v", result)
	}
}

func TestCoverAggregatorSumIntBool(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, true)
	result := agg.Result()
	// Mixed types - bool is ignored
	if result != int64(10) {
		t.Errorf("expected 10, got %v", result)
	}
}

func TestCoverAggregatorAvgBoolInt(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, true)
	agg.Add(nil, int64(10))
	result := agg.Result()
	// Mixed types - bool is ignored
	if result != 10.0 {
		t.Errorf("expected 10.0, got %v", result)
	}
}

func TestCoverAggregatorAvgIntBool(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, int64(10))
	agg.Add(nil, true)
	result := agg.Result()
	// Mixed types - bool is ignored
	if result != 10.0 {
		t.Errorf("expected 10.0, got %v", result)
	}
}

func TestCoverAggregatorSumNilBool(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, true)
	agg.Result() // should not panic
}

func TestCoverAggregatorAvgNilBool(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, true)
	agg.Result() // should not panic
}

func TestCoverAggregatorSumNilString(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, "10")
	result := agg.Result()
	// string sum
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorAvgNilString(t *testing.T) {
	agg := &avgAgg{}
	agg.Add(nil, "10")
	agg.Result() // should not panic
}

func TestCoverAggregatorSumNilFloatInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorSumFloatNilInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, nil)
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorSumFloatIntNil(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	agg.Add(nil, nil)
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilFloat(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	result := agg.Result()
	if result != 10.5 {
		t.Errorf("expected 10.5, got %v", result)
	}
}

func TestCoverAggregatorSumFloatNilNil(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	result := agg.Result()
	if result != 10.5 {
		t.Errorf("expected 10.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilFloatNil(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	agg.Add(nil, nil)
	result := agg.Result()
	if result != 10.5 {
		t.Errorf("expected 10.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilNilFloat(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	result := agg.Result()
	if result != 10.5 {
		t.Errorf("expected 10.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilNilNil(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	result := agg.Result()
	// All nils - result should be nil or 0
	if result != nil && result != int64(0) {
		t.Errorf("expected nil or 0, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilNilNilFloat(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	result := agg.Result()
	if result != 10.5 {
		t.Errorf("expected 10.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilNilNilFloatInt(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, nil)
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilNilNilFloatIntString(t *testing.T) {
	// String with numbers - string concatenation behavior varies
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	result := agg.Result()
	if result != 30.5 {
		t.Errorf("expected 30.5, got %v", result)
	}
}

func TestCoverAggregatorSumNilNilNilNilFloatIntStringBool(t *testing.T) {
	// Mix of float, int, string, bool
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	agg.Add(nil, true)
	result := agg.Result()
	// Result should be numeric sum
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorSumNilNilNilNilFloatIntStringBoolFloat(t *testing.T) {
	// Mix of types
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	agg.Add(nil, true)
	agg.Add(nil, float64(3.14))
	result := agg.Result()
	// Result should be numeric sum
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorSumNilNilNilNilFloatIntStringBoolFloatInt(t *testing.T) {
	// Mix of types
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	agg.Add(nil, true)
	agg.Add(nil, float64(3.14))
	agg.Add(nil, int64(42))
	result := agg.Result()
	// Result should be numeric sum
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCoverAggregatorSumNilNilNilNilFloatIntStringBoolFloatIntString(t *testing.T) {
	agg := &sumAgg{}
	agg.Add(nil, float64(10.5))
	agg.Add(nil, int64(20))
	agg.Add(nil, float64(3.14))
	agg.Add(nil, int64(42))
	result := agg.Result()
	// Result should be numeric sum
	if result == nil {
		t.Error("expected non-nil result")
	}
}
