package executor

import (
	"vaultdb/internal/core/parser"
)

// CTECommand executes CTE (WITH clause).
type CTECommand struct {
	stmt *parser.CTEStatement
}

func (c *CTECommand) Execute(ctx *ExecutionContext) (*Result, error) {
	return ExecuteCTEStatement(c.stmt, ctx)
}
