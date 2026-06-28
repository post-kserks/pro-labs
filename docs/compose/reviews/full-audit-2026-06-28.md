# VaultDB — Full Code Review

**Дата**: 2026-06-28
**Метод**: ручной статический анализ всего серверного кода (Go), excludes `client/` и `audit.md`

---

## Общая оценка

Проект впечатляет: полноценный SQL-движок с WAL, MVCC, оптимизатором, индексами (BTree/GIN/GiST/Hash), буфер-пулом, страничным хранилищем и транзакционным overlay. Архитектура модульная, слои разделены чётко. Тестов >50, покрытие хорошее, все тесты проходят.

Ниже — все найденные проблемы, сгруппированные по критичности.

---

## Critical (Must Fix)

### 1. Lock ordering WAL ↔ PageEngine — реальный deadlock-вектор

**Файл**: `server/internal/storage/page_engine.go:541-575`  
**Файл**: `server/internal/wal/wal.go:498-525`

**Проблема**: `doCheckpoint()` захватывает `e.mu.Lock()`, затем вызывает `e.wal.Append()` (берёт `w.mu`). Recovery-коллбэки (`redoPhase`/`undoPhase`) захватывают `e.mu` под уже удерживаемым `w.mu` — **обратный порядок**.

Это НЕ theoretical: при старте сервера `RecoverFromWAL()` → `redoPhase()` → `redoInsert()` захватывает `e.mu.Lock()` **после** того, как `wal.Replay()` захватил `w.mu`. А `doCheckpoint()` делает `e.mu.Lock()` → `w.mu` (через `wal.Append`). Если recovery и checkpoint запускаются одновременно — deadlock.

**Статус в audit.md**:✅ описан как "potential". Я подтверждаю — это **реальный** deadlock-вектор при определённых условиях.

**Рекомендация**: Добавить документированный invariant: lock order всегда WAL → PageEngine. Или перестроить checkpoint так, чтобы `w.mu` не держался при вызове `e.mu`-protected методов.

---

### 2. `context.Background()` в executor — запросы не отменяются при shutdown

**Файл**: `server/internal/executor/executor.go:205-209`

```go
queryCtx := context.Background()
if queryTimeout > 0 {
    queryCtx, cancel = context.WithTimeout(context.Background(), queryTimeout)
}
```

**Проблема**: Контекст запроса НЕ привязан к контексту сервера. При `SIGTERM`/`SIGKILL` долгие запросы (большие SELECT, тяжёлые DDL) продолжают выполняться, не прерываясь. Сервер ждёт `ShutdownTimeoutSec` (30s по умолчанию), но запрос может работать дольше.

**Статус в audit.md**:✅ описан. Подтверждаю — это high-priority для production.

**Рекомендация**: Пробрасывать `context.Context` из `handleConnection` через `Session.Execute` → `Executor.Run`.

---

### 3. WAL silent error swallowing — потеря данных при corrupt WAL

**Файл**: `server/internal/wal/wal.go:413-425`, `466-473`, `508-514`

```go
if err == io.EOF || err == io.ErrUnexpectedEOF {
    break
}
break  // остальные ошибки тоже тихо break
```

**Проблема**: В `scanAndTruncate`, `AnalyzeTransactions`, `Replay` все ошибки кроме `io.EOF/ErrUnexpectedEOF` обрабатываются как `break` **без логирования**. Повреждённая запись в середине WAL приводит к молчаливой потере ВСЕХ последующих записей.

**Статус в audit.md**:✅ описан. Согласен — это serious data loss risk.

**Рекомендация**: Добавить `slog.Warn` с offset и типом ошибки. Лучше — вернуть ошибку и прервать recovery с явным сообщением.

---

## Important (Should Fix)

### 4. ConnectionPool — не пул, а счётчик соединений

**Файл**: `server/internal/pool/pool.go:156-173`  
**Файл**: `server/cmd/vaultdb-server/main.go:486-534`

**Проблема**: `AcquireConn()` не переиспользует idle-соединения. Каждая входящая коннекция создаёт новый wrapper. Пул не возвращает соединения — он только ограничивает максимум.

**Статус в audit.md**:✅ описан. Я согласен — для production нужен настоящий пул с reuse, или переименовать в `ConnectionTracker`.

