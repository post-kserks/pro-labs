package executor

// Join reordering and method selection.

import (
	"sort"

	"vaultdb/internal/parser"
)

// reorderJoins reorders joins so the smallest table is processed first.
func (o *Optimizer) reorderJoins(dbName string, plan *OptimizedPlan) {
	if len(plan.Stmt.Joins) <= 1 {
		return
	}

	type joinInfo struct {
		join        parser.JoinClause
		method      JoinMethod
		accessAfter AccessMethod
		stats       *TableStatistics
	}

	infos := make([]joinInfo, len(plan.Stmt.Joins))
	for i, j := range plan.Stmt.Joins {
		ts := o.stats.GetTableStats(dbName, j.TableName)
		method := NestedLoopJoin
		if i < len(plan.JoinMethods) {
			method = plan.JoinMethods[i]
		}
		accessAfter := SeqScan
		if am, ok := plan.AccessMethods[j.TableName]; ok {
			accessAfter = am
		}
		infos[i] = joinInfo{join: j, method: method, accessAfter: accessAfter, stats: ts}
	}

	sort.SliceStable(infos, func(i, k int) bool {
		ri, rk := o.rowCount(infos[i].stats), o.rowCount(infos[k].stats)
		return ri < rk
	})

	for i, info := range infos {
		plan.Stmt.Joins[i] = info.join
		if i < len(plan.JoinMethods) {
			plan.JoinMethods[i] = info.method
		}
		plan.AccessMethods[info.join.TableName] = info.accessAfter
	}
}

// chooseJoinMethods selects the best join method for JOIN.
func (o *Optimizer) chooseJoinMethods(dbName string, stmt *parser.SelectStatement) []JoinMethod {
	methods := make([]JoinMethod, len(stmt.Joins))
	for i, join := range stmt.Joins {
		leftStats := o.stats.GetTableStats(dbName, stmt.TableName)
		rightStats := o.stats.GetTableStats(dbName, join.TableName)

		leftRows := o.rowCount(leftStats)
		rightRows := o.rowCount(rightStats)

		if leftRows < 1000 && rightRows < 1000 {
			methods[i] = NestedLoopJoin
		} else if leftRows > 10000 || rightRows > 10000 {
			methods[i] = HashJoin
		} else {
			methods[i] = MergeJoin
		}
	}
	return methods
}
