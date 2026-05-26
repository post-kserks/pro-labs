# VaultDB — ТЗ на доработку


---

| Атрибут | Значение |
|---|---|
| Документ | Дополнение к VaultDB TZ v1.0.0 + Extensions v2.0.0 |
| Версия | 3.0.0 |
| Статус | Активен |
| Контекст | Конкурс Яндекса — production-quality code review |

---

## Предисловие: что важно для Яндекса

Яндекс оценивает не количество фич, а **глубину понимания**. Инженер на ревью будет
задавать вопросы вида: *"почему здесь table lock, а не row lock?"*, *"как вы защищаетесь от
split-brain при recovery?"*, *"покажите benchmark". * Поэтому каждое улучшение в этом ТЗ
сопровождается объяснением **почему** — это важнее самого кода.

Ниже — шесть доработок, ранжированных по соотношению усилий и эффекта на оценку.

| # | Доработка | Сложность | Эффект | Срок |
|---|---|---|---|---|
| 1 | Исправление partial reads в C++ | ★☆☆☆☆ | ★★★★☆ | 2–3 часа |
| 2 | Prometheus metrics | ★★☆☆☆ | ★★★☆☆ | 4–6 часов |
| 3 | VACUUM | ★★☆☆☆ | ★★★★☆ | 1 день |
| 4 | Hash-индекс на первичный ключ | ★★★★☆ | ★★★★★ | 3–4 дня |
| 5 | Транзакции BEGIN/COMMIT/ROLLBACK | ★★★★☆ | ★★★★★ | 3–5 дней |
| 6 | Prepared Statements | ★★★☆☆ | ★★★☆☆ | 2–3 дня |

---

## Содержание

