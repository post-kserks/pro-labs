package storage

import "sync"

// CatalogManager manages in-memory table schema lookups.
// It provides a thread-safe cache for table schemas indexed by "db/table" key.
// The PageStorageEngine delegates schema lookups to this subsystem.
type CatalogManager struct {
	schemas map[string]*TableSchema
	mu      sync.RWMutex
}

// NewCatalogManager creates a new CatalogManager with an empty schema cache.
func NewCatalogManager() *CatalogManager {
	return &CatalogManager{schemas: make(map[string]*TableSchema)}
}

// GetSchema returns the schema for the given database and table.
// Returns nil and false if the schema is not cached.
func (cm *CatalogManager) GetSchema(dbName, tableName string) (*TableSchema, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	key := dbName + "/" + tableName
	schema, ok := cm.schemas[key]
	return schema, ok
}

// SetSchema stores or updates the schema for the given database and table.
func (cm *CatalogManager) SetSchema(dbName, tableName string, schema *TableSchema) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	key := dbName + "/" + tableName
	cm.schemas[key] = schema
}

// RemoveSchema removes the schema entry for the given database and table.
func (cm *CatalogManager) RemoveSchema(dbName, tableName string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	key := dbName + "/" + tableName
	delete(cm.schemas, key)
}

// Clear removes all cached schemas.
func (cm *CatalogManager) Clear() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.schemas = make(map[string]*TableSchema)
}

// Len returns the number of cached schemas.
func (cm *CatalogManager) Len() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.schemas)
}
