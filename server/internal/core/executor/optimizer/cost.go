package optimizer

// Cost estimation for query plans.

import (
	"vaultdb/internal/core/parser"
)

const (
	SeqPageCost       = 1.0
	RandomPageCost    = 4.0
	CpuTupleCost      = 0.01
	CpuIndexTupleCost = 0.005
)

// estimateCost estimates the cost of a plan using I/O and CPU parameters.
func (o *Optimizer) estimateCost(dbName string, plan *OptimizedPlan) CostEstimate {
	totalCost := 0.0
	totalRows := 0

	for _, table := range o.collectTables(plan.Stmt) {
		stats := o.stats.GetTableStats(dbName, table)
		rows := o.rowCount(stats)
		if rows <= 0 {
			rows = defaultFallbackRows
		}
		pages := float64(rows) / 80.0
		if pages < 1.0 {
			pages = 1.0
		}

		var pred parser.Expression
		if plan.TablePredicates != nil {
			pred = plan.TablePredicates[table]
		}
		if pred == nil && plan.Stmt.TableName == table {
			pred = plan.Stmt.Where
		}
		sel := o.EstimateSelectivity(dbName, table, pred)
		if sel <= 0.0 || sel > 1.0 {
			sel = 0.1
		}
		matchedRows := float64(rows) * sel
		if matchedRows < 1.0 {
			matchedRows = 1.0
		}

		am := plan.AccessMethods[table]
		switch am {
		case SeqScan:
			totalCost += pages*SeqPageCost + float64(rows)*CpuTupleCost
		case IndexScan:
			indexPages := pages * 0.2 * sel
			if indexPages < 1.0 {
				indexPages = 1.0
			}
			heapLookups := matchedRows * RandomPageCost
			totalCost += indexPages*RandomPageCost + heapLookups + matchedRows*CpuTupleCost + matchedRows*CpuIndexTupleCost
		case IndexOnlyScan:
			indexPages := pages * 0.2 * sel
			if indexPages < 1.0 {
				indexPages = 1.0
			}
			totalCost += indexPages*RandomPageCost + matchedRows*CpuIndexTupleCost + matchedRows*CpuTupleCost
		}

		if totalRows == 0 || int(matchedRows) > totalRows {
			totalRows = int(matchedRows)
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

// EstimateSelectivity estimates predicate selectivity for a table using column MCV and Histogram statistics.
func (o *Optimizer) EstimateSelectivity(dbName, table string, pred parser.Expression) float64 {
	if o.stats == nil {
		return 0.3
	}
	return o.stats.EstimateSelectivity(dbName, table, pred)
}
