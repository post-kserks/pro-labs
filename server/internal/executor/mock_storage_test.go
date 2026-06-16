package executor

import (
	"fmt"
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

type MockStorage struct {
	databases map[string]bool
	tables    map[string]map[string]*storage.TableSchema
	rows      map[string]map[string][]storage.Row
	indexes   map[string]map[string]map[string][]int

	createDBErr  error
	dropDBErr    error
	createTblErr error
	insertErr    error
	selectErr    error
	deleteErr    error
	updateErr    error
}

func NewMockStorage() *MockStorage {
	return &MockStorage{
		databases: make(map[string]bool),
		tables:    make(map[string]map[string]*storage.TableSchema),
		rows:      make(map[string]map[string][]storage.Row),
		indexes:   make(map[string]map[string]map[string][]int),
	}
}

func (m *MockStorage) ensureDB(dbName string) {
	if m.tables[dbName] == nil {
		m.tables[dbName] = make(map[string]*storage.TableSchema)
	}
	if m.rows[dbName] == nil {
		m.rows[dbName] = make(map[string][]storage.Row)
	}
	if m.indexes[dbName] == nil {
		m.indexes[dbName] = make(map[string]map[string][]int)
	}
}

// AdminEngine

func (m *MockStorage) CreateDatabase(name string) error {
	if m.createDBErr != nil {
		return m.createDBErr
	}
	m.databases[name] = true
	m.ensureDB(name)
	return nil
}

func (m *MockStorage) DropDatabase(name string) error {
	if m.dropDBErr != nil {
		return m.dropDBErr
	}
	delete(m.databases, name)
	delete(m.tables, name)
	delete(m.rows, name)
	delete(m.indexes, name)
	return nil
}

func (m *MockStorage) FinalCheckpoint() error { return nil }
func (m *MockStorage) Close() error           { return nil }

// ReadOnlyEngine

func (m *MockStorage) DatabaseExists(name string) bool {
	return m.databases[name]
}

func (m *MockStorage) ListDatabases() ([]string, error) {
	var names []string
	for name := range m.databases {
		names = append(names, name)
	}
	return names, nil
}

func (m *MockStorage) TableExists(dbName, tableName string) bool {
	db := m.tables[dbName]
	if db == nil {
		return false
	}
	_, ok := db[tableName]
	return ok
}

func (m *MockStorage) ListTables(dbName string) ([]storage.TableInfo, error) {
	db := m.tables[dbName]
	if db == nil {
		return nil, nil
	}
	var infos []storage.TableInfo
	for name, schema := range db {
		rowCount := len(m.rows[dbName][name])
		infos = append(infos, storage.TableInfo{
			Name:      name,
			RowCount:  rowCount,
			CreatedAt: schema.CreatedAt,
		})
	}
	return infos, nil
}

func (m *MockStorage) GetTableSchema(dbName, tableName string) (*storage.TableSchema, error) {
	db := m.tables[dbName]
	if db == nil {
		return nil, fmt.Errorf("database '%s' not found", dbName)
	}
	schema, ok := db[tableName]
	if !ok {
		return nil, fmt.Errorf("table '%s' not found in '%s'", tableName, dbName)
	}
	return schema, nil
}

func (m *MockStorage) SelectRows(dbName, tableName string) ([]storage.Row, error) {
	if m.selectErr != nil {
		return nil, m.selectErr
	}
	return m.rows[dbName][tableName], nil
}

func (m *MockStorage) ReadCurrentRows(dbName, tableName string) ([]storage.Row, error) {
	if m.selectErr != nil {
		return nil, m.selectErr
	}
	return m.rows[dbName][tableName], nil
}

func (m *MockStorage) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]storage.Row, error) {
	if m.selectErr != nil {
		return nil, m.selectErr
	}
	return m.rows[dbName][tableName], nil
}

func (m *MockStorage) ReadRowsByPositions(dbName, tableName string, positions []int) ([]storage.Row, error) {
	if m.selectErr != nil {
		return nil, m.selectErr
	}
	allRows := m.rows[dbName][tableName]
	var result []storage.Row
	for _, pos := range positions {
		if pos < len(allRows) {
			result = append(result, allRows[pos])
		}
	}
	return result, nil
}

func (m *MockStorage) CountRows(dbName, tableName string) (int, error) {
	return len(m.rows[dbName][tableName]), nil
}

