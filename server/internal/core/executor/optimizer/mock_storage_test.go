package optimizer

import (
	"strings"
	"vaultdb/internal/core/index"
	"vaultdb/internal/core/storage"
)

// mockStorage is a minimal mock implementing storage.StorageEngine for optimizer tests.
type mockStorage struct {
	databases map[string]bool
	tables    map[string]map[string]*storage.TableSchema
	rows      map[string]map[string][]storage.Row
	indexes   map[string]map[string]map[string][]int
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		databases: make(map[string]bool),
		tables:    make(map[string]map[string]*storage.TableSchema),
		rows:      make(map[string]map[string][]storage.Row),
		indexes:   make(map[string]map[string]map[string][]int),
	}
}

func (m *mockStorage) ensureDB(dbName string) {
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

func (m *mockStorage) CreateDatabase(name string) error {
	m.databases[name] = true
	m.ensureDB(name)
	return nil
}

func (m *mockStorage) DropDatabase(name string) error {
	delete(m.databases, name)
	delete(m.tables, name)
	delete(m.rows, name)
	delete(m.indexes, name)
	return nil
}

func (m *mockStorage) FinalCheckpoint() error { return nil }
func (m *mockStorage) Close() error           { return nil }
func (m *mockStorage) DataDir() string        { return "" }

// ReadOnlyEngine

func (m *mockStorage) DatabaseExists(name string) bool {
	return m.databases[name]
}

func (m *mockStorage) ListDatabases() ([]string, error) {
	var dbs []string
	for db := range m.databases {
		dbs = append(dbs, db)
	}
	return dbs, nil
}

func (m *mockStorage) TableExists(dbName, tableName string) bool {
	if m.tables[dbName] == nil {
		return false
	}
	return m.tables[dbName][tableName] != nil
}

func (m *mockStorage) ListTables(dbName string) ([]storage.TableInfo, error) {
	return nil, nil
}

func (m *mockStorage) GetTableSchema(dbName, tableName string) (*storage.TableSchema, error) {
	if m.tables[dbName] == nil {
		return nil, nil
	}
	schema := m.tables[dbName][tableName]
	if schema == nil {
		return nil, nil
	}
	return schema, nil
}

func (m *mockStorage) SelectRows(dbName, tableName string) ([]storage.Row, error) {
	return m.rows[dbName][tableName], nil
}

func (m *mockStorage) SelectRowsVM(dbName, tableName string, predicate func(rawTuple []byte) (bool, error)) ([]storage.Row, error) {
	return m.SelectRows(dbName, tableName)
}

func (m *mockStorage) ReadCurrentRows(dbName, tableName string) ([]storage.Row, error) {
	return m.rows[dbName][tableName], nil
}

func (m *mockStorage) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]storage.Row, error) {
	return m.rows[dbName][tableName], nil
}

func (m *mockStorage) ReadRowsByPositions(dbName, tableName string, positions []int) ([]storage.Row, error) {
	return nil, nil
}

func (m *mockStorage) CountRows(dbName, tableName string) (int, error) {
	return len(m.rows[dbName][tableName]), nil
}

func (m *mockStorage) TxIDAtTimestamp(dbName, ts string) (uint64, error) {
	return 0, nil
}

func (m *mockStorage) RowHistory(dbName, tableName string, pkValue interface{}) ([]storage.VersionedRow, error) {
	return nil, nil
}

func (m *mockStorage) AllRowHistory(dbName, tableName string) ([]storage.VersionedRow, error) {
	return nil, nil
}

func (m *mockStorage) TableVersionStats(dbName, tableName string) (*storage.TableVersionStats, error) {
	return nil, nil
}

func (m *mockStorage) TableModifiedSince(db, table string, txID uint64) (bool, error) {
	return false, nil
}

func (m *mockStorage) CurrentTxID() uint64   { return 0 }
func (m *mockStorage) SchemaVersion() uint64 { return 0 }

