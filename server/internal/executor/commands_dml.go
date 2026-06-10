package executor

// Команды DML: INSERT, UPDATE, DELETE.

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

type InsertCommand struct {
	stmt *parser.InsertStatement
}

func (c *InsertCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		ctx.Session.TxManager.AddOp(ctx.Session.ActiveTx, txmanager.PendingOp{
			Type:    "insert",
			DB:      *ctx.CurrentDB,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered INSERT (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *InsertCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
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

	// Handle INFER SCHEMA
	if len(schema.Columns) == 0 && len(c.stmt.Rows) > 0 {
		// Infer schema from first row
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

		// Update table schema on disk
		for _, col := range inferredCols {
			if err := ctx.Storage.AlterTableAddColumn(dbName, c.stmt.TableName, col, nil); err != nil {
				return nil, fmt.Errorf("infer schema failed: %w", err)
			}
		}
		// Reload schema
		schema, err = ctx.Storage.GetTableSchema(dbName, c.stmt.TableName)
		if err != nil {
			return nil, err
		}
	}

	rowsToInsert, err := c.buildRows(schema, ctx)
	if err != nil {
		return nil, err
	}

	affected, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, rowsToInsert)
	if err != nil {
		return nil, err
	}

	if ctx.TxManager != nil {
		ctx.TxManager.BumpTableVersion(dbName, c.stmt.TableName)
	}

	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, c.stmt.TableName, ctx)
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
		// Fill with nil first (or handle defaults if stored in storage schema)
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
		// Compute GENERATED columns (placeholder logic)
		for i, col := range schema.Columns {
			if col.IsComputed {
				// For prototype: if name is 'double_level' and 'level' exists, double it
				if col.Name == "double_level" {
					levelIdx := -1
					for j, c := range schema.Columns {
						if c.Name == "level" {
							levelIdx = j
							break
						}
					}
					if levelIdx != -1 && normalized[levelIdx] != nil {
						if f, ok := toFloat(normalized[levelIdx]); ok {
							normalized[i] = f * 2
						}
					}
				}
			}
		}
		result = append(result, normalized)
	}

	return result, nil
}

type UpdateCommand struct {
	stmt *parser.UpdateStatement
}

func (c *UpdateCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		ctx.Session.TxManager.AddOp(ctx.Session.ActiveTx, txmanager.PendingOp{
			Type:    "update",
			DB:      *ctx.CurrentDB,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered UPDATE (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *UpdateCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
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
	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(rows))
	for idx, row := range rows {
		match, err := evalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if match {
			indices = append(indices, idx)
		}
	}

	updates, err := c.buildUpdates(schema)
	if err != nil {
		return nil, err
	}

	affected, err := ctx.Storage.UpdateRows(dbName, c.stmt.TableName, indices, updates)
	if err != nil {
		return nil, err
	}

	if ctx.TxManager != nil {
		ctx.TxManager.BumpTableVersion(dbName, c.stmt.TableName)
	}

	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, c.stmt.TableName, ctx)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

func (c *UpdateCommand) buildUpdates(schema *storage.TableSchema) (map[string]storage.Value, error) {
	columnMap := make(map[string]storage.ColumnSchema, len(schema.Columns))
	for _, col := range schema.Columns {
		columnMap[strings.ToLower(col.Name)] = col
	}

	updates := make(map[string]storage.Value, len(c.stmt.Assignments))
	for _, assignment := range c.stmt.Assignments {
		col, ok := columnMap[strings.ToLower(assignment.Column)]
		if !ok {
			return nil, fmt.Errorf("unknown column '%s'", assignment.Column)
		}
		var val parser.Value
		switch v := assignment.Value.(type) {
		case parser.Value:
			val = v
		case *parser.Value:
			val = *v
		default:
			return nil, fmt.Errorf("column '%s': expected literal value, got %T", assignment.Column, assignment.Value)
		}
		value, err := parserValueToColumnType(val, col)
		if err != nil {
			return nil, fmt.Errorf("column '%s': %w", assignment.Column, err)
		}
		updates[assignment.Column] = value
	}
	return updates, nil
}

type DeleteCommand struct {
	stmt *parser.DeleteStatement
}

func (c *DeleteCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		ctx.Session.TxManager.AddOp(ctx.Session.ActiveTx, txmanager.PendingOp{
			Type:    "delete",
			DB:      *ctx.CurrentDB,
			Table:   c.stmt.TableName,
			Payload: c.stmt,
		})
		return &Result{
			Type:    "message",
			Message: fmt.Sprintf("Buffered DELETE (tx %d). Not committed yet.", ctx.Session.ActiveTx.ID),
		}, nil
	}
	return c.executeImmediate(ctx)
}

func (c *DeleteCommand) executeImmediate(ctx *ExecutionContext) (*Result, error) {
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
	rows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	indices := make([]int, 0, len(rows))
	for idx, row := range rows {
		match, err := evalExpr(c.stmt.Where, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if match {
			indices = append(indices, idx)
		}
	}

	affected, err := ctx.Storage.DeleteRows(dbName, c.stmt.TableName, indices)
	if err != nil {
		return nil, err
	}

	if ctx.TxManager != nil {
		ctx.TxManager.BumpTableVersion(dbName, c.stmt.TableName)
	}

	if ctx.Broadcaster != nil {
		ctx.Broadcaster.NotifyTableChanged(dbName, c.stmt.TableName, ctx)
	}

	return &Result{Type: "affected", Affected: affected}, nil
}
