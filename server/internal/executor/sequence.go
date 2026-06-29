package executor

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"vaultdb/internal/storage"
)

var (
	sequenceMu     sync.Mutex
	sequenceCounters = make(map[string]int64) // key: "db.table.col" -> next value
)

func sequenceKey(dbName, tableName, colName string) string {
	return strings.ToLower(dbName + "." + tableName + "." + colName)
}

func getNextAutoIncrement(ctx *ExecutionContext, dbName, tableName, colName string) (int64, error) {
	sequenceMu.Lock()
	defer sequenceMu.Unlock()

	key := sequenceKey(dbName, tableName, colName)
	if next, ok := sequenceCounters[key]; ok {
		sequenceCounters[key] = next + 1
		return next, nil
	}

	// Initialize from existing rows
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

func fillAutoIncrementColumns(ctx *ExecutionContext, dbName, tableName string, schema *storage.TableSchema, rows []storage.Row) error {
	for i, col := range schema.Columns {
		if !col.AutoIncrement {
			continue
		}
		for j := range rows {
			if rows[j][i] != nil {
				continue
			}
			nextVal, err := getNextAutoIncrement(ctx, dbName, tableName, col.Name)
			if err != nil {
				return fmt.Errorf("auto-increment for column '%s': %w", col.Name, err)
			}
			rows[j][i] = nextVal
		}
	}
	return nil
}
