package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// queryClass — категория запроса для счётчиков. Набор известен заранее,
// поэтому на горячем пути используются прямые atomic-счётчики без sync.Map.
type queryClass int

const (
	classSelect queryClass = iota
	classInsert
	classUpdate
	classDelete
	classDDL
	classExplain
	classTransaction
	classOther
	numQueryClasses
)

var queryClassNames = [numQueryClasses]string{
	"select", "insert", "update", "delete", "ddl", "explain", "transaction", "other",
}

// QueryCounters — счётчики ok/error по каждой категории запросов.
type QueryCounters struct {
	ok  [numQueryClasses]atomic.Int64
	err [numQueryClasses]atomic.Int64
}

// classify сводит StatementType (в нижнем регистре) к категории счётчика.
func classify(queryType string) queryClass {
	switch queryType {
	case "select", "set_operation":
		return classSelect
	case "insert":
		return classInsert
	case "update":
		return classUpdate
	case "delete":
		return classDelete
	case "explain":
		return classExplain
	case "begin", "commit", "rollback":
		return classTransaction
	case "create_database", "drop_database", "create_table", "drop_table",
		"alter_table", "create_index", "drop_index", "create_policy",
		"enable_rls", "migration", "ddl":
		return classDDL
	default:
		return classOther
	}
}

// Collector хранит все метрики сервера и умеет сериализовать их
// в Prometheus text format.
type Collector struct {
	startTime time.Time

	// Счётчики запросов по категориям (прямые atomic, без sync.Map)
	queries QueryCounters

	// Гистограмма времени выполнения (в секундах)
	// Границы бакетов: 1ms, 5ms, 10ms, 50ms, 100ms, 500ms, 1s, +Inf
	histBuckets []float64
	histCounts  []atomic.Int64 // количество в каждом бакете
	histSum     atomic.Int64   // сумма * 1e9 (наносекунды, чтобы работать с int)
	histTotal   atomic.Int64   // общее количество

	// Gauge метрики
	activeConns atomic.Int64

	// WAL метрики
	walEntries     atomic.Int64
	walCheckpoints atomic.Int64

	// Индексные метрики
	indexHits   atomic.Int64
	indexMisses atomic.Int64

	// Метрики хранилища (обновляются периодически)
	storageMu   sync.RWMutex
	storageRows map[string]map[string]int64 // db → table → count
}

// Boundaries для histogram в секундах
var defaultBuckets = []float64{
	0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0,
}

func New() *Collector {
	c := &Collector{
		startTime:   time.Now(),
		histBuckets: defaultBuckets,
		histCounts:  make([]atomic.Int64, len(defaultBuckets)+1), // +1 для +Inf
		storageRows: make(map[string]map[string]int64),
	}
	return c
}

// RecordQuery фиксирует выполнение запроса.
// queryType: "select", "insert", "update", "delete", "ddl", "explain"
// status: "ok" или "error"
// duration: время выполнения
func (c *Collector) RecordQuery(queryType, status string, duration time.Duration) {
	// Счётчик запросов: прямое обращение к atomic — никакого sync.Map
	class := classify(strings.ToLower(queryType))
	if status == "error" {
		c.queries.err[class].Add(1)
	} else {
		c.queries.ok[class].Add(1)
	}

	// Гистограмма (только успешные запросы)
	if status == "ok" {
		secs := duration.Seconds()
		c.histSum.Add(duration.Nanoseconds())
		c.histTotal.Add(1)

		// Найти бакет для этого значения
		for i, bound := range c.histBuckets {
			if secs <= bound {
				c.histCounts[i].Add(1)
				return
			}
		}
		// +Inf бакет
		c.histCounts[len(c.histBuckets)].Add(1)
	}
}

func (c *Collector) IncConnections() { c.activeConns.Add(1) }
func (c *Collector) DecConnections() { c.activeConns.Add(-1) }
func (c *Collector) IncWALEntries()  { c.walEntries.Add(1) }
func (c *Collector) IncCheckpoints() { c.walCheckpoints.Add(1) }
func (c *Collector) IncIndexHit()    { c.indexHits.Add(1) }
func (c *Collector) IncIndexMiss()   { c.indexMisses.Add(1) }

