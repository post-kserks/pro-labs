package optimizer

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

func TestComputeMCVAndHistogram_SkewedDataset(t *testing.T) {
	// 100 total rows:
	// Value 100 appears 50 times (freq 0.50 >= 0.05) -> MCV #1
	// Value 200 appears 25 times (freq 0.25 >= 0.05) -> MCV #2
	// Value 300 appears 5 times (freq 0.05 >= 0.05) -> MCV #3
	// Remaining 20 occurrences are distinct values from 401 to 420 (each freq 0.01 < 0.05)
	colVals := make([]interface{}, 0, 100)
	for i := 0; i < 50; i++ {
		colVals = append(colVals, 100)
	}
	for i := 0; i < 25; i++ {
		colVals = append(colVals, 200)
	}
	for i := 0; i < 5; i++ {
		colVals = append(colVals, 300)
	}
	for i := 401; i <= 420; i++ {
		colVals = append(colVals, i)
	}

	mcv, hist := ComputeMCVAndHistogram(colVals, 10, 5, 100)

	// Verify MCV items
	if len(mcv) != 3 {
		t.Fatalf("expected 3 MCV items, got %d: %+v", len(mcv), mcv)
	}
	expectedMCV := []MCVItem{
		{Value: 100, Frequency: 0.50},
		{Value: 200, Frequency: 0.25},
		{Value: 300, Frequency: 0.05},
	}
	for i, exp := range expectedMCV {
		if compareValues(mcv[i].Value, exp.Value) != 0 {
			t.Errorf("MCV[%d] value mismatch: expected %v, got %v", i, exp.Value, mcv[i].Value)
		}
		if math.Abs(mcv[i].Frequency-exp.Frequency) > 1e-6 {
			t.Errorf("MCV[%d] frequency mismatch: expected %f, got %f", i, exp.Frequency, mcv[i].Frequency)
		}
	}

	// Verify histogram boundaries
	// With 20 remaining values (401..420) and maxBuckets=5, we get 5 equi-depth boundaries
	// step = 20 / 5 = 4.0
	// indices: 0, 4, 8, 12, 16 -> values: 401, 405, 409, 413, 417
	if len(hist) != 5 {
		t.Fatalf("expected 5 histogram boundaries, got %d: %+v", len(hist), hist)
	}
	expectedHist := []interface{}{401, 405, 409, 413, 417}
	for i, exp := range expectedHist {
		if compareValues(hist[i], exp) != 0 {
			t.Errorf("histogram[%d] value mismatch: expected %v, got %v", i, exp, hist[i])
		}
	}
}

func TestComputeMCVAndHistogram_EmptyAndNulls(t *testing.T) {
	// Empty values
	mcv, hist := ComputeMCVAndHistogram([]interface{}{}, 10, 10, 0)
	if mcv == nil || len(mcv) != 0 {
		t.Errorf("expected non-nil empty MCV for empty input, got %v", mcv)
	}
	if hist == nil || len(hist) != 0 {
		t.Errorf("expected non-nil empty histogram for empty input, got %v", hist)
	}

	// All nil values
	mcv, hist = ComputeMCVAndHistogram([]interface{}{nil, nil, nil}, 10, 10, 3)
	if len(mcv) != 0 || len(hist) != 0 {
		t.Errorf("expected empty MCV and histogram for all-nil input, got mcv=%v, hist=%v", mcv, hist)
	}

	// Mix of nulls and single value
	vals := []interface{}{nil, 42, 42, nil, 42, nil, 42, 42, 42, nil} // 6 non-nil out of 10 total (60%)
	mcv, hist = ComputeMCVAndHistogram(vals, 10, 10, 10)
	if len(mcv) != 1 || compareValues(mcv[0].Value, 42) != 0 || math.Abs(mcv[0].Frequency-0.6) > 1e-6 {
		t.Errorf("unexpected MCV for mixed nulls: %+v", mcv)
	}
	if len(hist) != 0 {
		t.Errorf("expected empty histogram when all non-nil values are in MCV, got %v", hist)
	}
}

func TestComputeMCVAndHistogram_StringsAndTypes(t *testing.T) {
	vals := []interface{}{"apple", "apple", "apple", "apple", "apple", "banana", "cherry", "date", "berry", "grape"}
	// 10 total values: "apple" appears 5 times (0.50 >= 0.05 -> MCV #1)
	// With maxMCV=1, only "apple" is picked into MCV.
	// Remaining 5 values: "banana", "cherry", "date", "berry", "grape"
	// Sorted remaining: "banana", "berry", "cherry", "date", "grape"
	mcv, hist := ComputeMCVAndHistogram(vals, 1, 5, 10)
	if len(mcv) != 1 || mcv[0].Value != "apple" || math.Abs(mcv[0].Frequency-0.50) > 1e-6 {
		t.Fatalf("unexpected string MCV: %+v", mcv)
	}
	if len(hist) != 5 {
		t.Fatalf("expected 5 histogram bounds for remaining strings, got %v", hist)
	}
	expected := []string{"banana", "berry", "cherry", "date", "grape"}
	for i, exp := range expected {
		if hist[i] != exp {
			t.Errorf("hist[%d] mismatch: expected %s, got %v", i, exp, hist[i])
		}
	}
}

