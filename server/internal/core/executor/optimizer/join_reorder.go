package optimizer

import (
	"vaultdb/internal/core/parser"
)

type JoinNodeType int

const (
	BaseRelation JoinNodeType = iota
	JoinOperation
)

type JoinTree struct {
	Type      JoinNodeType
	TableName string
	Alias     string
	Left      *JoinTree
	Right     *JoinTree
	Method    JoinMethod
	Cost      float64
	Rows      float64
	JoinCond  parser.Expression
}

type JoinGraph struct {
	Relations  []string
	Aliases    []string
	Predicates []parser.Expression
}

func (o *Optimizer) BuildJoinGraph(stmt *parser.SelectStatement) *JoinGraph {
	g := &JoinGraph{}
	g.Relations = append(g.Relations, stmt.TableName)
	g.Aliases = append(g.Aliases, stmt.Alias)

	for _, j := range stmt.Joins {
		g.Relations = append(g.Relations, j.TableName)
		g.Aliases = append(g.Aliases, j.Alias)
		if j.Condition != nil {
			g.Predicates = append(g.Predicates, j.Condition)
		}
	}
	return g
}

func (o *Optimizer) BuildPhysicalJoinTree(dbName string, plan *OptimizedPlan) *JoinTree {
	stmt := plan.Stmt
	if len(stmt.Joins) == 0 {
		return nil
	}

	g := o.BuildJoinGraph(stmt)
	n := len(g.Relations)

	if n > 7 {
		o.reorderJoins(dbName, plan)
		return nil
	}

	dp := make([]*JoinTree, 1<<n)

	for i := 0; i < n; i++ {
		ts := o.stats.GetTableStats(dbName, g.Relations[i])
		rows := o.effectiveRowCount(dbName, g.Relations[i], ts, plan)
		dp[1<<i] = &JoinTree{
			Type:      BaseRelation,
			TableName: g.Relations[i],
			Alias:     g.Aliases[i],
			Cost:      0,
			Rows:      rows,
		}
	}

	for mask := 1; mask < (1 << n); mask++ {
		if mask&(mask-1) == 0 {
			continue
		}

		for i := 0; i < n; i++ {
			if (mask & (1 << i)) != 0 {
				prevMask := mask ^ (1 << i)
				leftTree := dp[prevMask]
				rightTree := dp[1<<i]

				if leftTree == nil || rightTree == nil {
					continue
				}

				newTree := o.evaluateJoin(leftTree, rightTree, g.Predicates)
				if dp[mask] == nil || newTree.Cost < dp[mask].Cost {
					dp[mask] = newTree
				}
			}
		}
	}

	bestTree := dp[(1<<n)-1]

	// Apply reordering to plan.Stmt.Joins to maintain backward compatibility
	if bestTree != nil {
		o.applyJoinTreeToPlan(bestTree, plan)
	}

	return bestTree
}

func (o *Optimizer) applyJoinTreeToPlan(tree *JoinTree, plan *OptimizedPlan) {
	var relations []string
	var aliases []string
	var methods []JoinMethod
	var conditions []parser.Expression

	var traverse func(node *JoinTree)
	traverse = func(node *JoinTree) {
		if node.Type == BaseRelation {
			relations = append(relations, node.TableName)
			aliases = append(aliases, node.Alias)
			return
		}
		traverse(node.Left)
		traverse(node.Right)
		methods = append(methods, node.Method)
		conditions = append(conditions, node.JoinCond)
	}
	traverse(tree)

	plan.Stmt.TableName = relations[0]
	plan.Stmt.Alias = aliases[0]

	newJoins := make([]parser.JoinClause, len(relations)-1)
	for i := 1; i < len(relations); i++ {
		newJoins[i-1] = parser.JoinClause{
			Type:      "INNER", // Default to INNER, can be improved
			TableName: relations[i],
			Alias:     aliases[i],
			Condition: conditions[i-1],
		}
	}
	plan.Stmt.Joins = newJoins
	plan.JoinMethods = methods
}

func (o *Optimizer) evaluateJoin(left, right *JoinTree, preds []parser.Expression) *JoinTree {
	var cond parser.Expression
	sel := 0.1

	rows := left.Rows * right.Rows * sel

	costNL := left.Cost + right.Cost + (left.Rows*right.Rows)*costNestedLoopJoin
	costHash := left.Cost + right.Cost + (left.Rows+right.Rows)*costHashJoin

	method := HashJoin
	cost := costHash

	if costNL < costHash {
		method = NestedLoopJoin
		cost = costNL
	}

	return &JoinTree{
		Type:     JoinOperation,
		Left:     left,
		Right:    right,
		Method:   method,
		Cost:     cost,
		Rows:     rows,
		JoinCond: cond,
	}
}
