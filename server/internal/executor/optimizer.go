package executor

import (
	"fmt"
	"math"
	"strings"

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

// CostEstimate оценка стоимости плана.
type CostEstimate struct {
	Cost         float64
	EstimatedRows int
}

// Optimizer cost-based query optimizer.
type Optimizer struct {
	stats      *StatisticsCollector
	storage    storage.StorageEngine
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
	// 1. Собираем статистику
	tableStats := o.stats.GetTableStats(dbName, stmt.TableName)

	// 2. Выбираем лучший access method для каждой таблицы
	accessMethods := o.chooseAccessMethods(dbName, stmt, tableStats)

	// 3. Выбираем лучший join method
	joinMethods := o.chooseJoinMethods(dbName, stmt)

	// 4. Строим оптимизированный план
	plan := &OptimizedPlan{
		Stmt:          stmt,
		TableStats:    tableStats,
		AccessMethods: accessMethods,
		JoinMethods:   joinMethods,
	}

	// 5. Оцениваем стоимость
	plan.Cost = o.estimateCost(dbName, plan)

	return plan, nil
}

// chooseAccessMethods выбирает лучший метод доступа для каждой таблицы.
func (o *Optimizer) chooseAccessMethods(dbName string, stmt *parser.SelectStatement, stats *TableStatistics) map[string]AccessMethod {
	methods := make(map[string]AccessMethod)

	// Анализируем WHERE clause
	if stmt.Where != nil {
		if idx := o.findIndexableColumn(dbName, stmt.TableName, stmt.Where); idx != "" {
			// Есть индексный столбец в WHERE
			if _, found := o.storage.FindIndexForColumn(dbName, stmt.TableName, idx); found {
				methods[stmt.TableName] = IndexScan
				return methods
			}
		}
	}

	// По умолчанию — Sequential Scan
	methods[stmt.TableName] = SeqScan
	return methods
}

// findIndexableColumn находит столбец, который можно использовать через индекс.
func (o *Optimizer) findIndexableColumn(dbName, tableName string, expr parser.Expression) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *parser.BinaryExpr:
		if e.Operator == "=" {
			if col, ok := e.Left.(*parser.ColumnRef); ok {
				return col.Name
			}
			if col, ok := e.Right.(*parser.ColumnRef); ok {
				return col.Name
			}
		}
	case *parser.AndExpr:
		if col := o.findIndexableColumn(dbName, tableName, e.Left); col != "" {
			return col
		}
		return o.findIndexableColumn(dbName, tableName, e.Right)
	case *parser.OrExpr:
		// OR не может использовать индекс эффективно
		return ""
	}
	return ""
}

// chooseJoinMethods выбирает лучший метод соединения для JOIN.
func (o *Optimizer) chooseJoinMethods(dbName string, stmt *parser.SelectStatement) []JoinMethod {
	methods := make([]JoinMethod, 0, len(stmt.Joins))

	for _, join := range stmt.Joins {
		// Анализируем условие соединения
		if join.Condition != nil {
			if cmp, ok := join.Condition.(*parser.BinaryExpr); ok && cmp.Operator == "=" {
				// Equi-join: можем использовать Hash Join или Merge Join
				methods = append(methods, HashJoin)
			} else {
				// Non-equi join: только Nested Loop
				methods = append(methods, NestedLoopJoin)
			}
		} else {
			// CROSS JOIN: Nested Loop
			methods = append(methods, NestedLoopJoin)
		}
	}

	return methods
}

// estimateCost оценивает стоимость плана.
func (o *Optimizer) estimateCost(dbName string, plan *OptimizedPlan) CostEstimate {
	cost := CostEstimate{}

	// Стоимость SeqScan: N страниц
	// Стоимость IndexScan: log(N) + K (K — количество совпадений)
	stats := plan.TableStats
	if stats == nil {
		cost.Cost = 1000
		cost.EstimatedRows = 100
		return cost
	}

	tableCost := float64(stats.RowCount)

	// Если есть WHERE, уменьшаем стоимость
	if plan.Stmt.Where != nil {
		selectivity := o.stats.EstimateSelectivity(dbName, plan.Stmt.TableName, plan.Stmt.Where)
		tableCost *= selectivity
		cost.EstimatedRows = int(float64(stats.RowCount) * selectivity)
	} else {
		cost.EstimatedRows = stats.RowCount
	}

	// Учитываем метод доступа
	if method, ok := plan.AccessMethods[plan.Stmt.TableName]; ok {
		switch method {
		case IndexScan:
			// Index Scan: log(N) + selectivity * N
			sel := o.stats.EstimateSelectivity(dbName, plan.Stmt.TableName, plan.Stmt.Where)
			tableCost = math.Log2(float64(stats.RowCount)) + sel*float64(stats.RowCount)
		case IndexOnlyScan:
			// Index Only Scan: log(N)
			tableCost = math.Log2(float64(stats.RowCount))
		}
	}

	cost.Cost = tableCost

	// Учитываем JOIN
	if len(plan.Stmt.Joins) > 0 {
		for _, joinMethod := range plan.JoinMethods {
			switch joinMethod {
			case NestedLoopJoin:
				cost.Cost *= 10.0 // Nested Loop: O(N * M)
			case HashJoin:
				cost.Cost *= 2.0 // Hash Join: O(N + M)
			case MergeJoin:
				cost.Cost *= 1.5 // Merge Join: O(N log N + M log M)
			}
		}
	}

	return cost
}

