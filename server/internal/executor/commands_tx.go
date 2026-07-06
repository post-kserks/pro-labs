package executor

// Транзакции и prepared statements: BEGIN/COMMIT/ROLLBACK,
// PREPARE/EXECUTE/DEALLOCATE.

import (
	"fmt"
	"log/slog"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

type BeginCommand struct {
	stmt *parser.BeginStatement
}

func (c *BeginCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		return nil, fmt.Errorf("transaction already active; COMMIT or ROLLBACK first")
	}

	ctx.Session.SetActiveTx(ctx.Session.TxManager.Begin())

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d started.", ctx.Session.GetActiveTx().ID),
	}, nil
}

type CommitCommand struct {
	stmt *parser.CommitStatement
}

func (c *CommitCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("no active transaction")
	}

	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}

	// Читаем ops (из памяти или из spill файла)
	ops, err := tx.ReadOps()
	if err != nil {
		return nil, fmt.Errorf("read transaction ops: %w", err)
	}
	opsCount := len(ops)

	// Проверка конфликтов и применение выполняются под commit-локами только
	// тех таблиц, которые затронуты транзакцией: коммиты по разным таблицам
	// идут параллельно, а конфликт обнаруживается по версиям таблиц.
	var applied int
	var applyErr error
	commitErr := ctx.Session.TxManager.Commit(tx, func(pendingOps []txmanager.PendingOp) error {
		// Commit уже держит commit-локи затронутых таблиц. Помечаем контекст,
		// чтобы autocommit-обёртка (mutateUnderTableLock) НЕ брала те же
		// нереентрантные локи повторно — иначе self-deadlock (Bug #2 guard).
		ctx.InCommitApply = true
		defer func() { ctx.InCommitApply = false }()
		applied, applyErr = applyOps(ctx, pendingOps)
		return applyErr
	})

	if commitErr != nil && applyErr == nil {
		return nil, commitErr
	}
	if applyErr != nil {
		// Применение упало частично — откатываем уже применённые ops
		undoOps := ops[:applied]
		if undoErr := undoAppliedOps(ctx, undoOps); undoErr != nil {
			slog.Error("could not undo partial commit, data may be inconsistent",
				"xid", tx.ID, "error", undoErr)
		}
		tx.Rollback()
		ctx.Session.ClearActiveTx()
		if applied > 0 {
			return nil, fmt.Errorf("commit failed after applying %d of %d operations; data may be partially updated: %w", applied, opsCount, applyErr)
		}
		return nil, fmt.Errorf("commit failed, no operations applied: %w", applyErr)
	}

	// Записать COMMIT в WAL с тем же txID, что и операции транзакции
	if ctx.WAL != nil {
		if _, err := ctx.WAL.AppendWithTx(tx.ID, wal.OpCommit, nil); err != nil {
			// Не смогли записать COMMIT — транзакция считается незакоммиченной
			undoAppliedOps(ctx, ops)
			tx.Rollback()
			ctx.Session.ClearActiveTx()
			return nil, fmt.Errorf("wal commit failed, transaction rolled back: %w", err)
		}
	}

	tx.Rollback()
	ctx.Session.ClearActiveTx()

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d committed (%d operations).", tx.ID, opsCount),
	}, nil
}

type RollbackCommand struct {
	stmt *parser.RollbackStatement
}

func (c *RollbackCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("no active transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	ops, readErr := tx.ReadOps()
	if readErr != nil {
		slog.Error("failed to read transaction ops during rollback", "xid", tx.ID, "error", readErr)
	}
	opsCount := len(ops)

	if ctx.WAL != nil && opsCount > 0 {
		if _, err := ctx.WAL.Append(wal.OpAbort, nil); err != nil {
			slog.Error("failed to write WAL abort record", "xid", tx.ID, "error", err)
		}
	}

	tx.Rollback()
	ctx.Session.ClearActiveTx()
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction rolled back (%d operations discarded).", opsCount),
	}, nil
}