**Рекомендация**: Переименовать в `ConnectionTracker` или реализовать реальный пул (accept loop → pool → handler goroutine).

---

### 5. `isHealthy` — data race в pool

**Файл**: `server/internal/pool/pool.go:228-243`

```go
func (p *Pool) isHealthy(conn *Connection) bool {
    _ = conn.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
    _, err := conn.conn.Read(make([]byte, 0))
```

**Проблема**: Метод вызывает `conn.conn.SetReadDeadline` и `conn.conn.Read` **без блокировки `conn.mu`**, в то время как `Read`/`Write` в `Connection` держат тот же мьютекс. Race condition при конкурентном чтении/записи.

**Статус в audit.md**:✅ описан как data race. Подтверждаю.

**Рекомендация**: Взять `conn.mu.Lock()` в `isHealthy`, либо использовать `conn.conn` напрямую (обойдя обёртку).

---

### 6. `getTableForRead` / `getTableForWrite` — дублирование кода

**Файл**: `server/internal/storage/page_engine.go:797-893`

**Проблема**: ~45 строк скопированы с единственным отличием: `t.mu.RLock()` vs `t.mu.Lock()`. Нарушение DRY.

**Статус в audit.md**:✅ описан. Согласен.

**Рекомендация**: Вынести в общий метод `getTable(db, table, write bool)`.

---

### 7. `encodeColumnValue` fallback — тихая потеря типа

**Файл**: `server/internal/storage/binary_encoding.go:172-180`

```go
default:
    s := fmt.Sprintf("%v", v)
    // encode as string tag 's'
```

**Проблема**: Для неизвестных типов Go используется `fmt.Sprintf("%v")` с тегом `'s'`. При десериализации получится строка, а не оригинальный тип. Тихая деградация данных.

**Статус в audit.md**:✅ описан. Согласен.

**Рекомендация**: Возвращать ошибку для неизвестных типов, либо добавить тег `'?'` с raw JSON.

---

### 8. reflect-based command registry — хрупкость

**Файл**: `server/internal/executor/executor.go:23-96`

**Проблема**: При добавлении нового Statement нужно не забыть зарегистрировать фабрику в `init()`. Если забыть — `CommandFactory` вернёт `"unknown statement type"` в рантайме.

**Статус в audit.md**:✅ описан. Согласен — это latent bug source.

**Рекомендация**: Использовать type switch или registration через interface marker.

---

### 9. `validateConfig` дублирует `Default()` — неочевидный fallback

**Файл**: `server/internal/config/config.go:153-259`

**Проблема**: `validateConfig` повторно присваивает значения по умолчанию, которые уже установлены в `Default()`. Это fallback для случая, когда YAML содержит явный `0`/`""`/`false` и `Unmarshal` сбрасывает default. Но код неочевиден.

**Статус в audit.md**:✅ описан как мелкое замечание. Я считаю это medium — в production конфиге это может привести к неожиданному поведению.

---

## New Issues (Not in audit.md)

### 10. `isHealthy` в pool — неверная семантика для idle соединений

**Файл**: `server/internal/pool/pool.go:228-243`

```go
func (p *Pool) isHealthy(conn *Connection) bool {
    _, err := conn.conn.Read(make([]byte, 0))
    if err == io.EOF {
        return true
    }
    return false
}
```

**Проблема**: `io.EOF` означает, что remote side закрыл соединение. Для TCP это **мёртвое** соединение, но `isHealthy` считает его здоровым. При повторном использовании такого соединения будет получен `io.EOF` при чтении.

**Рекомендация**: Убрать `io.EOF` из "healthy" условий.

---

### 11. `sanitizeErrorMessage` в protocol.go — неполная защита

**Файл**: `server/cmd/vaultdb-server/protocol.go:37-53`

**Проблема**: Фильтр определяет внутренние ошибки по паттернам (`/go/src/`, `.go:`, `heapfile`), но не покрывает:
- Стек-трейсы (содержат `.go:` файлы)
- Ошибки с портами (`:5432`, `:8080`)
- Пути данных (`data/pagedb/`)

Некоторые ошибки storage могут просачиваться к клиенту.

**Рекомендация**: Использовать whitelist подход (всегда возвращать generic "internal error") вместо blacklist.

---

