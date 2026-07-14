package executor

import (
	"vaultdb/internal/core/parser"
)

type QueryPlan struct {
	Root       PlanNode
	PlanningMs float64
}

type PlanNode struct {
	NodeType  string
	Table     string
	IndexName string
	Filter    parser.Expression
	Children  []PlanNode
	Stats     *PlanStats
}

type PlanStats struct {
	RowsTotal    int
	RowsScanned  int
	RowsMatched  int
	RowsFiltered int
	ExecutionMs  float64
	UsedIndex    bool
}
