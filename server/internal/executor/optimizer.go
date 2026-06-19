package executor

import (
	"fmt"
	"math"
	"sort"
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
	// 0. Subquery decorrelation (convert correlated IN subqueries to joins)
	o.decorrelateSubqueries(dbName, stmt)

	// 1. Collect statistics for all tables
	tableStats := o.stats.GetTableStats(dbName, stmt.TableName)

	// 2. Choose best access method for each table
	accessMethods := o.chooseAccessMethods(dbName, stmt, tableStats)

	// 3. Choose best join methods
	joinMethods := o.chooseJoinMethods(dbName, stmt)

	// 4. Build optimized plan
	plan := &OptimizedPlan{
		Stmt:          stmt,
		TableStats:    tableStats,
		AccessMethods: accessMethods,
		JoinMethods:   joinMethods,
	}

	// 5. Predicate pushdown
	o.predicatePushdown(dbName, plan)

	// 6. Join reordering (smallest table first)
	o.reorderJoins(dbName, plan)

	// 7. Projection pushdown (track required columns per table)
	o.pushdownProjections(dbName, plan)

	// 8. Estimate cost
	plan.Cost = o.estimateCost(dbName, plan)

	return plan, nil
}

// reorderJoins reorders joins so the smallest table is processed first,
// reducing intermediate result sizes. It reorders Joins, AccessMethods keys,
// and JoinMethods in sync.
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

// rowCount returns a safe row count from statistics (0 if nil).
func (o *Optimizer) rowCount(s *TableStatistics) int {
	if s == nil {
		return defaultFallbackRows
	}
	return s.RowCount
}

// pushdownProjections identifies which columns are referenced in SELECT,
// WHERE, and JOIN conditions, and records them per table in RequiredColumns.
func (o *Optimizer) pushdownProjections(dbName string, plan *OptimizedPlan) {
	plan.RequiredColumns = make(map[string]map[string]bool)

	tables := o.collectTables(plan.Stmt)

	// Initialize per-table column sets
	for _, t := range tables {
		plan.RequiredColumns[t] = make(map[string]bool)
	}

	// Collect columns from SELECT
	for _, col := range plan.Stmt.Columns {
		o.collectColumnRefs(col.Expr, tables, plan.RequiredColumns)
	}

	// Collect columns from WHERE
	o.collectColumnRefs(plan.Stmt.Where, tables, plan.RequiredColumns)

	// Collect columns from JOIN conditions
	for _, join := range plan.Stmt.Joins {
		o.collectColumnRefs(join.Condition, tables, plan.RequiredColumns)
	}

	// If SELECT is *, mark all columns as required (conservative)
	if len(plan.Stmt.Columns) == 0 {
		for _, t := range tables {
			plan.RequiredColumns[t]["*"] = true
		}
	}
}

// collectColumnRefs finds ColumnRef nodes and assigns them to the global
// column set for each table. Since ColumnRef lacks table qualifiers, we
// conservatively mark the column as required on every table.
func (o *Optimizer) collectColumnRefs(expr parser.Expression, tables []string, required map[string]map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		for _, t := range tables {
			required[t][strings.ToLower(e.Name)] = true
		}
	case *parser.BinaryExpr:
		o.collectColumnRefs(e.Left, tables, required)
		o.collectColumnRefs(e.Right, tables, required)
	case *parser.AndExpr:
		o.collectColumnRefs(e.Left, tables, required)
		o.collectColumnRefs(e.Right, tables, required)
	case *parser.OrExpr:
		o.collectColumnRefs(e.Left, tables, required)
		o.collectColumnRefs(e.Right, tables, required)
	case *parser.NotExpr:
		o.collectColumnRefs(e.Expr, tables, required)
	case *parser.InExpr:
		o.collectColumnRefs(e.Left, tables, required)
		for _, r := range e.Right {
			o.collectColumnRefs(r, tables, required)
		}
	case *parser.FunctionCall:
		for _, arg := range e.Args {
			o.collectColumnRefs(arg, tables, required)
		}
	case *parser.AggregateExpr:
		for _, arg := range e.Args {
			o.collectColumnRefs(arg, tables, required)
		}
	case *parser.SubqueryExpr:
		// Subquery column refs are internal to the subquery
	}
}