// applyOps applies buffered operations in order and reports how many were
// applied before the first failure.
func applyOps(ctx *ExecutionContext, ops []txmanager.PendingOp) (int, error) {
	for i, op := range ops {
		switch op.Type {
		case "insert":
			stmt, ok := op.Payload.(*parser.InsertStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid insert payload type", i)
			}
			cmd := &InsertCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return i, err
			}
		case "update":
			stmt, ok := op.Payload.(*parser.UpdateStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid update payload type", i)
			}
			cmd := &UpdateCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return i, err
			}
		case "delete":
			stmt, ok := op.Payload.(*parser.DeleteStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid delete payload type", i)
			}
			cmd := &DeleteCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return i, err
			}
		case "truncate":
			stmt, ok := op.Payload.(*parser.TruncateStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid truncate payload type", i)
			}
			rows, err := ctx.Storage.ReadCurrentRows(op.DB, op.Table)
			if err != nil {
				return i, err
			}
			if len(rows) > 0 {
				indices := make([]int, len(rows))
				for idx := range indices {
					indices[idx] = idx
				}
				if _, err := ctx.Storage.DeleteRows(op.DB, op.Table, indices); err != nil {
					return i, err
				}
			}
			// Save rows for undo on rollback.
			ops[i].Row = rows
			notifyMutation(ctx, op.DB, op.Table)
			if ctx.Session.resultCache != nil {
				ctx.Session.resultCache.Invalidate(op.Table)
			}
			_ = stmt
		}
	}
	return len(ops), nil
}

// undoAppliedOps откатывает уже применённые операции в обратном порядке.
func undoAppliedOps(ctx *ExecutionContext, ops []txmanager.PendingOp) error {
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		var undoErr error
		switch op.Type {
		case "insert":
			// Undo INSERT = DELETE вставленных строк
			undoErr = undoInsert(ctx, op)
		case "update":
			// Undo UPDATE = восстановить старые значения
			undoErr = undoUpdate(ctx, op)
		case "delete":
			// Undo DELETE = вставить обратно
			undoErr = undoDelete(ctx, op)
		case "truncate":
			// Undo TRUNCATE = вставить обратно сохранённые строки
			undoErr = undoTruncate(ctx, op)
		}
		if undoErr != nil {
			return fmt.Errorf("undo op %d (%s): %w", i, op.Type, undoErr)
		}
	}
	return nil
}

func undoInsert(ctx *ExecutionContext, op txmanager.PendingOp) error {
	if op.DB == "" || op.Table == "" {
		return nil
	}

	schema, err := ctx.Storage.GetTableSchema(op.DB, op.Table)
	if err != nil {
		return err
	}

	stmt, ok := op.Payload.(*parser.InsertStatement)
	if !ok || stmt == nil {
		return fmt.Errorf("undo insert: invalid payload type")
	}
	cmd := &InsertCommand{stmt: stmt}
	insertCmd := &insertUndoHelper{cmd: cmd}
	rowsToUndo, err := insertCmd.buildRows(schema, ctx)
	if err != nil {
		return err
	}

	if len(rowsToUndo) == 0 {
		return nil
	}

	currentRows, err := ctx.Storage.ReadCurrentRows(op.DB, op.Table)
	if err != nil {
		return err
	}

	var indicesToDelete []int
	for _, insertedRow := range rowsToUndo {
		for idx, currentRow := range currentRows {
			if idxInSlice(idx, indicesToDelete) {
				continue
			}
			if rowsEqual(insertedRow, currentRow) {
				indicesToDelete = append(indicesToDelete, idx)
				break
			}
		}
	}

	if len(indicesToDelete) > 0 {
		_, err = ctx.Storage.DeleteRows(op.DB, op.Table, indicesToDelete)
		if err != nil {
			return err
		}
	}
	return nil
}

type insertUndoHelper struct {
	cmd *InsertCommand
}

func (h *insertUndoHelper) buildRows(schema *storage.TableSchema, ctx *ExecutionContext) ([]storage.Row, error) {
	return h.cmd.buildRows(schema, ctx)
}

