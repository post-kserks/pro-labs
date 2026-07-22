package optimizer

import (
	"strings"
	"sync"
)

type ColumnStats struct {
	TableName      string
	ColumnName     string
	TableRowCount  int64
	NullFraction   float64
	DistinctValues int64
	MCV            []interface{} // Most Common Values
	Histogram      []interface{} // Equi-depth Histogram Bounds
}

type StatsRegistry struct {
	mu    sync.RWMutex
	stats map[string]map[string]*ColumnStats // dbName.tableName -> columnName -> Stats
}

var GlobalStatsRegistry = &StatsRegistry{
	stats: make(map[string]map[string]*ColumnStats),
}

func (r *StatsRegistry) Set(dbName, tableName, columnName string, stats *ColumnStats) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(dbName + "." + tableName)
	if r.stats[key] == nil {
		r.stats[key] = make(map[string]*ColumnStats)
	}
	r.stats[key][strings.ToLower(columnName)] = stats
}

func (r *StatsRegistry) Get(dbName, tableName, columnName string) *ColumnStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := strings.ToLower(dbName + "." + tableName)
	if tblStats, ok := r.stats[key]; ok {
		return tblStats[strings.ToLower(columnName)]
	}
	return nil
}

func (r *StatsRegistry) GetTableSize(dbName, tableName string) int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := strings.ToLower(dbName + "." + tableName)
	if tblStats, ok := r.stats[key]; ok {
		// Just return the size from the first column we find
		for _, stat := range tblStats {
			return stat.TableRowCount
		}
	}
	return -1 // Unknown size
}

func (r *StatsRegistry) GetAll() []*ColumnStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var res []*ColumnStats
	for _, tblStats := range r.stats {
		for _, stat := range tblStats {
			res = append(res, stat)
		}
	}
	return res
}
