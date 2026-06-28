package executor

// Plan formatting for EXPLAIN output.

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
)

// FormatOptimizedPlan форматирует оптимизированный план для вывода.
func (p *OptimizedPlan) FormatOptimizedPlan() string {
	var b strings.Builder
	sep := strings.Repeat("═", 64)

	b.WriteString("OPTIMIZED QUERY PLAN\n")
	b.WriteString(sep + "\n")

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

	if p.TableStats != nil {
		b.WriteString(fmt.Sprintf("  Rows: %d\n", p.TableStats.RowCount))
		b.WriteString(fmt.Sprintf("  Estimated Output: %d\n", p.Cost.EstimatedRows))
	}

	if p.Stmt.Where != nil {
		b.WriteString("  Filter: ")
		b.WriteString(formatExpression(p.Stmt.Where))
		b.WriteString("\n")
	}

	if len(p.TablePredicates) > 0 {
		for table, pred := range p.TablePredicates {
			if pred != nil {
				b.WriteString(fmt.Sprintf("  Pushed to \"%s\": %s\n", table, formatExpression(pred)))
			}
		}
	}

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

	if len(p.RequiredColumns) > 0 {
		for table, cols := range p.RequiredColumns {
			if len(cols) > 0 && !cols["*"] {
				b.WriteString(fmt.Sprintf("  Columns needed from \"%s\": %s\n", table, formatColumnSet(cols)))
			}
		}
	}

	b.WriteString(fmt.Sprintf("\nEstimated Cost: %.2f\n", p.Cost.Cost))
	b.WriteString(fmt.Sprintf("Estimated Rows: %d\n", p.Cost.EstimatedRows))

	b.WriteString(sep + "\n")

	return b.String()
}

func accessMethodName(m AccessMethod) string {
	switch m {
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

func joinMethodName(m JoinMethod) string {
	switch m {
	case NestedLoopJoin:
		return "NestedLoop"
	case HashJoin:
		return "Hash"
	case MergeJoin:
		return "Merge"
	default:
		return "Unknown"
	}
}

func formatColumnSet(cols map[string]bool) string {
	var names []string
	for col := range cols {
		names = append(names, col)
	}
	return strings.Join(names, ", ")
}

func formatExpression(expr parser.Expression) string {
	if expr == nil {
		return ""
	}
	return fmt.Sprintf("%v", expr)
}
