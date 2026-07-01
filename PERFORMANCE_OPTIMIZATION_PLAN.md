# VaultDB — План оптимизации производительности

**Дата**: 2026-07-01
**Проблема**: Стресс-тесты падают по таймауту (120s), executor работает O(n²) в hot paths
**Цель**: Стресс-тесты проходят <30s, executor оптимизирован

---

## Анализ bottleneck'ов

### Критичные (P0) — влияют на все запросы

| # | Bottleneck | Файл | Сложность | Влияние |
|---|-----------|------|-----------|---------|
| 1 | Нет кэша парсера — каждый запрос проходит lexer→parser заново | parser.go | O(q) × N | Все запросы |
| 2 | PK scan — полный scan таблицы на каждый INSERT | page_engine_io.go:179 | O(n) × N | INSERT |
| 3 | resolveColumn — linear scan с EqualFold | eval_utils.go:14 | O(cols) × N | Все SELECT |

### Высокие (P1) — влияют на特定ные запросы

| # | Bottleneck | Файл | Сложность | Влияние |
|---|-----------|------|-----------|---------|
| 4 | Window SUM/AVG/COUNT — O(n²) на partition | select_window.go:252 | O(n²) | Window functions |
| 5 | RANK/DENSE_RANK — O(n²) на partition | select_window.go:112 | O(n²) | Window functions |
| 6 | valueToString через fmt.Sprintf | commands.go:183 | Аллокация | Все SELECT |
| 7 | JOIN копирует каждую строку | select_joins.go:61 | O(n×m) | JOIN |

### Средние (P2) —的影响较小

| # | Bottleneck | Файл | Сложность | Влияние |
|---|-----------|------|-----------|---------|
| 8 | Catalog JSON serialize на каждый INSERT batch | page_engine_io.go:312 | I/O | INSERT batch |
| 9 | Lexer lineColCache на каждый parse | lexer.go:224 | O(q) | Все запросы |
| 10 | DISTINCT через strings.Join | commands_select.go:426 | O(n×cols) | DISTINCT |

---

## Фаза 1: P0 критичные (3 субагента)

### 1.1 Parser Statement Cache

**Проблема**: Каждый запрос парсится заново. Стресс-тесты парсят одни и те же SQL-строки тысячи раз.

**Решение**: LRU кэш скормированных AST.

**Файлы**:
- `server/internal/parser/parser.go` — добавить `ParseCached(sql string) (Statement, error)`
- `server/internal/parser/cache.go` — (новый) `StatementCache` с LRU eviction

**Алгоритм**:
```go
type StatementCache struct {
    mu      sync.RWMutex
    cache   map[string]*list.Element
    lru     *list.List
    capacity int
}

func (c *StatementCache) Get(sql string) (Statement, bool) { ... }
func (c *StatementCache) Put(sql string, stmt Statement) { ... }
```

**Ключ кэша**: нормализованный SQL (trim + toLower).

**Ожидаемый эффект**: Повторные запросы — O(1) вместо O(q).

---

### 1.2 PK Index Lookup для INSERT

**Проблема**: `page_engine_io.go:179-186` делает полный scan таблицы для проверки PK uniqueness. 1000 INSERT'ов = O(n²).

**Решение**: Использовать существующий BTreeIndex для PK (auto-created в `CreateTable`).

**Файлы**:
- `server/internal/storage/page_engine_io.go:170-186` — заменить scanTuples на IndexLookup
- `server/internal/index/btree.go` — убедиться что BTree поддерживает point lookup

**Алгоритм**:
```go
// Вместо scanTuples:
if pkIdx >= 0 {
    mgr := e.getOrCreateIndexManager(dbName, tableName)
    if idx, ok := mgr.FindIndexForColumn(pkColumnName); ok {
        positions, _ := idx.Lookup(valueToString(pkValue))
        if len(positions) > 0 {
            return 0, fmt.Errorf("duplicate primary key")
        }
    }
}
```