func (m *MockStorage) TxIDAtTimestamp(dbName, ts string) (uint64, error) {
	return 1, nil
}

func (m *MockStorage) RowHistory(dbName, tableName string, pkValue interface{}) ([]storage.VersionedRow, error) {
	return nil, nil
}

func (m *MockStorage) TableVersionStats(dbName, tableName string) (*storage.TableVersionStats, error) {
	return &storage.TableVersionStats{TotalRows: len(m.rows[dbName][tableName])}, nil
}

func (m *MockStorage) TableModifiedSince(db, table string, txID uint64) (bool, error) {
	return false, nil
}

func (m *MockStorage) CurrentTxID() uint64 { return 1 }

func (m *MockStorage) ListIndexes(dbName, tableName string) ([]string, error) {
	var names []string
	dbIdx := m.indexes[dbName]
	if dbIdx == nil {
		return names, nil
	}
	tblIdx := dbIdx[tableName]
	for name := range tblIdx {
		names = append(names, name)
	}
	return names, nil
}

func (m *MockStorage) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	dbIdx := m.indexes[dbName]
	if dbIdx == nil {
		return "", false
	}
	tblIdx := dbIdx[tableName]
	for name, positions := range tblIdx {
		if name == column && len(positions) > 0 {
			return name, true
		}
	}
	return "", false
}

func (m *MockStorage) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	dbIdx := m.indexes[dbName]
	if dbIdx == nil {
		return nil, false
	}
	tblIdx := dbIdx[tableName]
	if tblIdx == nil {
		return nil, false
	}
	idx := tblIdx[column]
	if idx == nil {
		return nil, false
	}
	for _, pos := range idx {
		if pos < len(m.rows[dbName][tableName]) {
			return []int{pos}, true
		}
	}
	return nil, false
}

func (m *MockStorage) IndexRangeLookup(dbName, tableName, column, low, high string) ([]int, bool) {
	return m.IndexLookup(dbName, tableName, column, low)
}

func (m *MockStorage) IndexFTSLookup(dbName, tableName, column, query string) ([]int, bool) {
	return m.IndexLookup(dbName, tableName, column, query)
}

// WriteEngine

func (m *MockStorage) CreateTable(dbName string, schema storage.TableSchema) error {
	if m.createTblErr != nil {
		return m.createTblErr
	}
	m.ensureDB(dbName)
	s := schema
	m.tables[dbName][schema.Name] = &s
	return nil
}

func (m *MockStorage) DropTable(dbName, tableName string) error {
	db := m.tables[dbName]
	if db != nil {
		delete(db, tableName)
	}
	dbRows := m.rows[dbName]
	if dbRows != nil {
		delete(dbRows, tableName)
	}
	dbIdx := m.indexes[dbName]
	if dbIdx != nil {
		delete(dbIdx, tableName)
	}
	return nil
}

func (m *MockStorage) InsertRows(dbName, tableName string, rows []storage.Row) (int, error) {
	if m.insertErr != nil {
		return 0, m.insertErr
	}
	m.ensureDB(dbName)
	m.rows[dbName][tableName] = append(m.rows[dbName][tableName], rows...)
	return len(rows), nil
}

func (m *MockStorage) UpdateRows(dbName, tableName string, indices []int, updates map[string]storage.Value) (int, error) {
	if m.updateErr != nil {
		return 0, m.updateErr
	}
	schema := m.tables[dbName][tableName]
	if schema == nil {
		return 0, fmt.Errorf("table '%s' not found", tableName)
	}

	colIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		colIndex[col.Name] = i
	}

	for _, idx := range indices {
		if idx < len(m.rows[dbName][tableName]) {
			row := m.rows[dbName][tableName][idx]
			for colName, val := range updates {
				if colIdx, ok := colIndex[colName]; ok && colIdx < len(row) {
					row[colIdx] = val
				}
			}
			m.rows[dbName][tableName][idx] = row
		}
	}
	return len(indices), nil
}

func (m *MockStorage) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	if m.deleteErr != nil {
		return 0, m.deleteErr
	}
	rows := m.rows[dbName][tableName]
	if rows == nil {
		return 0, nil
	}

	indexSet := make(map[int]bool, len(indices))
	for _, idx := range indices {
		indexSet[idx] = true
	}

	var remaining []storage.Row
	for i, row := range rows {
		if !indexSet[i] {
			remaining = append(remaining, row)
		}
	}
	m.rows[dbName][tableName] = remaining
	return len(indices), nil
}

