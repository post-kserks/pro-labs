package optimizer

import (
	"fmt"
	"strings"
	"sync"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

const defaultSampleSize = 1000

// TableStatistics stores statistics for a table for the query optimizer.
type TableStatistics struct {
	TableName   string
	RowCount    int
	ColumnStats map[string]*ColumnStatistics
}

// ColumnStatistics stores statistics for a column.
type ColumnStatistics struct {
	ColumnName      string
	DistinctCount   int
	NullCount       int
	MinValue        interface{}
	MaxValue        interface{}
	MCV             []MCVItem
	HistogramBounds []interface{}
}

// StatisticsCollector collects and caches statistics for tables.
type StatisticsCollector struct {
	mu      sync.RWMutex
	cache   map[string]*TableStatistics // "db/table" → stats
	storage storage.StorageEngine
}

// NewStatisticsCollector creates a collector.
func NewStatisticsCollector(store storage.StorageEngine) *StatisticsCollector {
	return &StatisticsCollector{
		cache:   make(map[string]*TableStatistics),
		storage: store,
	}
}

// GetTableStats returns statistics for a table (cached).
func (sc *StatisticsCollector) GetTableStats(dbName, tableName string) *TableStatistics {
	key := dbName + "/" + tableName

	sc.mu.Lock()
	defer sc.mu.Unlock()

	if stats, ok := sc.cache[key]; ok {
		return stats
	}

	stats := sc.collectStats(dbName, tableName)
	sc.cache[key] = stats

	return stats
}

// InvalidateStats invalidates the statistics cache for a table.
func (sc *StatisticsCollector) InvalidateStats(dbName, tableName string) {
	key := dbName + "/" + tableName
	sc.mu.Lock()
	delete(sc.cache, key)
	sc.mu.Unlock()
}

// collectStats collects statistics for a table.
func (sc *StatisticsCollector) collectStats(dbName, tableName string) *TableStatistics {
	stats := &TableStatistics{
		TableName:   tableName,
		ColumnStats: make(map[string]*ColumnStatistics),
	}

	// Get row count
	rowCount, err := sc.storage.CountRows(dbName, tableName)
	if err == nil {
		stats.RowCount = rowCount
	}

	// Get schema
	schema, err := sc.storage.GetTableSchema(dbName, tableName)
	if err != nil || schema == nil {
		return stats
	}

	// If table is empty — return basic statistics
	if rowCount == 0 {
		if schema == nil {
			return stats
		}
		for _, col := range schema.Columns {
			stats.ColumnStats[strings.ToLower(col.Name)] = &ColumnStatistics{
				ColumnName:    col.Name,
				DistinctCount: 0,
				NullCount:     0,
			}
		}
		return stats
	}

	// Try to get stats from GlobalStatsRegistry (populated by ANALYZE)
	useGlobalStats := false
	for _, col := range schema.Columns {
		if GlobalStatsRegistry.Get(dbName, tableName, col.Name) != nil {
			useGlobalStats = true
			break
		}
	}

	if useGlobalStats {
		for _, col := range schema.Columns {
			gColStats := GlobalStatsRegistry.Get(dbName, tableName, col.Name)
			if gColStats != nil {
				colStats := &ColumnStatistics{
					ColumnName:      col.Name,
					DistinctCount:   int(gColStats.DistinctValues),
					NullCount:       int(float64(rowCount) * gColStats.NullFraction),
					HistogramBounds: gColStats.Histogram,
				}
				// MCV in GlobalStatsRegistry doesn't have frequencies explicitly,
				// but we can just use the bounds for histograms.
				stats.ColumnStats[strings.ToLower(col.Name)] = colStats
			}
		}
		return stats
	}

	// For statistics collection, read a limited sample — without full scan.
	rows, err := sc.storage.ReadSampleRows(dbName, tableName, defaultSampleSize)
	if err != nil {
		return stats
	}

	// Collect statistics for each column
	for colIdx, col := range schema.Columns {
		colStats := &ColumnStatistics{
			ColumnName: col.Name,
		}

		distinctValues := make(map[interface{}]bool)
		nullCount := 0
		colVals := make([]interface{}, 0, len(rows))

		for _, row := range rows {
			var val interface{}
			if colIdx < len(row) {
				val = row[colIdx]
			}
			colVals = append(colVals, val)
			if val == nil {
				nullCount++
			} else {
				// Use fmt.Sprintf for unhashable types (slices, maps)
				key := fmt.Sprintf("%v", val)
				distinctValues[key] = true
			}
		}

		colStats.DistinctCount = len(distinctValues)
		colStats.NullCount = nullCount
		colStats.MCV, colStats.HistogramBounds = ComputeMCVAndHistogram(colVals, 10, 10, rowCount)
		stats.ColumnStats[strings.ToLower(col.Name)] = colStats
	}

	return stats
}

// EstimateSelectivity estimates predicate selectivity (0.0 - 1.0).
func (sc *StatisticsCollector) EstimateSelectivity(dbName, tableName string, predicate interface{}) float64 {
	if predicate == nil {
		return 1.0
	}

	stats := sc.GetTableStats(dbName, tableName)
	if stats.RowCount == 0 {
		return 0.0
	}

	switch p := predicate.(type) {
	case *parser.BinaryExpr:
		// Extract column name for column-specific selectivity
		colName := ""
		var literalExpr parser.Expression
		op := p.Operator

		if col, ok := p.Left.(*parser.ColumnRef); ok {
			colName = strings.ToLower(col.Name)
			literalExpr = p.Right
		} else if col, ok := p.Right.(*parser.ColumnRef); ok {
			colName = strings.ToLower(col.Name)
			literalExpr = p.Left
			switch op {
			case "<":
				op = ">"
			case ">":
				op = "<"
			case "<=":
				op = ">="
			case ">=":
				op = "<="
			}
		}

		if colName != "" {
			if colStats, ok := stats.ColumnStats[colName]; ok {
				if literalVal, hasLit := extractLiteralValue(literalExpr); hasLit {
					if sel, found := sc.estimateSelectivityWithMCVAndHistogram(colStats, op, literalVal); found {
						return sel
					}
				}
				if colStats.DistinctCount > 0 {
					return sc.estimateBinarySelectivityWithStats(stats, colStats, op)
				}
			}
		}
		return sc.estimateBinarySelectivity(stats, op)
	case *parser.AndExpr:
		left := sc.EstimateSelectivity(dbName, tableName, p.Left)
		right := sc.EstimateSelectivity(dbName, tableName, p.Right)
		return left * right
	case *parser.OrExpr:
		left := sc.EstimateSelectivity(dbName, tableName, p.Left)
		right := sc.EstimateSelectivity(dbName, tableName, p.Right)
		return left + right - left*right
	}

	return 0.3
}

// extractLiteralValue extracts Go value from AST expression if it represents a literal.
func extractLiteralValue(expr parser.Expression) (interface{}, bool) {
	if expr == nil {
		return nil, false
	}
	switch v := expr.(type) {
	case *parser.Value:
		switch v.Type {
		case "int":
			return v.IntVal, true
		case "float":
			return v.FltVal, true
		case "string":
			return v.StrVal, true
		case "bool":
			return v.BoolVal, true
		case "null":
			return nil, true
		default:
			return nil, false
		}
	case parser.Value:
		switch v.Type {
		case "int":
			return v.IntVal, true
		case "float":
			return v.FltVal, true
		case "string":
			return v.StrVal, true
		case "bool":
			return v.BoolVal, true
		case "null":
			return nil, true
		default:
			return nil, false
		}
	}
	if val, ok := expr.(interface{}); ok {
		switch x := val.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, string, bool:
			return x, true
		}
	}
	return nil, false
}