func sanitizeMetricLabel(s string) string {
	s = strings.ReplaceAll(s, `\`, `_`)
	s = strings.ReplaceAll(s, `"`, `'`)
	s = strings.ReplaceAll(s, "\n", "_")
	s = strings.ReplaceAll(s, "\r", "_")
	return s
}

// UpdateStorageRows обновляет статистику хранилища.
// Вызывается периодически (каждые 30 секунд) из фоновой горутины.
func (c *Collector) UpdateStorageRows(db, table string, count int64) {
	c.storageMu.Lock()
	defer c.storageMu.Unlock()
	if c.storageRows[db] == nil {
		c.storageRows[db] = make(map[string]int64)
	}
	c.storageRows[db][table] = count
}

// CleanStaleStorageRows удаляет метрики таблиц, которые больше не существуют.
// activeTables — map[db]set(tableNames).
func (c *Collector) CleanStaleStorageRows(activeTables map[string]map[string]bool) {
	c.storageMu.Lock()
	defer c.storageMu.Unlock()
	for db, tables := range c.storageRows {
		active := activeTables[db]
		for table := range tables {
			if active == nil || !active[table] {
				delete(tables, table)
			}
		}
		if len(tables) == 0 {
			delete(c.storageRows, db)
		}
	}
}

// Render сериализует все метрики в Prometheus text format.
func (c *Collector) Render() string {
	var b strings.Builder

	// ── Счётчики запросов ─────────────────────────────────────────────

	b.WriteString("# HELP vaultdb_queries_total Total SQL queries executed\n")
	b.WriteString("# TYPE vaultdb_queries_total counter\n")

	// Прямые атомарные чтения; фиксированный порядок категорий
	for class := queryClass(0); class < numQueryClasses; class++ {
		fmt.Fprintf(&b,
			`vaultdb_queries_total{type="%s",status="ok"} %d`+"\n",
			queryClassNames[class], c.queries.ok[class].Load())
		fmt.Fprintf(&b,
			`vaultdb_queries_total{type="%s",status="error"} %d`+"\n",
			queryClassNames[class], c.queries.err[class].Load())
	}

	// ── Гистограмма времени выполнения ────────────────────────────────

	b.WriteString("\n# HELP vaultdb_query_duration_seconds Query duration\n")
	b.WriteString("# TYPE vaultdb_query_duration_seconds histogram\n")

	cumulative := int64(0)
	for i, bound := range c.histBuckets {
		cumulative += c.histCounts[i].Load()
		fmt.Fprintf(&b,
			"vaultdb_query_duration_seconds_bucket{le=\"%g\"} %d\n",
			bound, cumulative)
	}
	// +Inf всегда равен total
	total := c.histTotal.Load()
	fmt.Fprintf(&b,
		"vaultdb_query_duration_seconds_bucket{le=\"+Inf\"} %d\n", total)

	sumSecs := float64(c.histSum.Load()) / 1e9
	fmt.Fprintf(&b, "vaultdb_query_duration_seconds_sum %g\n", sumSecs)
	fmt.Fprintf(&b, "vaultdb_query_duration_seconds_count %d\n", total)

	// ── Gauge метрики ─────────────────────────────────────────────────

	fmt.Fprintf(&b,
		"\n# HELP vaultdb_active_connections Active TCP connections\n"+
			"# TYPE vaultdb_active_connections gauge\n"+
			"vaultdb_active_connections %d\n",
		c.activeConns.Load())

	fmt.Fprintf(&b,
		"\n# HELP vaultdb_uptime_seconds Server uptime\n"+
			"# TYPE vaultdb_uptime_seconds gauge\n"+
			"vaultdb_uptime_seconds %d\n",
		int64(time.Since(c.startTime).Seconds()))

	// ── WAL метрики ───────────────────────────────────────────────────

	fmt.Fprintf(&b,
		"\n# HELP vaultdb_wal_entries_total WAL entries written\n"+
			"# TYPE vaultdb_wal_entries_total counter\n"+
			"vaultdb_wal_entries_total %d\n",
		c.walEntries.Load())

	fmt.Fprintf(&b,
		"\n# HELP vaultdb_wal_checkpoint_total WAL checkpoints performed\n"+
			"# TYPE vaultdb_wal_checkpoint_total counter\n"+
			"vaultdb_wal_checkpoint_total %d\n",
		c.walCheckpoints.Load())

	// ── Индексные метрики ─────────────────────────────────────────────

	fmt.Fprintf(&b,
		"\n# HELP vaultdb_index_lookups_total Index lookup hits and misses\n"+
			"# TYPE vaultdb_index_lookups_total counter\n"+
			"vaultdb_index_lookups_total{result=\"hit\"} %d\n"+
			"vaultdb_index_lookups_total{result=\"miss\"} %d\n",
		c.indexHits.Load(), c.indexMisses.Load())

	// ── Статистика хранилища ──────────────────────────────────────────

	c.storageMu.RLock()
	defer c.storageMu.RUnlock()

	if len(c.storageRows) > 0 {
		b.WriteString(
			"\n# HELP vaultdb_storage_rows Total rows per table (current versions)\n")
		b.WriteString("# TYPE vaultdb_storage_rows gauge\n")

		// Сортируем для детерминированного вывода
		dbs := make([]string, 0, len(c.storageRows))
		for db := range c.storageRows {
			dbs = append(dbs, db)
		}
		sort.Strings(dbs)

		for _, db := range dbs {
			tables := make([]string, 0, len(c.storageRows[db]))
			for t := range c.storageRows[db] {
				tables = append(tables, t)
			}
			sort.Strings(tables)
			for _, t := range tables {
			fmt.Fprintf(&b,
				`vaultdb_storage_rows{database="%s",table="%s"} %d`+"\n",
				sanitizeMetricLabel(db), sanitizeMetricLabel(t), c.storageRows[db][t])
			}
		}
	}

	return b.String()
}
