package vm_test

import (
	"testing"
	_ "vaultdb/internal/core/executor"
	"vaultdb/internal/core/executor/eval/vm"
	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

func TestVMExecution(t *testing.T) {
	schema := &storage.TableSchema{
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "price", Type: "FLOAT"},
			{Name: "name", Type: "STRING"},
			{Name: "is_active", Type: "BOOL"},
		},
	}

	row := storage.Row{100, 19.99, "Product", true}

	expr := &parser.BinaryExpr{
		Operator: ">",
		Left:     &parser.ColumnRef{Name: "id"},
		Right:    &parser.Value{Type: "INT", IntVal: 50},
	}

	insts, err := vm.Compile(expr, schema)
	if err != nil {
		t.Fatalf("Failed to compile: %v", err)
	}

	res, err := vm.ExecuteVM(insts, row)
	if err != nil {
		t.Fatalf("Failed to execute: %v", err)
	}

	if b, ok := res.(bool); !ok || !b {
		t.Errorf("Expected true, got %v", res)
	}
}

func BenchmarkAST(b *testing.B) {
	schema := &storage.TableSchema{
		Columns: []storage.ColumnSchema{
			{Name: "a", Type: "INT"},
			{Name: "b", Type: "INT"},
			{Name: "c", Type: "INT"},
			{Name: "id", Type: "INT"},
			{Name: "price", Type: "FLOAT"},
		},
	}
	row := storage.Row{1, 2, 3, 100, 19.99}
	expr := &parser.BinaryExpr{
		Operator: ">",
		Left: &parser.BinaryExpr{
			Operator: "+",
			Left:     &parser.ColumnRef{Name: "id"},
			Right:    &parser.Value{Type: "INT", IntVal: 10},
		},
		Right: &parser.Value{Type: "INT", IntVal: 50},
	}
	ctx := &types.ExecutionContext{}
	types.EnsureColumnIndex(ctx, schema)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		types.EvalExpr(expr, row, schema, ctx)
	}
}

func BenchmarkVM(b *testing.B) {
	schema := &storage.TableSchema{
		Columns: []storage.ColumnSchema{
			{Name: "a", Type: "INT"},
			{Name: "b", Type: "INT"},
			{Name: "c", Type: "INT"},
			{Name: "id", Type: "INT"},
			{Name: "price", Type: "FLOAT"},
		},
	}
	row := storage.Row{1, 2, 3, 100, 19.99}
	expr := &parser.BinaryExpr{
		Operator: ">",
		Left: &parser.BinaryExpr{
			Operator: "+",
			Left:     &parser.ColumnRef{Name: "id"},
			Right:    &parser.Value{Type: "INT", IntVal: 10},
		},
		Right: &parser.Value{Type: "INT", IntVal: 50},
	}

	insts, _ := vm.Compile(expr, schema)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.ExecuteVM(insts, row)
	}
}

type vmTestCase struct {
	name     string
	insts    []vm.OpCode
	expected bool
}

