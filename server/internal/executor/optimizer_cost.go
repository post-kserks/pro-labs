package executor

// Cost estimation for query plans.

import ()

// estimateCost estimates the cost of a plan.
func (o *Optimizer) estimateCost(dbName string, plan *OptimizedPlan) CostEstimate {
	totalCost := 0.0
	totalRows := 0

	for _, table := range o.collectTables(plan.Stmt) {
		stats := o.stats.GetTableStats(dbName, table)
		rows := o.rowCount(stats)
		totalRows = max(totalRows, rows)

		am := plan.AccessMethods[table]
		switch am {
		case SeqScan:
			totalCost += float64(rows)
		case IndexScan:
			totalCost += float64(rows) * 0.1
		case IndexOnlyScan:
			totalCost += float64(rows) * 0.05
		}
	}

	for _, jm := range plan.JoinMethods {
		switch jm {
		case NestedLoopJoin:
			totalCost += costNestedLoopJoin * float64(totalRows)
		case HashJoin:
			totalCost += costHashJoin * float64(totalRows)
		case MergeJoin:
			totalCost += costMergeJoin * float64(totalRows)
		}
	}

	return CostEstimate{
		Cost:          totalCost,
		EstimatedRows: totalRows,
	}
}
