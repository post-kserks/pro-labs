package types

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"vaultdb/internal/core/storage"
)

// ─── Foreign Key Enforcement ────────────────────────────────────────────────

// BuildRefIndex creates a set of composite keys from referenced rows for FK validation.
func BuildRefIndex(rows []storage.Row, schema *storage.TableSchema, refCols []string) map[string]bool {
	set := make(map[string]bool)
	for _, row := range rows {
		key := BuildFKKey(row, schema, refCols)
		set[key] = true
	}
	return set
}

// BuildFKKey builds a composite key string from row values for FK columns.
func BuildFKKey(row storage.Row, schema *storage.TableSchema, columns []string) string {
	colIndex := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		colIndex[strings.ToLower(col.Name)] = i
	}
	var b strings.Builder
	for i, colName := range columns {
		if i > 0 {
			b.WriteByte(0)
		}
		idx, ok := colIndex[strings.ToLower(colName)]
		if !ok || idx >= len(row) {
			continue
		}
		b.WriteString(ValueToString(row[idx]))
	}
	return b.String()
}

// EnforceForeignKeysOnInsert checks that all FK references in new rows are valid.
func EnforceForeignKeysOnInsert(ctx *ExecutionContext, dbName string, tableName string, rows []storage.Row) error {
	if dbName == "" {
		return nil
	}
	schema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return err
	}
	if len(schema.Constraints) == 0 {
		return nil
	}
	for _, fk := range schema.Constraints {
		if fk.Type != "FOREIGN_KEY" || fk.RefTable == "" {
			continue
		}
		if !ctx.Storage.TableExists(dbName, fk.RefTable) {
			return fmt.Errorf("foreign key constraint '%s': referenced table '%s' does not exist", fk.Name, fk.RefTable)
		}
		refSchema, err := ctx.Storage.GetTableSchema(dbName, fk.RefTable)
		if err != nil {
			return err
		}
		refRows, err := ctx.Storage.ReadCurrentRows(dbName, fk.RefTable)
		if err != nil {
			return err
		}
		refSet := BuildRefIndex(refRows, refSchema, fk.RefCols)
		for i, row := range rows {
			key := BuildFKKey(row, schema, fk.Columns)
			if len(key) == 0 {
				continue
			}
			if !refSet[key] {
				return fmt.Errorf("foreign key constraint '%s' violated: row %d references non-existent value in '%s'", fk.Name, i, fk.RefTable)
			}
		}
	}
	return nil
}

// EnforceForeignKeysOnDelete checks that deleting rows won't break FK constraints.
func EnforceForeignKeysOnDelete(ctx *ExecutionContext, dbName string, tableName string, indices []int, deletedRows []storage.Row) error {
	if dbName == "" {
		return nil
	}
	tables, err := ctx.Storage.ListTables(dbName)
	if err != nil {
		return err
	}
	for _, info := range tables {
		if info.Name == tableName {
			continue
		}
		childSchema, err := ctx.Storage.GetTableSchema(dbName, info.Name)
		if err != nil {
			continue
		}
		for _, fk := range childSchema.Constraints {
			if fk.Type != "FOREIGN_KEY" || fk.RefTable != tableName {
				continue
			}

			childRows, err := ctx.Storage.ReadCurrentRows(dbName, info.Name)
			if err != nil {
				continue
			}
			parentSchema, err := ctx.Storage.GetTableSchema(dbName, tableName)
			if err != nil {
				continue
			}

			for i := range indices {
				if i >= len(deletedRows) {
					continue
				}
				parentKey := BuildFKKey(deletedRows[i], parentSchema, fk.RefCols)
				for ci, childRow := range childRows {
					childKey := BuildFKKey(childRow, childSchema, fk.Columns)
					if childKey == parentKey {
						if fk.OnDeleteCascade {
							continue
						}
						return fmt.Errorf("foreign key constraint '%s' violated: row in '%s' references deleted parent row (index %d)", fk.Name, info.Name, ci)
					}
				}
			}
		}
	}
	return nil
}

// EnforceForeignKeysOnUpdate checks that updated FK values are valid.
func EnforceForeignKeysOnUpdate(ctx *ExecutionContext, dbName string, tableName string, indices []int, newValues []storage.Row) error {
	if dbName == "" {
		return nil
	}
	tableSchema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return err
	}

	for _, fk := range tableSchema.Constraints {
		if fk.Type != "FOREIGN_KEY" || fk.RefTable == "" {
			continue
		}
		if !ctx.Storage.TableExists(dbName, fk.RefTable) {
			continue
		}
		refSchema, err := ctx.Storage.GetTableSchema(dbName, fk.RefTable)
		if err != nil {
			continue
		}
		refRows, err := ctx.Storage.ReadCurrentRows(dbName, fk.RefTable)
		if err != nil {
			continue
		}
		refSet := BuildRefIndex(refRows, refSchema, fk.RefCols)
		for i := range indices {
			if i >= len(newValues) {
				continue
			}
			newRow := newValues[i]
			newKey := BuildFKKey(newRow, tableSchema, fk.Columns)
			if len(newKey) == 0 {
				continue
			}
			if !refSet[newKey] {
				return fmt.Errorf("foreign key constraint '%s' violated: row references non-existent value in '%s'", fk.Name, fk.RefTable)
			}
		}
	}

	return nil
}

