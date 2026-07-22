package txmanager

import (
	"encoding/json"
	"errors"
	"fmt"
)

// DDLOpType represents the type of a DDL operation.
type DDLOpType string

const (
	OpCreateTable DDLOpType = "CREATE_TABLE"
	OpDropTable   DDLOpType = "DROP_TABLE"
	OpAlterTable  DDLOpType = "ALTER_TABLE"
)

// DDLUndoOp stores information required to undo a DDL operation.
type DDLUndoOp struct {
	Type      DDLOpType
	DBName    string
	TableName string
	OldSchema json.RawMessage
}

// Catalog defines the contract for catalog operations during rollback.
type Catalog interface {
	DropTable(dbName, tableName string) error
	CreateTable(dbName, tableName string, schema json.RawMessage) error
	AlterTable(dbName, tableName string, schema json.RawMessage) error
}

// RevertDDLOp reverts a catalog state based on the provided DDLUndoOp.
// It accepts catalog as interface{} to match the required signature and performs a type assertion.
func RevertDDLOp(op DDLUndoOp, catalog interface{}) error {
	cat, ok := catalog.(Catalog)
	if !ok {
		return errors.New("catalog does not implement the required Catalog interface")
	}

	switch op.Type {
	case OpCreateTable:
		// To undo a CREATE TABLE, we drop the newly created table.
		return cat.DropTable(op.DBName, op.TableName)
	case OpDropTable:
		// To undo a DROP TABLE, we recreate it using the old schema.
		if len(op.OldSchema) == 0 {
			return errors.New("missing old schema for DROP_TABLE revert")
		}
		return cat.CreateTable(op.DBName, op.TableName, op.OldSchema)
	case OpAlterTable:
		// To undo an ALTER TABLE, we revert the schema to its previous state.
		if len(op.OldSchema) == 0 {
			return errors.New("missing old schema for ALTER_TABLE revert")
		}
		return cat.AlterTable(op.DBName, op.TableName, op.OldSchema)
	default:
		return fmt.Errorf("unsupported DDL operation type: %s", op.Type)
	}
}