// decorrelateSubqueries finds correlated IN subqueries in WHERE and converts
// them to semi-joins. Handles the pattern: WHERE col IN (SELECT col FROM t2)
// where the subquery references the outer table.
func (o *Optimizer) decorrelateSubqueries(dbName string, stmt *parser.SelectStatement) {
	if stmt.Where == nil {
		return
	}
	o.decorrelateWhere(dbName, stmt, &stmt.Where, &stmt.Joins)
}

// decorrelateWhere recursively walks the WHERE expression tree.
func (o *Optimizer) decorrelateWhere(dbName string, outer *parser.SelectStatement, expr *parser.Expression, joins *[]parser.JoinClause) {
	if expr == nil {
		return
	}

	switch e := (*expr).(type) {
	case *parser.InExpr:
		if subq, ok := e.Right[0].(*parser.SubqueryExpr); ok && len(e.Right) == 1 {
			if o.canDecorrelate(outer, e.Left, subq) {
				*expr = o.convertToJoin(outer, e.Left, subq, joins)
				return
			}
		}
	case *parser.AndExpr:
		o.decorrelateWhere(dbName, outer, &e.Left, joins)
		o.decorrelateWhere(dbName, outer, &e.Right, joins)
	case *parser.OrExpr:
		o.decorrelateWhere(dbName, outer, &e.Left, joins)
		o.decorrelateWhere(dbName, outer, &e.Right, joins)
	case *parser.NotExpr:
		o.decorrelateWhere(dbName, outer, &e.Expr, joins)
	}
}

// canDecorrelate checks if a subquery is correlated with the outer query.
// A simple correlated subquery: WHERE col IN (SELECT col2 FROM t2 WHERE t2.fk = t1.id)
// We check that the subquery references the outer table's columns.
func (o *Optimizer) canDecorrelate(outer *parser.SelectStatement, outerExpr parser.Expression, subq *parser.SubqueryExpr) bool {
	if subq.Query == nil {
		return false
	}
	// Get the outer column name
	outerCol, ok := outerExpr.(*parser.ColumnRef)
	if !ok {
		return false
	}
	_ = outerCol

	// Check if the subquery's WHERE references the outer table
	outerTables := o.collectTables(outer)
	return o.exprReferencesTables(subq.Query.Where, outerTables)
}

// exprReferencesTables checks if an expression references any of the given tables
// (by looking for ColumnRef names that match known patterns).
func (o *Optimizer) exprReferencesTables(expr parser.Expression, tables []string) bool {
	if expr == nil || len(tables) == 0 {
		return false
	}
	found := false
	o.walkForTableRefs(expr, tables, &found)
	return found
}

func (o *Optimizer) walkForTableRefs(expr parser.Expression, tables []string, found *bool) {
	if *found || expr == nil {
		return
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		// Without table qualifiers in ColumnRef, we use a heuristic:
		// assume correlation if there's any column ref in a subquery's WHERE
		*found = true
	case *parser.BinaryExpr:
		o.walkForTableRefs(e.Left, tables, found)
		o.walkForTableRefs(e.Right, tables, found)
	case *parser.AndExpr:
		o.walkForTableRefs(e.Left, tables, found)
		o.walkForTableRefs(e.Right, tables, found)
	case *parser.OrExpr:
		o.walkForTableRefs(e.Left, tables, found)
		o.walkForTableRefs(e.Right, tables, found)
	}
}

