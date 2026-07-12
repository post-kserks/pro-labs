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
	// Fast path: single row, no conflicts, no returning, no tx active
	if len(c.stmt.Rows) == 1 && c.stmt.OnConflict == nil && c.stmt.Returning == nil &&
		c.stmt.SelectQuery == nil && !c.stmt.OrReplace &&
		ctx.Session.GetActiveTx() == nil {
		return c.fastPathInsert(ctx)
	}

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

// fastPathInsert handles single-row inserts without table lock overhead.
func (c *InsertCommand) fastPathInsert(ctx *ExecutionContext) (*Result, error) {
	dbName, err := requireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	if err := enforceRLSPolicies(ctx, dbName, c.stmt.TableName); err != nil {
		return nil, err
	}

	schema, err := ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
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

	if err := fillAutoIncrementColumns(ctx, dbName, c.stmt.TableName, schema, rowsToInsert); err != nil {
		return nil, err
	}

	if err := enforceForeignKeysOnInsert(ctx, dbName, c.stmt.TableName, rowsToInsert); err != nil {
		return nil, err
	}

	// Validate NOT NULL and ENUM constraints
	for j, col := range schema.Columns {
		if col.NotNull && j < len(rowsToInsert[0]) && rowsToInsert[0][j] == nil {
			return nil, fmt.Errorf("NOT NULL constraint failed for column '%s'", col.Name)
		}
		if col.Type == "ENUM" && len(col.EnumValues) > 0 && j < len(rowsToInsert[0]) && rowsToInsert[0][j] != nil {
			val := valueToString(rowsToInsert[0][j])
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

	// Validate CHECK constraints
	for i, row := range rowsToInsert {
		if err := enforceCheckConstraints(schema, row); err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
	}

	// Validate UNIQUE constraints
	if err := enforceUniqueConstraints(dbName, c.stmt.TableName, schema, rowsToInsert, ctx); err != nil {
		return nil, err
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

	return &Result{Type: "affected", Affected: affected}, nil
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

	// Convert INSERT OR REPLACE to ON CONFLICT DO UPDATE SET all columns
	if c.stmt.OrReplace && c.stmt.OnConflict == nil {
		c.stmt.OnConflict = &parser.OnConflictClause{Action: "UPDATE"}
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

	if err := fillAutoIncrementColumns(ctx, dbName, c.stmt.TableName, schema, rowsToInsert); err != nil {
		return nil, err
	}

	if err := enforceForeignKeysOnInsert(ctx, dbName, c.stmt.TableName, rowsToInsert); err != nil {
		return nil, err
	}

	for i, row := range rowsToInsert {
		for j, col := range schema.Columns {
			if col.NotNull && j < len(row) && row[j] == nil {
				return nil, fmt.Errorf("NOT NULL constraint failed for column '%s'", col.Name)
			}
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

	// Validate UNIQUE constraints
	if err := enforceUniqueConstraints(dbName, c.stmt.TableName, schema, rowsToInsert, ctx); err != nil {
		return nil, err
	}

	if c.stmt.OnConflict != nil {
		return c.executeUpsert(ctx, dbName, schema, rowsToInsert)
	}

	// Route to partition if table is partitioned
	if schema.PartitionBy != nil {
		return c.executePartitionedInsert(ctx, dbName, schema, rowsToInsert)
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
			if err := applyComputedColumns(normalized, schema, ctx); err != nil {
				return nil, err
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

	specifiedCols := make(map[int]bool, len(mappedColumns))
	for _, idx := range mappedColumns {
		specifiedCols[idx] = true
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
		applyDefaults(normalized, schema, specifiedCols)
		if err := applyComputedColumns(normalized, schema, ctx); err != nil {
			return nil, err
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
				if len(c.stmt.OnConflict.Assignments) == 0 {
					// INSERT OR REPLACE: update ALL columns
					for i, col := range schema.Columns {
						if i < len(row) && row[i] != nil {
							updates[col.Name] = row[i]
						}
					}
				} else {
					for _, assign := range c.stmt.OnConflict.Assignments {
						val, err := evalOperand(assign.Value, row, schema, ctx)
						if err != nil {
							return nil, err
						}
						updates[assign.Column] = val
					}
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
	for ri, row := range res.Rows {
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
		applyDefaults(storageRow, schema, nil)
		if err := applyComputedColumns(storageRow, schema, ctx); err != nil {
			return nil, fmt.Errorf("INSERT ... SELECT: %w", err)
		}
		for j, col := range schema.Columns {
			if col.NotNull && j < len(storageRow) && storageRow[j] == nil {
				return nil, fmt.Errorf("INSERT ... SELECT: NOT NULL constraint failed for column '%s' in row %d", col.Name, ri)
			}
		}
		if err := enforceCheckConstraints(schema, storageRow); err != nil {
			return nil, fmt.Errorf("INSERT ... SELECT: row %d: %w", ri, err)
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

// applyDefaults fills in DEFAULT values for columns that were not specified
// in the INSERT column list. specifiedCols is a set of column indices that
// were explicitly provided in the INSERT statement.
func applyDefaults(row storage.Row, schema *storage.TableSchema, specifiedCols map[int]bool) {
	for i, col := range schema.Columns {
		if row[i] == nil && col.Default != nil && !specifiedCols[i] {
			row[i] = *col.Default
		}
	}
}

// applyComputedColumns evaluates computed column expressions and sets the values.
func applyComputedColumns(row storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) error {
	for i, col := range schema.Columns {
		if col.IsComputed && col.ComputedExpr != "" {
			expr, err := parser.ParseExpression(col.ComputedExpr)
			if err != nil {
				return fmt.Errorf("parsing computed expression for column '%s': %w", col.Name, err)
			}
			val, err := evalOperand(expr, row, schema, ctx)
			if err != nil {
				return fmt.Errorf("evaluating computed expression for column '%s': %w", col.Name, err)
			}
			converted, err := normalizeForColumn(val, col)
			if err != nil {
				return fmt.Errorf("normalizing computed expression for column '%s': %w", col.Name, err)
			}
			row[i] = converted
		}
	}
	return nil
}

// executePartitionedInsert routes rows to the correct partition for a partitioned table.
func (c *InsertCommand) executePartitionedInsert(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, rowsToInsert []storage.Row) (*Result, error) {
	pt := storage.NewPartitionedTable(schema)
	if pt == nil {
		return nil, fmt.Errorf("table '%s' has partition spec but could not initialize partition routing", c.stmt.TableName)
	}

	// Group rows by target partition
	routes := make(map[string][]storage.Row)
	for _, row := range rowsToInsert {
		targetTable, err := pt.InsertRoute(row)
		if err != nil {
			return nil, fmt.Errorf("partition routing: %w", err)
		}
		routes[targetTable] = append(routes[targetTable], row)
	}

	totalAffected := 0
	for targetTable, rows := range routes {
		affected, err := ctx.Storage.InsertRows(dbName, targetTable, rows)
		if err != nil {
			return nil, fmt.Errorf("insert into partition '%s': %w", targetTable, err)
		}
		totalAffected += affected
	}

	notifyMutation(ctx, dbName, c.stmt.TableName)
	if ctx.Session.planCache != nil {
		ctx.Session.planCache.Invalidate(c.stmt.TableName)
	}
	fireTriggers(ctx, dbName, c.stmt.TableName, "INSERT")

	return &Result{Type: "affected", Affected: totalAffected}, nil
}