func (m *MockStorage) Vacuum(dbName, tableName string) (*storage.VacuumStats, error) {
	return &storage.VacuumStats{
		TableName:  tableName,
		RowsBefore: len(m.rows[dbName][tableName]),
		RowsAfter:  len(m.rows[dbName][tableName]),
	}, nil
}

func (m *MockStorage) AlterTableAddColumn(dbName, tableName string, col storage.ColumnSchema, defaultVal storage.Value) error {
	schema := m.tables[dbName][tableName]
	if schema == nil {
		return fmt.Errorf("table '%s' not found", tableName)
	}
	schema.Columns = append(schema.Columns, col)
	for i := range m.rows[dbName][tableName] {
		m.rows[dbName][tableName][i] = append(m.rows[dbName][tableName][i], defaultVal)
	}
	return nil
}

func (m *MockStorage) AlterTableDropColumn(dbName, tableName, colName string) error {
	schema := m.tables[dbName][tableName]
	if schema == nil {
		return fmt.Errorf("table '%s' not found", tableName)
	}
	idx := -1
	for i, col := range schema.Columns {
		if col.Name == colName {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("column '%s' not found", colName)
	}
	schema.Columns = append(schema.Columns[:idx], schema.Columns[idx+1:]...)
	return nil
}

func (m *MockStorage) AlterTableRenameColumn(dbName, tableName, oldName, newName string) error {
	schema := m.tables[dbName][tableName]
	if schema == nil {
		return fmt.Errorf("table '%s' not found", tableName)
	}
	for i, col := range schema.Columns {
		if col.Name == oldName {
			schema.Columns[i].Name = newName
			return nil
		}
	}
	return fmt.Errorf("column '%s' not found", oldName)
}

func (m *MockStorage) AlterTableRenameTable(dbName, oldName, newName string) error {
	db := m.tables[dbName]
	if db == nil {
		return fmt.Errorf("database '%s' not found", dbName)
	}
	schema, ok := db[oldName]
	if !ok {
		return fmt.Errorf("table '%s' not found", oldName)
	}
	schema.Name = newName
	db[newName] = schema
	delete(db, oldName)
	dbRows := m.rows[dbName]
	if dbRows != nil {
		dbRows[newName] = dbRows[oldName]
		delete(dbRows, oldName)
	}
	return nil
}

func (m *MockStorage) CreateIndex(dbName, tableName, indexName, column string) error {
	m.ensureDB(dbName)
	if m.indexes[dbName][tableName] == nil {
		m.indexes[dbName][tableName] = make(map[string][]int)
	}
	m.indexes[dbName][tableName][column] = []int{0}
	return nil
}

func (m *MockStorage) CreateIndexMulti(dbName, tableName, indexName string, columns []string) error {
	return m.CreateIndex(dbName, tableName, indexName, columns[0])
}

func (m *MockStorage) DropIndex(dbName, indexName string) error {
	dbIdx := m.indexes[dbName]
	if dbIdx == nil {
		return fmt.Errorf("index '%s' not found", indexName)
	}
	for _, tblIdx := range dbIdx {
		for col, positions := range tblIdx {
			if len(positions) > 0 {
				delete(tblIdx, col)
				return nil
			}
		}
	}
	return fmt.Errorf("index '%s' not found", indexName)
}

func newTestSession(store storage.StorageEngine) *Session {
	return NewSession(store, nil, nil, nil)
}

func TestCreateDatabaseCommand(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := NewMockStorage()
		currentDB := ""
		ctx := &ExecutionContext{
			Storage:   store,
			CurrentDB: &currentDB,
		}
		stmt := &parser.CreateDatabaseStatement{DatabaseName: "testdb"}
		cmd := &CreateDatabaseCommand{stmt: stmt}

		result, err := cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "message" {
			t.Fatalf("expected message result, got %s", result.Type)
		}
		if !store.databases["testdb"] {
			t.Fatal("database 'testdb' was not created")
		}
	})

	t.Run("error", func(t *testing.T) {
		store := NewMockStorage()
		store.createDBErr = fmt.Errorf("disk full")
		currentDB := ""
		ctx := &ExecutionContext{
			Storage:   store,
			CurrentDB: &currentDB,
		}
		stmt := &parser.CreateDatabaseStatement{DatabaseName: "testdb"}
		cmd := &CreateDatabaseCommand{stmt: stmt}

		_, err := cmd.Execute(ctx)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "disk full" {
			t.Fatalf("expected 'disk full', got %v", err)
		}
	})
}

