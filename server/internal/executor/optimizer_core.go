package executor

// Core optimizer types and main entry point.

import (
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// AccessMethod тип доступа к данным.
type AccessMethod int

const (
	SeqScan AccessMethod = iota
	IndexScan
	IndexOnlyScan
)

// JoinMethod тип соединения.
type JoinMethod int

const (
	NestedLoopJoin JoinMethod = iota
	HashJoin
	MergeJoin
)

const (
	costNestedLoopJoin  = 10.0
	costHashJoin        = 2.0
	costMergeJoin       = 1.5
	defaultFallbackCost = 1000
	defaultFallbackRows = 100
)

// CostEstimate оценка стоимости плана.
type CostEstimate struct {
	Cost          float64
	EstimatedRows int
}

// Optimizer cost-based query optimizer.
type Optimizer struct {
	stats   *StatisticsCollector
	storage storage.StorageEngine
}

// NewOptimizer создаёт новый optimizer.
func NewOptimizer(store storage.StorageEngine) *Optimizer {
	return &Optimizer{
		stats:   NewStatisticsCollector(store),
		storage: store,
	}
}

// OptimizePlan оптимизирует SELECT запрос.
func (o *Optimizer) OptimizePlan(dbName string, stmt *parser.SelectStatement) (*OptimizedPlan, error) {
	o.decorrelateSubqueries(dbName, stmt)

	tableStats := o.stats.GetTableStats(dbName, stmt.TableName)
	accessMethods := o.chooseAccessMethods(dbName, stmt, tableStats)
	joinMethods := o.chooseJoinMethods(dbName, stmt)

	plan := &OptimizedPlan{
		Stmt:          stmt,
		TableStats:    tableStats,
		AccessMethods: accessMethods,
		JoinMethods:   joinMethods,
	}

	o.predicatePushdown(dbName, plan)
	o.reorderJoins(dbName, plan)
	o.pushdownProjections(dbName, plan)
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

// OptimizedPlan оптимизированный план запроса.
type OptimizedPlan struct {
	Stmt             *parser.SelectStatement
	TableStats       *TableStatistics
	AccessMethods    map[string]AccessMethod
	JoinMethods      []JoinMethod
	Cost             CostEstimate
	TablePredicates  map[string]parser.Expression
	RequiredColumns  map[string]map[string]bool
	DecorrelatedJoins []parser.JoinClause
}

// chooseAccessMethods выбирает лучший метод доступа для каждой таблицы.
func (o *Optimizer) chooseAccessMethods(dbName string, stmt *parser.SelectStatement, stats *TableStatistics) map[string]AccessMethod {
	methods := make(map[string]AccessMethod)

	tables := o.collectTables(stmt)
	for _, table := range tables {
		method := SeqScan

		if stats != nil && stats.RowCount > 0 {
			indexes, _ := o.storage.ListIndexes(dbName, table)
			if len(indexes) > 0 && stats.RowCount > 100 {
				method = IndexScan
			}
		}

		methods[table] = method
	}

	return methods
}
