package parallel

import (
	"fmt"
	"runtime"
	"testing"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// mockEvaluator implements Evaluator for testing.
type mockEvaluator struct{}

func (m *mockEvaluator) EvalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (bool, error) {
	if expr == nil {
		return true, nil
	}
	if be, ok := expr.(*parser.BinaryExpr); ok {
		left, _ := m.EvalOperand(be.Left, row, schema, ctx)
		right, _ := m.EvalOperand(be.Right, row, schema, ctx)
		switch be.Operator {
		case ">":
			l, _ := left.(int64)
			r, _ := right.(int64)
			return l > r, nil
		case "<":
			l, _ := left.(int64)
			r, _ := right.(int64)
			return l < r, nil
		case "=":
			return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right), nil
		}
	}
	return true, nil
}

func (m *mockEvaluator) EvalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (interface{}, error) {
	if expr == nil {
		return nil, nil
	}
	if colRef, ok := expr.(*parser.ColumnRef); ok {
		for i, col := range schema.Columns {
			if col.Name == colRef.Name && i < len(row) {
				return row[i], nil
			}
		}
	}
	if val, ok := expr.(*parser.Value); ok {
		return val.IntVal, nil
	}
	if be, ok := expr.(*parser.BinaryExpr); ok {
		left, _ := m.EvalOperand(be.Left, row, schema, ctx)
		right, _ := m.EvalOperand(be.Right, row, schema, ctx)
		if l, ok := left.(int64); ok {
			if r, ok := right.(int64); ok {
				switch be.Operator {
				case "*":
					return l * r, nil
				case "+":
					return l + r, nil
				}
			}
		}
	}
	return nil, nil
}

func (m *mockEvaluator) ValueToString(val interface{}) string {
	return fmt.Sprintf("%v", val)
}

func (m *mockEvaluator) CollectAggregates(columns []parser.SelectColumn) []*parser.AggregateExpr {
	var result []*parser.AggregateExpr
	for _, col := range columns {
		if agg, ok := col.Expr.(*parser.AggregateExpr); ok {
			result = append(result, agg)
		}
	}
	return result
}

func (m *mockEvaluator) CompareValues(a, b interface{}) int {
	sa, sb := fmt.Sprintf("%v", a), fmt.Sprintf("%v", b)
	if sa < sb {
		return -1
	}
	if sa > sb {
		return 1
	}
	return 0
}

func (m *mockEvaluator) NewAggregator(name string, distinct bool, args ...interface{}) Aggregator {
	return &mockAggregator{name: name}
}

type mockAggregator struct {
	name   string
	count  int64
	result interface{}
}

func (a *mockAggregator) Add(key, value interface{}) {
	a.count++
	a.result = a.count
}

func (a *mockAggregator) Result() interface{} {
	return a.result
}

var testEvaluator Evaluator = &mockEvaluator{}

func makeLargeRows(n int) []storage.Row {
	rows := make([]storage.Row, n)
	for i := 0; i < n; i++ {
		rows[i] = storage.Row{
			int64(i),
			float64(i) * 1.5,
			fmt.Sprintf("row_%d", i),
			i%2 == 0,
		}
	}
	return rows
}

func largeTableSchema() *storage.TableSchema {
	return &storage.TableSchema{
		Name: "test_table",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "val", Type: "FLOAT"},
			{Name: "name", Type: "TEXT"},
			{Name: "flag", Type: "BOOL"},
		},
	}
}

func TestParallelFilter_Basic(t *testing.T) {
	rows := makeLargeRows(1000)
	schema := largeTableSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 500},
	}

	pc := NewParallelCoordinator(4, testEvaluator)
	result := pc.ParallelFilter(rows, schema, where, nil)

	if len(result) != 499 {
		t.Errorf("expected 499 rows (id > 500 = 501..999), got %d", len(result))
	}

	for _, row := range result {
		if row[0].(int64) <= 500 {
			t.Errorf("row id %d should be > 500", row[0])
		}
	}
}

func TestParallelFilter_Empty(t *testing.T) {
	pc := NewParallelCoordinator(4, testEvaluator)
	result := pc.ParallelFilter(nil, nil, nil, nil)
	if len(result) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result))
	}
}

func TestParallelFilter_AllMatch(t *testing.T) {
	rows := makeLargeRows(100)
	schema := largeTableSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: -1},
	}

	pc := NewParallelCoordinator(4, testEvaluator)
	result := pc.ParallelFilter(rows, schema, where, nil)

	if len(result) != 100 {
		t.Errorf("expected 100 rows, got %d", len(result))
	}
}

func TestParallelFilter_NoneMatch(t *testing.T) {
	rows := makeLargeRows(100)
	schema := largeTableSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 9999},
	}

	pc := NewParallelCoordinator(4, testEvaluator)
	result := pc.ParallelFilter(rows, schema, where, nil)

	if len(result) != 0 {
		t.Errorf("expected 0 rows, got %d", len(result))
	}
}

func TestParallelFilter_SingleWorker(t *testing.T) {
	rows := makeLargeRows(500)
	schema := largeTableSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 250},
	}

	pc := NewParallelCoordinator(1, testEvaluator)
	result := pc.ParallelFilter(rows, schema, where, nil)

	if len(result) != 249 {
		t.Errorf("expected 249 rows, got %d", len(result))
	}
}