var commonVMTests = []vmTestCase{
	// int comparisons
	{"int_eq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 42}, {Op: vm.OpEq}, {Op: vm.OpReturn}}, true},
	{"int_eq_false", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 99}, {Op: vm.OpEq}, {Op: vm.OpReturn}}, false},
	{"int_neq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 99}, {Op: vm.OpNeq}, {Op: vm.OpReturn}}, true},
	{"int_lt_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 50}, {Op: vm.OpLt}, {Op: vm.OpReturn}}, true},
	{"int_gt_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 10}, {Op: vm.OpGt}, {Op: vm.OpReturn}}, true},
	{"int_lte_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 42}, {Op: vm.OpLte}, {Op: vm.OpReturn}}, true},
	{"int_gte_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 0}, {Op: vm.OpPushInt, Int: 42}, {Op: vm.OpGte}, {Op: vm.OpReturn}}, true},

	// float comparisons
	{"float_eq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 1}, {Op: vm.OpPushFloat, Float: 3.14}, {Op: vm.OpEq}, {Op: vm.OpReturn}}, true},
	{"float_neq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 1}, {Op: vm.OpPushFloat, Float: 2.71}, {Op: vm.OpNeq}, {Op: vm.OpReturn}}, true},
	{"float_lt_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 1}, {Op: vm.OpPushFloat, Float: 4.0}, {Op: vm.OpLt}, {Op: vm.OpReturn}}, true},
	{"float_gt_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 1}, {Op: vm.OpPushFloat, Float: 1.0}, {Op: vm.OpGt}, {Op: vm.OpReturn}}, true},
	{"float_lte_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 1}, {Op: vm.OpPushFloat, Float: 3.14}, {Op: vm.OpLte}, {Op: vm.OpReturn}}, true},
	{"float_gte_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 1}, {Op: vm.OpPushFloat, Float: 3.14}, {Op: vm.OpGte}, {Op: vm.OpReturn}}, true},

	// string comparisons
	{"str_eq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 2}, {Op: vm.OpPushString, Str: "hello"}, {Op: vm.OpEq}, {Op: vm.OpReturn}}, true},
	{"str_neq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 2}, {Op: vm.OpPushString, Str: "world"}, {Op: vm.OpNeq}, {Op: vm.OpReturn}}, true},
	{"str_lt_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 2}, {Op: vm.OpPushString, Str: "world"}, {Op: vm.OpLt}, {Op: vm.OpReturn}}, true},
	{"str_gt_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 2}, {Op: vm.OpPushString, Str: "abc"}, {Op: vm.OpGt}, {Op: vm.OpReturn}}, true},
	{"str_lte_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 2}, {Op: vm.OpPushString, Str: "hello"}, {Op: vm.OpLte}, {Op: vm.OpReturn}}, true},
	{"str_gte_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 2}, {Op: vm.OpPushString, Str: "hello"}, {Op: vm.OpGte}, {Op: vm.OpReturn}}, true},

	// bool comparisons
	{"bool_eq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 3}, {Op: vm.OpPushBool, Bool: true}, {Op: vm.OpEq}, {Op: vm.OpReturn}}, true},
	{"bool_neq_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 3}, {Op: vm.OpPushBool, Bool: false}, {Op: vm.OpNeq}, {Op: vm.OpReturn}}, true},

	// logical AND, OR, NOT
	{"logical_and_false", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 3}, {Op: vm.OpLoadColumn, ColIdx: 4}, {Op: vm.OpAnd}, {Op: vm.OpReturn}}, false},
	{"logical_or_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 3}, {Op: vm.OpLoadColumn, ColIdx: 4}, {Op: vm.OpOr}, {Op: vm.OpReturn}}, true},
	{"logical_not_true", []vm.OpCode{{Op: vm.OpLoadColumn, ColIdx: 4}, {Op: vm.OpNot}, {Op: vm.OpReturn}}, true},
}

func TestExecuteVMBool(t *testing.T) {
	row := storage.Row{int64(42), 3.14, "hello", true, false}

	for _, tc := range commonVMTests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := vm.ExecuteVMBool(tc.insts, row)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, res)
			}

			allocs := testing.AllocsPerRun(100, func() {
				_, _ = vm.ExecuteVMBool(tc.insts, row)
			})
			if allocs != 0 {
				t.Errorf("expected 0 allocations per run, got %f", allocs)
			}
		})
	}
}

func TestExecuteVMRaw(t *testing.T) {
	row := storage.Row{int64(42), 3.14, "hello", true, false}
	rawTuple, err := storage.EncodeRow(0, 0, row)
	if err != nil {
		t.Fatalf("Failed to encode row: %v", err)
	}

	for _, tc := range commonVMTests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := vm.ExecuteVMRaw(tc.insts, rawTuple)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, res)
			}

			allocs := testing.AllocsPerRun(100, func() {
				_, _ = vm.ExecuteVMRaw(tc.insts, rawTuple)
			})
			if allocs != 0 {
				t.Errorf("expected 0 allocations per run, got %f", allocs)
			}
		})
	}
}
