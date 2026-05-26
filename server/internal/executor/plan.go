package executor

import (
	"fmt"
	"strings"

	"vaultdb/internal/parser"
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

func buildPlan(ctx *ExecutionContext, dbName string, stmt *parser.SelectStatement) (QueryPlan, error) {
	if !ctx.Storage.TableExists(dbName, stmt.TableName) {
		return QueryPlan{}, fmt.Errorf("table '%s' does not exist", stmt.TableName)
	}

	nodeType := "Sequential Scan"
	indexName := ""

	// Try to detect if index would be used
	if stmt.Where != nil && stmt.AsOf == nil {
		if cmp, ok := stmt.Where.(*parser.BinaryExpr); ok && cmp.Operator == "=" {
			if col, ok := cmp.Left.(*parser.ColumnRef); ok {
				if idxName, found := ctx.Storage.FindIndexForColumn(dbName, stmt.TableName, col.Name); found {
					nodeType = "Index Scan"
					indexName = idxName
				}
			}
		}
	}

	return QueryPlan{
		Root: PlanNode{
			NodeType:  nodeType,
			Table:     stmt.TableName,
			IndexName: indexName,
			Filter:    stmt.Where,
		},
	}, nil
}

func formatPlan(plan QueryPlan) *Result {
	var b strings.Builder
	sep := strings.Repeat("═", 64)

	b.WriteString("QUERY PLAN\n")
	b.WriteString(sep + "\n")
	renderPlanNode(&b, plan.Root, 0)

	if stats := plan.Root.Stats; stats != nil {
		b.WriteString(fmt.Sprintf("\nExecution Time: %.2f ms\n", stats.ExecutionMs))
	}
	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf("Planning Time:  %.2f ms\n", plan.PlanningMs))
	if stats := plan.Root.Stats; stats != nil {
		b.WriteString(fmt.Sprintf("Total Time:     %.2f ms\n", plan.PlanningMs+stats.ExecutionMs))
	}

	return &Result{
		Type:    "message",
		Message: b.String(),
	}
}

func renderPlanNode(b *strings.Builder, node PlanNode, depth int) {
	indent := strings.Repeat("  ", depth)

	if node.NodeType == "Index Scan" && node.IndexName != "" {
		b.WriteString(fmt.Sprintf(`%sIndex Scan using "%s"`, indent, node.IndexName))
	} else {
		b.WriteString(fmt.Sprintf("%s%s", indent, node.NodeType))
	}

	if node.Table != "" {
		b.WriteString(fmt.Sprintf(` on "%s"`, node.Table))
	}
	b.WriteString("\n")

	if node.Filter != nil {
		b.WriteString(fmt.Sprintf("%s  Filter:\n", indent))
		renderExpressionTree(b, node.Filter, depth+2)
	}

	for _, child := range node.Children {
		renderPlanNode(b, child, depth+1)
	}

	if stats := node.Stats; stats != nil {
		b.WriteString(fmt.Sprintf("\n%s  ├── Rows total:    %d\n", indent, stats.RowsTotal))
		b.WriteString(fmt.Sprintf("%s  ├── Rows scanned:  %d\n", indent, stats.RowsScanned))
		b.WriteString(fmt.Sprintf("%s  ├── Rows matched:  %d\n", indent, stats.RowsMatched))
		b.WriteString(fmt.Sprintf("%s  └── Rows filtered: %d\n", indent, stats.RowsFiltered))
	}
}

func renderExpressionTree(b *strings.Builder, expr parser.Expression, depth int) {
	indent := strings.Repeat("  ", depth)

	switch e := expr.(type) {
	case *parser.BinaryExpr:
		col, _ := e.Left.(*parser.ColumnRef)
		left := "<expr>"
		if col != nil {
			left = col.Name
		}
		b.WriteString(fmt.Sprintf("%s%s %s %s\n", indent, left, e.Operator, formatExprValue(e.Right)))

	case *parser.AndExpr:
		b.WriteString(fmt.Sprintf("%sAND\n", indent))
		renderExpressionTree(b, e.Left, depth+1)
		renderExpressionTree(b, e.Right, depth+1)

	case *parser.OrExpr:
		b.WriteString(fmt.Sprintf("%sOR\n", indent))
		renderExpressionTree(b, e.Left, depth+1)
		renderExpressionTree(b, e.Right, depth+1)

	case *parser.NotExpr:
		b.WriteString(fmt.Sprintf("%sNOT\n", indent))
		renderExpressionTree(b, e.Expr, depth+1)

	default:
		b.WriteString(fmt.Sprintf("%s%s\n", indent, formatExprValue(e)))
	}
}

func formatExprValue(expr parser.Expression) string {
	switch v := expr.(type) {
	case parser.Value:
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
		}
	case *parser.ColumnRef:
		return v.Name
	}
	return "<expr>"
}
