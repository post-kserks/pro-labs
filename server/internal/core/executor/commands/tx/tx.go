package tx

// Transactions and prepared statements: BEGIN/COMMIT/ROLLBACK,
// PREPARE/EXECUTE/DEALLOCATE, SAVEPOINT.

import (
	"fmt"
	"log/slog"

	"vaultdb/internal/core/executor/commands/dml"
	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/core/wal"
)

func init() {
	types.RegisterCommand("BEGIN", func(stmt parser.Statement) types.Command {
		return &BeginCommand{stmt: stmt.(*parser.BeginStatement)}
	})
	types.RegisterCommand("COMMIT", func(stmt parser.Statement) types.Command {
		return &CommitCommand{stmt: stmt.(*parser.CommitStatement)}
	})
	types.RegisterCommand("ROLLBACK", func(stmt parser.Statement) types.Command {
		return &RollbackCommand{stmt: stmt.(*parser.RollbackStatement)}
	})
	types.RegisterCommand("SAVEPOINT", func(stmt parser.Statement) types.Command {
		return &SavepointCommand{stmt: stmt.(*parser.SavepointStatement)}
	})
	types.RegisterCommand("ROLLBACK_TO_SAVEPOINT", func(stmt parser.Statement) types.Command {
		return &RollbackToSavepointCommand{stmt: stmt.(*parser.RollbackToSavepointStatement)}
	})
	types.RegisterCommand("RELEASE_SAVEPOINT", func(stmt parser.Statement) types.Command {
		return &ReleaseSavepointCommand{stmt: stmt.(*parser.ReleaseSavepointStatement)}
	})
	types.RegisterCommand("PREPARE", func(stmt parser.Statement) types.Command {
		return &PrepareCommand{stmt: stmt.(*parser.PrepareStatement)}
	})
	types.RegisterCommand("EXECUTE", func(stmt parser.Statement) types.Command {
		return &ExecutePreparedCommand{stmt: stmt.(*parser.ExecuteStatement)}
	})
	types.RegisterCommand("DEALLOCATE", func(stmt parser.Statement) types.Command {
		return &DeallocateCommand{stmt: stmt.(*parser.DeallocateStatement)}
	})
}

type BeginCommand struct {
	stmt *parser.BeginStatement
}

func (c *BeginCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if ctx.Session.IsInTx() {
		return nil, fmt.Errorf("transaction already active; COMMIT or ROLLBACK first")
	}

	ctx.Session.SetActiveTx(ctx.Session.GetTxManager().Begin())

	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d started.", ctx.Session.GetActiveTx().ID),
	}, nil
}

type CommitCommand struct {
	stmt *parser.CommitStatement
}

func (c *CommitCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("no active transaction")
	}

	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}

	ops, err := tx.ReadOps()
	if err != nil {
		return nil, fmt.Errorf("read transaction ops: %w", err)
	}
	opsCount := len(ops)

	var applied int
	var applyErr error
	commitErr := ctx.Session.GetTxManager().Commit(tx, func(pendingOps []txmanager.PendingOp) error {
		ctx.InCommitApply = true
		defer func() { ctx.InCommitApply = false }()
		applied, applyErr = applyOps(ctx, pendingOps)
		return applyErr
	})

	if commitErr != nil && applyErr == nil {
		return nil, commitErr
	}
	if applyErr != nil {
		undoOps := ops[:applied]
		if undoErr := undoAppliedOps(ctx, undoOps); undoErr != nil {
			slog.Error("could not undo partial commit, data may be inconsistent",
				"xid", tx.ID, "error", undoErr)
		}
		tx.Rollback(ctx.Storage)
		ctx.Session.ClearActiveTx()
		if applied > 0 {
			return nil, fmt.Errorf("commit failed after applying %d of %d operations; data may be partially updated: %w", applied, opsCount, applyErr)
		}
		return nil, fmt.Errorf("commit failed, no operations applied: %w", applyErr)
	}

	if ctx.WAL != nil {
		var err error
		if ctx.Session.GetVariable("synchronous_commit") == "off" {
			_, err = ctx.WAL.AppendWithWriteBehind(tx.ID, wal.OpCommit, nil)
		} else {
			_, err = ctx.WAL.AppendWithTx(tx.ID, wal.OpCommit, nil)
		}
		if err != nil {
			undoAppliedOps(ctx, ops)
			tx.Rollback(ctx.Storage)
			ctx.Session.ClearActiveTx()
			return nil, fmt.Errorf("wal commit failed, transaction rolled back: %w", err)
		}
	}

	ctx.Storage.ReleaseRowLocks(tx.ID)
	tx.Rollback(ctx.Storage)
	ctx.Session.ClearActiveTx()

	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d committed (%d operations).", tx.ID, opsCount),
	}, nil
}

type RollbackCommand struct {
	stmt *parser.RollbackStatement
}

func (c *RollbackCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
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

	ctx.Storage.ReleaseRowLocks(tx.ID)
	tx.Rollback(ctx.Storage)
	ctx.Session.ClearActiveTx()
	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction rolled back (%d operations discarded).", opsCount),
	}, nil
}

type SavepointCommand struct {
	stmt *parser.SavepointStatement
}

