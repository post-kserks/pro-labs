package vm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unsafe"

	"vaultdb/internal/core/storage"
)

type Op byte

const (
	OpPushInt Op = iota
	OpPushFloat
	OpPushString
	OpPushBool
	OpLoadColumn
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpEq
	OpNeq
	OpLt
	OpGt
	OpLte
	OpGte
	OpAnd
	OpOr
	OpNot
	OpReturn
)

type OpCode struct {
	Op     Op
	Int    int64
	Float  float64
	Str    string
	Bool   bool
	ColIdx int
}

type ValType byte

const (
	ValInt ValType = iota
	ValFloat
	ValString
	ValBool
	ValNull
)

type Value struct {
	Type  ValType
	Int   int64
	Float float64
	Str   string
	Bool  bool
}

func (v Value) ToInterface() interface{} {
	switch v.Type {
	case ValInt:
		return v.Int
	case ValFloat:
		return v.Float
	case ValString:
		return v.Str
	case ValBool:
		return v.Bool
	default:
		return nil
	}
}

func ExecuteVM(instructions []OpCode, row storage.Row) (interface{}, error) {
	var stack [8]Value
	sp := 0

	for _, inst := range instructions {
		switch inst.Op {
		case OpPushInt:
			stack[sp] = Value{Type: ValInt, Int: inst.Int}
			sp++
		case OpPushFloat:
			stack[sp] = Value{Type: ValFloat, Float: inst.Float}
			sp++
		case OpPushString:
			stack[sp] = Value{Type: ValString, Str: inst.Str}
			sp++
		case OpPushBool:
			stack[sp] = Value{Type: ValBool, Bool: inst.Bool}
			sp++
		case OpLoadColumn:
			if inst.ColIdx < 0 || inst.ColIdx >= len(row) {
				return nil, errors.New("column index out of range")
			}
			val := row[inst.ColIdx]
			switch v := val.(type) {
			case int:
				stack[sp] = Value{Type: ValInt, Int: int64(v)}
			case int64:
				stack[sp] = Value{Type: ValInt, Int: v}
			case float64:
				stack[sp] = Value{Type: ValFloat, Float: v}
			case string:
				stack[sp] = Value{Type: ValString, Str: v}
			case bool:
				stack[sp] = Value{Type: ValBool, Bool: v}
			case nil:
				stack[sp] = Value{Type: ValNull}
			default:
				return nil, fmt.Errorf("unsupported column type: %T", val)
			}
			sp++
		case OpAdd, OpSub, OpMul, OpDiv:
			if sp < 2 {
				return nil, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2

			if left.Type == ValInt && right.Type == ValInt {
				var res int64
				switch inst.Op {
				case OpAdd:
					res = left.Int + right.Int
				case OpSub:
					res = left.Int - right.Int
				case OpMul:
					res = left.Int * right.Int
				case OpDiv:
					if right.Int == 0 {
						return nil, errors.New("division by zero")
					}
					res = left.Int / right.Int
				}
				stack[sp] = Value{Type: ValInt, Int: res}
			} else if left.Type == ValFloat && right.Type == ValFloat {
				var res float64
				switch inst.Op {
				case OpAdd:
					res = left.Float + right.Float
				case OpSub:
					res = left.Float - right.Float
				case OpMul:
					res = left.Float * right.Float
				case OpDiv:
					if right.Float == 0 {
						return nil, errors.New("division by zero")
					}
					res = left.Float / right.Float
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else if left.Type == ValInt && right.Type == ValFloat {
				var res float64
				lfloat := float64(left.Int)
				switch inst.Op {
				case OpAdd:
					res = lfloat + right.Float
				case OpSub:
					res = lfloat - right.Float
				case OpMul:
					res = lfloat * right.Float
				case OpDiv:
					if right.Float == 0 {
						return nil, errors.New("division by zero")
					}
					res = lfloat / right.Float
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else if left.Type == ValFloat && right.Type == ValInt {
				var res float64
				rfloat := float64(right.Int)
				switch inst.Op {
				case OpAdd:
					res = left.Float + rfloat
				case OpSub:
					res = left.Float - rfloat
				case OpMul:
					res = left.Float * rfloat
				case OpDiv:
					if rfloat == 0 {
						return nil, errors.New("division by zero")
					}
					res = left.Float / rfloat
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else {
				return nil, errors.New("type mismatch in arithmetic operation")
			}
			sp++
		case OpEq, OpNeq, OpLt, OpGt, OpLte, OpGte:
			if sp < 2 {
				return nil, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2

			var res bool
			if left.Type == ValInt && right.Type == ValInt {
				switch inst.Op {
				case OpEq:
					res = left.Int == right.Int
				case OpNeq:
					res = left.Int != right.Int
				case OpLt:
					res = left.Int < right.Int
				case OpGt:
					res = left.Int > right.Int
				case OpLte:
					res = left.Int <= right.Int
				case OpGte:
					res = left.Int >= right.Int
				}
			} else if left.Type == ValFloat && right.Type == ValFloat {
				switch inst.Op {
				case OpEq:
					res = left.Float == right.Float
				case OpNeq:
					res = left.Float != right.Float
				case OpLt:
					res = left.Float < right.Float
				case OpGt:
					res = left.Float > right.Float
				case OpLte:
					res = left.Float <= right.Float
				case OpGte:
					res = left.Float >= right.Float
				}
			} else if left.Type == ValInt && right.Type == ValFloat {
				lfloat := float64(left.Int)
				switch inst.Op {
				case OpEq:
					res = lfloat == right.Float
				case OpNeq:
					res = lfloat != right.Float
				case OpLt:
					res = lfloat < right.Float
				case OpGt:
					res = lfloat > right.Float
				case OpLte:
					res = lfloat <= right.Float
				case OpGte:
					res = lfloat >= right.Float
				}
			} else if left.Type == ValFloat && right.Type == ValInt {
				rfloat := float64(right.Int)
				switch inst.Op {
				case OpEq:
					res = left.Float == rfloat
				case OpNeq:
					res = left.Float != rfloat
				case OpLt:
					res = left.Float < rfloat
				case OpGt:
					res = left.Float > rfloat
				case OpLte:
					res = left.Float <= rfloat
				case OpGte:
					res = left.Float >= rfloat
				}
			} else if left.Type == ValString && right.Type == ValString {
				switch inst.Op {
				case OpEq:
					res = left.Str == right.Str
				case OpNeq:
					res = left.Str != right.Str
				case OpLt:
					res = left.Str < right.Str
				case OpGt:
					res = left.Str > right.Str
				case OpLte:
					res = left.Str <= right.Str
				case OpGte:
					res = left.Str >= right.Str
				}
			} else if left.Type == ValBool && right.Type == ValBool && (inst.Op == OpEq || inst.Op == OpNeq) {
				switch inst.Op {
				case OpEq:
					res = left.Bool == right.Bool
				case OpNeq:
					res = left.Bool != right.Bool
				}
			} else {
				if inst.Op == OpEq {
					res = false
				} else if inst.Op == OpNeq {
					res = true
				} else {
					return nil, errors.New("type mismatch in comparison")
				}
			}
			stack[sp] = Value{Type: ValBool, Bool: res}
			sp++
		case OpAnd, OpOr:
			if sp < 2 {
				return nil, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2
			if left.Type != ValBool || right.Type != ValBool {
				return nil, errors.New("type mismatch in logical operation")
			}
			var res bool
			if inst.Op == OpAnd {
				res = left.Bool && right.Bool
			} else {
				res = left.Bool || right.Bool
			}
			stack[sp] = Value{Type: ValBool, Bool: res}
			sp++
		case OpNot:
			if sp < 1 {
				return nil, errors.New("stack underflow")
			}
			val := stack[sp-1]
			sp--
			if val.Type != ValBool {
				return nil, errors.New("type mismatch in logical NOT")
			}
			stack[sp] = Value{Type: ValBool, Bool: !val.Bool}
			sp++
		case OpReturn:
			if sp < 1 {
				return nil, errors.New("stack underflow")
			}
			return stack[sp-1].ToInterface(), nil
		default:
			return nil, fmt.Errorf("unknown opcode: %v", inst.Op)
		}
	}

	if sp > 0 {
		return stack[sp-1].ToInterface(), nil
	}
	return nil, nil
}

// ExecuteVMBool executes VM instructions against []any row without boxing the return boolean.
func ExecuteVMBool(instructions []OpCode, row storage.Row) (bool, error) {
	var stack [8]Value
	sp := 0

	for _, inst := range instructions {
		switch inst.Op {
		case OpPushInt:
			stack[sp] = Value{Type: ValInt, Int: inst.Int}
			sp++
		case OpPushFloat:
			stack[sp] = Value{Type: ValFloat, Float: inst.Float}
			sp++
		case OpPushString:
			stack[sp] = Value{Type: ValString, Str: inst.Str}
			sp++
		case OpPushBool:
			stack[sp] = Value{Type: ValBool, Bool: inst.Bool}
			sp++
		case OpLoadColumn:
			if inst.ColIdx < 0 || inst.ColIdx >= len(row) {
				return false, errors.New("column index out of range")
			}
			val := row[inst.ColIdx]
			switch v := val.(type) {
			case int:
				stack[sp] = Value{Type: ValInt, Int: int64(v)}
			case int64:
				stack[sp] = Value{Type: ValInt, Int: v}
			case float64:
				stack[sp] = Value{Type: ValFloat, Float: v}
			case string:
				stack[sp] = Value{Type: ValString, Str: v}
			case bool:
				stack[sp] = Value{Type: ValBool, Bool: v}
			case nil:
				stack[sp] = Value{Type: ValNull}
			default:
				return false, fmt.Errorf("unsupported column type: %T", val)
			}
			sp++
		case OpAdd, OpSub, OpMul, OpDiv:
			if sp < 2 {
				return false, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2

			if left.Type == ValInt && right.Type == ValInt {
				var res int64
				switch inst.Op {
				case OpAdd:
					res = left.Int + right.Int
				case OpSub:
					res = left.Int - right.Int
				case OpMul:
					res = left.Int * right.Int
				case OpDiv:
					if right.Int == 0 {
						return false, errors.New("division by zero")
					}
					res = left.Int / right.Int
				}
				stack[sp] = Value{Type: ValInt, Int: res}
			} else if left.Type == ValFloat && right.Type == ValFloat {
				var res float64
				switch inst.Op {
				case OpAdd:
					res = left.Float + right.Float
				case OpSub:
					res = left.Float - right.Float
				case OpMul:
					res = left.Float * right.Float
				case OpDiv:
					if right.Float == 0 {
						return false, errors.New("division by zero")
					}
					res = left.Float / right.Float
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else if left.Type == ValInt && right.Type == ValFloat {
				var res float64
				lfloat := float64(left.Int)
				switch inst.Op {
				case OpAdd:
					res = lfloat + right.Float
				case OpSub:
					res = lfloat - right.Float
				case OpMul:
					res = lfloat * right.Float
				case OpDiv:
					if right.Float == 0 {
						return false, errors.New("division by zero")
					}
					res = lfloat / right.Float
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else if left.Type == ValFloat && right.Type == ValInt {
				var res float64
				rfloat := float64(right.Int)
				switch inst.Op {
				case OpAdd:
					res = left.Float + rfloat
				case OpSub:
					res = left.Float - rfloat
				case OpMul:
					res = left.Float * rfloat
				case OpDiv:
					if rfloat == 0 {
						return false, errors.New("division by zero")
					}
					res = left.Float / rfloat
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else {
				return false, errors.New("type mismatch in arithmetic operation")
			}
			sp++
		case OpEq, OpNeq, OpLt, OpGt, OpLte, OpGte:
			if sp < 2 {
				return false, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2

			var res bool
			if left.Type == ValInt && right.Type == ValInt {
				switch inst.Op {
				case OpEq:
					res = left.Int == right.Int
				case OpNeq:
					res = left.Int != right.Int
				case OpLt:
					res = left.Int < right.Int
				case OpGt:
					res = left.Int > right.Int
				case OpLte:
					res = left.Int <= right.Int
				case OpGte:
					res = left.Int >= right.Int
				}
			} else if left.Type == ValFloat && right.Type == ValFloat {
				switch inst.Op {
				case OpEq:
					res = left.Float == right.Float
				case OpNeq:
					res = left.Float != right.Float
				case OpLt:
					res = left.Float < right.Float
				case OpGt:
					res = left.Float > right.Float
				case OpLte:
					res = left.Float <= right.Float
				case OpGte:
					res = left.Float >= right.Float
				}
			} else if left.Type == ValInt && right.Type == ValFloat {
				lfloat := float64(left.Int)
				switch inst.Op {
				case OpEq:
					res = lfloat == right.Float
				case OpNeq:
					res = lfloat != right.Float
				case OpLt:
					res = lfloat < right.Float
				case OpGt:
					res = lfloat > right.Float
				case OpLte:
					res = lfloat <= right.Float
				case OpGte:
					res = lfloat >= right.Float
				}
			} else if left.Type == ValFloat && right.Type == ValInt {
				rfloat := float64(right.Int)
				switch inst.Op {
				case OpEq:
					res = left.Float == rfloat
				case OpNeq:
					res = left.Float != rfloat
				case OpLt:
					res = left.Float < rfloat
				case OpGt:
					res = left.Float > rfloat
				case OpLte:
					res = left.Float <= rfloat
				case OpGte:
					res = left.Float >= rfloat
				}
			} else if left.Type == ValString && right.Type == ValString {
				switch inst.Op {
				case OpEq:
					res = left.Str == right.Str
				case OpNeq:
					res = left.Str != right.Str
				case OpLt:
					res = left.Str < right.Str
				case OpGt:
					res = left.Str > right.Str
				case OpLte:
					res = left.Str <= right.Str
				case OpGte:
					res = left.Str >= right.Str
				}
			} else if left.Type == ValBool && right.Type == ValBool && (inst.Op == OpEq || inst.Op == OpNeq) {
				switch inst.Op {
				case OpEq:
					res = left.Bool == right.Bool
				case OpNeq:
					res = left.Bool != right.Bool
				}
			} else {
				if inst.Op == OpEq {
					res = false
				} else if inst.Op == OpNeq {
					res = true
				} else {
					return false, errors.New("type mismatch in comparison")
				}
			}
			stack[sp] = Value{Type: ValBool, Bool: res}
			sp++
		case OpAnd, OpOr:
			if sp < 2 {
				return false, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2
			if left.Type != ValBool || right.Type != ValBool {
				return false, errors.New("type mismatch in logical operation")
			}
			var res bool
			if inst.Op == OpAnd {
				res = left.Bool && right.Bool
			} else {
				res = left.Bool || right.Bool
			}
			stack[sp] = Value{Type: ValBool, Bool: res}
			sp++
		case OpNot:
			if sp < 1 {
				return false, errors.New("stack underflow")
			}
			val := stack[sp-1]
			sp--
			if val.Type != ValBool {
				return false, errors.New("type mismatch in logical NOT")
			}
			stack[sp] = Value{Type: ValBool, Bool: !val.Bool}
			sp++
		case OpReturn:
			if sp < 1 {
				return false, errors.New("stack underflow")
			}
			return stack[sp-1].Bool, nil
		default:
			return false, fmt.Errorf("unknown opcode: %v", inst.Op)
		}
	}

	if sp > 0 {
		return stack[sp-1].Bool, nil
	}
	return false, nil
}

// ExecuteVMRaw executes VM instructions directly against raw page tuple bytes without decoding.
func ExecuteVMRaw(instructions []OpCode, rawTuple []byte) (bool, error) {
	var stack [8]Value
	sp := 0

	for _, inst := range instructions {
		switch inst.Op {
		case OpPushInt:
			stack[sp] = Value{Type: ValInt, Int: inst.Int}
			sp++
		case OpPushFloat:
			stack[sp] = Value{Type: ValFloat, Float: inst.Float}
			sp++
		case OpPushString:
			stack[sp] = Value{Type: ValString, Str: inst.Str}
			sp++
		case OpPushBool:
			stack[sp] = Value{Type: ValBool, Bool: inst.Bool}
			sp++
		case OpLoadColumn:
			if len(rawTuple) < 18 {
				return false, errors.New("tuple too short")
			}
			colCount := binary.LittleEndian.Uint16(rawTuple[16:18])
			if inst.ColIdx < 0 || inst.ColIdx >= int(colCount) {
				return false, errors.New("column index out of range")
			}
			offIdx := 18 + inst.ColIdx*2
			if len(rawTuple) < offIdx+2 {
				return false, errors.New("tuple header truncated")
			}
			valOffset := binary.LittleEndian.Uint16(rawTuple[offIdx : offIdx+2])
			var nextOffset uint16
			if inst.ColIdx+1 < int(colCount) {
				nextOffIdx := 18 + (inst.ColIdx+1)*2
				if len(rawTuple) < nextOffIdx+2 {
					return false, errors.New("tuple header truncated")
				}
				nextOffset = binary.LittleEndian.Uint16(rawTuple[nextOffIdx : nextOffIdx+2])
			} else {
				nextOffset = uint16(len(rawTuple))
			}
			if int(valOffset) > len(rawTuple) || int(nextOffset) > len(rawTuple) || valOffset > nextOffset {
				return false, errors.New("corrupted tuple column offsets")
			}
			valBytes := rawTuple[valOffset:nextOffset]
			if len(valBytes) == 0 || valBytes[0] == 0x00 {
				stack[sp] = Value{Type: ValNull}
			} else {
				switch valBytes[0] {
				case 'i':
					if len(valBytes) < 9 {
						return false, errors.New("corrupted int value")
					}
					stack[sp] = Value{Type: ValInt, Int: int64(binary.LittleEndian.Uint64(valBytes[1:9]))}
				case 'f':
					if len(valBytes) < 9 {
						return false, errors.New("corrupted float value")
					}
					stack[sp] = Value{Type: ValFloat, Float: math.Float64frombits(binary.LittleEndian.Uint64(valBytes[1:9]))}
				case 'b':
					if len(valBytes) < 2 {
						return false, errors.New("corrupted bool value")
					}
					stack[sp] = Value{Type: ValBool, Bool: valBytes[1] == 1}
				case 's':
					if len(valBytes) < 3 {
						return false, errors.New("corrupted string header")
					}
					strLen := int(binary.LittleEndian.Uint16(valBytes[1:3]))
					if len(valBytes) < 3+strLen {
						return false, errors.New("corrupted string bytes")
					}
					stack[sp] = Value{Type: ValString, Str: unsafe.String(unsafe.SliceData(valBytes[3:3+strLen]), strLen)}
				default:
					return false, fmt.Errorf("unsupported binary tag: %c", valBytes[0])
				}
			}
			sp++
		case OpAdd, OpSub, OpMul, OpDiv:
			if sp < 2 {
				return false, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2

			if left.Type == ValInt && right.Type == ValInt {
				var res int64
				switch inst.Op {
				case OpAdd:
					res = left.Int + right.Int
				case OpSub:
					res = left.Int - right.Int
				case OpMul:
					res = left.Int * right.Int
				case OpDiv:
					if right.Int == 0 {
						return false, errors.New("division by zero")
					}
					res = left.Int / right.Int
				}
				stack[sp] = Value{Type: ValInt, Int: res}
			} else if left.Type == ValFloat && right.Type == ValFloat {
				var res float64
				switch inst.Op {
				case OpAdd:
					res = left.Float + right.Float
				case OpSub:
					res = left.Float - right.Float
				case OpMul:
					res = left.Float * right.Float
				case OpDiv:
					if right.Float == 0 {
						return false, errors.New("division by zero")
					}
					res = left.Float / right.Float
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else if left.Type == ValInt && right.Type == ValFloat {
				var res float64
				lfloat := float64(left.Int)
				switch inst.Op {
				case OpAdd:
					res = lfloat + right.Float
				case OpSub:
					res = lfloat - right.Float
				case OpMul:
					res = lfloat * right.Float
				case OpDiv:
					if right.Float == 0 {
						return false, errors.New("division by zero")
					}
					res = lfloat / right.Float
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else if left.Type == ValFloat && right.Type == ValInt {
				var res float64
				rfloat := float64(right.Int)
				switch inst.Op {
				case OpAdd:
					res = left.Float + rfloat
				case OpSub:
					res = left.Float - rfloat
				case OpMul:
					res = left.Float * rfloat
				case OpDiv:
					if rfloat == 0 {
						return false, errors.New("division by zero")
					}
					res = left.Float / rfloat
				}
				stack[sp] = Value{Type: ValFloat, Float: res}
			} else {
				return false, errors.New("type mismatch in arithmetic operation")
			}
			sp++
		case OpEq, OpNeq, OpLt, OpGt, OpLte, OpGte:
			if sp < 2 {
				return false, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2

			var res bool
			if left.Type == ValInt && right.Type == ValInt {
				switch inst.Op {
				case OpEq:
					res = left.Int == right.Int
				case OpNeq:
					res = left.Int != right.Int
				case OpLt:
					res = left.Int < right.Int
				case OpGt:
					res = left.Int > right.Int
				case OpLte:
					res = left.Int <= right.Int
				case OpGte:
					res = left.Int >= right.Int
				}
			} else if left.Type == ValFloat && right.Type == ValFloat {
				switch inst.Op {
				case OpEq:
					res = left.Float == right.Float
				case OpNeq:
					res = left.Float != right.Float
				case OpLt:
					res = left.Float < right.Float
				case OpGt:
					res = left.Float > right.Float
				case OpLte:
					res = left.Float <= right.Float
				case OpGte:
					res = left.Float >= right.Float
				}
			} else if left.Type == ValInt && right.Type == ValFloat {
				lfloat := float64(left.Int)
				switch inst.Op {
				case OpEq:
					res = lfloat == right.Float
				case OpNeq:
					res = lfloat != right.Float
				case OpLt:
					res = lfloat < right.Float
				case OpGt:
					res = lfloat > right.Float
				case OpLte:
					res = lfloat <= right.Float
				case OpGte:
					res = lfloat >= right.Float
				}
			} else if left.Type == ValFloat && right.Type == ValInt {
				rfloat := float64(right.Int)
				switch inst.Op {
				case OpEq:
					res = left.Float == rfloat
				case OpNeq:
					res = left.Float != rfloat
				case OpLt:
					res = left.Float < rfloat
				case OpGt:
					res = left.Float > rfloat
				case OpLte:
					res = left.Float <= rfloat
				case OpGte:
					res = left.Float >= rfloat
				}
			} else if left.Type == ValString && right.Type == ValString {
				switch inst.Op {
				case OpEq:
					res = left.Str == right.Str
				case OpNeq:
					res = left.Str != right.Str
				case OpLt:
					res = left.Str < right.Str
				case OpGt:
					res = left.Str > right.Str
				case OpLte:
					res = left.Str <= right.Str
				case OpGte:
					res = left.Str >= right.Str
				}
			} else if left.Type == ValBool && right.Type == ValBool && (inst.Op == OpEq || inst.Op == OpNeq) {
				switch inst.Op {
				case OpEq:
					res = left.Bool == right.Bool
				case OpNeq:
					res = left.Bool != right.Bool
				}
			} else {
				if inst.Op == OpEq {
					res = false
				} else if inst.Op == OpNeq {
					res = true
				} else {
					return false, errors.New("type mismatch in comparison")
				}
			}
			stack[sp] = Value{Type: ValBool, Bool: res}
			sp++
		case OpAnd, OpOr:
			if sp < 2 {
				return false, errors.New("stack underflow")
			}
			right, left := stack[sp-1], stack[sp-2]
			sp -= 2
			if left.Type != ValBool || right.Type != ValBool {
				return false, errors.New("type mismatch in logical operation")
			}
			var res bool
			if inst.Op == OpAnd {
				res = left.Bool && right.Bool
			} else {
				res = left.Bool || right.Bool
			}
			stack[sp] = Value{Type: ValBool, Bool: res}
			sp++
		case OpNot:
			if sp < 1 {
				return false, errors.New("stack underflow")
			}
			val := stack[sp-1]
			sp--
			if val.Type != ValBool {
				return false, errors.New("type mismatch in logical NOT")
			}
			stack[sp] = Value{Type: ValBool, Bool: !val.Bool}
			sp++
		case OpReturn:
			if sp < 1 {
				return false, errors.New("stack underflow")
			}
			return stack[sp-1].Bool, nil
		default:
			return false, fmt.Errorf("unknown opcode: %v", inst.Op)
		}
	}

	if sp > 0 {
		return stack[sp-1].Bool, nil
	}
	return false, nil
}
