package vm

import (
	"errors"
	"strings"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

func Compile(expr parser.Expression, schema *storage.TableSchema) ([]OpCode, error) {
	if expr == nil {
		return []OpCode{{Op: OpPushBool, Bool: true}}, nil
	}

	var insts []OpCode
	err := compileNode(expr, schema, &insts)
	if err != nil {
		return nil, err
	}
	return insts, nil
}

func compileNode(expr parser.Expression, schema *storage.TableSchema, insts *[]OpCode) error {
	switch e := expr.(type) {
	case parser.Value:
		switch strings.ToLower(e.Type) {
		case "int":
			*insts = append(*insts, OpCode{Op: OpPushInt, Int: e.IntVal})
		case "float":
			*insts = append(*insts, OpCode{Op: OpPushFloat, Float: e.FltVal})
		case "string":
			*insts = append(*insts, OpCode{Op: OpPushString, Str: e.StrVal})
		case "bool":
			*insts = append(*insts, OpCode{Op: OpPushBool, Bool: e.BoolVal})
		default:
			return errors.New("unsupported value type")
		}
	case *parser.Value:
		switch strings.ToLower(e.Type) {
		case "int":
			*insts = append(*insts, OpCode{Op: OpPushInt, Int: e.IntVal})
		case "float":
			*insts = append(*insts, OpCode{Op: OpPushFloat, Float: e.FltVal})
		case "string":
			*insts = append(*insts, OpCode{Op: OpPushString, Str: e.StrVal})
		case "bool":
			*insts = append(*insts, OpCode{Op: OpPushBool, Bool: e.BoolVal})
		default:
			return errors.New("unsupported value type")
		}
	case *parser.ColumnRef:
		idx := -1
		for i, col := range schema.Columns {
			if strings.EqualFold(col.Name, e.Name) {
				idx = i
				break
			}
		}
		if idx == -1 {
			return errors.New("column not found")
		}
		*insts = append(*insts, OpCode{Op: OpLoadColumn, ColIdx: idx})
	case *parser.BinaryExpr:
		err := compileNode(e.Left, schema, insts)
		if err != nil {
			return err
		}
		err = compileNode(e.Right, schema, insts)
		if err != nil {
			return err
		}
		switch e.Operator {
		case "+":
			*insts = append(*insts, OpCode{Op: OpAdd})
		case "-":
			*insts = append(*insts, OpCode{Op: OpSub})
		case "*":
			*insts = append(*insts, OpCode{Op: OpMul})
		case "/":
			*insts = append(*insts, OpCode{Op: OpDiv})
		case "=":
			*insts = append(*insts, OpCode{Op: OpEq})
		case "!=":
			*insts = append(*insts, OpCode{Op: OpNeq})
		case "<":
			*insts = append(*insts, OpCode{Op: OpLt})
		case ">":
			*insts = append(*insts, OpCode{Op: OpGt})
		case "<=":
			*insts = append(*insts, OpCode{Op: OpLte})
		case ">=":
			*insts = append(*insts, OpCode{Op: OpGte})
		default:
			return errors.New("unsupported binary operator: " + e.Operator)
		}
	case *parser.AndExpr:
		err := compileNode(e.Left, schema, insts)
		if err != nil {
			return err
		}
		err = compileNode(e.Right, schema, insts)
		if err != nil {
			return err
		}
		*insts = append(*insts, OpCode{Op: OpAnd})
	case *parser.OrExpr:
		err := compileNode(e.Left, schema, insts)
		if err != nil {
			return err
		}
		err = compileNode(e.Right, schema, insts)
		if err != nil {
			return err
		}
		*insts = append(*insts, OpCode{Op: OpOr})
	case *parser.NotExpr:
		err := compileNode(e.Expr, schema, insts)
		if err != nil {
			return err
		}
		*insts = append(*insts, OpCode{Op: OpNot})
	default:
		return errors.New("unsupported expression type for compilation")
	}
	return nil
}