func (c *SavepointCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	tx.Savepoint(c.stmt.Name)
	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Savepoint '%s' established.", c.stmt.Name),
	}, nil
}

type RollbackToSavepointCommand struct {
	stmt *parser.RollbackToSavepointStatement
}

func (c *RollbackToSavepointCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("ROLLBACK TO SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	if err := tx.RollbackToSavepoint(c.stmt.Name); err != nil {
		return nil, err
	}
	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Rolled back to savepoint '%s'.", c.stmt.Name),
	}, nil
}

type ReleaseSavepointCommand struct {
	stmt *parser.ReleaseSavepointStatement
}

func (c *ReleaseSavepointCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("RELEASE SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	if !tx.ReleaseSavepoint(c.stmt.Name) {
		return nil, fmt.Errorf("savepoint %q does not exist", c.stmt.Name)
	}
	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Savepoint '%s' released.", c.stmt.Name),
	}, nil
}

type PrepareCommand struct {
	stmt *parser.PrepareStatement
}

func (c *PrepareCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if err := ctx.Session.SetPreparedStatement(c.stmt.Name, &types.PreparedStatement{
		Name:  c.stmt.Name,
		Query: c.stmt.Query,
	}); err != nil {
		return nil, err
	}
	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' prepared.", c.stmt.Name),
	}, nil
}

type ExecutePreparedCommand struct {
	stmt *parser.ExecuteStatement
}

func (c *ExecutePreparedCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	ps, ok := ctx.Session.GetPreparedStatement(c.stmt.Name)
	if !ok {
		return nil, fmt.Errorf("prepared statement '%s' not found", c.stmt.Name)
	}

	boundStmt, err := types.BindParams(ps.Query, c.stmt.Params)
	if err != nil {
		return nil, err
	}

	return ctx.RunSubquery.RunSubquery(ctx, boundStmt)
}

type DeallocateCommand struct {
	stmt *parser.DeallocateStatement
}

func (c *DeallocateCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	ctx.Session.DeletePreparedStatement(c.stmt.Name)
	return &types.Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' deallocated.", c.stmt.Name),
	}, nil
}

// applyOps applies buffered operations in order and reports how many were
// applied before the first failure.
func applyOps(ctx *types.ExecutionContext, ops []txmanager.PendingOp) (int, error) {
	for i, op := range ops {
		switch op.Type {
		case "insert":
			stmt, ok := op.Payload.(*parser.InsertStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid insert payload type", i)
			}
			cmd := &dml.InsertCommand{}
			cmd.SetStmt(stmt)
			if _, err := cmd.ExecuteImmediate(ctx); err != nil {
				return i, err
			}
		case "update":
			stmt, ok := op.Payload.(*parser.UpdateStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid update payload type", i)
			}
			cmd := &dml.UpdateCommand{}
			cmd.SetStmt(stmt)
			if _, err := cmd.ExecuteImmediate(ctx); err != nil {
				return i, err
			}
		case "delete":
			stmt, ok := op.Payload.(*parser.DeleteStatement)
			if !ok {
				return i, fmt.Errorf("op %d: invalid delete payload type", i)
			}
			cmd := &dml.DeleteCommand{}
			cmd.SetStmt(stmt)
			if _, err := cmd.ExecuteImmediate(ctx); err != nil {
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
			ops[i].Row = rows
			types.NotifyBroadcaster(ctx, op.DB, op.Table)
			ctx.Session.InvalidateResultCache(op.Table)
			_ = stmt
		}
	}
	return len(ops), nil
}

// undoAppliedOps rolls back already applied operations in reverse order.
func undoAppliedOps(ctx *types.ExecutionContext, ops []txmanager.PendingOp) error {
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		var undoErr error
		switch op.Type {
		case "insert":
			undoErr = UndoInsert(ctx, op)
		case "update":
			undoErr = UndoUpdate(ctx, op)
		case "delete":
			undoErr = UndoDelete(ctx, op)
		case "truncate":
			undoErr = UndoTruncate(ctx, op)
		}
		if undoErr != nil {
			return fmt.Errorf("undo op %d (%s): %w", i, op.Type, undoErr)
		}
	}
	return nil
}

func UndoInsert(ctx *types.ExecutionContext, op txmanager.PendingOp) error {
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
	cmd := &dml.InsertCommand{}
	cmd.SetStmt(stmt)
	rowsToUndo, err := cmd.BuildRows(schema, ctx)
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
			if types.RowsEqual(insertedRow, currentRow) {
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

func UndoUpdate(ctx *types.ExecutionContext, op txmanager.PendingOp) error {
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
				if !types.ValuesEqual(oldVal, newVal) {
					updates[col.Name] = oldVal
				}
			}
		}
		if len(updates) > 0 {
			restoreIndices = append(restoreIndices, origIdx)
			restoreUpdates = append(restoreUpdates, updates)
		}
	}

	for i := len(restoreIndices) - 1; i >= 0; i-- {
		if _, err := ctx.Storage.UpdateRows(op.DB, op.Table, []int{restoreIndices[i]}, restoreUpdates[i]); err != nil {
			return err
		}
	}
	return nil
}

func UndoDelete(ctx *types.ExecutionContext, op txmanager.PendingOp) error {
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

func UndoTruncate(ctx *types.ExecutionContext, op txmanager.PendingOp) error {
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