// estimateSelectivityWithMCVAndHistogram checks MCV and HistogramBounds for selectivity.
func (sc *StatisticsCollector) estimateSelectivityWithMCVAndHistogram(colStats *ColumnStatistics, op string, literalVal interface{}) (float64, bool) {
	if colStats == nil {
		return 0, false
	}

	switch op {
	case "=":
		for _, mcvItem := range colStats.MCV {
			if compareValues(literalVal, mcvItem.Value) == 0 {
				return mcvItem.Frequency, true
			}
		}
		if len(colStats.MCV) > 0 && colStats.DistinctCount > len(colStats.MCV) {
			sumMCV := 0.0
			for _, item := range colStats.MCV {
				sumMCV += item.Frequency
			}
			remFreq := 1.0 - sumMCV
			if remFreq < 0 {
				remFreq = 0
			}
			remDistinct := float64(colStats.DistinctCount - len(colStats.MCV))
			if remDistinct > 0 {
				return remFreq / remDistinct, true
			}
		}
		return 0, false

	case "!=", "<>":
		for _, mcvItem := range colStats.MCV {
			if compareValues(literalVal, mcvItem.Value) == 0 {
				return 1.0 - mcvItem.Frequency, true
			}
		}
		return 0, false

	case ">", "<", ">=", "<=":
		if len(colStats.HistogramBounds) > 0 {
			return sc.estimateHistogramSelectivity(colStats.HistogramBounds, literalVal, op), true
		}
		return 0, false
	}

	return 0, false
}

