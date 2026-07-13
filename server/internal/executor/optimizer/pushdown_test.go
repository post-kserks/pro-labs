package optimizer

import (
	"testing"

	"vaultdb/internal/parser"
)

func TestPredicatePushdown_SingleTable(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "users",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Where: &parser.BinaryExpr{
			Left:     &parser.ColumnRef{Name: "age"},
			Operator: ">",
			Right:    &parser.Value{Type: "int", IntVal: 18},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	preds, ok := plan.TablePredicates["users"]
	if !ok {
		t.Fatal("expected predicate pushed to 'users'")
	}
	if preds == nil {
		t.Fatal("pushed predicate should not be nil")
	}
}

func TestPredicatePushdown_JoinSplit(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "orders",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Joins: []parser.JoinClause{
			{
				Type:      "INNER",
				TableName: "customers",
				Condition: &parser.BinaryExpr{
					Left:     &parser.ColumnRef{Name: "customer_id"},
					Operator: "=",
					Right:    &parser.ColumnRef{Name: "id"},
				},
			},
		},
		Where: &parser.AndExpr{
			Left: &parser.BinaryExpr{
				Left:     &parser.ColumnRef{Name: "amount"},
				Operator: ">",
				Right:    &parser.Value{Type: "int", IntVal: 100},
			},
			Right: &parser.BinaryExpr{
				Left:     &parser.ColumnRef{Name: "active"},
				Operator: "=",
				Right:    &parser.Value{Type: "bool", BoolVal: true},
			},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// With ColumnRef lacking table qualifiers, predicates are conservatively pushed to all tables
	if len(plan.TablePredicates) < 2 {
		t.Fatalf("expected predicates pushed to join tables, got %d tables", len(plan.TablePredicates))
	}
	for _, table := range []string{"orders", "customers"} {
		if _, ok := plan.TablePredicates[table]; !ok {
			t.Errorf("expected predicate pushed to %q", table)
		}
	}
}

func TestPredicatePushdown_NoWhere(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "users",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	if len(plan.TablePredicates) != 0 {
		t.Fatalf("expected no pushed predicates without WHERE, got %d", len(plan.TablePredicates))
	}
}

func TestPredicatePushdown_AndChain(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "users",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Where: &parser.AndExpr{
			Left: &parser.AndExpr{
				Left: &parser.BinaryExpr{
					Left:     &parser.ColumnRef{Name: "age"},
					Operator: ">=",
					Right:    &parser.Value{Type: "int", IntVal: 18},
				},
				Right: &parser.BinaryExpr{
					Left:     &parser.ColumnRef{Name: "status"},
					Operator: "=",
					Right:    &parser.Value{Type: "string", StrVal: "active"},
				},
			},
			Right: &parser.BinaryExpr{
				Left:     &parser.ColumnRef{Name: "role"},
				Operator: "=",
				Right:    &parser.Value{Type: "string", StrVal: "admin"},
			},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	preds, ok := plan.TablePredicates["users"]
	if !ok {
		t.Fatal("expected predicate pushed to 'users'")
	}
	if preds == nil {
		t.Fatal("pushed predicate should not be nil")
	}
}

func TestSplitAnd(t *testing.T) {
	expr := &parser.AndExpr{
		Left: &parser.AndExpr{
			Left:  &parser.BinaryExpr{Left: &parser.ColumnRef{Name: "a"}, Operator: "=", Right: &parser.Value{Type: "int", IntVal: 1}},
			Right: &parser.BinaryExpr{Left: &parser.ColumnRef{Name: "b"}, Operator: "=", Right: &parser.Value{Type: "int", IntVal: 2}},
		},
		Right: &parser.BinaryExpr{Left: &parser.ColumnRef{Name: "c"}, Operator: "=", Right: &parser.Value{Type: "int", IntVal: 3}},
	}

	parts := splitAnd(expr)
	if len(parts) != 3 {
		t.Fatalf("expected 3 conjuncts, got %d", len(parts))
	}
}

func TestSplitAnd_NonAnd(t *testing.T) {
	expr := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "a"},
		Operator: "=",
		Right:    &parser.Value{Type: "int", IntVal: 1},
	}

	parts := splitAnd(expr)
	if len(parts) != 1 {
		t.Fatalf("expected 1 conjunct for non-AND expr, got %d", len(parts))
	}
}

func TestAppendConjunction(t *testing.T) {
	a := &parser.BinaryExpr{Left: &parser.ColumnRef{Name: "x"}, Operator: "=", Right: &parser.Value{Type: "int", IntVal: 1}}
	b := &parser.BinaryExpr{Left: &parser.ColumnRef{Name: "y"}, Operator: "=", Right: &parser.Value{Type: "int", IntVal: 2}}

	combined := appendConjunction(a, b)
	andExpr, ok := combined.(*parser.AndExpr)
	if !ok {
		t.Fatal("expected AndExpr")
	}
	if andExpr.Left != a || andExpr.Right != b {
		t.Fatal("AndExpr children mismatch")
	}

	if appendConjunction(nil, b) != b {
		t.Fatal("nil + b should be b")
	}
	if appendConjunction(a, nil) != a {
		t.Fatal("a + nil should be a")
	}
}

func TestCollectTables(t *testing.T) {
	store := newMockStorage()
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "t1",
		Joins: []parser.JoinClause{
			{TableName: "t2"},
			{TableName: "t3"},
		},
	}

	tables := opt.collectTables(stmt)
	if len(tables) != 3 || tables[0] != "t1" || tables[1] != "t2" || tables[2] != "t3" {
		t.Fatalf("unexpected tables: %v", tables)
	}
}