// convertToJoin converts a correlated IN subquery into a semi-join.
// Pattern: WHERE t1.col IN (SELECT t2.col FROM t2 WHERE ...)
// Becomes: JOIN t2 ON t2.col = t1.col with the subquery's WHERE as join condition.
func (o *Optimizer) convertToJoin(outer *parser.SelectStatement, outerExpr parser.Expression, subq *parser.SubqueryExpr, joins *[]parser.JoinClause) parser.Expression {
	subStmt := subq.Query
	joinTableName := subStmt.TableName

	// Build the semi-join condition: outer.col = subquery.col
	// The subquery SELECT column is the left side of the IN
	var innerExpr parser.Expression
	if len(subStmt.Columns) > 0 {
		innerExpr = subStmt.Columns[0].Expr
	} else {
		innerExpr = outerExpr
	}

	var joinCondition parser.Expression
	joinCondition = &parser.BinaryExpr{
		Left:     outerExpr,
		Operator: "=",
		Right:    innerExpr,
	}

	// If the subquery has a WHERE, add it as an AND to the join condition
	if subStmt.Where != nil {
		joinCondition = &parser.AndExpr{
			Left:  joinCondition,
			Right: subStmt.Where,
		}
	}

	semiJoin := parser.JoinClause{
		Type:      "INNER",
		TableName: joinTableName,
		Condition: joinCondition,
	}

	*joins = append(*joins, semiJoin)

	// Return TRUE (the IN condition is now handled by the join)
	return &parser.Value{Type: "bool", BoolVal: true}
}

// chooseAccessMethods выбирает лучший метод доступа для каждой таблицы.
func (o *Optimizer) chooseAccessMethods(dbName string, stmt *parser.SelectStatement, stats *TableStatistics) map[string]AccessMethod {
	methods := make(map[string]AccessMethod)

	if stmt.Where != nil {
		if idx := o.findIndexableColumn(dbName, stmt.TableName, stmt.Where); idx != "" {
			if _, found := o.storage.FindIndexForColumn(dbName, stmt.TableName, idx); found {
				// Compare IndexScan vs SeqScan cost to choose the better one
				if stats != nil && stats.RowCount > 0 {
					sel := o.stats.EstimateSelectivity(dbName, stmt.TableName, stmt.Where)
					indexCost := math.Log2(float64(stats.RowCount)) + sel*float64(stats.RowCount)
					seqCost := float64(stats.RowCount)
					if indexCost < seqCost {
						methods[stmt.TableName] = IndexScan
						return methods
					}
				} else {
					methods[stmt.TableName] = IndexScan
					return methods
				}
			}
		}
	}

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

// predicatePushdown pushes WHERE predicates down to individual tables.
// For single-table queries, predicates are stored under the table name.
// For joins, predicates referencing only one table are pushed to that table.
// When table ownership cannot be determined, predicates are pushed to all tables.
func (o *Optimizer) predicatePushdown(dbName string, plan *OptimizedPlan) {
	plan.TablePredicates = make(map[string]parser.Expression)

	if plan.Stmt.Where == nil {
		return
	}

	// Collect all referenced tables
	tables := o.collectTables(plan.Stmt)

	if len(tables) <= 1 {
		// Single table: push entire WHERE to that table
		for _, t := range tables {
			plan.TablePredicates[t] = plan.Stmt.Where
		}
		return
	}

	// Multi-table (joins): split AND-connected predicates by referenced tables
	predicates := splitAnd(plan.Stmt.Where)
	for _, pred := range predicates {
		refs := o.referencedTables(pred, tables)
		if len(refs) == 1 {
			// Predicate references exactly one table — push to it
			plan.TablePredicates[refs[0]] = appendConjunction(plan.TablePredicates[refs[0]], pred)
		} else if len(refs) == 0 {
			// Cannot determine ownership — push to all tables (conservative)
			for _, t := range tables {
				plan.TablePredicates[t] = appendConjunction(plan.TablePredicates[t], pred)
			}
		}
		// len(refs) > 1: cross-table predicate, cannot push down
	}

	// Also push join conditions down to the respective tables
	for _, join := range plan.Stmt.Joins {
		if join.Condition != nil {
			joinPreds := splitAnd(join.Condition)
			for _, pred := range joinPreds {
				refs := o.referencedTables(pred, tables)
				if len(refs) == 0 {
					refs = tables
				}
				for _, t := range refs {
					plan.TablePredicates[t] = appendConjunction(plan.TablePredicates[t], pred)
				}
			}
		}
	}
}

// collectTables returns all table names referenced in the statement.
func (o *Optimizer) collectTables(stmt *parser.SelectStatement) []string {
	tables := []string{stmt.TableName}
	for _, join := range stmt.Joins {
		tables = append(tables, join.TableName)
	}
	return tables
}

// referencedTables returns which of the known tables appear in the expression.
func (o *Optimizer) referencedTables(expr parser.Expression, knownTables []string) []string {
	if expr == nil {
		return nil
	}

	tableSet := make(map[string]bool)
	o.findTableRefs(expr, knownTables, tableSet)

	result := make([]string, 0, len(tableSet))
	for _, t := range knownTables {
		if tableSet[t] {
			result = append(result, t)
		}
	}
	return result
}

// findTableRefs recursively finds table references in an expression.
func (o *Optimizer) findTableRefs(expr parser.Expression, knownTables []string, found map[string]bool) {
	if expr == nil {
		return
	}

	switch e := expr.(type) {
	case *parser.ColumnRef:
		// ColumnRef only has Name, not Table — cannot determine table reference
		// without schema info, so skip
	case *parser.BinaryExpr:
		o.findTableRefs(e.Left, knownTables, found)
		o.findTableRefs(e.Right, knownTables, found)
	case *parser.AndExpr:
		o.findTableRefs(e.Left, knownTables, found)
		o.findTableRefs(e.Right, knownTables, found)
	case *parser.OrExpr:
		o.findTableRefs(e.Left, knownTables, found)
		o.findTableRefs(e.Right, knownTables, found)
	case *parser.NotExpr:
		o.findTableRefs(e.Expr, knownTables, found)
	case *parser.InExpr:
		o.findTableRefs(e.Left, knownTables, found)
		for _, r := range e.Right {
			o.findTableRefs(r, knownTables, found)
		}
	case *parser.FunctionCall:
		for _, arg := range e.Args {
			o.findTableRefs(arg, knownTables, found)
		}
	case *parser.AggregateExpr:
		for _, arg := range e.Args {
			o.findTableRefs(arg, knownTables, found)
		}
	case *parser.SubqueryExpr:
		// Subquery predicates are not pushable across tables
	}
}

// splitAnd breaks an AND expression into its conjuncts.
func splitAnd(expr parser.Expression) []parser.Expression {
	if expr == nil {
		return nil
	}
	if and, ok := expr.(*parser.AndExpr); ok {
		result := splitAnd(and.Left)
		result = append(result, splitAnd(and.Right)...)
		return result
	}
	return []parser.Expression{expr}
}

// appendConjunction combines two expressions with AND.
func appendConjunction(a, b parser.Expression) parser.Expression {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &parser.AndExpr{Left: a, Right: b}
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
		cost.Cost = defaultFallbackCost
		cost.EstimatedRows = defaultFallbackRows
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
				cost.Cost *= costNestedLoopJoin
			case HashJoin:
				cost.Cost *= costHashJoin
			case MergeJoin:
				cost.Cost *= costMergeJoin
			}
		}
	}

	return cost
}

