package ddl

import (
	"fmt"
	"strings"

	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
)

func init() {
	types.RegisterCommand("SET_VARIABLE", func(stmt parser.Statement) types.Command {
		return &SetVariableCommand{stmt: stmt.(*parser.SetVariableStatement)}
	})
}

type SetVariableCommand struct {
	stmt *parser.SetVariableStatement
}

func (c *SetVariableCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	// Set session variable
	varName := strings.ToLower(c.stmt.VariableName)
	val := strings.ToLower(c.stmt.Value)

	ctx.Session.SetVariable(varName, val)

	return &types.Result{
		Message: fmt.Sprintf("SET %s = %s", varName, val),
	}, nil
}