**Ожидаемый эффект**: INSERT с PK check — O(log n) вместо O(n).

---

### 1.3 Column Index Cache для resolveColumn

**Проблема**: `eval_utils.go:14-31` ищет колонку через linear scan с `strings.EqualFold` для каждого column reference в каждом выражении.

**Решение**: Кэшировать mapping column name → index в ExecutionContext.

**Файлы**:
- `server/internal/executor/eval_utils.go` — добавить `resolveColumnCached`
- `server/internal/executor/executor.go` — добавить `ColumnIndex map[string]int` в ExecutionContext

**Алгоритм**:
```go
// В ExecutionContext:
ColumnIndex map[string]int // column name (lowercase) → index

// При создании ExecutionContext (executor.go:217):
ctx.ColumnIndex = buildColumnIndex(schema)

// В resolveColumn:
func resolveColumnCached(row storage.Row, schema *storage.TableSchema, name string, colIndex map[string]int) (interface{}, error) {
    if idx, ok := colIndex[strings.ToLower(name)]; ok && idx < len(row) {
        return row[idx], nil
    }
    // fallback на linear scan для qualified names
    return resolveColumn(row, schema, name)
}
```

**Ожидаемый эффект**: Column lookup — O(1) вместо O(cols).

---

## Фаза 2: Executor оптимизации (3 субагента)

### 2.1 Window Function O(n²) → O(n)

**Проблема**: RANK/DENSE_RANK делают backward scan для каждого row = O(n²) на partition. SUM/AVG/COUNT пересчитывают агрегат для каждой строки.

**Решение**: Prefix sums для SUM/COUNT, running counter для RANK.

**Файлы**:
- `server/internal/executor/select_window.go:110-139` — RANK/DENSE_RANK
- `server/internal/executor/select_window.go:252-273` — COUNT/SUM/AVG

**Алгоритм для RANK**:
```go
// Предвычислить rank для каждой позиции в partition
ranks := make([]int, len(partitionIndices))
for i := 0; i < len(partitionIndices); i++ {
    if i == 0 {
        ranks[i] = 1
    } else {
        equal, _ := rowsEqualByOrderBy(...)
        if equal {
            ranks[i] = ranks[i-1]
        } else {
            ranks[i] = i + 1
        }
    }
}
```

**Алгоритм для SUM/COUNT**:
```go
// Prefix sum для running aggregate
runningSum := 0.0
for i, idx := range partitionIndices {
    val, _ := evalOperand(wf.Args[0], allRows[idx], schema, ctx)
    if f, ok := toFloat(val); ok {
        runningSum += f
    }
    results[i] = runningSum
}
```

**Ожидаемый эффект**: Window functions — O(n) вместо O(n²).

---

### 2.2 valueToString Optimization

**Проблема**: `commands.go:183-204` использует `fmt.Sprintf` для каждого значения — аллокация на каждый вызов.

**Решение**: Заменить на `strconv.FormatInt`/`strconv.FormatFloat`.

**Файлы**:
- `server/internal/executor/commands.go:183-204`

**Алгоритм**:
```go
func valueToString(value interface{}) string {
    if value == nil { return "" }
    switch v := value.(type) {
    case string: return v
    case bool: if v { return "true" }; return "false"
    case int: return strconv.Itoa(v)
    case int64: return strconv.FormatInt(v, 10)
    case float64: return strconv.FormatFloat(v, 'g', -1, 64)
    default: return fmt.Sprintf("%v", v)
    }
}
```

**Ожидаемый эффект**: Zero-allocation для int/int64/float64/string.

---

### 2.3 JOIN Row Copy Optimization

**Проблема**: `select_joins.go:61` — `append(append(Row{}, lrow...), rrow...)` создаёт новую строку для каждого cross-product.

**Решение**: Использовать reusable buffer.

**Файлы**:
- `server/internal/executor/select_joins.go` — все JOIN функции