func TestInsertCommand(t *testing.T) {
	store := NewMockStorage()
	currentDB := "mydb"
	store.databases["mydb"] = true
	store.ensureDB("mydb")
	store.tables["mydb"]["users"] = &storage.TableSchema{
		Name: "users",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}

	session := newTestSession(store)
	ctx := &ExecutionContext{
		Storage:   store,
		CurrentDB: &currentDB,
		Session:   session,
	}

	stmt := &parser.InsertStatement{
		TableName: "users",
		Columns:   []string{"id", "name"},
		Rows: [][]parser.Expression{
			{
				&parser.Value{Type: "int", IntVal: 1},
				&parser.Value{Type: "string", StrVal: "Alice"},
			},
			{
				&parser.Value{Type: "int", IntVal: 2},
				&parser.Value{Type: "string", StrVal: "Bob"},
			},
		},
	}

	cmd := &InsertCommand{stmt: stmt}
	result, err := cmd.Execute(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != "affected" {
		t.Fatalf("expected 'affected' result, got %s", result.Type)
	}
	if result.Affected != 2 {
		t.Fatalf("expected 2 affected rows, got %d", result.Affected)
	}
	if len(store.rows["mydb"]["users"]) != 2 {
		t.Fatalf("expected 2 rows in storage, got %d", len(store.rows["mydb"]["users"]))
	}
}

func TestSelectCommand(t *testing.T) {
	store := NewMockStorage()
	currentDB := "mydb"
	store.databases["mydb"] = true
	store.ensureDB("mydb")
	store.tables["mydb"]["items"] = &storage.TableSchema{
		Name: "items",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "name", Type: "TEXT"},
		},
	}
	store.rows["mydb"]["items"] = []storage.Row{
		{int64(1), "Widget"},
		{int64(2), "Gadget"},
		{int64(3), "Doohickey"},
	}

	session := newTestSession(store)
	ctx := &ExecutionContext{
		Storage:   store,
		CurrentDB: &currentDB,
		Session:   session,
	}

	t.Run("select all", func(t *testing.T) {
		stmt := &parser.SelectStatement{
			TableName: "items",
			Columns: []parser.SelectColumn{
				{Expr: &parser.ColumnRef{Name: "id"}},
				{Expr: &parser.ColumnRef{Name: "name"}},
			},
		}
		cmd := &SelectCommand{stmt: stmt}
		result, err := cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Type != "rows" {
			t.Fatalf("expected 'rows', got %s", result.Type)
		}
		if len(result.Rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(result.Rows))
		}
	})

	t.Run("select with where", func(t *testing.T) {
		stmt := &parser.SelectStatement{
			TableName: "items",
			Columns: []parser.SelectColumn{
				{Expr: &parser.ColumnRef{Name: "id"}},
				{Expr: &parser.ColumnRef{Name: "name"}},
			},
			Where: &parser.BinaryExpr{
				Left:     &parser.ColumnRef{Name: "id"},
				Operator: "=",
				Right:    parser.Value{Type: "int", IntVal: 2},
			},
		}
		cmd := &SelectCommand{stmt: stmt}
		result, err := cmd.Execute(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(result.Rows))
		}
		if result.Rows[0][1] != "Gadget" {
			t.Fatalf("expected 'Gadget', got %s", result.Rows[0][1])
		}
	})
}

