package parser_test

import (
	"testing"

	"vaultdb/internal/core/parser"
)

func TestParseWindowExprPhase5Track3(t *testing.T) {
	query := "SELECT id, name, ROW_NUMBER() OVER (PARTITION BY department ORDER BY salary DESC) FROM employees;"
	stmt, err := parser.Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", query, err)
	}

	sel, ok := stmt.(*parser.SelectStatement)
	if !ok {
		t.Fatalf("expected *SelectStatement, got %T", stmt)
	}
	if len(sel.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(sel.Columns))
	}

	win, ok := sel.Columns[2].Expr.(*parser.WindowExpr)
	if !ok {
		t.Fatalf("expected *parser.WindowExpr for column 2, got %T", sel.Columns[2].Expr)
	}
	if win.Function != "ROW_NUMBER" {
		t.Fatalf("expected function ROW_NUMBER, got %q", win.Function)
	}
	if len(win.PartitionBy) != 1 {
		t.Fatalf("expected 1 partition column, got %d", len(win.PartitionBy))
	}
	if len(win.OrderBy) != 1 {
		t.Fatalf("expected 1 order column, got %d", len(win.OrderBy))
	}
	if win.OrderBy[0].Direction != "DESC" {
		t.Fatalf("expected direction DESC, got %q", win.OrderBy[0].Direction)
	}
	if win.OrderBy[0].Asc != false {
		t.Fatalf("expected Asc=false for DESC order by, got true")
	}

	expectedStr := "ROW_NUMBER() OVER (PARTITION BY department ORDER BY salary DESC)"
	if gotStr := win.String(); gotStr != expectedStr {
		t.Fatalf("expected String()=%q, got %q", expectedStr, gotStr)
	}
}
