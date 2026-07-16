package optimizer

import (
	"testing"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

func TestDPJoinReordering(t *testing.T) {
	store := newMockStorage()
	store.CreateDatabase("testdb")

	// Create table small_table (100 rows)
	store.CreateTable("testdb", storage.TableSchema{
		Name:    "small_table",
		Columns: []storage.ColumnSchema{{Name: "id"}},
	})
	store.rows["testdb"]["small_table"] = make([]storage.Row, 100)

	// Create table large_table (100000 rows)
	store.CreateTable("testdb", storage.TableSchema{
		Name:    "large_table",
		Columns: []storage.ColumnSchema{{Name: "small_id"}},
	})
	store.rows["testdb"]["large_table"] = make([]storage.Row, 10000) // Need HashJoin -> 10000 is enough

	opt := NewOptimizer(store)

	stmt := &parser.SelectStatement{
		TableName: "large_table",
		Joins: []parser.JoinClause{
			{
				Type:      "INNER",
				TableName: "small_table",
				Condition: &parser.BinaryExpr{
					Left:     &parser.ColumnRef{Name: "large_table.small_id"},
					Operator: "=",
					Right:    &parser.ColumnRef{Name: "small_table.id"},
				},
			},
		},
	}

	plan := &OptimizedPlan{Stmt: stmt}

	tree := opt.BuildPhysicalJoinTree("testdb", plan)

	if tree == nil {
		t.Fatalf("Expected tree not to be nil")
	}

	if plan.Stmt.TableName != "small_table" {
		t.Errorf("Expected driving table to be small_table, got %s", plan.Stmt.TableName)
	}

	if len(plan.JoinMethods) > 0 && plan.JoinMethods[0] != HashJoin {
		t.Errorf("Expected HashJoin, got %s", plan.JoinMethods[0])
	}
}