**Алгоритм**:
```go
// В executeJoins:
combinedBuf := make(storage.Row, 0, len(leftSchema.Columns)+len(rightSchema.Columns))

for _, lrow := range leftRows {
    for _, rrow := range rightRows {
        combinedBuf = combinedBuf[:0]
        combinedBuf = append(combinedBuf, lrow...)
        combinedBuf = append(combinedBuf, rrow...)
        // ... eval condition
    }
}
```

**Ожидаемый эффект**: JOIN — O(1) аллокаций вместо O(n×m).

---

## Фаза 3: Storage оптимизации (2 субагента)

### 3.1 Catalog Save Batching

**Проблема**: `page_engine_io.go:312` — `saveCatalogLocked()` вызывается после каждого INSERT batch, сериализуя весь catalog в JSON.

**Решение**: Отложить сохранение каталога до checkpoint или до N операций.

**Файлы**:
- `server/internal/storage/page_engine.go` — добавить `catalogDirty` флаг
- `server/internal/storage/page_engine_io.go:312` — заменить прямой вызов на пометку dirty

**Алгоритм**:
```go
// В PageStorageEngine:
catalogDirty bool

// В InsertRows (после всех вставок):
e.catalogDirty = true

// В doCheckpoint:
if e.catalogDirty {
    e.saveCatalogLocked()
    e.catalogDirty = false
}
```

**Ожидаемый эффект**: INSERT batch — 0 I/O для каталога (отложено до checkpoint).

---

### 3.2 DISTINCT String Optimization

**Проблема**: `commands_select.go:426` — `strings.Join(row, "\x00")` создаёт новую строку для каждой строки.

**Решение**: Использовать hash-based dedup без аллокации строки.

**Файлы**:
- `server/internal/executor/commands_select.go:422-433`

**Алгоритм**:
```go
func distinctRows(rows [][]string) [][]string {
    seen := make(map[[32]byte]bool) // SHA256 hash
    result := make([][]string, 0, len(rows))
    for _, row := range rows {
        h := sha256.New()
        for _, cell := range row {
            h.Write([]byte(cell))
            h.Write([]byte{0})
        }
        var key [32]byte
        copy(key[:], h.Sum(nil))
        if !seen[key] {
            seen[key] = true
            result = append(result, row)
        }
    }
    return result
}
```

**Ожидаемый эффект**: DISTINCT — меньше аллокаций.

---

## Фаза 4: Верификация + Benchmarks

### 4.1 Benchmark Tests

**Файлы**:
- `server/internal/executor/benchmark_test.go` — (новый)
- `server/internal/storage/benchmark_test.go` — (новый)

**Бенчмарки**:
```go
func BenchmarkInsert(b *testing.B) { ... }
func BenchmarkSelect(b *testing.B) { ... }
func BenchmarkJoin(b *testing.B) { ... }
func BenchmarkWindowFunction(b *testing.B) { ... }
func BenchmarkParseCached(b *testing.B) { ... }
func BenchmarkResolveColumn(b *testing.B) { ... }
```

### 4.2 Stress Test Verification

После всех оптимизаций:
1. Запустить все стресс-тесты с таймаутом 60s
2. Сравнить время до/после
3. Убедиться что все тесты проходят

---

## Ожидаемый результат

| Метрика | До | После |
|---------|-----|-------|
| Stress test timeout | 120s (FAIL) | <60s (PASS) |
| INSERT с PK check | O(n) | O(log n) |
| resolveColumn | O(cols) | O(1) |
| Window RANK | O(n²) | O(n) |
| valueToString | аллокация | zero-alloc |
| JOIN row copy | O(n×m) | O(1) |
| Catalog save | каждый batch | до checkpoint |

---

## Порядок выполнения

| Фаза | Субагенты | Время |
|------|-----------|-------|
| 1 | 3 (parser cache, PK lookup, column cache) | Параллельно |
| 2 | 3 (window, valueToString, JOIN) | Параллельно |
| 3 | 2 (catalog batching, DISTINCT) | Параллельно |
| 4 | 1 (verification) | После фазы 3 |
