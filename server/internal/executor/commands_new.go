package executor

import (
	"fmt"
	"vaultdb/internal/parser"
)

// CTECommand executes CTE (WITH clause).
type CTECommand struct {
	stmt *parser.CTEStatement
}

func (c *CTECommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return ExecuteCTEStatement(c.stmt, ctx)
}

// SavepointCommand executes SAVEPOINT.
type SavepointCommand struct {
	stmt *parser.SavepointStatement
}

func (c *SavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("SAVEPOINT can only be used inside a transaction")
	}
	tx := ctx.Session.GetActiveTx()
	if tx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	tx.Savepoint(c.stmt.Name)
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Savepoint '%s' established.", c.stmt.Name),
	}, nil
}

// RollbackToSavepointCommand executes ROLLBACK TO SAVEPOINT.
type RollbackToSavepointCommand struct {
	stmt *parser.RollbackToSavepointStatement
}

func (c *RollbackToSavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
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
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Rolled back to savepoint '%s'.", c.stmt.Name),
	}, nil
}

// ReleaseSavepointCommand executes RELEASE SAVEPOINT.
type ReleaseSavepointCommand struct {
	stmt *parser.ReleaseSavepointStatement
}

func (c *ReleaseSavepointCommand) Execute(ctx *ExecutionContext) (*Result, error) {
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
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Savepoint '%s' released.", c.stmt.Name),
	}, nil
}