func undoUpdate(ctx *ExecutionContext, op txmanager.PendingOp) error {
	if op.DB == "" || op.Table == "" {
		return nil
	}

	oldRows, ok := op.OldRow.([]storage.Row)
	if !ok {
		return fmt.Errorf("undo update: invalid old row type")
	}
	if len(oldRows) == 0 {
		return nil
	}

	oldIndices, ok := op.Row.([]int)
	if !ok {
		return fmt.Errorf("undo update: invalid row indices type")
	}
	if len(oldIndices) != len(oldRows) {
		return nil
	}

	schema, err := ctx.Storage.GetTableSchema(op.DB, op.Table)
	if err != nil {
		return err
	}

	currentRows, err := ctx.Storage.ReadCurrentRows(op.DB, op.Table)
	if err != nil {
		return err
	}

	var restoreIndices []int
	var restoreUpdates []map[string]storage.Value
	for i, oldRow := range oldRows {
		origIdx := oldIndices[i]
		if origIdx >= len(currentRows) {
			continue
		}
		updates := make(map[string]storage.Value)
		for colIdx, col := range schema.Columns {
			if colIdx < len(oldRow) && colIdx < len(currentRows[origIdx]) {
				oldVal := oldRow[colIdx]
				newVal := currentRows[origIdx][colIdx]
				if !valuesEqual(oldVal, newVal) {
					updates[col.Name] = oldVal
				}
			}
		}
		if len(updates) > 0 {
			restoreIndices = append(restoreIndices, origIdx)
			restoreUpdates = append(restoreUpdates, updates)
		}
	}

	// Восстанавливаем каждую строку ЕЁ СОБСТВЕННЫМИ старыми значениями.
	// Нельзя сливать per-row карты в одну и применять ко всем индексам разом:
	// у строк разные старые значения, и слияние оставило бы одно (последнее)
	// и затёрло остальные — порча данных при откате.
	//
	// Идём по убыванию индекса: UpdateRows тумбстоунит старую версию и
	// дописывает новую в конец, из-за чего позиции строк ПОСЛЕ изменённой
	// сдвигаются. Обрабатывая от большего индекса к меньшему, мы не трогаем
	// ещё не восстановленные (меньшие) индексы — они остаются валидными.
	for i := len(restoreIndices) - 1; i >= 0; i-- {
		if _, err := ctx.Storage.UpdateRows(op.DB, op.Table, []int{restoreIndices[i]}, restoreUpdates[i]); err != nil {
			return err
		}
	}
	return nil
}

func undoDelete(ctx *ExecutionContext, op txmanager.PendingOp) error {
	if op.DB == "" || op.Table == "" {
		return nil
	}

	deletedRows, ok := op.Row.([]storage.Row)
	if !ok {
		return fmt.Errorf("undo delete: invalid deleted rows type")
	}
	if len(deletedRows) == 0 {
		return nil
	}

	_, err := ctx.Storage.InsertRows(op.DB, op.Table, deletedRows)
	return err
}

func undoTruncate(ctx *ExecutionContext, op txmanager.PendingOp) error {
	if op.DB == "" || op.Table == "" {
		return nil
	}

	truncatedRows, ok := op.Row.([]storage.Row)
	if !ok {
		return fmt.Errorf("undo truncate: invalid truncated rows type")
	}
	if len(truncatedRows) == 0 {
		return nil
	}

	_, err := ctx.Storage.InsertRows(op.DB, op.Table, truncatedRows)
	return err
}

func idxInSlice(idx int, s []int) bool {
	for _, v := range s {
		if v == idx {
			return true
		}
	}
	return false
}

type PrepareCommand struct {
	stmt *parser.PrepareStatement
}

func (c *PrepareCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if err := ctx.Session.SetPreparedStatement(c.stmt.Name, &PreparedStatement{
		Name:  c.stmt.Name,
		Query: c.stmt.Query,
	}); err != nil {
		return nil, err
	}
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' prepared.", c.stmt.Name),
	}, nil
}