1. [Исправление: Partial Reads в C++](#1-partial-reads)
2. [Prometheus Metrics](#2-prometheus-metrics)
3. [VACUUM](#3-vacuum)
4. [Hash-индекс](#4-hash-индекс)
5. [Транзакции](#5-транзакции)
6. [Prepared Statements](#6-prepared-statements)
7. [Benchmark-утилита](#7-benchmark)
8. [Распределение задач](#8-распределение)
9. [Чеклист приёмки](#9-чеклист)

---

## 1. Partial Reads

**Ответственный:** Dev4  
**Файл:** `client/lib/src/connection.cpp`

### 1.1 В чём проблема

`recv()` — системный вызов, который **не гарантирует** получение всего сообщения за один
вызов. Если сервер отправил 8 КБ JSON, первый `recv()` может вернуть только 4 КБ. Текущий
код этого не учитывает — при большом результате SELECT клиент получает обрезанный JSON и
падает с ошибкой десериализации.

Это **баг**, а не архитектурное ограничение. На ревью в Яндексе такой код — красный флаг.

### 1.2 Текущий (сломанный) код

```cpp
// Примерная структура текущего кода — НЕ ДЕЛАТЬ ТАК
Result Connection::execute(const std::string& sql) {
    std::string req = buildRequest(sql) + "\n";
    ::send(sockfd_, req.c_str(), req.size(), 0);

    char buf[4096];
    ssize_t n = ::recv(sockfd_, buf, sizeof(buf), 0); // ПРОБЛЕМА: один вызов
    return parseResponse(std::string(buf, n));
}
```

### 1.3 Правильная реализация

Протокол VaultDB использует NDJSON — каждый пакет завершается символом `\n`.
Значит нужно читать байт за байтом (или порциями) **до** символа `\n`.

```cpp
// client/lib/src/connection.cpp

std::string Connection::recvPacket() {
    std::string response;
    response.reserve(4096);

    char buf[4096];

    while (true) {
        // MSG_PEEK: посмотреть сколько данных доступно без извлечения
        // Это позволяет читать эффективными порциями
        ssize_t n = ::recv(sockfd_, buf, sizeof(buf), 0);

        if (n < 0) {
            if (errno == EINTR) {
                // Прерван сигналом — продолжаем
                continue;
            }
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                // Таймаут (если сокет non-blocking) — ошибка
                throw NetworkError("recv timeout: server did not respond");
            }
            throw NetworkError(
                std::string("recv failed: ") + strerror(errno));
        }

        if (n == 0) {
            throw NetworkError("connection closed by server");
        }

        // Ищем \n в полученном буфере
        const char* nl = static_cast<const char*>(
            std::memchr(buf, '\n', static_cast<size_t>(n)));

        if (nl != nullptr) {
            // Нашли конец пакета
            // Добавляем данные до \n (включительно)
            response.append(buf, static_cast<size_t>(nl - buf));
            return response;
        }

        // \n ещё не получен — добавляем весь буфер и читаем дальше
        response.append(buf, static_cast<size_t>(n));

        // Защита от бесконечного роста буфера
        // 64 МБ — разумный лимит для учебного проекта
        constexpr size_t MAX_RESPONSE_SIZE = 64 * 1024 * 1024;
        if (response.size() > MAX_RESPONSE_SIZE) {
            throw NetworkError("response too large (> 64 MB)");
        }
    }
}
```

### 1.4 Аналогично для send()

Полная отправка тоже не гарантируется за один `send()`:

```cpp
void Connection::sendPacket(const std::string& data) {
    size_t total_sent = 0;
    const char* ptr   = data.c_str();
    size_t remaining  = data.size();

    while (remaining > 0) {
        ssize_t n = ::send(sockfd_, ptr + total_sent, remaining,
                           MSG_NOSIGNAL); // MSG_NOSIGNAL: не генерировать SIGPIPE

        if (n < 0) {
            if (errno == EINTR) continue;
            throw NetworkError(
                std::string("send failed: ") + strerror(errno));
        }

        total_sent += static_cast<size_t>(n);
        remaining  -= static_cast<size_t>(n);
    }
}
```

### 1.5 Таймаут на сокете

Добавить SO_RCVTIMEO и SO_SNDTIMEO при создании соединения:

```cpp
bool Connection::connect() {
    sockfd_ = ::socket(AF_INET, SOCK_STREAM, 0);
    if (sockfd_ < 0) return false;

    // Таймаут на чтение и запись
    struct timeval tv{};
    tv.tv_sec  = opts_.timeout_ms / 1000;
    tv.tv_usec = (opts_.timeout_ms % 1000) * 1000;

    ::setsockopt(sockfd_, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
    ::setsockopt(sockfd_, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));

    // TCP_NODELAY: отключить алгоритм Nagle для низкой латентности
    int flag = 1;
    ::setsockopt(sockfd_, IPPROTO_TCP, TCP_NODELAY, &flag, sizeof(flag));

    // ... остальной код connect() ...
}
```

### 1.6 Тест

```cpp
// Тест: результат SELECT с 10 000 строк должен приходить полностью
TEST(ConnectionTest, LargeResultPartialReads) {
    // Вставить 10 000 строк через API
    // Выполнить SELECT *
    // Проверить что вернулось ровно 10 000 строк
    // Проверить что JSON корректно десериализован
}
```

---

## 2. Prometheus Metrics

**Ответственный:** Dev3  
**Файл:** `server/internal/metrics/collector.go`  
**Endpoint:** `GET /metrics`

### 2.1 В чём проблема

Prometheus — стандарт де-факто для метрик в современном бэкенде. Яндекс использует его
во всей инфраструктуре. Если `/metrics` возвращает кастомный JSON вместо
Prometheus text format — это сигнал, что команда незнакома с индустриальными стандартами.

### 2.2 Prometheus Text Format

Формат строгий и хорошо задокументирован:

```
# HELP <name> <description>
# TYPE <name> <type>   # counter | gauge | histogram | summary
<name>{label="value"} <number> [timestamp]
```

Пример правильного вывода:

```
# HELP vaultdb_queries_total Total number of SQL queries executed
# TYPE vaultdb_queries_total counter
vaultdb_queries_total{type="select",status="ok"} 1024
vaultdb_queries_total{type="insert",status="ok"} 256
vaultdb_queries_total{type="update",status="ok"} 89
vaultdb_queries_total{type="delete",status="ok"} 12
vaultdb_queries_total{type="select",status="error"} 7
vaultdb_queries_total{type="insert",status="error"} 2

# HELP vaultdb_query_duration_seconds Query execution duration in seconds
# TYPE vaultdb_query_duration_seconds histogram
vaultdb_query_duration_seconds_bucket{le="0.001"} 820
vaultdb_query_duration_seconds_bucket{le="0.005"} 950
vaultdb_query_duration_seconds_bucket{le="0.01"} 1000
vaultdb_query_duration_seconds_bucket{le="0.05"} 1020
vaultdb_query_duration_seconds_bucket{le="0.1"}  1024
vaultdb_query_duration_seconds_bucket{le="+Inf"} 1024
vaultdb_query_duration_seconds_sum 2.847
vaultdb_query_duration_seconds_count 1024

# HELP vaultdb_active_connections Current number of active TCP connections
# TYPE vaultdb_active_connections gauge
vaultdb_active_connections 3

# HELP vaultdb_storage_rows_total Total number of rows per table (current versions only)
# TYPE vaultdb_storage_rows_total gauge
vaultdb_storage_rows_total{database="analytics",table="users"} 142
vaultdb_storage_rows_total{database="analytics",table="events"} 8431

# HELP vaultdb_wal_entries_total Total WAL entries written
# TYPE vaultdb_wal_entries_total counter
vaultdb_wal_entries_total 3847

# HELP vaultdb_wal_checkpoint_total Total WAL checkpoints performed
# TYPE vaultdb_wal_checkpoint_total counter
vaultdb_wal_checkpoint_total 38

# HELP vaultdb_index_lookups_total Index lookups (hits = found in index)
# TYPE vaultdb_index_lookups_total counter
vaultdb_index_lookups_total{type="hit"} 890
vaultdb_index_lookups_total{type="miss"} 134

# HELP vaultdb_uptime_seconds Server uptime in seconds
# TYPE vaultdb_uptime_seconds gauge
vaultdb_uptime_seconds 3601
```

### 2.3 Реализация Collector

```go
// server/internal/metrics/collector.go

package metrics

import (
    "fmt"
    "math"
    "sort"
    "strings"
    "sync"
    "sync/atomic"
    "time"
)

// Collector хранит все метрики сервера и умеет сериализовать их
// в Prometheus text format.
type Collector struct {
    startTime time.Time

    // Счётчики запросов: ключ = "type:status", например "select:ok"
    queryCounts sync.Map // map[string]*atomic.Int64

    // Гистограмма времени выполнения (в секундах)
    // Границы бакетов: 1ms, 5ms, 10ms, 50ms, 100ms, 500ms, 1s, +Inf
    histBuckets []float64
    histCounts  []atomic.Int64  // количество в каждом бакете
    histSum     atomic.Int64    // сумма * 1e9 (наносекунды, чтобы работать с int)
    histTotal   atomic.Int64    // общее количество

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
    // Счётчик запросов
    key := queryType + ":" + status
    v, _ := c.queryCounts.LoadOrStore(key, new(atomic.Int64))
    v.(*atomic.Int64).Add(1)

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

func (c *Collector) IncConnections()  { c.activeConns.Add(1) }
func (c *Collector) DecConnections()  { c.activeConns.Add(-1) }
func (c *Collector) IncWALEntries()   { c.walEntries.Add(1) }
func (c *Collector) IncCheckpoints()  { c.walCheckpoints.Add(1) }
func (c *Collector) IncIndexHit()     { c.indexHits.Add(1) }
func (c *Collector) IncIndexMiss()    { c.indexMisses.Add(1) }

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

// Render сериализует все метрики в Prometheus text format.
func (c *Collector) Render() string {
    var b strings.Builder

    // ── Счётчики запросов ─────────────────────────────────────────────

    b.WriteString("# HELP vaultdb_queries_total Total SQL queries executed\n")
    b.WriteString("# TYPE vaultdb_queries_total counter\n")

    // Собираем и сортируем ключи для детерминированного вывода
    var queryKeys []string
    c.queryCounts.Range(func(k, _ interface{}) bool {
        queryKeys = append(queryKeys, k.(string))
        return true
    })
    sort.Strings(queryKeys)

    for _, key := range queryKeys {
        v, _ := c.queryCounts.Load(key)
        parts := strings.SplitN(key, ":", 2)
        if len(parts) == 2 {
            fmt.Fprintf(&b,
                `vaultdb_queries_total{type="%s",status="%s"} %d`+"\n",
                parts[0], parts[1], v.(*atomic.Int64).Load())
        }
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
                    db, t, c.storageRows[db][t])
            }
        }
    }

    _ = math.IsNaN // suppress unused import
    return b.String()
}
```

### 2.4 HTTP Handler

```go
// server/internal/httpserver/server.go — обновить handleMetrics

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
    // Content-Type обязателен для Prometheus scraper'а
    w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
    w.WriteHeader(http.StatusOK)
    fmt.Fprint(w, s.metrics.Render())
}
```

### 2.5 Интеграция в Executor

```go
// internal/executor/executor.go

func (e *Executor) Run(ctx context.Context, sess *Session, stmt parser.Statement) (*Result, error) {
    start := time.Now()

    result, err := e.run(ctx, sess, stmt)

    duration   := time.Since(start)
    queryType  := strings.ToLower(stmt.StatementType())
    status     := "ok"
    if err != nil {
        status = "error"
    }

    e.metrics.RecordQuery(queryType, status, duration)
    return result, err
}
```

### 2.6 Фоновое обновление storage metrics

```go
// cmd/vaultdb-server/main.go

// Запустить горутину обновления storage metrics
go func() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            updateStorageMetrics(store, metricsCollector)
        }
    }
}()

func updateStorageMetrics(s storage.Engine, m *metrics.Collector) {
    dbs, err := s.ListDatabases(context.Background())
    if err != nil {
        return
    }
    for _, db := range dbs {
        tables, _ := s.ListTables(context.Background(), db.Name)
        for _, t := range tables {
            count, _ := s.RowCount(context.Background(), db.Name, t.Name)
            m.UpdateStorageRows(db.Name, t.Name, int64(count))
        }
    }
}
```

---

## 3. VACUUM

**Ответственный:** Dev2 (Storage) + Dev1 (Parser)  
**Пакеты:** `internal/storage`, `internal/parser`, `internal/executor`

### 3.1 В чём проблема

Time Travel хранит все версии строк. `UPDATE` на 1000 строк × 100 раз = 100 000 записей
в файле при 1000 актуальных. Без vacuum файлы растут бесконечно.

Это не только дисковое пространство — это время чтения. `ReadCurrentRows` при каждом
SELECT вычитывает ВСЕ версии и фильтрует. При большой истории это деградация в разы.

### 3.2 Синтаксис

```sql
-- Очистить устаревшие версии строк в конкретной таблице
VACUUM users;

-- Очистить всё в текущей базе данных
VACUUM;

-- Показать статистику перед vacuum
VACUUM ANALYZE users;
```

### 3.3 Парсер (Dev1)

```go
// internal/parser/ast.go

type VacuumStatement struct {
    TableName string // пустая строка = vacuum всех таблиц в текущей БД
    Analyze   bool   // true = VACUUM ANALYZE: показать статистику
}

func (s *VacuumStatement) statementNode()       {}
func (s *VacuumStatement) StatementType() string { return "VACUUM" }
```

Грамматика:
```bnf
<vacuum> ::= "VACUUM" [ "ANALYZE" ] [ <table-name> ] ";"
```

### 3.4 Storage Engine — новый метод (Dev2)

```go
// internal/storage/engine.go — добавить в интерфейс

// Vacuum физически удаляет устаревшие версии строк (_vdb_deleted_tx != 0)
// из указанной таблицы. Возвращает статистику.
Vacuum(ctx context.Context, db, table string) (*VacuumStats, error)

// VacuumStats — результат vacuum.
type VacuumStats struct {
    TableName      string
    RowsBefore     int     // строк до vacuum (включая все версии)
    RowsAfter      int     // строк после vacuum (только актуальные)
    ReclaimedRows  int     // удалено устаревших версий
    FileSizeBefore int64   // байт до
    FileSizeAfter  int64   // байт после
    DurationMs     float64
}
```

### 3.5 Реализация Vacuum в FileEngine (Dev2)

```go
// internal/storage/file_engine.go

func (e *FileEngine) Vacuum(ctx context.Context, db, table string) (*VacuumStats, error) {
    // Эксклюзивная блокировка: во время vacuum никто не читает и не пишет
    e.lock(db, table)
    defer e.unlock(db, table)

    start := time.Now()

    dataPath := e.dataPath(db, table)

    // Размер файла до
    statBefore, err := os.Stat(dataPath)
    if err != nil {
        return nil, fmt.Errorf("vacuum: stat: %w", err)
    }

    // Загружаем все данные
    data, err := e.loadDataFileRaw(db, table)
    if err != nil {
        return nil, fmt.Errorf("vacuum: load: %w", err)
    }

    rowsBefore := len(data.Rows)

    // Оставляем только актуальные строки (_vdb_deleted_tx == 0)
    var liveRows []VersionedRow
    for _, row := range data.Rows {
        if row.DeletedTx == 0 {
            liveRows = append(liveRows, row)
        }
    }

    data.Rows = liveRows

    // Атомарная запись (write-and-rename)
    if err := e.writeDataFile(db, table, data); err != nil {
        return nil, fmt.Errorf("vacuum: write: %w", err)
    }

    // Записываем в WAL факт vacuum
    e.wal.Append(wal.OpVacuum, walVacuumPayload{
        DB:    db,
        Table: table,
    })

    statAfter, _ := os.Stat(dataPath)

    return &VacuumStats{
        TableName:      table,
        RowsBefore:     rowsBefore,
        RowsAfter:      len(liveRows),
        ReclaimedRows:  rowsBefore - len(liveRows),
        FileSizeBefore: statBefore.Size(),
        FileSizeAfter:  statAfter.Size(),
        DurationMs:     float64(time.Since(start).Microseconds()) / 1000.0,
    }, nil
}
```

### 3.6 VacuumCommand (Dev3)

```go
// internal/executor/commands.go

type VacuumCommand struct {
    stmt *parser.VacuumStatement
}

func (c *VacuumCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {
    if sess.CurrentDB == "" {
        return nil, &VaultDBError{Code: ErrNoDatabase}
    }

    var tables []string

    if c.stmt.TableName != "" {
        tables = []string{c.stmt.TableName}
    } else {
        // VACUUM без таблицы — все таблицы текущей БД
        tableInfos, err := s.ListTables(ctx, sess.CurrentDB)
        if err != nil {
            return nil, err
        }
        for _, t := range tableInfos {
            tables = append(tables, t.Name)
        }
    }

    // Формируем результат в виде таблицы статистики
    columns := []string{
        "table", "rows_before", "rows_after",
        "reclaimed", "size_before_kb", "size_after_kb", "duration_ms",
    }
    var rows [][]interface{}

    for _, table := range tables {
        stats, err := s.Vacuum(ctx, sess.CurrentDB, table)
        if err != nil {
            // Не останавливаемся при ошибке одной таблицы
            slog.Warn("vacuum failed for table",
                "table", table, "error", err)
            continue
        }
        rows = append(rows, []interface{}{
            stats.TableName,
            stats.RowsBefore,
            stats.RowsAfter,
            stats.ReclaimedRows,
            stats.FileSizeBefore / 1024,
            stats.FileSizeAfter / 1024,
            fmt.Sprintf("%.2f", stats.DurationMs),
        })
    }

    return &Result{
        Type:    "rows",
        Columns: columns,
        Rows:    valuesToStrings(rows),
    }, nil
}
```

### 3.7 Пример вывода в TUI

```
VACUUM ANALYZE users;

╔════════════╦══════════╦═════════╦═══════════╦═══════════════╦══════════════╦═══════════╗
║ table      ║ before   ║ after   ║ reclaimed ║ size_before   ║ size_after   ║ time      ║
╠════════════╬══════════╬═════════╬═══════════╬═══════════════╬══════════════╬═══════════╣
║ users      ║ 15420    ║ 142     ║ 15278     ║ 2847 KB       ║ 28 KB        ║ 12.41 ms  ║
╚════════════╩══════════╩═════════╩═══════════╩═══════════════╩══════════════╩═══════════╝
```

### 3.8 Автоматический VACUUM

Добавить background goroutine которая запускает VACUUM при превышении порога:

```go
// internal/storage/autovacuum.go

// AutoVacuum запускает vacuum для таблицы если доля устаревших строк
// превышает threshold (по умолчанию 20%).
type AutoVacuum struct {
    engine    Engine
    threshold float64       // 0.2 = 20%
    interval  time.Duration // как часто проверять
    logger    *slog.Logger
}

func (av *AutoVacuum) Run(ctx context.Context) {
    ticker := time.NewTicker(av.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            av.checkAndVacuum(ctx)
        }
    }
}

func (av *AutoVacuum) checkAndVacuum(ctx context.Context) {
    dbs, _ := av.engine.ListDatabases(ctx)
    for _, db := range dbs {
        tables, _ := av.engine.ListTables(ctx, db.Name)
        for _, t := range tables {
            stats, err := av.engine.TableVersionStats(ctx, db.Name, t.Name)
            if err != nil {
                continue
            }
            deadRatio := float64(stats.DeadRows) / float64(stats.TotalRows+1)
            if deadRatio > av.threshold {
                av.logger.Info("autovacuum triggered",
                    "db", db.Name,
                    "table", t.Name,
                    "dead_ratio", fmt.Sprintf("%.1f%%", deadRatio*100))
                av.engine.Vacuum(ctx, db.Name, t.Name)
            }
        }
    }
}
```

---

## 4. Hash-индекс

**Ответственный:** Dev2 (Storage, основная реализация) + Dev1 (Parser) + Dev3 (Executor)  
**Пакеты:** `internal/index`, `internal/storage`, `internal/executor`

### 4.1 Почему это важно для Яндекса

Отсутствие индексов — самое весомое замечание тимлида. Без индексов каждый
`WHERE id = 42` делает полный перебор всей таблицы. При 100 000 строк это
неприемлемо. Яндекс работает с данными на порядки больше — они это заметят.

Hash-индекс выбран (а не B-tree) потому что:
- Реализуется за разумное время
- Даёт O(1) lookup для точных совпадений (=)
- B-tree лучше для range queries, но его реализация — несколько недель

### 4.2 Синтаксис

```sql
-- Создать hash-индекс
CREATE INDEX idx_users_id ON users (id);
CREATE INDEX idx_users_email ON users (email);

-- Удалить индекс
DROP INDEX idx_users_id;

-- Список индексов
SHOW INDEXES ON users;
```

### 4.3 Парсер (Dev1)

```go
// internal/parser/ast.go

type CreateIndexStatement struct {
    IndexName string
    TableName string
    Column    string  // MVP: только один столбец
}

type DropIndexStatement struct {
    IndexName string
}

type ShowIndexesStatement struct {
    TableName string
}
```

### 4.4 Структура индекса в памяти

```go
// internal/index/hash_index.go

package index

// HashIndex — in-memory хэш-индекс на один столбец таблицы.
// Хранит маппинг: значение_столбца → []int (индексы строк в data-файле).
// После vacuum индексы строк обновляются.
type HashIndex struct {
    mu        sync.RWMutex
    name      string
    column    string
    colIndex  int               // позиция столбца в схеме (0-based)

    // Основное хранилище: ключ → список позиций строк
    // Используем string-ключи для универсальности по типу
    data      map[string][]int

    // Обратный маппинг: позиция строки → ключ
    // Нужен для UPDATE (удалить старую запись из индекса)
    reverse   map[int]string
}

func New(name, column string, colIndex int) *HashIndex {
    return &HashIndex{
        name:     name,
        column:   column,
        colIndex: colIndex,
        data:     make(map[string][]int),
        reverse:  make(map[int]string),
    }
}

// Lookup возвращает индексы строк для заданного значения.
// O(1) амортизированно.
func (idx *HashIndex) Lookup(value string) ([]int, bool) {
    idx.mu.RLock()
    defer idx.mu.RUnlock()
    positions, ok := idx.data[value]
    if !ok {
        return nil, false
    }
    // Возвращаем копию чтобы не утекать внутреннее состояние
    result := make([]int, len(positions))
    copy(result, positions)
    return result, true
}

// Insert добавляет маппинг значение → позиция строки.
// Вызывается при InsertRows.
func (idx *HashIndex) Insert(value string, rowPos int) {
    idx.mu.Lock()
    defer idx.mu.Unlock()
    idx.data[value] = append(idx.data[value], rowPos)
    idx.reverse[rowPos] = value
}

// Delete удаляет позицию строки из индекса.
// Вызывается при DeleteRows и UPDATE (старая версия).
func (idx *HashIndex) Delete(rowPos int) {
    idx.mu.Lock()
    defer idx.mu.Unlock()
    key, ok := idx.reverse[rowPos]
    if !ok {
        return
    }
    delete(idx.reverse, rowPos)
    positions := idx.data[key]
    for i, p := range positions {
        if p == rowPos {
            idx.data[key] = append(positions[:i], positions[i+1:]...)
            break
        }
    }
    if len(idx.data[key]) == 0 {
        delete(idx.data, key)
    }
}

// Rebuild пересоздаёт индекс из актуальных строк таблицы.
// Вызывается при старте сервера и после VACUUM.
func (idx *HashIndex) Rebuild(rows []storage.VersionedRow, colIndex int) {
    idx.mu.Lock()
    defer idx.mu.Unlock()

    idx.data    = make(map[string][]int)
    idx.reverse = make(map[int]string)

    for pos, row := range rows {
        if row.DeletedTx != 0 {
            continue // устаревшая версия не индексируется
        }
        if colIndex >= len(row.Data) {
            continue
        }
        key := valueToIndexKey(row.Data[colIndex])
        idx.data[key] = append(idx.data[key], pos)
        idx.reverse[pos] = key
    }
}

// valueToIndexKey конвертирует любое значение в строковый ключ для хэш-таблицы.
func valueToIndexKey(v storage.Value) string {
    if v == nil {
        return "\x00NULL" // NULL как специальный ключ
    }
    return fmt.Sprintf("%v", v)
}
```

### 4.5 IndexManager — управление индексами таблицы

```go
// internal/index/manager.go

// IndexManager хранит все индексы одной таблицы.
type IndexManager struct {
    mu      sync.RWMutex
    indexes map[string]*HashIndex // имя индекса → индекс
}

func NewManager() *IndexManager {
    return &IndexManager{
        indexes: make(map[string]*HashIndex),
    }
}

func (m *IndexManager) Add(idx *HashIndex) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.indexes[idx.name] = idx
}

func (m *IndexManager) Remove(name string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    delete(m.indexes, name)
}

// FindForColumn возвращает индекс для указанного столбца (если есть).
func (m *IndexManager) FindForColumn(column string) (*HashIndex, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    for _, idx := range m.indexes {
        if idx.column == column {
            return idx, true
        }
    }
    return nil, false
}

func (m *IndexManager) All() []*HashIndex {
    m.mu.RLock()
    defer m.mu.RUnlock()
    result := make([]*HashIndex, 0, len(m.indexes))
    for _, idx := range m.indexes {
        result = append(result, idx)
    }
    return result
}
```

### 4.6 Персистентность индексов

Индексы хранятся в `{data_dir}/databases/{db}/{table}/.indexes.json`:

```json
{
  "indexes": [
    { "name": "idx_users_id",    "column": "id",    "col_index": 0 },
    { "name": "idx_users_email", "column": "email", "col_index": 2 }
  ]
}
```

При старте сервера: читаем `.indexes.json` → вызываем `Rebuild()` для каждого индекса.

### 4.7 Интеграция в Storage Engine (Dev2)

```go
// internal/storage/file_engine.go

type FileEngine struct {
    // ... существующие поля ...

    // NEW: индексы, ключ = "db/table"
    indexes   map[string]*index.IndexManager
    indexesMu sync.RWMutex
}

func (e *FileEngine) InsertRows(ctx context.Context, db, table string, rows []Row) (int, error) {
    // WAL first
    e.wal.Append(wal.OpInsert, ...)

    // Загружаем текущие данные для определения позиций новых строк
    data, _ := e.loadDataFileRaw(db, table)
    startPos := len(data.Rows)

    // Применяем вставку
    n, err := e.insertRowsInternal(ctx, db, table, rows)

    // Обновляем индексы
    if mgr := e.getIndexManager(db, table); mgr != nil {
        schema, _ := e.GetSchema(ctx, db, table)
        for i, row := range rows {
            for _, idx := range mgr.All() {
                if idx.colIndex < len(row) {
                    key := index.ValueToIndexKey(row[idx.colIndex])
                    idx.Insert(key, startPos+i)
                    e.metrics.IncIndexHit() // индекс обновлён
                }
            }
            _ = schema
        }
    }

    return n, err
}
```

### 4.8 Использование индекса в Executor (Dev3)

```go
// internal/executor/commands.go — SelectCommand

func (c *SelectCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {

    schema, _  := s.GetSchema(ctx, c.currentDB, c.stmt.TableName)

    // Попытка использовать индекс для WHERE
    if c.stmt.Where != nil {
        if positions, ok := tryIndexLookup(c.stmt.Where, s, c.currentDB, c.stmt.TableName); ok {
            // Индекс найден — читаем только нужные строки
            c.metrics.IncIndexHit()
            rows, _ := s.ReadRowsByPositions(ctx, c.currentDB, c.stmt.TableName, positions)
            filtered, _ := filterRows(rows, schema, c.stmt.Where)
            projected, columns := projectRows(filtered, schema, c.stmt.Columns)
            return &Result{Type: "rows", Columns: columns, Rows: valuesToStrings(projected)}, nil
        }
        c.metrics.IncIndexMiss()
    }

    // Fallback: full table scan
    rows, _ := s.ReadCurrentRows(ctx, c.currentDB, c.stmt.TableName)
    filtered, _ := filterRows(rows, schema, c.stmt.Where)
    projected, columns := projectRows(filtered, schema, c.stmt.Columns)
    return &Result{Type: "rows", Columns: columns, Rows: valuesToStrings(projected)}, nil
}

// tryIndexLookup пытается найти индекс для WHERE-условия.
// Поддерживает только простые условия вида: column = value
func tryIndexLookup(
    where parser.Expression,
    s storage.Engine,
    db, table string,
) ([]int, bool) {

    // Только BinaryExpr с оператором "=" поддерживается индексом
    cmp, ok := where.(*parser.ComparisonExpr)
    if !ok || cmp.Operator != "=" {
        return nil, false
    }

    // Левая часть должна быть ColumnRef
    col, ok := cmp.Left.(*parser.ColumnRef)
    if !ok {
        return nil, false
    }

    // Правая часть — скалярное значение
    val, ok := cmp.Right.(*parser.Value)
    if !ok {
        return nil, false
    }

    // Запрашиваем индекс у storage
    return s.IndexLookup(db, table, col.Name, fmt.Sprintf("%v", val.Value()))
}
```

### 4.9 EXPLAIN с индексом

Теперь `EXPLAIN ANALYZE` показывает использование индекса:

```
QUERY PLAN
════════════════════════════════════════════════════════════════
Index Scan using "idx_users_id" on "users"
  Index Condition: (id = 42)
  ├── Index Lookup: O(1)
  ├── Rows fetched:   1
  ├── Rows matched:   1
  └── Rows filtered:  0

Execution Time: 0.03 ms        ← было 12 ms при Seq Scan
════════════════════════════════════════════════════════════════
Planning Time:  0.01 ms
Total Time:     0.04 ms
```

---

## 5. Транзакции

**Ответственный:** Dev3 (Executor, Transaction Manager) + Dev1 (Parser) + Dev2 (Storage)  
**Пакеты:** `internal/txmanager`, `internal/executor`, `internal/parser`

### 5.1 Почему это важно

Транзакции — фундаментальное свойство реляционных СУБД. `BEGIN/COMMIT/ROLLBACK` — то,
что отличает СУБД от простого key-value хранилища. Для Яндекса это базовый ожидаемый
функционал. Без транзакций любое многошаговое обновление данных небезопасно.

### 5.2 Синтаксис

```sql
BEGIN;
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
UPDATE accounts SET balance = balance + 100 WHERE id = 2;
COMMIT;

-- При ошибке:
BEGIN;
UPDATE accounts SET balance = -9999 WHERE id = 1;
ROLLBACK;  -- откатить изменения

-- Автоматический rollback если соединение разорвано
```

### 5.3 Парсер (Dev1)

```go
// internal/parser/ast.go

type BeginStatement    struct{}
type CommitStatement   struct{}
type RollbackStatement struct{}

func (s *BeginStatement)    statementNode(); StatementType() string { return "BEGIN" }
func (s *CommitStatement)   statementNode(); StatementType() string { return "COMMIT" }
func (s *RollbackStatement) statementNode(); StatementType() string { return "ROLLBACK" }
```

### 5.4 Модель транзакций

В MVP реализуется **optimistic concurrency** с полным снимком данных:

```
BEGIN
  │
  ├── Читаем snapshot текущего состояния таблиц
  │   (копируем текущие TxID границы)
  │
  ├── Все операции записываются в буфер транзакции
  │   (не применяются к основному хранилищу)
  │
COMMIT
  │
  ├── Проверяем конфликты (были ли изменены строки другой транзакцией?)
  │
  ├── Если конфликт → ERR_TRANSACTION_CONFLICT (пользователь делает ROLLBACK и повторяет)
  │
  └── Если нет конфликтов → применяем все операции атомарно

ROLLBACK
  │
  └── Просто очищаем буфер транзакции
```

### 5.5 Transaction Manager

```go
// internal/txmanager/manager.go

package txmanager

// TxState — состояние транзакции.
type TxState int

const (
    TxIdle    TxState = iota // нет активной транзакции
    TxActive                 // BEGIN выполнен, ожидаем COMMIT/ROLLBACK
)

// PendingOp — одна буферизованная операция внутри транзакции.
type PendingOp struct {
    Type    string      // "insert", "update", "delete"
    DB      string
    Table   string
    Payload interface{} // зависит от Type
}

// Transaction — активная транзакция одной сессии.
type Transaction struct {
    ID        uint64
    StartedAt time.Time
    State     TxState
    Ops       []PendingOp   // буфер операций

    // Snapshot: TxID на момент BEGIN.
    // Используется для обнаружения конфликтов при COMMIT.
    SnapshotTxID uint64
}

// Manager управляет транзакциями всех сессий.
type Manager struct {
    mu      sync.Mutex
    counter atomic.Uint64
}

func (m *Manager) Begin(snapshotTxID uint64) *Transaction {
    return &Transaction{
        ID:           m.counter.Add(1),
        StartedAt:    time.Now(),
        State:        TxActive,
        SnapshotTxID: snapshotTxID,
    }
}

// AddOp добавляет операцию в буфер транзакции.
func (tx *Transaction) AddOp(op PendingOp) {
    tx.Ops = append(tx.Ops, op)
}

// Rollback очищает буфер без применения.
func (tx *Transaction) Rollback() {
    tx.Ops = nil
    tx.State = TxIdle
}
```

### 5.6 Интеграция транзакций в Session

```go
// internal/executor/session.go

type Session struct {
    ID          string
    CurrentDB   string
    TokenLabel  string
    ConnectedAt time.Time
    QueryHistory QueryHistory
    LastError    *ErrorContext

    // NEW: транзакция
    ActiveTx    *txmanager.Transaction // nil = нет активной транзакции
    TxManager   *txmanager.Manager
}

// IsInTx возвращает true если сессия находится в активной транзакции.
func (s *Session) IsInTx() bool {
    return s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive
}
```

### 5.7 Модификация Executor для транзакций (Dev3)

```go
// internal/executor/commands.go

// BeginCommand
func (c *BeginCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {
    if sess.IsInTx() {
        return nil, &VaultDBError{Code: ErrTxAlreadyActive,
            Message: "transaction already active; COMMIT or ROLLBACK first"}
    }

    currentTxID := s.CurrentTxID()
    sess.ActiveTx = sess.TxManager.Begin(currentTxID)

    return &Result{
        Type:    "message",
        Message: fmt.Sprintf("Transaction %d started.", sess.ActiveTx.ID),
    }, nil
}

// CommitCommand
func (c *CommitCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {
    if !sess.IsInTx() {
        return nil, &VaultDBError{Code: ErrNoActiveTx, Message: "no active transaction"}
    }

    tx := sess.ActiveTx

    // Проверяем конфликты: не изменились ли затронутые строки
    // с момента BEGIN (по SnapshotTxID)
    if err := checkConflicts(ctx, s, tx); err != nil {
        return nil, &VaultDBError{
            Code:    ErrTxConflict,
            Message: "transaction conflict detected; ROLLBACK and retry",
        }
    }

    // Применяем все буферизованные операции атомарно
    if err := applyOps(ctx, s, tx.Ops); err != nil {
        // Если применение упало — автоматически откатываем
        tx.Rollback()
        sess.ActiveTx = nil
        return nil, fmt.Errorf("commit failed, transaction rolled back: %w", err)
    }

    opsCount := len(tx.Ops)
    tx.Rollback()
    sess.ActiveTx = nil

    return &Result{
        Type:    "message",
        Message: fmt.Sprintf("Transaction %d committed (%d operations).", tx.ID, opsCount),
    }, nil
}

// RollbackCommand
func (c *RollbackCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {
    if !sess.IsInTx() {
        return nil, &VaultDBError{Code: ErrNoActiveTx, Message: "no active transaction"}
    }
    opsCount := len(sess.ActiveTx.Ops)
    sess.ActiveTx.Rollback()
    sess.ActiveTx = nil
    return &Result{
        Type:    "message",
        Message: fmt.Sprintf("Transaction rolled back (%d operations discarded).", opsCount),
    }, nil
}
```

### 5.8 Буферизация DML внутри транзакции

```go
// InsertCommand — модификация для поддержки транзакций
func (c *InsertCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {

    if sess.IsInTx() {
        // Внутри транзакции: буферизуем операцию, не применяем
        sess.ActiveTx.AddOp(txmanager.PendingOp{
            Type:    "insert",
            DB:      sess.CurrentDB,
            Table:   c.stmt.TableName,
            Payload: c.stmt, // сохраняем AST-узел
        })
        return &Result{
            Type:    "message",
            Message: fmt.Sprintf("Buffered INSERT (tx %d). Not committed yet.", sess.ActiveTx.ID),
        }, nil
    }

    // Вне транзакции: применяем немедленно (auto-commit)
    return c.executeImmediate(ctx, s)
}
```

### 5.9 Автоматический ROLLBACK при разрыве соединения

```go
// internal/server/handler.go

func handleConnection(conn net.Conn, store storage.Engine, ...) {
    defer func() {
        // При разрыве соединения — откатить незавершённую транзакцию
        if sess.IsInTx() {
            logger.Warn("connection closed with active transaction, rolling back",
                "session", sess.ID,
                "tx_id", sess.ActiveTx.ID,
                "buffered_ops", len(sess.ActiveTx.Ops))
            sess.ActiveTx.Rollback()
        }
        conn.Close()
    }()
    // ...
}
```

### 5.10 Новые коды ошибок

```go
// internal/errors/codes.go

const (
    ErrTxAlreadyActive = 7001  // BEGIN при уже активной транзакции
    ErrNoActiveTx      = 7002  // COMMIT/ROLLBACK без BEGIN
    ErrTxConflict      = 7003  // конфликт при COMMIT (optimistic concurrency)
    ErrTxTimeout       = 7004  // транзакция висит дольше max_tx_duration
)
```

---

## 6. Prepared Statements

**Ответственный:** Dev3 (Executor) + Dev1 (Parser)  
**Суть:** разобрать SQL один раз, выполнять много раз с разными параметрами.

### 6.1 Синтаксис

```sql
-- Подготовить запрос (параметры обозначаются $1, $2, ...)
PREPARE get_user AS
  SELECT * FROM users WHERE id = $1;

PREPARE update_score AS
  UPDATE users SET score = $1 WHERE id = $2;

-- Выполнить с конкретными значениями
EXECUTE get_user(42);
EXECUTE get_user(99);

EXECUTE update_score(9.5, 42);
EXECUTE update_score(8.0, 99);

-- Удалить prepared statement
DEALLOCATE get_user;
```

### 6.2 Парсер (Dev1)

```go
// internal/parser/ast.go

type PrepareStatement struct {
    Name  string    // имя statement'а
    Query Statement // внутренний запрос с параметрами $N
}

type ExecuteStatement struct {
    Name   string  // имя prepared statement
    Params []Value // значения параметров
}

type DeallocateStatement struct {
    Name string
}

// ParamRef — ссылка на параметр $N в выражении
type ParamRef struct {
    Index int // 1-based
}
```

### 6.3 Statement Cache в Session

```go
// internal/executor/session.go

type PreparedStatement struct {
    Name  string
    Query parser.Statement // AST с ParamRef вместо значений
    Params []string        // типы параметров (для валидации, опционально)
}

// В Session добавить:
PreparedStatements map[string]*PreparedStatement
```

### 6.4 PrepareCommand и ExecuteCommand

```go
type PrepareCommand struct {
    stmt *parser.PrepareStatement
}

func (c *PrepareCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {
    // Сохраняем AST в кэше сессии
    sess.PreparedStatements[c.stmt.Name] = &executor.PreparedStatement{
        Name:  c.stmt.Name,
        Query: c.stmt.Query,
    }
    return &Result{
        Type:    "message",
        Message: fmt.Sprintf("Statement '%s' prepared.", c.stmt.Name),
    }, nil
}

type ExecuteCommand struct {
    stmt *parser.ExecuteStatement
}

func (c *ExecuteCommand) Execute(ctx context.Context, s storage.Engine) (*Result, error) {
    ps, ok := sess.PreparedStatements[c.stmt.Name]
    if !ok {
        return nil, &VaultDBError{
            Code:    ErrPreparedNotFound,
            Message: fmt.Sprintf("prepared statement '%s' not found", c.stmt.Name),
        }
    }

    // Подставляем параметры в AST (bind parameters)
    boundStmt, err := bindParams(ps.Query, c.stmt.Params)
    if err != nil {
        return nil, err
    }

    // Выполняем уже готовый AST без повторного парсинга
    cmd, _ := CommandFactory(boundStmt)
    return cmd.Execute(ctx, s)
}
```

### 6.5 bindParams — подстановка параметров

```go
// bindParams обходит AST и заменяет ParamRef на конкретные Value.
func bindParams(stmt parser.Statement, params []parser.Value) (parser.Statement, error) {
    // Используем deep copy + visitor pattern
    // Для MVP: только для SelectStatement и UpdateStatement
    switch s := stmt.(type) {
    case *parser.SelectStatement:
        return &parser.SelectStatement{
            Columns:   s.Columns,
            TableName: s.TableName,
            Where:     bindExpr(s.Where, params),
            AsOf:      s.AsOf,
        }, nil
    case *parser.UpdateStatement:
        // аналогично
    }
    return nil, fmt.Errorf("EXECUTE not supported for %T", stmt)
}

func bindExpr(expr parser.Expression, params []parser.Value) parser.Expression {
    switch e := expr.(type) {
    case *parser.ParamRef:
        if e.Index < 1 || e.Index > len(params) {
            // ошибка — параметр вне диапазона
            return &parser.Value{Kind: parser.ValNull}
        }
        return &params[e.Index-1]
    case *parser.ComparisonExpr:
        return &parser.ComparisonExpr{
            Left:     bindExpr(e.Left, params),
            Operator: e.Operator,
            Right:    bindExpr(e.Right, params),
        }
    case *parser.LogicalExpr:
        return &parser.LogicalExpr{
            Left:     bindExpr(e.Left, params),
            Operator: e.Operator,
            Right:    bindExpr(e.Right, params),
        }
    default:
        return expr // листовые узлы не меняем
    }
}
```

---

## 7. Benchmark-утилита

**Ответственный:** Dev4  
**Файл:** `tools/benchmark/main.go`

### 7.1 Зачем

Яндекс ценит данные. Прийти на конкурс и сказать *"наша СУБД быстрая"* — слабо.
Прийти и показать **конкретные цифры** с графиками — сильно.

### 7.2 Что измеряем

```
VaultDB Benchmark v1.0.0
═══════════════════════════════════════════════════════════════════
Config: 127.0.0.1:5432 | DB: benchmark | 10 000 rows | 4 workers
═══════════════════════════════════════════════════════════════════

INSERT (sequential, 10 000 rows):
  Total:    1.24s
  Rate:     8 064 rows/sec
  P50:      0.11ms  P95:  0.31ms  P99:  0.89ms

SELECT * (full scan, 10 000 rows):
  Total:    0.18s
  Rate:     55 556 queries/sec
  P50:      0.07ms  P95:  0.18ms  P99:  0.42ms

SELECT WHERE id = N (index scan, 10 000 lookups):
  Total:    0.09s
  Rate:     111 111 queries/sec
  P50:      0.03ms  P95:  0.08ms  P99:  0.19ms

SELECT WHERE id = N (NO index, full scan):
  Total:    2.31s
  Rate:     4 329 queries/sec
  P50:      0.21ms  P95:  0.89ms  P99:  2.1ms

  Index speedup: 25.7x ← это и есть главный аргумент за индексы

UPDATE WHERE id = N (10 000 updates):
  Total:    1.87s
  Rate:     5 347 rows/sec

DELETE WHERE id = N (10 000 deletes):
  Total:    1.92s
  Rate:     5 208 rows/sec

EXPLAIN ANALYZE SELECT * WHERE score > 8.0:
  Planning:   0.02ms
  Execution:  0.84ms
  Rows scanned / matched / filtered: 10000 / 3142 / 6858

WAL Recovery test (crash simulation):
  Inserted rows before kill -9:  1000
  Recovered rows after restart:  1000  ✓ (100%)
  Recovery time:                 23ms
═══════════════════════════════════════════════════════════════════
```

### 7.3 Реализация benchmark

```go
// tools/benchmark/main.go

package main

import (
    "flag"
    "fmt"
    "math/rand"
    "sort"
    "sync"
    "time"

    vaultdb "github.com/your-org/vaultdb/client"
)

type BenchResult struct {
    Name      string
    Total     time.Duration
    Latencies []time.Duration
    Count     int
}

func (r *BenchResult) P(percentile float64) time.Duration {
    sorted := make([]time.Duration, len(r.Latencies))
    copy(sorted, r.Latencies)
    sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
    idx := int(float64(len(sorted)) * percentile)
    if idx >= len(sorted) {
        idx = len(sorted) - 1
    }
    return sorted[idx]
}

func (r *BenchResult) Rate() float64 {
    return float64(r.Count) / r.Total.Seconds()
}

func runBench(name string, count int, fn func(i int) error) BenchResult {
    latencies := make([]time.Duration, 0, count)
    start := time.Now()

    for i := 0; i < count; i++ {
        t := time.Now()
        if err := fn(i); err != nil {
            fmt.Printf("  [ERROR] %s[%d]: %v\n", name, i, err)
        }
        latencies = append(latencies, time.Since(t))
    }

    return BenchResult{
        Name:      name,
        Total:     time.Since(start),
        Latencies: latencies,
        Count:     count,
    }
}

func printResult(r BenchResult) {
    fmt.Printf("\n%s (%d ops):\n", r.Name, r.Count)
    fmt.Printf("  Total:  %v\n", r.Total.Round(time.Millisecond))
    fmt.Printf("  Rate:   %.0f ops/sec\n", r.Rate())
    fmt.Printf("  P50:    %.2fms\n", float64(r.P(0.50).Microseconds())/1000)
    fmt.Printf("  P95:    %.2fms\n", float64(r.P(0.95).Microseconds())/1000)
    fmt.Printf("  P99:    %.2fms\n", float64(r.P(0.99).Microseconds())/1000)
}

func main() {
    host  := flag.String("host", "127.0.0.1", "VaultDB host")
    port  := flag.Int("port", 5432, "VaultDB port")
    token := flag.String("token", "", "Auth token")
    rows  := flag.Int("rows", 10000, "Number of rows for benchmarks")
    flag.Parse()

    conn := vaultdb.NewConnection(vaultdb.ConnectionOptions{
        Host:  *host,
        Port:  *port,
        Token: *token,
    })
    if err := conn.Connect(); err != nil {
        panic(err)
    }
    defer conn.Disconnect()

    fmt.Printf("VaultDB Benchmark\n")
    fmt.Printf("═══════════════════════════════════════\n")
    fmt.Printf("Host: %s:%d | Rows: %d\n", *host, *port, *rows)
    fmt.Printf("═══════════════════════════════════════\n")

    // Подготовка
    conn.Execute("CREATE DATABASE IF NOT EXISTS benchmark;")
    conn.Execute("USE benchmark;")
    conn.Execute("DROP TABLE IF EXISTS bench_table;")
    conn.Execute("CREATE TABLE bench_table (id INT, name VARCHAR(64), score FLOAT, active BOOL);")

    // INSERT benchmark
    insertResult := runBench("INSERT (sequential)", *rows, func(i int) error {
        sql := fmt.Sprintf(
            "INSERT INTO bench_table VALUES (%d, 'user_%d', %.2f, TRUE);",
            i, i, rand.Float64()*10)
        rs := conn.Execute(sql)
        if !rs.Ok() {
            return fmt.Errorf(rs.ErrorMessage())
        }
        return nil
    })
    printResult(insertResult)

    // SELECT full scan
    selectResult := runBench("SELECT * (full scan)", 100, func(i int) error {
        rs := conn.Execute("SELECT * FROM bench_table;")
        if !rs.Ok() {
            return fmt.Errorf(rs.ErrorMessage())
        }
        return nil
    })
    printResult(selectResult)

    // ... остальные бенчмарки ...

    fmt.Printf("\n═══════════════════════════════════════\n")
    fmt.Printf("Benchmark complete.\n")
}
```

---

## 8. Распределение задач

| Задача | Dev1 (Parser) | Dev2 (Storage) | Dev3 (Executor+Server) | Dev4 (Client+Build) |
|---|---|---|---|---|
| Partial reads | — | — | — | ✅ Основной |
| Prometheus | — | — | ✅ Основной | — |
| VACUUM синтаксис | ✅ Основной | — | — | — |
| VACUUM логика | — | ✅ Основной | ✅ Command | — |
| AutoVacuum | — | ✅ Основной | — | — |
| Hash Index синтаксис | ✅ Основной | — | — | — |
| Hash Index логика | — | ✅ Основной | — | — |
| Index в Executor | — | — | ✅ Основной | — |
| Транзакции синтаксис | ✅ Основной | — | — | — |
| TxManager | — | — | ✅ Основной | — |
| Tx в Storage | — | ✅ Поддержка | ✅ Основной | — |
| Prepared синтаксис | ✅ Основной | — | — | — |
| Prepared логика | — | — | ✅ Основной | — |
| Benchmark | — | — | — | ✅ Основной |

### Рекомендуемый порядок

```
Неделя 1:
  Dev4: Partial reads (2–3 часа) → сразу в main
  Dev3: Prometheus metrics → интегрировать в /metrics
  Dev1: Синтаксис VACUUM, CREATE/DROP INDEX, BEGIN/COMMIT/ROLLBACK, PREPARE/EXECUTE
  Dev2: Hash Index структура + Rebuild

Неделя 2:
  Dev2: IndexManager + персистентность + интеграция в InsertRows/UpdateRows/DeleteRows
  Dev3: ExplainCommand обновить (Index Scan vs Seq Scan)
  Dev3: TxManager + BeginCommand, CommitCommand, RollbackCommand
  Dev2: VACUUM логика + AutoVacuum

Неделя 3:
  Dev3: PrepareCommand, ExecuteCommand, DeallocateCommand
  Dev4: Benchmark утилита + README с результатами
  Все: интеграционные тесты всего нового
  Все: обновить документацию
```

---

## 9. Чеклист приёмки

### Partial Reads

| # | Критерий | Метод |
|---|---|---|
| P1 | SELECT с 50 000 строк возвращает все строки без обрезания | Функциональный тест |
| P2 | send() при разрыве соединения не паникует, возвращает NetworkError | Unit-тест |
| P3 | SO_RCVTIMEO установлен на сокете | Code review |

### Prometheus Metrics

| # | Критерий | Метод |
|---|---|---|
| M1 | `GET /metrics` возвращает `Content-Type: text/plain; version=0.0.4` | `curl -I /metrics` |
| M2 | Prometheus может распарсить вывод без ошибок | `promtool check metrics < output.txt` |
| M3 | `vaultdb_queries_total` incrementируется при каждом запросе | Функциональный тест |
| M4 | Гистограмма `vaultdb_query_duration_seconds` содержит `_bucket`, `_sum`, `_count` | `curl /metrics` |
| M5 | `vaultdb_storage_rows` обновляется каждые 30 секунд | Ожидание 30с + проверка |

### VACUUM

| # | Критерий | Метод |
|---|---|---|
| V1 | `VACUUM users;` возвращает таблицу статистики | Функциональный тест |
| V2 | После 100 UPDATE строки `_data.json` содержит 1 версию, не 101 | Прямой просмотр файла |
| V3 | `VACUUM;` без имени таблицы обрабатывает все таблицы текущей БД | Функциональный тест |
| V4 | AutoVacuum срабатывает когда dead_ratio > 20% | Unit-тест |
| V5 | `SELECT *` после VACUUM возвращает те же актуальные данные | Функциональный тест |

### Hash Index

| # | Критерий | Метод |
|---|---|---|
| I1 | `CREATE INDEX idx ON t (col);` создаёт индекс | Функциональный тест |
| I2 | `SELECT WHERE id = N` с индексом быстрее чем без индекса | Benchmark |
| I3 | Speedup с индексом ≥ 10x на таблице из 10 000 строк | Benchmark |
| I4 | `EXPLAIN ANALYZE` показывает "Index Scan" при наличии индекса | Функциональный тест |
| I5 | Индекс консистентен после INSERT, UPDATE, DELETE | Unit-тест |
| I6 | Индекс восстанавливается из `.indexes.json` при перезапуске | Перезапуск + проверка |
| I7 | `DROP INDEX` удаляет индекс | Функциональный тест |

### Транзакции

| # | Критерий | Метод |
|---|---|---|
| T1 | `BEGIN; INSERT ...; COMMIT;` применяет данные | Функциональный тест |
| T2 | `BEGIN; INSERT ...; ROLLBACK;` не применяет данные | Функциональный тест |
| T3 | `BEGIN` внутри активной транзакции → ERR_TX_ALREADY_ACTIVE | Функциональный тест |
| T4 | `COMMIT` без `BEGIN` → ERR_NO_ACTIVE_TX | Функциональный тест |
| T5 | Разрыв соединения в середине транзакции → автоматический ROLLBACK | Функциональный тест |
| T6 | Данные внутри транзакции не видны другим соединениям до COMMIT | Тест с двумя клиентами |

### Prepared Statements

| # | Критерий | Метод |
|---|---|---|
| S1 | `PREPARE name AS SELECT ...;` сохраняет запрос | Функциональный тест |
| S2 | `EXECUTE name(value);` выполняет запрос с параметром | Функциональный тест |
| S3 | `EXECUTE name(v1); EXECUTE name(v2);` — разные значения, один AST | Функциональный тест |
| S4 | `DEALLOCATE name;` удаляет prepared statement | Функциональный тест |
| S5 | `EXECUTE` несуществующего statement → корректная ошибка | Функциональный тест |

### Benchmark

| # | Критерий | Метод |
|---|---|---|
| B1 | `./benchmark --rows 10000` выводит все метрики | Запуск |
| B2 | Index speedup ≥ 10x задокументирован в README | Code review |
| B3 | WAL recovery test показывает 100% восстановление | Запуск |

---
