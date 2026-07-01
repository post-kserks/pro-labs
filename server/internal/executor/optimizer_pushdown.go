package executor

// Projection pushdown, decorrelation, and column reference collection.

import (
	"strings"

	"vaultdb/internal/parser"
)

// pushdownProjections identifies which columns are referenced in SELECT,
// WHERE, and JOIN conditions, and records them per table in RequiredColumns.
func (o *Optimizer) pushdownProjections(dbName string, plan *OptimizedPlan) {
	plan.RequiredColumns = make(map[string]map[string]bool)

	tables := o.collectTables(plan.Stmt)
	for _, t := range tables {
		plan.RequiredColumns[t] = make(map[string]bool)
	}

	for _, col := range plan.Stmt.Columns {
		o.collectColumnRefs(col.Expr, tables, plan.RequiredColumns)
	}
	o.collectColumnRefs(plan.Stmt.Where, tables, plan.RequiredColumns)
	for _, join := range plan.Stmt.Joins {
		o.collectColumnRefs(join.Condition, tables, plan.RequiredColumns)
	}

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
// them to semi-joins.
func (o *Optimizer) decorrelateSubqueries(dbName string, stmt *parser.SelectStatement) {
	if stmt.Where == nil {
		return
	}
	o.decorrelateWhere(dbName, stmt, &stmt.Where, &stmt.Joins)
}

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

func (o *Optimizer) canDecorrelate(outer *parser.SelectStatement, outerExpr parser.Expression, subq *parser.SubqueryExpr) bool {
	subSel, ok := subq.Query.(*parser.SelectStatement)
	if !ok {
		return false
	}
	outerCol, ok := outerExpr.(*parser.ColumnRef)
	if !ok {
		return false
	}
	_ = outerCol
	outerTables := o.collectTables(outer)
	return o.exprReferencesTables(subSel.Where, outerTables)
}

func (o *Optimizer) exprReferencesTables(expr parser.Expression, tables []string) bool {
	if expr == nil || len(tables) == 0 {
		return false
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		for _, t := range tables {
			if strings.HasPrefix(e.Name, t+".") || strings.EqualFold(e.Name, t) {
				return true
			}
		}
	case *parser.BinaryExpr:
		return o.exprReferencesTables(e.Left, tables) || o.exprReferencesTables(e.Right, tables)
	case *parser.AndExpr:
		return o.exprReferencesTables(e.Left, tables) || o.exprReferencesTables(e.Right, tables)
	case *parser.OrExpr:
		return o.exprReferencesTables(e.Left, tables) || o.exprReferencesTables(e.Right, tables)
	case *parser.InExpr:
		if o.exprReferencesTables(e.Left, tables) {
			return true
		}
		for _, r := range e.Right {
			if o.exprReferencesTables(r, tables) {
				return true
			}
		}
	}
	return false
}

func (o *Optimizer) convertToJoin(outer *parser.SelectStatement, outerExpr parser.Expression, subq *parser.SubqueryExpr, joins *[]parser.JoinClause) parser.Expression {
	subStmt, ok := subq.Query.(*parser.SelectStatement)
	if !ok {
		// Non-SELECT subqueries (UNION etc.) can't be decorrelated — leave as-is
		return subq
	}
	joinTableName := subStmt.TableName

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

	return &parser.Value{Type: "bool", BoolVal: true}
}

// referencedTables returns which of the known tables appear in the expression.
func (o *Optimizer) referencedTables(expr parser.Expression, knownTables []string) []string {
	if expr == nil {
		return nil
	}
	found := make(map[string]bool)
	o.findTableRefs(expr, knownTables, found)
	var tables []string
	for t := range found {
		tables = append(tables, t)
	}
	return tables
}

func (o *Optimizer) findTableRefs(expr parser.Expression, knownTables []string, found map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *parser.ColumnRef:
		parts := strings.SplitN(e.Name, ".", 2)
		if len(parts) == 2 {
			for _, t := range knownTables {
				if strings.EqualFold(parts[0], t) {
					found[t] = true
				}
			}
		}
	case *parser.BinaryExpr:
		o.findTableRefs(e.Left, knownTables, found)
		o.findTableRefs(e.Right, knownTables, found)
	case *parser.AndExpr:
		o.findTableRefs(e.Left, knownTables, found)
		o.findTableRefs(e.Right, knownTables, found)
	case *parser.OrExpr:
		o.findTableRefs(e.Left, knownTables, found)
		o.findTableRefs(e.Right, knownTables, found)
	case *parser.InExpr:
		o.findTableRefs(e.Left, knownTables, found)
		for _, r := range e.Right {
			o.findTableRefs(r, knownTables, found)
		}
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

// predicatePushdown pushes WHERE predicates down to individual tables.
func (o *Optimizer) predicatePushdown(dbName string, plan *OptimizedPlan) {
	plan.TablePredicates = make(map[string]parser.Expression)

	if plan.Stmt.Where == nil {
		return
	}

	tables := o.collectTables(plan.Stmt)

	if len(tables) <= 1 {
		for _, t := range tables {
			plan.TablePredicates[t] = plan.Stmt.Where
		}
		return
	}

	predicates := splitAnd(plan.Stmt.Where)
	for _, pred := range predicates {
		refs := o.referencedTables(pred, tables)
		if len(refs) == 1 {
			plan.TablePredicates[refs[0]] = appendConjunction(plan.TablePredicates[refs[0]], pred)
		} else if len(refs) == 0 {
			for _, t := range tables {
				plan.TablePredicates[t] = appendConjunction(plan.TablePredicates[t], pred)
			}
		}
	}

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

func (o *Optimizer) collectTables(stmt *parser.SelectStatement) []string {
	tables := []string{stmt.TableName}
	for _, join := range stmt.Joins {
		tables = append(tables, join.TableName)
	}
	return tables
}

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
		return ""
	}
	return ""
}
