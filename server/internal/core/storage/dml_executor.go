package storage

// DMLExecutor handles Data Manipulation Language operations (INSERT, UPDATE, DELETE).
// It delegates to the underlying PageStorageEngine for the actual execution.
type DMLExecutor struct {
	engine *PageStorageEngine
}

// NewDMLExecutor creates a new DMLExecutor bound to the given engine.
func NewDMLExecutor(engine *PageStorageEngine) *DMLExecutor {
	return &DMLExecutor{engine: engine}
}

// InsertRows inserts rows into the specified table.
// Returns the number of rows inserted.
func (d *DMLExecutor) InsertRows(dbName, tableName string, rows []Row) (int, error) {
	return d.engine.InsertRows(dbName, tableName, rows)
}

// UpdateRows updates rows at the given indices with the provided column values.
// Returns the number of rows updated.
func (d *DMLExecutor) UpdateRows(dbName, tableName string, indices []int, updates map[string]Value) (int, error) {
	return d.engine.UpdateRows(dbName, tableName, indices, updates)
}

// UpdateRowsDirect replaces rows at given indices with pre-computed new values.
// Used when assignment expressions reference columns (e.g., SET amount = amount - 100).
func (d *DMLExecutor) UpdateRowsDirect(dbName, tableName string, indices []int, newValues []Row) (int, error) {
	return d.engine.UpdateRowsDirect(dbName, tableName, indices, newValues)
}

// DeleteRows deletes rows at the given indices.
// Returns the number of rows deleted.
func (d *DMLExecutor) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	return d.engine.DeleteRows(dbName, tableName, indices)
}
