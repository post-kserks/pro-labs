package parallel

import (
	"fmt"
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func benchRows(n int) []storage.Row {
	rows := make([]storage.Row, n)
	for i := 0; i < n; i++ {
		rows[i] = storage.Row{
			int64(i),
			float64(i) * 1.5,
			fmt.Sprintf("name_%d", i),
			i%2 == 0,
		}
	}
	return rows
}

func benchSchema() *storage.TableSchema {
	return &storage.TableSchema{
		Name: "bench",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "val", Type: "FLOAT"},
			{Name: "name", Type: "TEXT"},
			{Name: "flag", Type: "BOOL"},
		},
	}
}

func BenchmarkSequentialFilter_10k(b *testing.B) {
	rows := benchRows(10000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 5000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filtered := make([]storage.Row, 0, len(rows)/2)
		for _, row := range rows {
			ok, _ := testEvaluator.EvalExpr(where, row, schema, nil)
			if ok {
				_ = append(filtered, row)
			}
		}
	}
}

func BenchmarkParallelFilter_10k_2(b *testing.B) {
	rows := benchRows(10000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 5000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(2, testEvaluator)
		pc.ParallelFilter(rows, schema, where, nil)
	}
}

func BenchmarkParallelFilter_10k_4(b *testing.B) {
	rows := benchRows(10000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 5000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(4, testEvaluator)
		pc.ParallelFilter(rows, schema, where, nil)
	}
}

func BenchmarkParallelFilter_10k_8(b *testing.B) {
	rows := benchRows(10000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 5000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(8, testEvaluator)
		pc.ParallelFilter(rows, schema, where, nil)
	}
}

func BenchmarkSequentialFilter_100k(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 50000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filtered := make([]storage.Row, 0, len(rows)/2)
		for _, row := range rows {
			ok, _ := testEvaluator.EvalExpr(where, row, schema, nil)
			if ok {
				_ = append(filtered, row)
			}
		}
	}
}

func BenchmarkParallelFilter_100k_4(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 50000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(4, testEvaluator)
		pc.ParallelFilter(rows, schema, where, nil)
	}
}

func BenchmarkParallelFilter_100k_8(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 50000},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(8, testEvaluator)
		pc.ParallelFilter(rows, schema, where, nil)
	}
}

func BenchmarkSequentialProject_100k(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	columns := []parser.SelectColumn{
		{Expr: &parser.ColumnRef{Name: "id"}},
		{Expr: &parser.ColumnRef{Name: "name"}},
		{Expr: &parser.ColumnRef{Name: "val"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		projected := make([][]string, 0, len(rows))
		for _, row := range rows {
			projectedRow := make([]string, len(columns))
			for j, col := range columns {
				val, _ := testEvaluator.EvalOperand(col.Expr, row, schema, nil)
				projectedRow[j] = testEvaluator.ValueToString(val)
			}
			_ = append(projected, projectedRow)
		}
	}
}

func BenchmarkParallelProject_100k_4(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	columns := []parser.SelectColumn{
		{Expr: &parser.ColumnRef{Name: "id"}},
		{Expr: &parser.ColumnRef{Name: "name"}},
		{Expr: &parser.ColumnRef{Name: "val"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(4, testEvaluator)
		pc.ParallelProject(rows, columns, schema, nil)
	}
}

func BenchmarkParallelProject_100k_8(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	columns := []parser.SelectColumn{
		{Expr: &parser.ColumnRef{Name: "id"}},
		{Expr: &parser.ColumnRef{Name: "name"}},
		{Expr: &parser.ColumnRef{Name: "val"}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(8, testEvaluator)
		pc.ParallelProject(rows, columns, schema, nil)
	}
}

func BenchmarkSequentialAggregate_100k(b *testing.B) {
	rows := benchRows(100000)
	stmt := &parser.SelectStatement{
		TableName: "bench",
		Columns: []parser.SelectColumn{
			{Expr: &parser.AggregateExpr{Name: "COUNT", Args: []parser.Expression{&parser.ColumnRef{Name: "*"}}}},
			{Expr: &parser.AggregateExpr{Name: "SUM", Args: []parser.Expression{&parser.ColumnRef{Name: "val"}}}},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groups := make(map[string][]storage.Row)
		groups[""] = rows
		groupOrder := []string{""}

		projectColumns := make([]string, len(stmt.Columns))
		for j, col := range stmt.Columns {
			if colRef, ok := col.Expr.(*parser.ColumnRef); ok {
				projectColumns[j] = colRef.Name
			} else if aggExpr, ok := col.Expr.(*parser.AggregateExpr); ok {
				projectColumns[j] = aggExpr.Name
			}
		}

		_ = groups
		_ = groupOrder
		_ = projectColumns
	}
}

func BenchmarkParallelAggregate_100k_4(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	stmt := &parser.SelectStatement{
		TableName: "bench",
		Columns: []parser.SelectColumn{
			{Expr: &parser.AggregateExpr{Name: "COUNT", Args: []parser.Expression{&parser.ColumnRef{Name: "*"}}}},
			{Expr: &parser.AggregateExpr{Name: "SUM", Args: []parser.Expression{&parser.ColumnRef{Name: "val"}}}},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(4, testEvaluator)
		pc.ParallelGroupAndAggregate(stmt, rows, schema, "", nil)
	}
}

func BenchmarkParallelAggregate_100k_8(b *testing.B) {
	rows := benchRows(100000)
	schema := benchSchema()
	stmt := &parser.SelectStatement{
		TableName: "bench",
		Columns: []parser.SelectColumn{
			{Expr: &parser.AggregateExpr{Name: "COUNT", Args: []parser.Expression{&parser.ColumnRef{Name: "*"}}}},
			{Expr: &parser.AggregateExpr{Name: "SUM", Args: []parser.Expression{&parser.ColumnRef{Name: "val"}}}},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pc := NewParallelCoordinator(8, testEvaluator)
		pc.ParallelGroupAndAggregate(stmt, rows, schema, "", nil)
	}
}