// OptimizedPlan оптимизированный план запроса.
type OptimizedPlan struct {
	Stmt          *parser.SelectStatement
	TableStats    *TableStatistics
	AccessMethods map[string]AccessMethod
	JoinMethods   []JoinMethod
	Cost          CostEstimate
}

// FormatOptimizedPlan форматирует оптимизированный план для вывода.
func (p *OptimizedPlan) FormatOptimizedPlan() string {
	var b strings.Builder
	sep := strings.Repeat("═", 64)

	b.WriteString("OPTIMIZED QUERY PLAN\n")
	b.WriteString(sep + "\n")

	// Access Method
	if method, ok := p.AccessMethods[p.Stmt.TableName]; ok {
		switch method {
		case SeqScan:
			b.WriteString(fmt.Sprintf("Seq Scan on \"%s\"\n", p.Stmt.TableName))
		case IndexScan:
			b.WriteString(fmt.Sprintf("Index Scan on \"%s\"\n", p.Stmt.TableName))
		case IndexOnlyScan:
			b.WriteString(fmt.Sprintf("Index Only Scan on \"%s\"\n", p.Stmt.TableName))
		}
	} else {
		b.WriteString(fmt.Sprintf("Seq Scan on \"%s\"\n", p.Stmt.TableName))
	}

	// Statistics
	if p.TableStats != nil {
		b.WriteString(fmt.Sprintf("  Rows: %d\n", p.TableStats.RowCount))
		b.WriteString(fmt.Sprintf("  Estimated Output: %d\n", p.Cost.EstimatedRows))
	}

	// Filter
	if p.Stmt.Where != nil {
		b.WriteString("  Filter: ")
		b.WriteString(formatExpression(p.Stmt.Where))
		b.WriteString("\n")
	}

	// JOINs
	for i, join := range p.Stmt.Joins {
		method := NestedLoopJoin
		if i < len(p.JoinMethods) {
			method = p.JoinMethods[i]
		}

		switch method {
		case HashJoin:
			b.WriteString(fmt.Sprintf("  Hash Join on \"%s\"\n", join.TableName))
		case MergeJoin:
			b.WriteString(fmt.Sprintf("  Merge Join on \"%s\"\n", join.TableName))
		default:
			b.WriteString(fmt.Sprintf("  Nested Loop Join on \"%s\"\n", join.TableName))
		}

		if join.Condition != nil {
			b.WriteString(fmt.Sprintf("    Condition: %s\n", formatExpression(join.Condition)))
		}
	}

	// Cost
	b.WriteString(fmt.Sprintf("\nEstimated Cost: %.2f\n", p.Cost.Cost))
	b.WriteString(fmt.Sprintf("Estimated Rows: %d\n", p.Cost.EstimatedRows))

	b.WriteString(sep + "\n")

	return b.String()
}

// formatExpression форматирует выражение для вывода.
func formatExpression(expr parser.Expression) string {
	if expr == nil {
		return ""
	}

	switch e := expr.(type) {
	case *parser.BinaryExpr:
		left := formatExpression(e.Left)
		right := formatExpression(e.Right)
		return fmt.Sprintf("%s %s %s", left, e.Operator, right)
	case *parser.AndExpr:
		return fmt.Sprintf("(%s AND %s)", formatExpression(e.Left), formatExpression(e.Right))
	case *parser.OrExpr:
		return fmt.Sprintf("(%s OR %s)", formatExpression(e.Left), formatExpression(e.Right))
	case *parser.NotExpr:
		return fmt.Sprintf("NOT %s", formatExpression(e.Expr))
	case *parser.ColumnRef:
		return e.Name
	case parser.Value:
		return formatValue(e)
	case *parser.Value:
		return formatValue(*e)
	default:
		return "<expr>"
	}
}

func formatValue(v parser.Value) string {
	switch v.Type {
	case "string":
		return "'" + v.StrVal + "'"
	case "int":
		return fmt.Sprintf("%d", v.IntVal)
	case "float":
		return fmt.Sprintf("%g", v.FltVal)
	case "bool":
		if v.BoolVal {
			return "TRUE"
		}
		return "FALSE"
	case "null":
		return "NULL"
	default:
		return "?"
	}
}
