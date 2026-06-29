package executor

import (
	"fmt"
	"strings"

	"vaultdb/internal/storage"
)

func buildRefIndex(rows []storage.Row, schema *storage.TableSchema, refCols []string) map[string]bool {
	set := make(map[string]bool)
	for _, row := range rows {
		key := buildFKKey(row, schema, refCols)
		set[key] = true
	}
	return set
}

func buildFKKey(row storage.Row, schema *storage.TableSchema, columns []string) string {
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
		b.WriteString(valueToString(row[idx]))
	}
	return b.String()
}

func enforceForeignKeysOnInsert(ctx *ExecutionContext, dbName string, tableName string, rows []storage.Row) error {
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
		refSet := buildRefIndex(refRows, refSchema, fk.RefCols)
		for i, row := range rows {
			key := buildFKKey(row, schema, fk.Columns)
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

func enforceForeignKeysOnDelete(ctx *ExecutionContext, dbName string, tableName string, indices []int) error {
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
			parentRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
			if err != nil {
				continue
			}
			childRows, err := ctx.Storage.ReadCurrentRows(dbName, info.Name)
			if err != nil {
				continue
			}
			indexSet := make(map[int]bool, len(indices))
			for _, idx := range indices {
				indexSet[idx] = true
			}
			for _, idx := range indices {
				if idx >= len(parentRows) {
					continue
				}
				parentKey := buildFKKey(parentRows[idx], childSchema, fk.RefCols)
				for ci, childRow := range childRows {
					childKey := buildFKKey(childRow, childSchema, fk.Columns)
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

func enforceForeignKeysOnUpdate(ctx *ExecutionContext, dbName string, tableName string, indices []int, newValues []storage.Row) error {
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
		refSet := buildRefIndex(refRows, refSchema, fk.RefCols)
		for i := range indices {
			if i >= len(newValues) {
				continue
			}
			newRow := newValues[i]
			newKey := buildFKKey(newRow, tableSchema, fk.Columns)
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

func enforceCascadeDeletes(ctx *ExecutionContext, dbName string, tableName string, indices []int) error {
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
			parentRows, err := ctx.Storage.ReadCurrentRows(dbName, tableName)
			if err != nil {
				continue
			}
			childRows, err := ctx.Storage.ReadCurrentRows(dbName, info.Name)
			if err != nil {
				continue
			}
			var toDelete []int
			for _, idx := range indices {
				if idx >= len(parentRows) {
					continue
				}
				parentKey := buildFKKey(parentRows[idx], childSchema, fk.RefCols)
				for ci, childRow := range childRows {
					childKey := buildFKKey(childRow, childSchema, fk.Columns)
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
