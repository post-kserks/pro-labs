package executor

// INSERT command implementation.

import (
	"fmt"
	"strconv"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

type InsertCommand struct {
	stmt *parser.InsertStatement
}

func (c *InsertCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	dbName, _ := requireCurrentDB(ctx)
	if dbName != "" {
		if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
			return nil, err
		}
	}
	activeTx := ctx.Session.GetActiveTx()
	if activeTx != nil && activeTx.State == txmanager.TxActive {
		frozen, err := freezeInsert(c.stmt, ctx)
		if err != nil {
			return nil, err
		}
		ctx.Session.TxManager.AddOp(activeTx, txmanager.PendingOp{
			Type:    "insert",
			DB:      ctx.Session.CurrentDatabase(),
			Table:   c.stmt.TableName,
			Payload: frozen,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered INSERT (tx %d). Not committed yet.", ctx.Session.GetActiveTx().ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *InsertCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return c.executeImmediateInner(ctx)
	}
	var result *Result
	err = mutateUnderTableLock(ctx, dbName, c.stmt.TableName, func() error {
		var e error
		result, e = c.executeImmediateInner(ctx)
		return e
	})
	return result, err
}

func (c *InsertCommand) executeImmediateInner(ctx *ExecutionContext) (*Result, error) {
	if ctx.Ctx != nil {
		select {
		case <-ctx.Ctx.Done():
			return nil, fmt.Errorf("query timeout: %w", ctx.Ctx.Err())
		default:
		}
	}

	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if !ctx.Storage.TableExists(dbName, c.stmt.TableName) {
		return nil, fmt.Errorf("table '%s' does not exist", c.stmt.TableName)
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	if c.stmt.SelectQuery != nil {
		return c.executeInsertSelect(ctx, dbName, schema)
	}

	// Handle INFER SCHEMA
	if len(schema.Columns) == 0 && len(c.stmt.Rows) > 0 {
		inferredCols := make([]storage.ColumnSchema, 0, len(c.stmt.Rows[0]))
		for i, expr := range c.stmt.Rows[0] {
			val, _ := evalOperand(expr, nil, nil, ctx)
			colType := inferType(val)
			name := fmt.Sprintf("col%d", i)
			if len(c.stmt.Columns) > i {
				name = c.stmt.Columns[i]
			}
			inferredCols = append(inferredCols, storage.ColumnSchema{Name: name, Type: colType})
		}
		for _, col := range inferredCols {
			if err := ctx.Storage.AlterTableAddColumn(dbName, c.stmt.TableName, col, nil); err != nil {
				return nil, fmt.Errorf("infer schema failed: %w", err)
			}
		}
		schema, err = ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
	}

	rowsToInsert, err := c.buildRows(schema, ctx)
	if err != nil {
		return nil, err
	}

	if err := enforceForeignKeysOnInsert(ctx, dbName, c.stmt.TableName, rowsToInsert); err != nil {
		return nil, err
	}

	for i, row := range rowsToInsert {
		for j, col := range schema.Columns {
			if col.Type == "ENUM" && len(col.EnumValues) > 0 && j < len(row) && row[j] != nil {
				val := valueToString(row[j])
				valid := false
				for _, ev := range col.EnumValues {
					if val == ev {
						valid = true
						break
					}
				}
				if !valid {
					return nil, fmt.Errorf("invalid ENUM value '%s' for column '%s' (valid: %v)", val, col.Name, col.EnumValues)
				}
			}
		}
		_ = i
	}

	for i, row := range rowsToInsert {
		if err := enforceCheckConstraints(schema, row); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
	}

	if c.stmt.OnConflict != nil {
		return c.executeUpsert(ctx, dbName, schema, rowsToInsert)
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}
	fireTriggers(ctx, dbName, c.stmt.TableName, "INSERT")

	if c.stmt.Returning != nil {
		return c.executeReturning(ctx, dbName, schema, rowsToInsert)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *InsertCommand) buildRows(schema *storage.TableSchema, ctx *ExecutionContext) ([]storage.Row, error) {
	result := make([]storage.Row, 0, len(c.stmt.Rows))

	if len(c.stmt.Columns) == 0 {
		for rowIndex, row := range c.stmt.Rows {
			if len(row) != len(schema.Columns) {
				return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(schema.Columns))
			}
			normalized := make(storage.Row, len(row))
			for i, expr := range row {
				val, err := evalOperand(expr, nil, nil, ctx)
				if err != nil {
					return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
				}
				converted, err := normalizeForColumn(val, schema.Columns[i])
				if err != nil {
					return nil, fmt.Errorf("column '%s': %w", schema.Columns[i].Name, err)
				}
				normalized[i] = converted
			}
			result = append(result, normalized)
		}
		return result, nil
	}

	columnIndex := make(map[string]int, len(schema.Columns))
	for idx, col := range schema.Columns {
		columnIndex[strings.ToLower(col.Name)] = idx
	}

	mappedColumns := make([]int, len(c.stmt.Columns))
	for i, name := range c.stmt.Columns {
		idx, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("unknown column '%s'", name)
		}
		mappedColumns[i] = idx
	}

	for rowIndex, row := range c.stmt.Rows {
		if len(row) != len(mappedColumns) {
			return nil, fmt.Errorf("insert row %d has %d values, expected %d", rowIndex, len(row), len(mappedColumns))
		}

		normalized := make(storage.Row, len(schema.Columns))
		for i := range normalized {
			normalized[i] = nil
		}

		for i, expr := range row {
			colIdx := mappedColumns[i]
			val, err := evalOperand(expr, nil, nil, ctx)
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			converted, err := normalizeForColumn(val, schema.Columns[colIdx])
			if err != nil {
				return nil, fmt.Errorf("column '%s': %w", schema.Columns[colIdx].Name, err)
			}
			normalized[colIdx] = converted
		}
		result = append(result, normalized)
	}

	return result, nil
}

func (c *InsertCommand) executeUpsert(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, rowsToInsert []storage.Row) (*Result, error) {
	affected := 0

	conflictCols := c.stmt.OnConflict.Columns
	if len(conflictCols) == 0 {
		// Use PRIMARY KEY columns for conflict detection
		conflictCols = nil
		for _, col := range schema.Columns {
			if col.PrimaryKey {
				conflictCols = append(conflictCols, col.Name)
			}
		}
		// Fallback to all columns if no PRIMARY KEY defined
		if len(conflictCols) == 0 {
			for _, col := range schema.Columns {
				conflictCols = append(conflictCols, col.Name)
			}
		}
	}
	colIdxMap := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		colIdxMap[strings.ToLower(col.Name)] = i
	}

	existingRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	conflictMap := make(map[string]int, len(existingRows))
	for idx, row := range existingRows {
		key := buildUpsertConflictKey(row, conflictCols, colIdxMap)
		conflictMap[key] = idx
	}

	for _, row := range rowsToInsert {
		key := buildUpsertConflictKey(row, conflictCols, colIdxMap)
		conflictIdx, conflict := conflictMap[key]

		if conflict {
			if c.stmt.OnConflict.Action == "NOTHING" {
				continue
			}
			if c.stmt.OnConflict.Action == "UPDATE" {
				updates := make(map[string]storage.Value)
				for _, assign := range c.stmt.OnConflict.Assignments {
					val, err := evalOperand(assign.Value, row, schema, ctx)
					if err != nil {
						return nil, err
					}
					updates[assign.Column] = val
				}
				_, err := ctx.Storage.UpdateRows(dbName, c.stmt.TableName, []int{conflictIdx}, updates)
				if err != nil {
					return nil, err
				}
				affected++
				existingRows[conflictIdx] = row
				conflictMap[key] = conflictIdx
			}
		} else {
			_, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, []storage.Row{row})
			if err != nil {
				return nil, err
			}
			affected++
			newIdx := len(existingRows)
			existingRows = append(existingRows, row)
			conflictMap[key] = newIdx
		}
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func buildUpsertConflictKey(row storage.Row, conflictCols []string, colIdxMap map[string]int) string {
	var b strings.Builder
	for i, colName := range conflictCols {
		if i > 0 {
			b.WriteByte(0)
		}
		ci, ok := colIdxMap[strings.ToLower(colName)]
		if !ok {
			continue
		}
		if ci < len(row) {
			b.WriteString(valueToString(row[ci]))
		}
	}
	return b.String()
}

func (c *InsertCommand) executeInsertSelect(ctx *ExecutionContext, dbName string, schema *storage.TableSchema) (*Result, error) {
	cmd, err := CommandFactory(c.stmt.SelectQuery)
	if err != nil {
		return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
	}

	res, err := cmd.Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
	}

	rowsToInsert := make([]storage.Row, 0, len(res.Rows))
	for _, row := range res.Rows {
		storageRow := make(storage.Row, len(schema.Columns))
		for i := range schema.Columns {
			if i < len(row) {
				val, err := convertStringToValue(row[i], schema.Columns[i])
				if err != nil {
					return nil, fmt.Errorf("INSERT ... SELECT: column '%s': %w", schema.Columns[i].Name, err)
				}
				storageRow[i] = val
			}
		}
		rowsToInsert = append(rowsToInsert, storageRow)
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}

	if c.stmt.Returning != nil {
		return c.executeReturning(ctx, dbName, schema, rowsToInsert)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *InsertCommand) executeReturning(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, insertedRows []storage.Row) (*Result, error) {
	return executeReturningGeneric(insertedRows, c.stmt.Returning, schema, ctx)
}

func convertStringToValue(s string, col storage.ColumnSchema) (storage.Value, error) {
	switch strings.ToUpper(col.Type) {
	case "INT":
		val, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to INT", s)
		}
		return val, nil
	case "FLOAT":
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to FLOAT", s)
		}
		return val, nil
	case "BOOL":
		val, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("cannot convert '%s' to BOOL", s)
		}
		return val, nil
	case "TEXT", "VARCHAR":
		return s, nil
	default:
		return s, nil
	}
}