// EnforceCascadeDeletes performs cascade deletes for FK relationships.
func EnforceCascadeDeletes(ctx *ExecutionContext, dbName string, tableName string, indices []int, deletedRows []storage.Row) error {
	if dbName == "" {
		return nil
	}
	tables, err := ctx.Storage.ListTables(dbName)
	if err != nil {
		return err
	}
	for _, info := range tables {
		if info.Name == tableName {
			continue
		}
		childSchema, err := ctx.Storage.GetTableSchema(dbName, info.Name)
		if err != nil {
			continue
		}
		for _, fk := range childSchema.Constraints {
			if fk.Type != "FOREIGN_KEY" || fk.RefTable != tableName {
				continue
			}
			if !fk.OnDeleteCascade {
				continue
			}
			childRows, err := ctx.Storage.ReadCurrentRows(dbName, info.Name)
			if err != nil {
				continue
			}
			parentSchema, err := ctx.Storage.GetTableSchema(dbName, tableName)
			if err != nil {
				continue
			}
			var toDelete []int
			for i := range indices {
				if i >= len(deletedRows) {
					continue
				}
				parentKey := BuildFKKey(deletedRows[i], parentSchema, fk.RefCols)
				for ci, childRow := range childRows {
					childKey := BuildFKKey(childRow, childSchema, fk.Columns)
					if childKey == parentKey {
						toDelete = append(toDelete, ci)
					}
				}
			}
			if len(toDelete) > 0 {
				if _, err := ctx.Storage.DeleteRows(dbName, info.Name, toDelete); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ─── Sequence / Auto-Increment ──────────────────────────────────────────────

var (
	sequenceMu       sync.Mutex
	sequenceCounters = make(map[string]int64)
)

func sequenceKey(dbName, tableName, colName string) string {
	return strings.ToLower(dbName + "." + tableName + "." + colName)
}

// ResetSequenceCounters clears all in-memory sequence state (used by tests).
func ResetSequenceCounters() {
	sequenceMu.Lock()
	sequenceCounters = make(map[string]int64)
	sequenceMu.Unlock()
}

// GetNextAutoIncrement returns the next auto-increment value for a column.
func GetNextAutoIncrement(ctx *ExecutionContext, dbName, tableName, colName string) (int64, error) {
	sequenceMu.Lock()
	defer sequenceMu.Unlock()

	key := sequenceKey(dbName, tableName, colName)
	if next, ok := sequenceCounters[key]; ok {
		sequenceCounters[key] = next + 1
		return next, nil
	}

	maxVal, err := initSequenceFromTable(ctx, dbName, tableName, colName)
	if err != nil {
		return 0, err
	}

	next := maxVal + 1
	sequenceCounters[key] = next + 1
	return next, nil
}

func initSequenceFromTable(ctx *ExecutionContext, dbName, tableName, colName string) (int64, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
	if err != nil {
		return 0, err
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return 0, err
	}

	colIdx := -1
	for i, col := range schema.Columns {
		if strings.EqualFold(col.Name, colName) {
			colIdx = i
			break
		}
	}
	if colIdx == -1 {
		return 0, fmt.Errorf("column '%s' not found in table '%s'", colName, tableName)
	}

	var maxVal int64
	for _, row := range rows {
		if colIdx >= len(row) || row[colIdx] == nil {
			continue
		}
		var val int64
		switch v := row[colIdx].(type) {
		case int64:
			val = v
		case float64:
			val = int64(v)
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				continue
			}
			val = parsed
		default:
			continue
		}
		if val > maxVal {
			maxVal = val
		}
	}
	return maxVal, nil
}

// FillAutoIncrementColumns fills nil auto-increment columns with the next sequence value.
func FillAutoIncrementColumns(ctx *ExecutionContext, dbName, tableName string, schema *storage.TableSchema, rows []storage.Row) error {
	for i, col := range schema.Columns {
		if !col.AutoIncrement {
			continue
		}
		for j := range rows {
			if rows[j][i] != nil {
				continue
			}
			nextVal, err := GetNextAutoIncrement(ctx, dbName, tableName, col.Name)
			if err != nil {
				return fmt.Errorf("auto-increment for column '%s': %w", col.Name, err)
			}
			rows[j][i] = nextVal
		}
	}
	return nil
}

// ─── DDL Object Storage ─────────────────────────────────────────────────────

const (
	ObjTypeView      = "view"
	ObjTypeTrigger   = "trigger"
	ObjTypeFunction  = "function"
	ObjTypeProcedure = "procedure"
)

const SystemTableName = "_objects"

var SystemTableSchema = &storage.TableSchema{
	Name: SystemTableName,
	Columns: []storage.ColumnSchema{
		{Name: "name", Type: "TEXT"},
		{Name: "type", Type: "TEXT"},
		{Name: "definition", Type: "TEXT"},
		{Name: "created_at", Type: "INT"},
	},
}

// EnsureSystemTable creates the _objects table if it does not exist yet.
func EnsureSystemTable(ctx *ExecutionContext, dbName string) error {
	if ctx.Storage.TableExists(dbName, SystemTableName) {
		return nil
	}
	schema := storage.TableSchema{
		Name: SystemTableName,
		Columns: []storage.ColumnSchema{
			{Name: "name", Type: "TEXT"},
			{Name: "type", Type: "TEXT"},
			{Name: "definition", Type: "TEXT"},
			{Name: "created_at", Type: "INT"},
		},
	}
	return ctx.Storage.CreateTable(dbName, schema)
}

// StoreObject stores a DDL object (view/trigger/function/procedure).
func StoreObject(ctx *ExecutionContext, dbName, objType, name string, definition interface{}) error {
	if err := EnsureSystemTable(ctx, dbName); err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	defJSON, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("store object: marshal definition: %w", err)
	}

	existing, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	var createdAt int64
	for _, row := range existing {
		if len(row) >= 3 && ValuesEqualCaseInsensitive(row[0], name) && ValuesEqual(row[1], objType) {
			if len(row) >= 4 {
				if ts, ok := row[3].(int64); ok {
					createdAt = ts
				}
			}
		}
	}

	// Delete existing object via DeleteRowsVM (single pass).
	_, err = ctx.Storage.DeleteRowsVM(dbName, SystemTableName, nil, func(rawTuple []byte) (bool, error) {
		_, _, row, errRow := storage.DecodeRow(rawTuple, SystemTableSchema)
		if errRow != nil {
			return false, nil
		}
		if len(row) >= 3 && ValuesEqualCaseInsensitive(row[0], name) && ValuesEqual(row[1], objType) {
			return true, nil
		}
		return false, nil
	}, nil)
	if err != nil {
		return fmt.Errorf("store object: delete old: %w", err)
	}

	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}

	newRow := storage.Row{name, objType, string(defJSON), createdAt}
	if _, err := ctx.Storage.InsertRows(dbName, SystemTableName, []storage.Row{newRow}); err != nil {
		return fmt.Errorf("store object: insert: %w", err)
	}

	return nil
}

// LoadObject loads a DDL object by name and type.
func LoadObject(ctx *ExecutionContext, dbName, objType, name string) (map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return nil, fmt.Errorf("load object: %w", err)
	}

	for _, row := range rows {
		if len(row) >= 3 && ValuesEqualCaseInsensitive(row[0], name) && ValuesEqual(row[1], objType) {
			defJSON, _ := row[2].(string)
			// read JSON: %s\n", defJSON)
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

// DeleteObject deletes a DDL object by name and type.
func DeleteObject(ctx *ExecutionContext, dbName, objType, name string) error {
	_, err := ctx.Storage.DeleteRowsVM(dbName, SystemTableName, nil, func(rawTuple []byte) (bool, error) {
		_, _, row, errRow := storage.DecodeRow(rawTuple, SystemTableSchema)
		if errRow != nil {
			return false, nil
		}
		if len(row) >= 3 && ValuesEqualCaseInsensitive(row[0], name) && ValuesEqual(row[1], objType) {
			return true, nil
		}
		return false, nil
	}, nil)
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// LoadAllObjectsByType loads all objects of the given type.
func LoadAllObjectsByType(ctx *ExecutionContext, dbName, objType string) ([]map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, SystemTableName)
	if err != nil {
		return nil, fmt.Errorf("load all objects: %w", err)
	}

	var results []map[string]interface{}
	for _, row := range rows {
		if len(row) >= 3 && ValuesEqual(row[1], objType) {
			defJSON, _ := row[2].(string)
			// read JSON: %s\n", defJSON)
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

// LoadViewRLS loads the RLS metadata from a view definition.
func LoadViewRLS(ctx *ExecutionContext, dbName, viewName string) (bool, []storage.RLSPolicy, error) {
	def, err := LoadObject(ctx, dbName, ObjTypeView, viewName)
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

// SetViewRLS updates the RLS enabled flag on a view definition.
func SetViewRLS(ctx *ExecutionContext, dbName, viewName string, enabled bool) error {
	def, err := LoadObject(ctx, dbName, ObjTypeView, viewName)
	if err != nil {
		return err
	}
	if def == nil {
		return fmt.Errorf("view '%s' not found", viewName)
	}
	def["rls_enabled"] = enabled
	return StoreObject(ctx, dbName, ObjTypeView, viewName, def)
}

// AddViewPolicy appends an RLS policy to a view definition.
func AddViewPolicy(ctx *ExecutionContext, dbName, viewName string, policy storage.RLSPolicy) error {
	def, err := LoadObject(ctx, dbName, ObjTypeView, viewName)
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
	import_fmt := "fmt"
	_ = import_fmt
	return StoreObject(ctx, dbName, ObjTypeView, viewName, def)
}