### 12. `VaultDB.Open()` не инициализирует WAL

**Файл**: `server/vaultdb.go:25-38`

```go
func Open(dataDir string) (*VaultDB, error) {
    s, err := storage.NewPageStorageEngine(dataDir, nil, txm)
```

**Проблема**: Embedded API передаёт `nil` для WAL. Это значит, что `PageStorageEngine` работает **без WAL** — нет crash recovery, нет durability. Пользователи embedded API могут не осознавать это.

**Рекомендация**: Либо автоматически создавать WAL, либо явно документировать отсутствие durability.

---

### 13. Missing `go.sum` dependency pinning

**Файл**: `server/go.mod`

```
go 1.23
require gopkg.in/yaml.v3 v3.0.1
```

**Проблема**: Единственная зависимость — `yaml.v3`. Но в CI используется `staticcheck@v0.5.1`, `gosec@v2.21.4`, `govulncheck@v1.1.3` — внешние инструменты без pinning версий. При изменении их поведения CI может сломаться.

**Рекомендация**: Использовать `go tool` directive (Go 1.24+) или зафиксировать версии в Makefile.

---

### 14. `handleConnection` — panic recovery без stack trace

**Файл**: `server/cmd/vaultdb-server/main.go:177-184`

```go
defer func() {
    if r := recover(); r != nil {
        logger.Error("panic in connection handler",
            "remote", conn.RemoteAddr(),
            "panic", r)
        sendError(conn, "", "internal server error", logger)
    }
}()
```

**Проблема**: Panic recovery не логирует stack trace. При production panic информация для диагностики будет потеряна.

**Рекомендация**: Добавить `debug.Stack()` в лог.

---

### 15. `RateLimiter` hardcoded values

**Файл**: `server/cmd/vaultdb-server/main.go:104`

```go
rateLimiter := httpserver.NewRateLimiter(100, 200) // 100 req/s, burst 200
```

**Проблема**: Rate limiter захардкожен в коде, не конфигурируется через `vaultdb.yaml`. В production разным деплоям могут нужны разные лимиты.

**Рекомендация**: Добавить `server.rate_limit_rps` и `server.rate_limit_burst` в конфиг.

---

### 16. `ConnectionRateLimiter` дублирует `httpserver.RateLimiter`

**Файл**: `server/cmd/vaultdb-server/main.go:140-174`  
**Файл**: `server/internal/httpserver/ratelimit.go`

**Проблема**: Два разных rate limiter'а: один для TCP (`ConnectionRateLimiter`), другой для HTTP (`httpserver.RateLimiter`). Код `ConnectionRateLimiter` написан с нуля и не переиспользует существующий.

**Рекомендация**: Переиспользовать `httpserver.RateLimiter` или вынести общий token bucket в `internal/ratelimit`.

---

### 17. Missing `VERSION` consistency check

**Файл**: `VERSION`  
**Файл**: `Makefile:4`  
**Файл**: `server/cmd/vaultdb-server/main.go:41`

**Проблема**: `VERSION` файл, `Makefile` и `main.go` содержат version. `main.go` по умолчанию `dev`, но нет проверки что `VERSION` файл существует при сборке через `make build`. Если файл удалён — будет пустая строка.

---

### 18. `InsertRows` — double sync

**Файл**: `server/internal/storage/page_engine_io.go:249-261`

```go
if err := t.heap.Sync(); err != nil {  // sync внутри loop
    return 0, err
}
...
if err := t.heap.Sync(); err != nil {  // sync после loop
    return 0, err
}
```

**Проблема**: `t.heap.Sync()` вызывается и внутри цикла (при page overflow), и после цикла. Внутренний sync избыточен — он происходит при flush'е страницы.

**Рекомендация**: Убрать внутренний sync (строка 249-251), оставить только финальный.

---

## Minor (Nice to Have)

### 19. `generateID()` — мёртвый код
`pool.go:300-304` — `generateID()` не используется, дублирует `randomID()`.

### 20. `PoolStats` — мёртвый тип
`pool.go:306-311` — `PoolStats` и `Stats()` не вызываются извне.

### 21. `VAULTDB_LOG_LEVEL` — no-op в config
`config.go:293-295` — переменная читается, но сразу отбрасывается.

