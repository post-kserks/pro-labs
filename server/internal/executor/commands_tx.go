package executor

// Транзакции и prepared statements: BEGIN/COMMIT/ROLLBACK,
// PREPARE/EXECUTE/DEALLOCATE.

import (
	"fmt"

	"vaultdb/internal/parser"
	"vaultdb/internal/txmanager"
)

type BeginCommand struct {
	stmt *parser.BeginStatement
}

func (c *BeginCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if ctx.Session.IsInTx() {
		return nil, fmt.Errorf("transaction already active; COMMIT or ROLLBACK first")
	}

	ctx.Session.ActiveTx = ctx.Session.TxManager.Begin()

	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Transaction %d started.", ctx.Session.ActiveTx.ID),
	}, nil
}

type CommitCommand struct {
	stmt *parser.CommitStatement
}

func (c *CommitCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	if !ctx.Session.IsInTx() {
		return nil, fmt.Errorf("no active transaction")
	}

	tx := ctx.Session.ActiveTx
	opsCount := len(tx.Ops)

	// Проверка конфликтов и применение выполняются под commit-локами только
	// тех таблиц, которые затронуты транзакцией: коммиты по разным таблицам
	// идут параллельно, а конфликт обнаруживается по версиям таблиц.
	var applied int
	var applyErr error
	commitErr := ctx.Session.TxManager.Commit(tx, func(ops []txmanager.PendingOp) error {
		applied, applyErr = applyOps(ctx, ops)
		return applyErr
	})

	if commitErr != nil && applyErr == nil {
		// Конфликт версий: операции не применялись, транзакция остаётся
		// активной — клиент решает, делать ли ROLLBACK.
		return nil, commitErr
	}
	if applyErr != nil {
		tx.Rollback()
		ctx.Session.ActiveTx = nil
		if applied > 0 {
			return nil, fmt.Errorf("commit failed after applying %d of %d operations; data may be partially updated: %w", applied, opsCount, applyErr)
		}
		return nil, fmt.Errorf("commit failed, no operations applied: %w", applyErr)
	}

	tx.Rollback()
	ctx.Session.ActiveTx = nil

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
	opsCount := len(ctx.Session.ActiveTx.Ops)
	ctx.Session.ActiveTx.Rollback()
	ctx.Session.ActiveTx = nil
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
			stmt := op.Payload.(*parser.InsertStatement)
			cmd := &InsertCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return i, err
			}
		case "update":
			stmt := op.Payload.(*parser.UpdateStatement)
			cmd := &UpdateCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return i, err
			}
		case "delete":
			stmt := op.Payload.(*parser.DeleteStatement)
			cmd := &DeleteCommand{stmt: stmt}
			if _, err := cmd.executeImmediate(ctx); err != nil {
				return i, err
			}
		}
	}
	return len(ops), nil
}

type PrepareCommand struct {
	stmt *parser.PrepareStatement
}

func (c *PrepareCommand) Execute(ctx *ExecutionContext) (*Result, error) {
	ctx.Session.PreparedStatements[c.stmt.Name] = &PreparedStatement{
		Name:  c.stmt.Name,
		Query: c.stmt.Query,
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
	ps, ok := ctx.Session.PreparedStatements[c.stmt.Name]
	if !ok {
		return nil, fmt.Errorf("prepared statement '%s' not found", c.stmt.Name)
	}

	boundStmt, err := bindParams(ps.Query, c.stmt.Params)
	if err != nil {
		return nil, err
	}

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
	delete(ctx.Session.PreparedStatements, c.stmt.Name)
	return &Result{
		Type:    "message",
		Message: fmt.Sprintf("Statement '%s' deallocated.", c.stmt.Name),
	}, nil
}

func bindParams(stmt parser.Statement, params []parser.Value) (parser.Statement, error) {
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
