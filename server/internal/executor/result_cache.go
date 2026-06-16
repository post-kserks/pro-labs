package executor

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"vaultdb/internal/parser"
)

const defaultResultCacheSize = 256
const defaultResultCacheTTL = 30 * time.Second

// CachedResult — кэшированный результат SELECT запроса.
type CachedResult struct {
	result    *Result
	tables    map[string]bool // затронутые таблицы (для инвалидации)
	createdAt time.Time
}

// ResultCache — LRU кэш результатов SELECT запросов.
// Автоматически инвалидируется при INSERT/UPDATE/DELETE.
// Поддерживает TTL для устаревших записей.
type ResultCache struct {
	mu        sync.RWMutex
	cache     map[string]*list.Element
	lru       *list.List
	maxSize   int
	ttl       time.Duration
	hitCount  int64
	missCount int64
}

type resultCacheEntry struct {
	key   string
	value *CachedResult
}

// NewResultCache создаёт новый кэш результатов.
func NewResultCache(maxSize int, ttl time.Duration) *ResultCache {
	if maxSize <= 0 {
		maxSize = defaultResultCacheSize
	}
	if ttl <= 0 {
		ttl = defaultResultCacheTTL
	}
	return &ResultCache{
		cache:   make(map[string]*list.Element, maxSize),
		lru:     list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get возвращает кэшированный результат или nil если не найден/устарел.
func (rc *ResultCache) Get(key string) *Result {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	elem, ok := rc.cache[key]
	if !ok {
		rc.missCount++
		return nil
	}

	entry := elem.Value.(*resultCacheEntry)

	// Проверяем TTL
	if time.Since(entry.value.createdAt) > rc.ttl {
		rc.removeEntry(elem)
		rc.missCount++
		return nil
	}

	// Перемещаем в начало LRU
	rc.lru.MoveToFront(elem)
	rc.hitCount++
	return entry.value.result
}

// Put сохраняет результат в кэш.
func (rc *ResultCache) Put(key string, result *Result, tables map[string]bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Если ключ уже есть — обновляем
	if elem, ok := rc.cache[key]; ok {
		entry := elem.Value.(*resultCacheEntry)
		entry.value = &CachedResult{
			result:    result,
			tables:    tables,
			createdAt: time.Now(),
		}
		rc.lru.MoveToFront(elem)
		return
	}

	// Если кэш полон — удаляем LRU
	for rc.lru.Len() >= rc.maxSize {
		back := rc.lru.Back()
		if back == nil {
			break
		}
		rc.removeEntry(back)
	}

	entry := &resultCacheEntry{
		key: key,
		value: &CachedResult{
			result:    result,
			tables:    tables,
			createdAt: time.Now(),
		},
	}
	elem := rc.lru.PushFront(entry)
	rc.cache[key] = elem
}

// Invalidate удаляет все записи, затронутые указанной таблицей.
func (rc *ResultCache) Invalidate(tableName string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	var toRemove []*list.Element
	for _, elem := range rc.cache {
		entry := elem.Value.(*resultCacheEntry)
		if entry.value.tables[tableName] {
			toRemove = append(toRemove, elem)
		}
	}
	for _, elem := range toRemove {
		rc.removeEntry(elem)
	}
}

// InvalidateAll очищает весь кэш.
func (rc *ResultCache) InvalidateAll() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.cache = make(map[string]*list.Element)
	rc.lru.Init()
}

// Stats возвращает статистику кэша.
func (rc *ResultCache) Stats() (hits, misses, size int64) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.hitCount, rc.missCount, int64(rc.lru.Len())
}

func (rc *ResultCache) removeEntry(elem *list.Element) {
	entry := elem.Value.(*resultCacheEntry)
	delete(rc.cache, entry.key)
	rc.lru.Remove(elem)
}

// ResultCacheKey строит ключ кэша для SELECT запроса.
func ResultCacheKey(stmt *parser.SelectStatement, dbName string) string {
	key := dbName + ":"
	if stmt.TableName != "" {
		key += stmt.TableName
	}
	// Include SELECT columns in key
	for _, col := range stmt.Columns {
		key += ":" + formatSelectColumnForCache(col)
	}
	if stmt.Where != nil {
		key += ":W:" + formatExpressionForCache(stmt.Where)
	}
	if len(stmt.GroupBy) > 0 {
		key += ":GB"
	}
	if stmt.Having != nil {
		key += ":H"
	}
	if len(stmt.OrderBy) > 0 {
		key += ":O"
		for _, ob := range stmt.OrderBy {
			key += formatExpressionForCache(ob.Expr) + ob.Direction
		}
	}
	if stmt.HasLimit {
		key += fmt.Sprintf(":L%d", stmt.Limit)
	}
	if stmt.HasOffset {
		key += fmt.Sprintf(":OF%d", stmt.Offset)
	}
	if stmt.Distinct {
		key += ":D"
	}
	if stmt.AsOf != nil {
		if stmt.AsOf.UseVersion {
			key += fmt.Sprintf(":ASOFv%d", stmt.AsOf.Version)
		} else {
			key += ":ASOF:" + stmt.AsOf.Timestamp
		}
	}
	return key
}

func formatSelectColumnForCache(col parser.SelectColumn) string {
	if col.Alias != "" {
		return "A" + col.Alias
	}
	return formatExpressionForCache(col.Expr)
}

func formatExpressionForCache(expr parser.Expression) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		return formatExpressionForCache(e.Left) + e.Operator + formatExpressionForCache(e.Right)
	case *parser.AndExpr:
		return formatExpressionForCache(e.Left) + "AND" + formatExpressionForCache(e.Right)
	case *parser.OrExpr:
		return formatExpressionForCache(e.Left) + "OR" + formatExpressionForCache(e.Right)
	case *parser.NotExpr:
		return "NOT" + formatExpressionForCache(e.Expr)
	case *parser.ColumnRef:
		return e.Name
	case *parser.FunctionCall:
		args := ""
		for i, arg := range e.Args {
			if i > 0 {
				args += ","
			}
			args += formatExpressionForCache(arg)
		}
		return e.Name + "(" + args + ")"
	case *parser.AggregateExpr:
		args := ""
		for i, arg := range e.Args {
			if i > 0 {
				args += ","
			}
			args += formatExpressionForCache(arg)
		}
		prefix := ""
		if e.Distinct {
			prefix = "DISTINCT"
		}
		return e.Name + "(" + prefix + args + ")"
	case *parser.WindowFunctionExpr:
		return "WIN:" + e.FuncName
	case parser.Value:
		return formatValueForCache(e)
	case *parser.Value:
		return formatValueForCache(*e)
	default:
		return fmt.Sprintf("E%T", expr)
	}
}

func formatValueForCache(v parser.Value) string {
	switch v.Type {
	case "int":
		return fmt.Sprintf("%d", v.IntVal)
	case "float":
		return fmt.Sprintf("%g", v.FltVal)
	case "string":
		return v.StrVal
	case "bool":
		if v.BoolVal {
			return "T"
		}
		return "F"
	default:
		return "?"
	}
}