func TestCollectStats_PopulatesMCVAndHistogram(t *testing.T) {
	store := newMockStorage()
	dbName := "testdb"
	tableName := "users"
	_ = store.CreateDatabase(dbName)
	_ = store.CreateTable(dbName, storage.TableSchema{
		Name: tableName,
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "int"},
			{Name: "role", Type: "string"},
		},
	})

	// Insert 20 rows: role="admin" 10 times (0.5), role="guest" 6 times (0.3), role="user1".."user4" 1 each (0.05)
	for i := 1; i <= 10; i++ {
		store.rows[dbName][tableName] = append(store.rows[dbName][tableName], storage.Row{i, "admin"})
	}
	for i := 11; i <= 16; i++ {
		store.rows[dbName][tableName] = append(store.rows[dbName][tableName], storage.Row{i, "guest"})
	}
	for i := 17; i <= 20; i++ {
		store.rows[dbName][tableName] = append(store.rows[dbName][tableName], storage.Row{i, fmt.Sprintf("user%d", i-16)})
	}

	sc := NewStatisticsCollector(store)
	stats := sc.GetTableStats(dbName, tableName)

	roleStats, ok := stats.ColumnStats["role"]
	if !ok {
		t.Fatal("role column stats not collected")
	}

	// Admin (0.50), guest (0.30), user1..user4 (0.05 each) -> all frequency >= 0.05!
	// With maxMCV=10, all 6 distinct values become MCVs
	if len(roleStats.MCV) != 6 {
		t.Fatalf("expected 6 MCVs for role column, got %d: %+v", len(roleStats.MCV), roleStats.MCV)
	}
}

func TestEstimateSelectivity_ExactMCVAndRangeHistogram(t *testing.T) {
	store := newMockStorage()
	dbName := "shop"
	tableName := "orders"
	_ = store.CreateDatabase(dbName)
	_ = store.CreateTable(dbName, storage.TableSchema{
		Name: tableName,
		Columns: []storage.ColumnSchema{
			{Name: "amount", Type: "int"},
		},
	})

	// Add synthetic rows so CountRows returns 100
	store.rows[dbName][tableName] = make([]storage.Row, 100)

	sc := NewStatisticsCollector(store)
	// Inject synthetic statistics
	stats := &TableStatistics{
		TableName:   tableName,
		RowCount:    100,
		ColumnStats: make(map[string]*ColumnStatistics),
	}
	colStats := &ColumnStatistics{
		ColumnName:    "amount",
		DistinctCount: 20,
		MCV: []MCVItem{
			{Value: int64(100), Frequency: 0.35},
			{Value: int64(200), Frequency: 0.25},
		},
		HistogramBounds: []interface{}{int64(10), int64(30), int64(50), int64(70), int64(90)},
	}
	stats.ColumnStats["amount"] = colStats
	sc.cache[dbName+"/"+tableName] = stats

	// Test exact match (=) for MCV value 100
	predEqMCV := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "amount"},
		Operator: "=",
		Right:    &parser.Value{Type: "int", IntVal: 100},
	}
	sel := sc.EstimateSelectivity(dbName, tableName, predEqMCV)
	if math.Abs(sel-0.35) > 1e-6 {
		t.Errorf("expected exact MCV frequency 0.35, got %f", sel)
	}

	// Test exact match (=) when literal is on Left
	predEqMCVLeft := &parser.BinaryExpr{
		Left:     &parser.Value{Type: "int", IntVal: 200},
		Operator: "=",
		Right:    &parser.ColumnRef{Name: "amount"},
	}
	sel = sc.EstimateSelectivity(dbName, tableName, predEqMCVLeft)
	if math.Abs(sel-0.25) > 1e-6 {
		t.Errorf("expected exact MCV frequency 0.25 when literal on Left, got %f", sel)
	}

	// Test inequality (!=) for MCV value 100
	predNeqMCV := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "amount"},
		Operator: "!=",
		Right:    &parser.Value{Type: "int", IntVal: 100},
	}
	sel = sc.EstimateSelectivity(dbName, tableName, predNeqMCV)
	if math.Abs(sel-0.65) > 1e-6 {
		t.Errorf("expected 1-0.35 = 0.65 for != MCV, got %f", sel)
	}

	// Test range query (<) checking HistogramBounds
	// bounds: [10, 30, 50, 70, 90] -> 4 buckets: [10,30], [30,50], [50,70], [70,90]
	// amount < 50: buckets [10,30] and [30,50] fall entirely below 50 -> 2/4 = 0.50
	predLT := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "amount"},
		Operator: "<",
		Right:    &parser.Value{Type: "int", IntVal: 50},
	}
	sel = sc.EstimateSelectivity(dbName, tableName, predLT)
	if math.Abs(sel-0.50) > 1e-6 {
		t.Errorf("expected 0.50 for amount < 50, got %f", sel)
	}

	// Test range query (>) checking HistogramBounds
	// amount > 50 -> 1 - 0.50 = 0.50
	predGT := &parser.BinaryExpr{
		Left:     &parser.ColumnRef{Name: "amount"},
		Operator: ">",
		Right:    &parser.Value{Type: "int", IntVal: 50},
	}
	sel = sc.EstimateSelectivity(dbName, tableName, predGT)
	if math.Abs(sel-0.50) > 1e-6 {
		t.Errorf("expected 0.50 for amount > 50, got %f", sel)
	}

	// Verify Optimizer wrapper method works and produces identical results
	opt := NewOptimizer(store)
	opt.stats = sc
	optSel := opt.EstimateSelectivity(dbName, tableName, predEqMCV)
	if optSel != 0.35 {
		t.Errorf("Optimizer.EstimateSelectivity returned %f, expected 0.35", optSel)
	}
}

// Ensure unused packages/types are referenced if needed or silence reflection checks
var _ = reflect.TypeOf(MCVItem{})
