package optimizer

// Core optimizer types and main entry point.

import (
	"strings"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

// AccessMethod data access type.
type AccessMethod int

const (
	SeqScan AccessMethod = iota
	IndexScan
	IndexOnlyScan
)

func (am AccessMethod) String() string {
	switch am {
	case SeqScan:
		return "SeqScan"
	case IndexScan:
		return "IndexScan"
	case IndexOnlyScan:
		return "IndexOnlyScan"
	default:
		return "Unknown"
	}
}

// JoinMethod join execution type.
type JoinMethod int

const (
	NestedLoopJoin JoinMethod = iota
	HashJoin
	MergeJoin
)

func (jm JoinMethod) String() string {
	switch jm {
	case NestedLoopJoin:
		return "NestedLoopJoin"
	case HashJoin:
		return "HashJoin"
	case MergeJoin:
		return "MergeJoin"
	default:
		return "Unknown"
	}
}

const (
	costNestedLoopJoin  = 10.0
	costHashJoin        = 2.0
	costMergeJoin       = 1.5
	defaultFallbackCost = 1000
	defaultFallbackRows = 100
)

// CostEstimate cost components.
type CostEstimate struct {
	Cost          float64
	EstimatedRows int
}

// Optimizer query optimizer.
type Optimizer struct {
	stats   *StatisticsCollector
	storage storage.StorageEngine
}

// NewOptimizer creates a query optimizer.
func NewOptimizer(store storage.StorageEngine) *Optimizer {
	return &Optimizer{
		stats:   NewStatisticsCollector(store),
		storage: store,
	}
}

// OptimizePlan optimizes SELECT query.
func (o *Optimizer) OptimizePlan(dbName string, stmt *parser.SelectStatement) (*OptimizedPlan, error) {
	o.decorrelateSubqueries(dbName, stmt)

	tableStats := o.stats.GetTableStats(dbName, stmt.TableName)
	plan := &OptimizedPlan{
		Stmt:       stmt,
		TableStats: tableStats,
	}

	o.predicatePushdown(dbName, plan)
	o.pushdownProjections(dbName, plan)

	plan.AccessMethods = o.chooseAccessMethods(dbName, stmt, tableStats, plan)
	plan.JoinMethods = o.chooseJoinMethods(dbName, stmt)

	if len(stmt.Joins) > 0 {
		plan.PhysicalJoinTree = o.BuildPhysicalJoinTree(dbName, plan)
	} else {
		o.reorderJoins(dbName, plan)
	}
	plan.Cost = o.estimateCost(dbName, plan)

	return plan, nil
}

// rowCount returns a safe row count from statistics (0 if nil).
func (o *Optimizer) rowCount(s *TableStatistics) int {
	if s == nil {
		return 0
	}
	return s.RowCount
}

// OptimizedPlan optimized query plan.
type OptimizedPlan struct {
	Stmt              *parser.SelectStatement
	TableStats        *TableStatistics
	AccessMethods     map[string]AccessMethod
	JoinMethods       []JoinMethod
	Cost              CostEstimate
	TablePredicates   map[string]parser.Expression
	RequiredColumns   map[string]map[string]bool
	DecorrelatedJoins []parser.JoinClause
	PhysicalJoinTree  *JoinTree
}

// chooseAccessMethods chooses the best access method for each table.
func (o *Optimizer) chooseAccessMethods(dbName string, stmt *parser.SelectStatement, stats *TableStatistics, plan *OptimizedPlan) map[string]AccessMethod {
	methods := make(map[string]AccessMethod)

	tables := o.collectTables(stmt)
	for _, table := range tables {
		method := SeqScan

		if stats != nil && stats.RowCount > 100 {
			indexes, _ := o.storage.ListIndexes(dbName, table)
			if len(indexes) > 0 {
				var pred parser.Expression
				if plan != nil && plan.TablePredicates != nil {
					pred = plan.TablePredicates[table]
				} else {
					pred = stmt.Where
				}

				if o.hasIndexablePredicate(dbName, table, stmt.TableName == table, pred, stmt.Joins) {
					method = IndexScan
					if plan != nil && o.canUseIndexOnlyScan(dbName, table, plan) {
						method = IndexOnlyScan
					}
				}
			}
		}

		methods[table] = method
	}

	return methods
}

var indexOperators = map[string]bool{
	"=": true, ">": true, "<": true, ">=": true, "<=": true, "LIKE": true,
	"->": true, "->>": true, "@>": true,
}

func (o *Optimizer) hasIndexablePredicate(dbName, table string, isPrimaryTable bool, pred parser.Expression, joins []parser.JoinClause) bool {
	if o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, pred) {
		return true
	}
	for _, join := range joins {
		if strings.EqualFold(join.TableName, table) && join.Condition != nil {
			if o.exprHasIndexablePredicate(dbName, table, false, join.Condition) {
				return true
			}
		}
	}
	return false
}

func (o *Optimizer) exprHasIndexablePredicate(dbName, table string, isPrimaryTable bool, expr parser.Expression) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		if indexOperators[e.Operator] {
			if colRef, ok := e.Left.(*parser.ColumnRef); ok {
				colName := o.extractColumnName(colRef.Name, table, isPrimaryTable)
				if colName != "" {
					if _, ok := o.storage.FindIndexForColumn(dbName, table, colName); ok {
						return true
					}
				}
			}
		}
		return o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, e.Left) || o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, e.Right)
	case *parser.JSONAccess:
		if indexOperators[e.Operator] {
			if colRef, ok := e.Expr.(*parser.ColumnRef); ok {
				colName := o.extractColumnName(colRef.Name, table, isPrimaryTable)
				if colName != "" {
					if _, ok := o.storage.FindIndexForColumn(dbName, table, colName); ok {
						return true
					}
				}
			}
		}
	case *parser.AndExpr:
		return o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, e.Left) || o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, e.Right)
	case *parser.OrExpr:
		return o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, e.Left) && o.exprHasIndexablePredicate(dbName, table, isPrimaryTable, e.Right)
	case *parser.InExpr:
		if colRef, ok := e.Left.(*parser.ColumnRef); ok {
			colName := o.extractColumnName(colRef.Name, table, isPrimaryTable)
			if colName != "" {
				if _, ok := o.storage.FindIndexForColumn(dbName, table, colName); ok {
					return true
				}
			}
		}
	}
	return false
}

func (o *Optimizer) extractColumnName(refName, table string, isPrimaryTable bool) string {
	parts := strings.SplitN(refName, ".", 2)
	if len(parts) == 2 {
		if strings.EqualFold(parts[0], table) {
			return parts[1]
		}
		return ""
	}
	if isPrimaryTable {
		return parts[0]
	}
	return ""
}

func (o *Optimizer) canUseIndexOnlyScan(dbName, table string, plan *OptimizedPlan) bool {
	reqCols, ok := plan.RequiredColumns[table]
	if !ok || len(reqCols) == 0 {
		return false
	}
	if reqCols["*"] {
		return false
	}
	for colName := range reqCols {
		idxName, found := o.storage.FindIndexForColumn(dbName, table, colName)
		if !found {
			return false
		}
		idx, found := o.storage.GetIndex(dbName, table, idxName)
		if !found || !idx.HasStoredColumns() {
			return false
		}
	}
	return true
}
