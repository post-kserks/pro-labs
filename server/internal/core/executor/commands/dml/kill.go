package dml

import (
	"fmt"

	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

func init() {
	types.RegisterCommand("KILL", func(stmt parser.Statement) types.Command {
		return &KillCommand{stmt: stmt.(*parser.KillStatement)}
	})
}

type KillCommand struct {
	stmt *parser.KillStatement
}

func (c *KillCommand) Execute(ctx *types.ExecutionContext) (*types.Result, error) {
	if types.KillSessionFunc == nil {
		return nil, fmt.Errorf("session kill not available")
	}
	killed := types.KillSessionFunc(c.stmt.SessionID)
	if !killed {
		return nil, fmt.Errorf("session not found")
	}
	row := []string{fmt.Sprintf("killed session %d", c.stmt.SessionID)}
	return &types.Result{
		Type:    "rows",
		Columns: []string{"status"},
		Rows:    [][]string{row},
		Schema: &storage.TableSchema{
			Name: "kill",
			Columns: []storage.ColumnSchema{
				{Name: "status", Type: "TEXT"},
			},
		},
		Affected: 1,
	}, nil
}
