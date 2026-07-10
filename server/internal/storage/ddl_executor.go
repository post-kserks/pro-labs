package storage

// DDLExecutor handles Data Definition Language operations (CREATE TABLE, DROP TABLE).
// It delegates to the underlying PageStorageEngine for the actual execution.
type DDLExecutor struct {
	engine *PageStorageEngine
}

// NewDDLExecutor creates a new DDLExecutor bound to the given engine.
func NewDDLExecutor(engine *PageStorageEngine) *DDLExecutor {
	return &DDLExecutor{engine: engine}
}

// CreateTable creates a new table with the given schema in the specified database.
func (d *DDLExecutor) CreateTable(dbName string, schema TableSchema) error {
	return d.engine.CreateTable(dbName, schema)
}

// DropTable drops the specified table from the given database.
func (d *DDLExecutor) DropTable(dbName, tableName string) error {
	return d.engine.DropTable(dbName, tableName)
}
