package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"vaultdb/internal/storage"
)

// objectType — типы DDL-объектов, хранимых в _objects.
const (
	objTypeView      = "view"
	objTypeTrigger   = "trigger"
	objTypeFunction  = "function"
	objTypeProcedure = "procedure"
)

// systemTableName — имя виртуальной таблицы для хранения DDL-объектов.
const systemTableName = "_objects"

// objectRow — представление DDL-объекта как строка таблицы.
// Колонки: name TEXT, type TEXT, definition TEXT.
type objectRow = storage.Row

// ensureSystemTable создаёт таблицу _objects, если она ещё не существует.
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
		},
	}
	return ctx.Storage.CreateTable(dbName, schema)
}

// storeObject сохраняет DDL-объект (view/trigger/function/procedure) через storage engine.
// Если объект с таким именем и типом уже существует — обновляет definition.
func storeObject(ctx *ExecutionContext, dbName, objType, name string, definition interface{}) error {
	if err := ensureSystemTable(ctx, dbName); err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	defJSON, err := json.Marshal(definition)
	if err != nil {
		return fmt.Errorf("store object: marshal definition: %w", err)
	}

	// Проверяем существование
	existing, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return fmt.Errorf("store object: %w", err)
	}

	// Ищем существующую запись
	var existingIdx []int
	for i, row := range existing {
		if len(row) >= 3 && valuesEqual(row[0], name) && valuesEqual(row[1], objType) {
			existingIdx = append(existingIdx, i)
		}
	}

	// Удаляем старую запись
	if len(existingIdx) > 0 {
		if _, err := ctx.Storage.DeleteRows(dbName, systemTableName, existingIdx); err != nil {
			return fmt.Errorf("store object: delete old: %w", err)
		}
	}

	// Вставляем новую
	newRow := storage.Row{name, objType, string(defJSON)}
	if _, err := ctx.Storage.InsertRows(dbName, systemTableName, []storage.Row{newRow}); err != nil {
		return fmt.Errorf("store object: insert: %w", err)
	}

	return nil
}

// loadObject загружает DDL-объект по имени и типу.
// Возвращает definition как map (распакованный JSON) или nil, если не найден.
func loadObject(ctx *ExecutionContext, dbName, objType, name string) (map[string]interface{}, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return nil, fmt.Errorf("load object: %w", err)
	}

	for _, row := range rows {
		if len(row) >= 3 && valuesEqual(row[0], name) && valuesEqual(row[1], objType) {
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

// loadObjectBody загружает тело (body) DDL-объекта.
func loadObjectBody(ctx *ExecutionContext, dbName, objType, name string) (string, error) {
	def, err := loadObject(ctx, dbName, objType, name)
	if err != nil {
		return "", err
	}
	if def == nil {
		return "", nil
	}
	body, _ := def["body"].(string)
	return body, nil
}

// deleteObject удаляет DDL-объект по имени и типу.
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

// listObjectsByType возвращает список имён объектов указанного типа.
func listObjectsByType(ctx *ExecutionContext, dbName, objType string) ([]string, error) {
	rows, err := ctx.Storage.ReadCurrentRows(dbName, systemTableName)
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}

	var names []string
	for _, row := range rows {
		if len(row) >= 3 && valuesEqual(row[1], objType) {
			if name, ok := row[0].(string); ok {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

// loadAllObjectsByType загружает все объекты указанного типа.
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

// objectExists проверяет, существует ли объект с указанным именем и типом.
func objectExists(ctx *ExecutionContext, dbName, objType, name string) bool {
	def, err := loadObject(ctx, dbName, objType, name)
	return err == nil && def != nil
}

// objectNamesToCSV конвертирует список имён в строку через запятую.
func objectNamesToCSV(names []string) string {
	return strings.Join(names, ", ")
}
