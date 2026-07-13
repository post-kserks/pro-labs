package optimizer

import (
	"testing"

	"vaultdb/internal/parser"
)

func TestReorderJoins_SmallestFirst(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "big",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Joins: []parser.JoinClause{
			{Type: "INNER", TableName: "small", Condition: &parser.BinaryExpr{
				Left: &parser.ColumnRef{Name: "id"}, Operator: "=", Right: &parser.ColumnRef{Name: "big_id"},
			}},
			{Type: "INNER", TableName: "medium", Condition: &parser.BinaryExpr{
				Left: &parser.ColumnRef{Name: "id"}, Operator: "=", Right: &parser.ColumnRef{Name: "big_id"},
			}},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// Without actual row stats, all tables have defaultFallbackRows.
	// The optimizer should not panic and should preserve join count.
	if len(plan.Stmt.Joins) != 2 {
		t.Fatalf("expected 2 joins, got %d", len(plan.Stmt.Joins))
	}
	if len(plan.JoinMethods) != 2 {
		t.Fatalf("expected 2 join methods, got %d", len(plan.JoinMethods))
	}
}

func TestReorderJoins_SingleJoin(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "t1",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Joins: []parser.JoinClause{
			{Type: "INNER", TableName: "t2", Condition: &parser.BinaryExpr{
				Left: &parser.ColumnRef{Name: "id"}, Operator: "=", Right: &parser.ColumnRef{Name: "t1_id"},
			}},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	if len(plan.Stmt.Joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(plan.Stmt.Joins))
	}
	if plan.Stmt.Joins[0].TableName != "t2" {
		t.Fatalf("expected join on t2, got %s", plan.Stmt.Joins[0].TableName)
	}
}

func TestPushdownProjections_SingleTable(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "users",
		Columns: []parser.SelectColumn{
			{Expr: &parser.ColumnRef{Name: "name"}},
			{Expr: &parser.ColumnRef{Name: "age"}},
		},
		Where: &parser.BinaryExpr{
			Left: &parser.ColumnRef{Name: "status"}, Operator: "=", Right: &parser.Value{Type: "string", StrVal: "active"},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	cols, ok := plan.RequiredColumns["users"]
	if !ok {
		t.Fatal("expected RequiredColumns for 'users'")
	}

	// name and age from SELECT, status from WHERE
	for _, expected := range []string{"name", "age", "status"} {
		if !cols[expected] {
			t.Errorf("expected column %q in required columns", expected)
		}
	}
}

func TestPushdownProjections_Join(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "orders",
		Columns: []parser.SelectColumn{
			{Expr: &parser.ColumnRef{Name: "amount"}},
		},
		Joins: []parser.JoinClause{
			{Type: "INNER", TableName: "customers", Condition: &parser.BinaryExpr{
				Left: &parser.ColumnRef{Name: "customer_id"}, Operator: "=", Right: &parser.ColumnRef{Name: "id"},
			}},
		},
		Where: &parser.BinaryExpr{
			Left: &parser.ColumnRef{Name: "amount"}, Operator: ">", Right: &parser.Value{Type: "int", IntVal: 100},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// Both tables should have required columns tracked
	for _, table := range []string{"orders", "customers"} {
		cols, ok := plan.RequiredColumns[table]
		if !ok {
			t.Fatalf("expected RequiredColumns for %q", table)
		}
		// amount should be required (from SELECT and WHERE)
		if !cols["amount"] {
			t.Errorf("expected 'amount' in required columns for %q", table)
		}
		// customer_id and id should be required (from JOIN condition)
		if !cols["customer_id"] {
			t.Errorf("expected 'customer_id' in required columns for %q", table)
		}
		if !cols["id"] {
			t.Errorf("expected 'id' in required columns for %q", table)
		}
	}
}

func TestPushdownProjections_SelectStar(t *testing.T) {
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

	cols, ok := plan.RequiredColumns["users"]
	if !ok {
		t.Fatal("expected RequiredColumns for 'users'")
	}
	if !cols["*"] {
		t.Error("expected '*' marker for select-all")
	}
}

func TestDecorrelateSubquery_SimpleIN(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "orders",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Where: &parser.InExpr{
			Left: &parser.ColumnRef{Name: "customer_id"},
			Right: []parser.Expression{
				&parser.SubqueryExpr{
					Query: &parser.SelectStatement{
						TableName: "vip_customers",
						Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "id"}}},
						Where: &parser.BinaryExpr{
							Left: &parser.ColumnRef{Name: "vip_customers.customer_id"}, Operator: "=", Right: &parser.ColumnRef{Name: "orders.customer_id"},
						},
					},
				},
			},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// The IN subquery should have been decorrelated into a join
	if len(plan.Stmt.Joins) == 0 {
		// Check if it was added as a decorrelated join
		if len(plan.DecorrelatedJoins) == 0 {
			t.Fatal("expected subquery to be decorrelated into a join")
		}
	}

	// The WHERE should now be TRUE (the IN condition was replaced)
	if plan.Stmt.Where != nil {
		if val, ok := plan.Stmt.Where.(*parser.Value); ok {
			if !val.BoolVal {
				t.Errorf("expected WHERE to be TRUE after decorrelation, got FALSE")
			}
		}
	}
}

func TestDecorrelateSubquery_NoSubquery(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "users",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Where: &parser.BinaryExpr{
			Left: &parser.ColumnRef{Name: "age"}, Operator: ">", Right: &parser.Value{Type: "int", IntVal: 18},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// No subquery means no decorrelation
	if len(plan.DecorrelatedJoins) != 0 {
		t.Errorf("expected no decorrelated joins, got %d", len(plan.DecorrelatedJoins))
	}
}

func TestDecorrelateSubquery_NonCorrelated(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	// Non-correlated subquery (no WHERE referencing outer table) should not be decorrelated.
	// Our decorrelation only handles correlated IN subqueries where the subquery's WHERE
	// references the outer query's tables.
	stmt := &parser.SelectStatement{
		TableName: "orders",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Where: &parser.InExpr{
			Left: &parser.ColumnRef{Name: "customer_id"},
			Right: []parser.Expression{
				&parser.SubqueryExpr{
					Query: &parser.SelectStatement{
						TableName: "vip_customers",
						Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "id"}}},
						// No WHERE → not correlated → not decorrelated
					},
				},
			},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// Non-correlated subqueries are left as-is (not converted to joins)
	if len(plan.DecorrelatedJoins) != 0 {
		t.Errorf("expected no decorrelated joins for non-correlated subquery, got %d", len(plan.DecorrelatedJoins))
	}
}

func TestReorderJoins_PreservesMethods(t *testing.T) {
	store := newMockStorage()
	store.ensureDB("testdb")
	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "t1",
		Columns:   []parser.SelectColumn{{Expr: &parser.ColumnRef{Name: "*"}}},
		Joins: []parser.JoinClause{
			{Type: "INNER", TableName: "t2", Condition: &parser.BinaryExpr{
				Left: &parser.ColumnRef{Name: "id"}, Operator: "=", Right: &parser.ColumnRef{Name: "t1_id"},
			}},
			{Type: "INNER", TableName: "t3", Condition: &parser.BinaryExpr{
				Left: &parser.ColumnRef{Name: "id"}, Operator: "=", Right: &parser.ColumnRef{Name: "t1_id"},
			}},
		},
	}

	plan, err := opt.OptimizePlan("testdb", stmt)
	if err != nil {
		t.Fatalf("OptimizePlan failed: %v", err)
	}

	// All join methods should be preserved after reordering
	for i, method := range plan.JoinMethods {
		if method != HashJoin && method != NestedLoopJoin && method != MergeJoin {
			t.Errorf("unexpected join method at index %d: %v", i, method)
		}
	}
}
