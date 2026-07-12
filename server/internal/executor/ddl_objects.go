package executor

import (
	"encoding/json"
	"fmt"
	"time"

	"vaultdb/internal/storage"
)

// objectType — types of DDL objects stored in _objects.
const (
	objTypeView      = "view"
	objTypeTrigger   = "trigger"
	objTypeFunction  = "function"
	objTypeProcedure = "procedure"
)

// systemTableName — name of the virtual table for storing DDL objects.
const systemTableName = "_objects"

// objectRow — DDL object representation as a table row.
// Columns: name TEXT, type TEXT, definition TEXT.
// ensureSystemTable creates the _objects table if it does not exist yet.
func ensureSystemTable(ctx *ExecutionContext, dbName string) error {
	if ctx.Storage.TableExists(dbName, systemTableName) {
		return nil
	}
	schema := storage.TableSchema{
		Name: systemTableName,
		Columns: []storage.ColumnSchema{
			{Name: "name", Type: "TEXT"},
			{Name: "type", Type: "TEXT"},
			{Name: "definition", Type: "TEXT"},
			{Name: "created_at", Type: "INT"},
		},
	}
	return ctx.Storage.CreateTable(dbName, schema)
}

// storeObject stores a DDL object (view/trigger/function/procedure) via storage engine.
// If an object with the same name and type already exists — updates the definition.
func storeObject(ctx *ExecutionContext, dbName, objType, name string, definition interface{}) error {
	if err := ensureSystemTable(ctx, dbName); err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	defJSON, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("store object: marshal definition: %w", err)
	}

	// Check existence
	existing, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	// Find existing record and preserve its created_at
	var existingIdx []int
	var createdAt int64
	for i, row := range existing {
		if len(row) >= 3 && valuesEqual(row[0], name) && valuesEqual(row[1], objType) {
			existingIdx = append(existingIdx, i)
			if len(row) >= 4 {
				if ts, ok := row[3].(int64); ok {
					createdAt = ts
				}
			}
		}
	}

	// Delete old record
	if len(existingIdx) > 0 {
		if _, err := ctx.Storage.DeleteRows(dbName, systemTableName, existingIdx); err != nil {
			return fmt.Errorf("store object: delete old: %w", err)
		}
	}

	// Use existing created_at or current time
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	// Insert new one
	newRow := storage.Row{name, objType, string(defJSON), createdAt}
	if _, err := ctx.Storage.InsertRows(dbName, systemTableName, []storage.Row{newRow}); err != nil {
		return fmt.Errorf("store object: insert: %w", err)
	}

	return nil
}

// loadObject loads a DDL object by name and type.
// Returns definition as a map (decoded JSON) or nil if not found.
func loadObject(ctx *ExecutionContext, dbName, objType, name string) (map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return nil, fmt.Errorf("load object: %w", err)
	}

	for _, row := range rows {
		if len(row) >= 3 && valuesEqualCaseInsensitive(row[0], name) && valuesEqual(row[1], objType) {
			defJSON, _ := row[2].(string)
			if defJSON == "" {
				return nil, nil
			}
			var def map[string]interface{}
			if err := json.Unmarshal([]byte(defJSON), &def); err != nil {
				return nil, fmt.Errorf("load object: unmarshal: %w", err)
			}
			return def, nil
		}
	}
	return nil, nil
}

// loadObjectBody loads the body of a DDL object.

// deleteObject deletes a DDL object by name and type.
func deleteObject(ctx *ExecutionContext, dbName, objType, name string) error {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}

	var indices []int
	for i, row := range rows {
		if len(row) >= 3 && valuesEqual(row[0], name) && valuesEqual(row[1], objType) {
			indices = append(indices, i)
		}
	}

	if len(indices) == 0 {
		return fmt.Errorf("object '%s' not found", name)
	}

	if _, err := ctx.Storage.DeleteRows(dbName, systemTableName, indices); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// listObjectsByType returns a list of object names of the given type.

// loadAllObjectsByType loads all objects of the given type.
func loadAllObjectsByType(ctx *ExecutionContext, dbName, objType string) ([]map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return nil, fmt.Errorf("load all objects: %w", err)
	}

	var results []map[string]interface{}
	for _, row := range rows {
		if len(row) >= 3 && valuesEqual(row[1], objType) {
			defJSON, _ := row[2].(string)
			if defJSON == "" {
				continue
			}
			var def map[string]interface{}
			if err := json.Unmarshal([]byte(defJSON), &def); err != nil {
				continue
			}
			results = append(results, def)
		}
	}
	return results, nil
}

// loadViewRLS loads the RLS metadata from a view definition.
func loadViewRLS(ctx *ExecutionContext, dbName, viewName string) (bool, []storage.RLSPolicy, error) {
	def, err := loadObject(ctx, dbName, objTypeView, viewName)
	if err != nil || def == nil {
		return false, nil, err
	}
	rlsEnabled, _ := def["rls_enabled"].(bool)
	var policies []storage.RLSPolicy
	if pRaw, ok := def["policies"]; ok {
		pBytes, err := json.Marshal(pRaw)
		if err != nil {
			return false, nil, fmt.Errorf("marshal view policies: %w", err)
		}
		if err := json.Unmarshal(pBytes, &policies); err != nil {
			return false, nil, fmt.Errorf("unmarshal view policies: %w", err)
		}
	}
	return rlsEnabled, policies, nil
}

// setViewRLS updates the RLS enabled flag on a view definition.
func setViewRLS(ctx *ExecutionContext, dbName, viewName string, enabled bool) error {
	def, err := loadObject(ctx, dbName, objTypeView, viewName)
	if err != nil {
		return err
	}
	if def == nil {
		return fmt.Errorf("view '%s' not found", viewName)
	}
	def["rls_enabled"] = enabled
	return storeObject(ctx, dbName, objTypeView, viewName, def)
}

// addViewPolicy appends an RLS policy to a view definition.
func addViewPolicy(ctx *ExecutionContext, dbName, viewName string, policy storage.RLSPolicy) error {
	def, err := loadObject(ctx, dbName, objTypeView, viewName)
	if err != nil {
		return err
	}
	if def == nil {
		return fmt.Errorf("view '%s' not found", viewName)
	}
	var policies []storage.RLSPolicy
	if pRaw, ok := def["policies"]; ok {
		pBytes, _ := json.Marshal(pRaw)
		_ = json.Unmarshal(pBytes, &policies)
	}
	policies = append(policies, policy)
	def["policies"] = policies
	return storeObject(ctx, dbName, objTypeView, viewName, def)
}

// objectExists checks whether an object with the given name and type exists.

// objectNamesToCSV converts a list of names to a comma-separated string.