// OptimizedPlan оптимизированный план запроса.
type OptimizedPlan struct {
	Stmt             *parser.SelectStatement
	TableStats       *TableStatistics
	AccessMethods    map[string]AccessMethod
	JoinMethods      []JoinMethod
	Cost             CostEstimate
	TablePredicates  map[string]parser.Expression  // table → pushed-down predicates
	RequiredColumns  map[string]map[string]bool     // table → set of required column names
	DecorrelatedJoins []parser.JoinClause           // joins created from decorrelated subqueries
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

	// Pushed-down predicates
	if len(p.TablePredicates) > 0 {
		for table, pred := range p.TablePredicates {
			if pred != nil {
				b.WriteString(fmt.Sprintf("  Pushed to \"%s\": %s\n", table, formatExpression(pred)))
			}
		}
	}

	// JOINs
	allJoins := append([]parser.JoinClause{}, p.Stmt.Joins...)
	allJoins = append(allJoins, p.DecorrelatedJoins...)
	for i, join := range allJoins {
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

	// Required columns (projection pushdown)
	if len(p.RequiredColumns) > 0 {
		for table, cols := range p.RequiredColumns {
			if len(cols) > 0 && !cols["*"] {
				b.WriteString(fmt.Sprintf("  Columns needed from \"%s\": %s\n", table, formatColumnSet(cols)))
			}
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

// formatColumnSet formats a set of column names for display.
func formatColumnSet(cols map[string]bool) string {
	names := make([]string, 0, len(cols))
	for c := range cols {
		names = append(names, c)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
