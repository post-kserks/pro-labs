package executor

import (
	"strings"
	"sync"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

const defaultSampleSize = 1000

// TableStatistics хранит статистику по таблице для query optimizer.
type TableStatistics struct {
	TableName   string
	RowCount    int
	ColumnStats map[string]*ColumnStatistics
}

// ColumnStatistics хранит статистику по столбцу.
type ColumnStatistics struct {
	ColumnName    string
	DistinctCount int
	NullCount     int
	MinValue      interface{}
	MaxValue      interface{}
}

// StatisticsCollector собирает и кэширует статистику по таблицам.
type StatisticsCollector struct {
	mu      sync.RWMutex
	cache   map[string]*TableStatistics // "db/table" → stats
	storage storage.StorageEngine
}

// NewStatisticsCollector создаёт collector.
func NewStatisticsCollector(store storage.StorageEngine) *StatisticsCollector {
	return &StatisticsCollector{
		cache:   make(map[string]*TableStatistics),
		storage: store,
	}
}

// GetTableStats возвращает статистику по таблице (кэшируется).
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

// InvalidateStats сбрасывает кэш статистики для таблицы.
func (sc *StatisticsCollector) InvalidateStats(dbName, tableName string) {
	key := dbName + "/" + tableName
	sc.mu.Lock()
	delete(sc.cache, key)
	sc.mu.Unlock()
}

// collectStats собирает статистику по таблице.
func (sc *StatisticsCollector) collectStats(dbName, tableName string) *TableStatistics {
	stats := &TableStatistics{
		TableName:   tableName,
		ColumnStats: make(map[string]*ColumnStatistics),
	}

	// Получаем схему
	schema, err := sc.storage.GetTableSchema(dbName, tableName)
	if err != nil {
		return stats
	}

	// Получаем количество строк
	rowCount, err := sc.storage.CountRows(dbName, tableName)
	if err == nil {
		stats.RowCount = rowCount
	}

	// Если таблица пуста — возвращаем базовую статистику
	if rowCount == 0 {
		for _, col := range schema.Columns {
			stats.ColumnStats[strings.ToLower(col.Name)] = &ColumnStatistics{
				ColumnName:    col.Name,
				DistinctCount: 0,
				NullCount:     0,
			}
		}
		return stats
	}

	// Для сбора статистики читаем ограниченную выборку — без полного скана.
	rows, err := sc.storage.ReadSampleRows(dbName, tableName, defaultSampleSize)
	if err != nil {
		return stats
	}

	// Собираем статистику по каждому столбцу
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
				distinctValues[val] = true
			}
		}

		colStats.DistinctCount = len(distinctValues)
		colStats.NullCount = nullCount
		stats.ColumnStats[strings.ToLower(col.Name)] = colStats
	}

	return stats
}

// EstimateSelectivity оценивает селективность предиката (0.0 - 1.0).
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

// estimateBinarySelectivity оценивает селективность для бинарных операций.
func (sc *StatisticsCollector) estimateBinarySelectivity(stats *TableStatistics, operator string) float64 {
	switch operator {
	case "=":
		// Равенство: 1/distinct_count (если есть индекс)
		return 0.1
	case "!=", "<>":
		// Неравенство: 1 - selectivity(=)
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