type ExecutePreparedCommand struct {
	stmt *parser.ExecuteStatement
}

func (c *ExecutePreparedCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	ps, ok := ctx.Session.GetPreparedStatement(c.stmt.Name)
	if !ok {
		return nil, fmt.Errorf("prepared statement '%s' not found", c.stmt.Name)
	}

	boundStmt, err := BindParams(ps.Query, c.stmt.Params)
	if err != nil {
		return nil, err
	}

	// Plan cache intentionally disabled for prepared statements. QueryHash keys
	// on fully-bound SQL, so each parameter set produces a unique hash and the
	// cache never hits. Enabling it requires re-keying on (stmt-name, param-types)
	// across PlanCache.Get/Put, invalidation paths, and all callers.
	cmd, err := CommandFactory(boundStmt)
	if err != nil {
		return nil, err
	}
	return cmd.Execute(ctx)
}

type DeallocateCommand struct {
	stmt *parser.DeallocateStatement
}

func (c *DeallocateCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	ctx.Session.DeletePreparedStatement(c.stmt.Name)
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' deallocated.", c.stmt.Name),
	}, nil
}

func BindParams(stmt parser.Statement, params []parser.Value) (parser.Statement, error) {
	switch s := stmt.(type) {
	case *parser.SelectStatement:
		bound := *s
		bound.Where = bindExpr(s.Where, params)
		bound.Having = bindExpr(s.Having, params)
		if len(s.Joins) > 0 {
			bound.Joins = make([]parser.JoinClause, len(s.Joins))
			for i, join := range s.Joins {
				bound.Joins[i] = join
				bound.Joins[i].Condition = bindExpr(join.Condition, params)
			}
		}
		if s.LimitExpr != nil {
			bound.LimitExpr = bindExpr(s.LimitExpr, params)
		}
		if s.OffsetExpr != nil {
			bound.OffsetExpr = bindExpr(s.OffsetExpr, params)
		}
		return &bound, nil
	case *parser.UpdateStatement:
		bound := *s
		bound.Assignments = make([]parser.Assignment, len(s.Assignments))
		for i, a := range s.Assignments {
			bound.Assignments[i] = parser.Assignment{
				Column: a.Column,
				Value:  bindExpr(a.Value, params),
			}
		}
		bound.Where = bindExpr(s.Where, params)
		return &bound, nil
	case *parser.InsertStatement:
		bound := *s
		bound.Rows = make([][]parser.Expression, len(s.Rows))
		for i, row := range s.Rows {
			bound.Rows[i] = make([]parser.Expression, len(row))
			for j, expr := range row {
				bound.Rows[i][j] = bindExpr(expr, params)
			}
		}
		return &bound, nil
	case *parser.DeleteStatement:
		bound := *s
		bound.Where = bindExpr(s.Where, params)
		return &bound, nil
	}
	return nil, fmt.Errorf("EXECUTE not supported for %T", stmt)
}

func bindExpr(expr parser.Expression, params []parser.Value) parser.Expression {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *parser.ParamRef:
		if e.Index < 1 || e.Index > len(params) {
			return &parser.Value{Type: "null"}
		}
		p := params[e.Index-1]
		return &p
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{
			Left:     bindExpr(e.Left, params),
			Operator: e.Operator,
			Right:    bindExpr(e.Right, params),
		}
	case *parser.AndExpr:
		return &parser.AndExpr{
			Left:  bindExpr(e.Left, params),
			Right: bindExpr(e.Right, params),
		}
	case *parser.OrExpr:
		return &parser.OrExpr{
			Left:  bindExpr(e.Left, params),
			Right: bindExpr(e.Right, params),
		}
	case *parser.NotExpr:
		return &parser.NotExpr{
			Expr: bindExpr(e.Expr, params),
		}
	case *parser.Value:
		return e
	case *parser.ColumnRef:
		return e
	}
	return expr
}
