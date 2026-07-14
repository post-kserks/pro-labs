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
	ColumnName    string
	DistinctCount int
	NullCount     int
	MinValue      interface{}
	MaxValue      interface{}
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

	// Get schema
	schema, err := sc.storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return stats
	}

	// Get row count
	rowCount, err := sc.storage.CountRows(dbName, tableName)
	if err == nil {
		stats.RowCount = rowCount
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

		for _, row := range rows {
			if colIdx >= len(row) {
				continue
			}
			val := row[colIdx]
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
		if col, ok := p.Left.(*parser.ColumnRef); ok {
			colName = strings.ToLower(col.Name)
		} else if col, ok := p.Right.(*parser.ColumnRef); ok {
			colName = strings.ToLower(col.Name)
		}
		if colName != "" {
			if colStats, ok := stats.ColumnStats[colName]; ok && colStats.DistinctCount > 0 {
				return sc.estimateBinarySelectivityWithStats(stats, colStats, p.Operator)
			}
		}
		return sc.estimateBinarySelectivity(stats, p.Operator)
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