func TestParallelProject_Basic(t *testing.T) {
	rows := makeLargeRows(1000)
	schema := largeTableSchema()
	columns := []parser.SelectColumn{
		{Expr: &parser.ColumnRef{Name: "id"}},
		{Expr: &parser.ColumnRef{Name: "name"}},
	}

	pc := NewParallelCoordinator(4, testEvaluator)
	result := pc.ParallelProject(rows, columns, schema, nil)

	if len(result) != 1000 {
		t.Errorf("expected 1000 rows, got %d", len(result))
	}
	for i, row := range result {
		if len(row) != 2 {
			t.Errorf("row %d: expected 2 columns, got %d", i, len(row))
		}
	}
}

func TestParallelProject_Expression(t *testing.T) {
	rows := makeLargeRows(500)
	schema := largeTableSchema()
	columns := []parser.SelectColumn{
		{Expr: &parser.BinaryExpr{
			Left:     &parser.ColumnRef{Name: "id"},
			Operator: "*",
			Right:    &parser.Value{Type: "int", IntVal: 2},
		}},
	}

	pc := NewParallelCoordinator(2, testEvaluator)
	result := pc.ParallelProject(rows, columns, schema, nil)

	if len(result) != 500 {
		t.Errorf("expected 500 rows, got %d", len(result))
	}
	if result[0][0] != "0" {
		t.Errorf("expected first value '0', got %q", result[0][0])
	}
	if result[1][0] != "2" {
		t.Errorf("expected second value '2', got %q", result[1][0])
	}
}

func TestShouldUseParallel(t *testing.T) {
	tests := []struct {
		name         string
		config       ParallelConfig
		numRows      int
		hasJoins     bool
		hasOrder     bool
		wantParallel bool
	}{
		{
			name:         "disabled",
			config:       ParallelConfig{Enabled: false, MinRows: 100},
			numRows:      1000,
			wantParallel: false,
		},
		{
			name:         "below threshold",
			config:       ParallelConfig{Enabled: true, MinRows: 100},
			numRows:      50,
			wantParallel: false,
		},
		{
			name:         "has joins",
			config:       ParallelConfig{Enabled: true, MinRows: 100},
			numRows:      1000,
			hasJoins:     true,
			wantParallel: false,
		},
		{
			name:         "has order by",
			config:       ParallelConfig{Enabled: true, MinRows: 100},
			numRows:      1000,
			hasOrder:     true,
			wantParallel: false,
		},
		{
			name:         "eligible",
			config:       ParallelConfig{Enabled: true, MinRows: 100},
			numRows:      1000,
			wantParallel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldUseParallel(tt.config, tt.numRows, tt.hasJoins, tt.hasOrder)
			if got != tt.wantParallel {
				t.Errorf("ShouldUseParallel() = %v, want %v", got, tt.wantParallel)
			}
		})
	}
}

func TestNewParallelCoordinator_MinWorkers(t *testing.T) {
	pc := NewParallelCoordinator(0, testEvaluator)
	if pc.numWorkers != 2 {
		t.Errorf("expected minimum 2 workers, got %d", pc.numWorkers)
	}
}

func TestDefaultParallelConfig(t *testing.T) {
	cfg := DefaultParallelConfig()
	if !cfg.Enabled {
		t.Error("expected Enabled=true")
	}
	if cfg.NumWorkers < 2 {
		t.Errorf("expected NumWorkers >= 2, got %d", cfg.NumWorkers)
	}
	if cfg.MinRows != 10000 {
		t.Errorf("expected MinRows=10000, got %d", cfg.MinRows)
	}
}

func TestParallelFilter_ConcurrentAccess(t *testing.T) {
	rows := makeLargeRows(10000)
	schema := largeTableSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 5000},
	}

	// Run multiple times to catch race conditions
	for trial := 0; trial < 5; trial++ {
		pc := NewParallelCoordinator(runtime.NumCPU(), testEvaluator)
		result := pc.ParallelFilter(rows, schema, where, nil)
		if len(result) != 4999 {
			t.Errorf("trial %d: expected 4999 rows (id > 5000 = 5001..9999), got %d", trial, len(result))
		}
	}
}

func TestParallelFilter_SmallTable(t *testing.T) {
	// Verify parallel filter works correctly with fewer rows than workers
	rows := makeLargeRows(3)
	schema := largeTableSchema()
	where := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "id"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 0},
	}

	pc := NewParallelCoordinator(8, testEvaluator)
	result := pc.ParallelFilter(rows, schema, where, nil)

	if len(result) != 2 {
		t.Errorf("expected 2 rows, got %d", len(result))
	}
}

func TestParallelFilter_ParallelWorkersActive(t *testing.T) {
	rows := makeLargeRows(10000)

	// Verify the coordinator uses correct chunk splitting
	pc := NewParallelCoordinator(4, testEvaluator)
	chunks := pc.splitRows(rows)
	if len(chunks) != 4 {
		t.Errorf("expected 4 chunks, got %d", len(chunks))
	}

	totalLen := 0
	for _, c := range chunks {
		totalLen += len(c)
	}
	if totalLen != 10000 {
		t.Errorf("expected total 10000 rows across chunks, got %d", totalLen)
	}
}