func (sc *StatisticsCollector) estimateHistogramSelectivity(bounds []interface{}, literalVal interface{}, op string) float64 {
	if len(bounds) == 0 {
		return 0.3
	}
	if len(bounds) == 1 {
		if op == "<" || op == "<=" {
			if compareValues(literalVal, bounds[0]) > 0 {
				return 1.0
			}
			if compareValues(literalVal, bounds[0]) < 0 {
				return 0.0
			}
			return 0.1
		}
		if op == ">" || op == ">=" {
			if compareValues(literalVal, bounds[0]) < 0 {
				return 1.0
			}
			if compareValues(literalVal, bounds[0]) > 0 {
				return 0.0
			}
			return 0.1
		}
		return 0.3
	}

	numBuckets := len(bounds) - 1
	if numBuckets <= 0 {
		return 0.3
	}

	fractionBelow := 0.0
	for j := 0; j < numBuckets; j++ {
		low := bounds[j]
		high := bounds[j+1]
		cmpLow := compareValues(literalVal, low)
		cmpHigh := compareValues(literalVal, high)

		if cmpHigh >= 0 {
			fractionBelow += 1.0
		} else if cmpLow <= 0 {
			fractionBelow += 0.0
		} else {
			flow, ok1 := toFloat64(low)
			fhigh, ok2 := toFloat64(high)
			fv, ok3 := toFloat64(literalVal)
			if ok1 && ok2 && ok3 && fhigh > flow {
				fractionBelow += (fv - flow) / (fhigh - flow)
			} else {
				fractionBelow += 0.5
			}
		}
	}

	selBelow := fractionBelow / float64(numBuckets)
	if selBelow < 0.0 {
		selBelow = 0.0
	}
	if selBelow > 1.0 {
		selBelow = 1.0
	}

	if op == "<" || op == "<=" {
		return selBelow
	}
	if op == ">" || op == ">=" {
		return 1.0 - selBelow
	}
	return 0.3
}

// estimateBinarySelectivityWithStats uses actual column statistics for selectivity.
func (sc *StatisticsCollector) estimateBinarySelectivityWithStats(tableStats *TableStatistics, colStats *ColumnStatistics, operator string) float64 {
	switch operator {
	case "=":
		// Equality: 1 / distinct_count (uniform distribution assumption)
		return 1.0 / float64(colStats.DistinctCount)
	case "!=", "<>":
		return 1.0 - 1.0/float64(colStats.DistinctCount)
	case "<", ">", "<=", ">=":
		// Range: assume uniform distribution, ~30% for open ranges
		return 0.3
	case "LIKE":
		return 0.2
	default:
		return 0.3
	}
}

// estimateBinarySelectivity estimates selectivity for binary operations.
func (sc *StatisticsCollector) estimateBinarySelectivity(stats *TableStatistics, operator string) float64 {
	switch operator {
	case "=":
		// Equality: 1/distinct_count (if index exists)
		return 0.1
	case "!=", "<>":
		// Inequality: 1 - selectivity(=)
		return 0.9
	case "<", ">", "<=", ">=":
		// Range: ~30%
		return 0.3
	case "LIKE":
		// Pattern matching: ~20%
		return 0.2
	default:
		return 0.3
	}
}
