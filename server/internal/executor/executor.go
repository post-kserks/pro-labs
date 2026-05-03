package executor

import (
	"fmt"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// Command is the Command pattern abstraction.
type Command interface {
	Execute(ctx *ExecutionContext) (*Result, error)
}

// Result is a uniform executor output for all statements.
type Result struct {
	Type     string
	Columns  []string
	Rows     [][]string
	Affected int
	Message  string
}

// ExecutionContext carries mutable session state and dependencies.
type ExecutionContext struct {
	Storage   storage.StorageEngine
	CurrentDB *string
}

type Executor struct {
	storage storage.StorageEngine
}

func New(store storage.StorageEngine) *Executor {
	return &Executor{storage: store}
}

func (e *Executor) Run(stmt parser.Statement, currentDB *string) (*Result, error) {
	cmd, err := CommandFactory(stmt)
	if err != nil {
		return nil, err
	}

	ctx := &ExecutionContext{Storage: e.storage, CurrentDB: currentDB}
	return cmd.Execute(ctx)
}

func CommandFactory(stmt parser.Statement) (Command, error) {
	switch s := stmt.(type) {
	case *parser.CreateDatabaseStatement:
		return &CreateDatabaseCommand{stmt: s}, nil
	case *parser.DropDatabaseStatement:
		return &DropDatabaseCommand{stmt: s}, nil
	case *parser.UseDatabaseStatement:
		return &UseDatabaseCommand{stmt: s}, nil
	case *parser.ShowDatabasesStatement:
		return &ShowDatabasesCommand{stmt: s}, nil
	case *parser.ShowTablesStatement:
		return &ShowTablesCommand{stmt: s}, nil
	case *parser.DescribeTableStatement:
		return &DescribeTableCommand{stmt: s}, nil
	case *parser.CreateTableStatement:
		return &CreateTableCommand{stmt: s}, nil
	case *parser.DropTableStatement:
		return &DropTableCommand{stmt: s}, nil
	case *parser.SelectStatement:
		return &SelectCommand{stmt: s}, nil
	case *parser.InsertStatement:
		return &InsertCommand{stmt: s}, nil
	case *parser.UpdateStatement:
		return &UpdateCommand{stmt: s}, nil
	case *parser.DeleteStatement:
		return &DeleteCommand{stmt: s}, nil
	default:
		return nil, fmt.Errorf("unknown statement type: %T", stmt)
	}
}