### 22. `OpUpdate` отсутствует в WAL
На уровне WAL нет единого `OpUpdate`. UPDATE = DELETE + INSERT — два WAL-записи.

### 23. `distinctRows` — O(n) memory, potential O(n²) strings
`commands_select.go:414-425` — `strings.Join(row, "\x00")` для каждого row создаёт новую строку. При больших результатах — много аллокаций.

### 24. `evalOperandRaw` — potential nil dereference
`commands_select.go:311` — если `cmp.Right` это nil, `evalOperandRaw` может упасть.

---

## Тесты и покрытие

| Пакет | Статус | Время |
|-------|--------|-------|
| `cmd/vaultdb-server` | ✅ ok | 0.4s |
| `internal/ai` | ✅ ok | 2.1s |
| `internal/auth` | ✅ ok | 1.9s |
| `internal/config` | ✅ ok | 1.1s |
| `internal/executor` | ✅ ok | 299s (stress tests) |
| `internal/httpserver` | ✅ ok | 5.7s |
| `internal/index` | ✅ ok | 1.5s |
| `internal/lexer` | ✅ ok | 1.7s |
| `internal/logging` | ✅ ok | 2.0s |
| `internal/metrics` | ✅ ok | 1.8s |
| `internal/parser` | ✅ ok | 1.7s |
| `internal/pool` | ✅ ok | 1.1s |
| `internal/protocol` | ✅ ok | 0.9s |
| `internal/storage` | ✅ ok | 11.2s |
| `internal/storage/fsm` | ✅ ok | 1.0s |
| `internal/storage/heap` | ✅ ok | 1.1s |
| `internal/storage/page` | ✅ ok | 1.2s |
| `internal/tls` | ✅ ok | 1.4s |
| `internal/txmanager` | ✅ ok | 1.4s |
| `internal/wal` | ✅ ok | 1.6s |
| `internal/websocket` | ✅ ok | 1.6s |

**Все тесты проходят.** `go vet` чистый.

---

## CI/CD

CI включает: `gofmt`, `go vet`, `staticcheck`, `go test -race`, `gosec`, `govulncheck`, `npm audit`, C++ build+test, Docker smoke test. Это хорошая практика.

---

## Итоговая сводка

| Категория | Найдено | Приоритет |
|-----------|---------|-----------|
| Critical deadlock | 1 | Critical |
| Shutdown non-cancellation | 1 | High |
| Silent WAL data loss | 1 | High |
| Data race (pool) | 1 | High |
| Semantic issues (pool, EOF) | 2 | High |
| Code duplication | 2 | Medium |
| Silent data degradation | 1 | Medium |
| Hardcoded config | 2 | Medium |
| Missing error context | 1 | Medium |
| Embedded API durability gap | 1 | Medium |
| Minor/dead code | 6 | Low |

**Всего**: 20 замечаний (4 Critical/High, 8 Medium, 8 Low)

---

## Сравнение с audit.md

| audit.md # | Мой статус | Комментарий |
|------------|-----------|-------------|
| A (deadlock) | ✅ Agree, real risk | Не theoretical — реальный вектор |
| B (pool) | ✅ Agree | Дополнительно: isHealthy data race |
| C (context) | ✅ Agree | Критично для production |
| D (WAL errors) | ✅ Agree | Критично для durability |
| E (DRY) | ✅ Agree | |
| F (reflect) | ✅ Agree | |
| G (fallback) | ✅ Agree | |
| H (LOG_LEVEL) | ✅ Agree | |
| I (isHealthy race) | ✅ Agree | |
| J (token race) | ✅ Agree | Low priority |
| 3.1-3.5 | ✅ Agree | |

**Новые замечания** (не в audit.md): #10-18, 20-24.

---

## Рекомендации по приоритету для production

1. **Fix deadlock** (#1) — задокументировать lock ordering или перестроить checkpoint
2. **Fix context** (#2) — пробросить shutdown context в executor
3. **Fix WAL errors** (#3) — добавить slog.Warn на corrupt entries
4. **Fix pool race** (#5) — заблокировать conn.mu в isHealthy
5. **Fix isHealthy EOF** (#10) — убрать io.EOF из healthy
6. **Fix panic stack trace** (#14) — добавить debug.Stack()
7. **Fix embedded WAL** (#12) — документировать или автоматизировать