func TestCommandFactory(t *testing.T) {
	stmts := []struct {
		name string
		stmt parser.Statement
	}{
		{"CreateDatabase", &parser.CreateDatabaseStatement{DatabaseName: "x"}},
		{"DropDatabase", &parser.DropDatabaseStatement{DatabaseName: "x"}},
		{"AlterTable", &parser.AlterTableStatement{TableName: "x", Action: &parser.AlterAddColumn{Column: parser.ColumnDef{Name: "c", DataType: "INT"}}}},
		{"UseDatabase", &parser.UseDatabaseStatement{DatabaseName: "x"}},
		{"ShowDatabases", &parser.ShowDatabasesStatement{}},
		{"ShowTables", &parser.ShowTablesStatement{}},
		{"DescribeTable", &parser.DescribeTableStatement{TableName: "x"}},
		{"CreateTable", &parser.CreateTableStatement{TableName: "x"}},
		{"DropTable", &parser.DropTableStatement{TableName: "x"}},
		{"Select", &parser.SelectStatement{TableName: "x"}},
		{"Explain", &parser.ExplainStatement{Inner: &parser.SelectStatement{TableName: "x"}}},
		{"History", &parser.HistoryStatement{TableName: "x"}},
		{"Insert", &parser.InsertStatement{TableName: "x"}},
		{"Update", &parser.UpdateStatement{TableName: "x"}},
		{"Delete", &parser.DeleteStatement{TableName: "x"}},
		{"Vacuum", &parser.VacuumStatement{}},
		{"CreateIndex", &parser.CreateIndexStatement{TableName: "x", IndexName: "idx", Column: "c"}},
		{"DropIndex", &parser.DropIndexStatement{IndexName: "idx"}},
		{"ShowIndexes", &parser.ShowIndexesStatement{TableName: "x"}},
		{"Begin", &parser.BeginStatement{}},
		{"Commit", &parser.CommitStatement{}},
		{"Rollback", &parser.RollbackStatement{}},
		{"Prepare", &parser.PrepareStatement{Name: "ps"}},
		{"Execute", &parser.ExecuteStatement{Name: "ps"}},
		{"Deallocate", &parser.DeallocateStatement{Name: "ps"}},
		{"Migration", &parser.MigrationStatement{Op: "CREATE", Name: "m1"}},
		{"CreatePolicy", &parser.CreatePolicyStatement{Name: "p1", TableName: "x"}},
		{"EnableRls", &parser.EnableRlsStatement{TableName: "x"}},
		{"Truncate", &parser.TruncateStatement{TableName: "x"}},
		{"Savepoint", &parser.SavepointStatement{Name: "sp"}},
		{"RollbackToSavepoint", &parser.RollbackToSavepointStatement{Name: "sp"}},
		{"ReleaseSavepoint", &parser.ReleaseSavepointStatement{Name: "sp"}},
		{"CreateView", &parser.CreateViewStatement{Name: "v", Query: &parser.SelectStatement{TableName: "x"}}},
		{"DropView", &parser.DropViewStatement{Name: "v"}},
		{"CreateTrigger", &parser.CreateTriggerStatement{Name: "t", TableName: "x"}},
		{"DropTrigger", &parser.DropTriggerStatement{Name: "t"}},
		{"CreateFunction", &parser.CreateFunctionStatement{Name: "f"}},
		{"DropFunction", &parser.DropFunctionStatement{Name: "f"}},
		{"CreateProcedure", &parser.CreateProcedureStatement{Name: "p"}},
		{"DropProcedure", &parser.DropProcedureStatement{Name: "p"}},
		{"CallProcedure", &parser.CallProcedureStatement{Name: "p"}},
	}

	for _, s := range stmts {
		t.Run(s.name, func(t *testing.T) {
			cmd, err := CommandFactory(s.stmt)
			if err != nil {
				t.Fatalf("CommandFactory failed for %s: %v", s.name, err)
			}
			if cmd == nil {
				t.Fatalf("CommandFactory returned nil for %s", s.name)
			}
		})
	}
}

func TestCommandFactorySetOperation(t *testing.T) {
	stmt := &parser.SetOperationStatement{
		Op:    "UNION",
		Left:  &parser.SelectStatement{TableName: "a"},
		Right: &parser.SelectStatement{TableName: "b"},
	}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
}

func TestCommandFactoryCTE(t *testing.T) {
	stmt := &parser.CTEStatement{
		CTEs: []parser.CTEDefinition{
			{
				Name:  "cte",
				Query: &parser.SelectStatement{TableName: "t"},
			},
		},
		Body: &parser.SelectStatement{TableName: "cte"},
	}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
}

func TestCommandFactoryMerge(t *testing.T) {
	stmt := &parser.MergeStatement{
		TargetTable: "target",
		SourceTable: "source",
		OnCondition: &parser.BinaryExpr{
			Left:     &parser.ColumnRef{Name: "id"},
			Operator: "=",
			Right:    parser.Value{Type: "int", IntVal: 1},
		},
	}
	cmd, err := CommandFactory(stmt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil command")
	}
}

func TestCommandFactoryUnknown(t *testing.T) {
	_, err := CommandFactory(nil)
	if err == nil {
		t.Fatal("expected error for nil statement")
	}
}