func (m *mockStorage) ListIndexes(dbName, tableName string) ([]string, error) {
	if m.indexes[dbName] == nil {
		return nil, nil
	}
	var idxNames []string
	for idx := range m.indexes[dbName][tableName] {
		idxNames = append(idxNames, idx)
	}
	return idxNames, nil
}

func (m *mockStorage) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	if m.indexes[dbName] == nil || m.indexes[dbName][tableName] == nil {
		return "", false
	}
	for idxName := range m.indexes[dbName][tableName] {
		if idxName == column || idxName == "idx_"+column || strings.Contains(idxName, column) {
			return idxName, true
		}
	}
	return "", false
}

func (m *mockStorage) GetIndex(dbName, tableName, indexName string) (index.Index, bool) {
	return nil, false
}

func (m *mockStorage) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	return nil, false
}

func (m *mockStorage) IndexRangeLookup(dbName, tableName, column, low, high string) ([]int, bool) {
	return nil, false
}

func (m *mockStorage) IndexFTSLookup(dbName, tableName, column, query string) ([]int, bool) {
	return nil, false
}

func (m *mockStorage) ReadSampleRows(dbName, tableName string, limit int) ([]storage.Row, error) {
	rows := m.rows[dbName][tableName]
	if len(rows) > limit {
		return rows[:limit], nil
	}
	return rows, nil
}

// WriteEngine

func (m *mockStorage) CreateTable(dbName string, schema storage.TableSchema) error {
	m.ensureDB(dbName)
	m.tables[dbName][schema.Name] = &schema
	return nil
}

func (m *mockStorage) DropTable(dbName, tableName string) error {
	if m.tables[dbName] != nil {
		delete(m.tables[dbName], tableName)
	}
	return nil
}

func (m *mockStorage) InsertRows(dbName, tableName string, rows []storage.Row) (int, error) {
	m.ensureDB(dbName)
	m.rows[dbName][tableName] = append(m.rows[dbName][tableName], rows...)
	return len(rows), nil
}

func (m *mockStorage) UpdateRows(dbName, tableName string, indices []int, updates map[string]storage.Value) (int, error) {
	return 0, nil
}

func (m *mockStorage) UpdateRowsDirect(dbName, tableName string, indices []int, newValues []storage.Row) (int, error) {
	return 0, nil
}

func (m *mockStorage) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	return 0, nil
}

func (m *mockStorage) TruncateTable(dbName, tableName string) error {
	m.ensureDB(dbName)
	m.rows[dbName][tableName] = nil
	return nil
}

func (m *mockStorage) Vacuum(dbName, tableName string) (*storage.VacuumStats, error) {
	return nil, nil
}

func (m *mockStorage) AlterTableAddColumn(dbName, tableName string, col storage.ColumnSchema, defaultVal storage.Value) error {
	return nil
}

func (m *mockStorage) AlterTableDropColumn(dbName, tableName string, colName string) error {
	return nil
}

func (m *mockStorage) AlterTableRenameColumn(dbName, tableName, oldName, newName string) error {
	return nil
}

func (m *mockStorage) SetTableRLS(dbName, tableName string, enabled bool) error {
	return nil
}

func (m *mockStorage) AddPolicy(dbName, tableName string, policy storage.RLSPolicy) error {
	return nil
}

func (m *mockStorage) AlterTableRenameTable(dbName, oldName, newName string) error {
	return nil
}

func (m *mockStorage) CreateIndex(dbName, tableName, indexName, column, indexType string) error {
	return nil
}

func (m *mockStorage) CreateIndexMulti(dbName, tableName, indexName string, columns []string) error {
	return nil
}

func (m *mockStorage) CreateIndexUnique(dbName, tableName, indexName, column, indexType string) error {
	return nil
}

func (m *mockStorage) CreateIndexMultiUnique(dbName, tableName, indexName string, columns []string) error {
	return nil
}

func (m *mockStorage) DropIndex(dbName, indexName string) error {
	return nil
}
