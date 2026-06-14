# VaultDB — ТЗ на продакшен-готовность

## Документ
Code Review → Production Readiness TZ v2.0

## Статус
Предварительный анализ завершён. Реализация начата.

---

## 1. CRITICAL: WAL Recovery (ARIES)

### 1.1 Текущее состояние
- `page_engine.go:100-180`: RecoverFromWAL, redoPhase, undoPhase — ** реализованы **
- `page_engine.go:183-280`: redoInsert, redoDelete, undoInsert, undoDelete — ** реализованы **
- **ПРОБЛЕМА**: redo/undo ищут таблицы по `fmt.Sprintf("%d/%d", TableID, PageNo)` — это неверный формат. Таблицы ищутся по `"db/table"`.
- **ПРОБЛЕМА**: WAL не записывается при INSERT/UPDATE/DELETE в page engine — только при VACUUM.
- **ПРОБЛЕМА**: Executor не пишет в WAL (ctx.WAL есть, но CommitCommand не использует его).

### 1.2 Необходимые изменения

**1.2.1 WAL-интеграция с DML (3-4 дня)**
- Файл: `storage/page_engine.go`
- Каждый INSERT/UPDATE/DELETE должен писать в WAL ПЕРЕД изменением heap
- Формат payload: `{TableID, SegmentNo, PageNo, SlotNo, XID, TupleData}`
- Порядок: WAL append → heap modify → catalog update

**1.2.2 Исправление redo/undo (2-3 дня)**
- Файл: `storage/page_engine.go:183-280`
- Исправить формат ключа таблицы: использовать `db/table` вместо `TableID/PageNo`
- Добавить проверку LSN (не replay'ить записи старше текущего LSN страницы)
- Реализовать redoInsert через findOrAllocPage

**1.2.3 WAL-интеграция executor (1-2 дня)**
- Файл: `executor/commands_tx.go`
- CommitCommand должен писать OpCommit в WAL
- InsertCommand/UpdateCommand/DeleteCommand должны писать в WAL при выполнении

### 1.3 Критерии приёмки
- Kill -9 во время INSERT → после restart 0 строк (неатомарность)
- Kill -9 после COMMIT → после restart строки на месте
- Kill -9 во время VACUUM → после restart данные целы
- Kill -9 во время UPDATE → после restart консистентное состояние

---

## 2. HIGH: Buffer Pool Integration

### 2.1 Текущее состояние
- `storage/buffer_pool.go`: ** реализован ** (LRU, dirty tracking, flush)
- `storage/page_engine.go`: ** НЕ ИСПОЛЬЗУЕТ ** — читает/пишет напрямую в HeapFile

### 2.2 Необходимые изменения

**2.2.1 Интеграция с page engine (3-4 дня)**
- Файл: `storage/page_engine.go`
- Добавить поле `bufPool *BufferPool` в PageStorageEngine
- Заменить прямые вызовы `t.heap.ReadPage/WritePage` на `bufPool.FetchPage/Unpin`
- При INSERT: FetchPage → InsertTuple → Unpin(dirty=true)
- При SELECT: FetchPage → чтение → Unpin(dirty=false)
- CheckpointLoop: FlushDirtyPagesUpToLSN

**2.2.2 Buffer Pool для scanTuples (1-2 дня)**
- Файл: `storage/page_engine.go:677-708`
- scanTuples должен использовать BufferPool вместо прямого чтения

### 2.3 Критерии приёмки
- Повторные чтения одной страницы —命中缓存 (нет disk I/O)
- Checkpoint сбрасывает dirty pages на диск
- Memory usage предсказуемо (не растёт бесконечно)

---

## 3. HIGH: Connection Pooling (реальный пул)

### 3.1 Текущее состояние
- `main.go:268-274`: Semaphore на 1000 соединений
- **ПРОБЛЕМА**: Это не пул, а лимит. Нет переиспользования соединений.

### 3.2 Необходимые изменения

**3.2.1 Реализация connection pool (2-3 дня)**
- Новый файл: `internal/pool/pool.go`
- Пул соединений с min/max размером
- Idle timeout для закрытия неиспользуемых соединений
- Health check для проверки состояния соединений
- Метрики: active/idle/total connections

**3.2.2 Интеграция с executor (1 день)**
- Файл: `executor/session.go`
- Session должен брать соединение из пула при создании
- Session должен возвращать соединение в пул при закрытии

### 3.3 Критерии приёмки
- 1000+ concurrent connections — memory usage стабильно
- Idle connections закрываются по таймауту
- Connection reuse работает корректно

---

## 4. HIGH: Query Optimizer improvements

### 4.1 Текущее состояние
- `executor/optimizer.go`: ** реализован ** (CBO с статистикой)
- **ПРОБЛЕМА**: Только Sequential Scan и Index Scan
- **ПРОБЛЕМА**: Только Nested Loop Join

### 4.2 Необходимые изменения

**4.2.1 Hash Join (2-3 дня)**
- Файл: `executor/optimizer.go`
- Реализация Hash Join для equi-joins
- Выбор между Nested Loop и Hash Join по cost

**4.2.2 Merge Join (2-3 дня)**
- Файл: `executor/optimizer.go`
- Реализация Merge Join для отсортированных данных
- Выбор по cost

**4.2.3 Index-only scan (1-2 дня)**
- Файл: `executor/optimizer.go`
- Если все запрашиваемые столбцы есть в индексе — не читать heap

### 4.3 Критерии приёмки
- JOIN на 10K строк — < 100ms
- SELECT с WHERE по индексу — < 10ms
- EXPLAIN показывает оптимальный план
- Hash Join выбран для equi-joins больших таблиц

---

## 5. HIGH: Security hardening

### 5.1 Текущее состояние
- `auth/manager.go`: HMAC-SHA256 — ** OK **
- `tls/tls.go`: Self-signed cert — ** OK **
- **ПРОБЛЕМА**: SQL injection через string values
- **ПРОБЛЕМА**: CORS wildcard
- **ПРОБЛЕМА**: Нет rate limiting

### 5.2 Необходимые изменения

**5.2.1 SQL injection prevention (2-3 дня)**
- Файл: `executor/eval.go`
- Экранирование строковых литералов
- Параметризованные запросы (prepared statements)

**5.2.2 Rate limiting (1-2 дня)**
- Новый файл: `internal/httpserver/ratelimit.go`
- Token bucket algorithm
- Per-IP rate limiting
- Per-user rate limiting

**5.2.3 CORS hardening (1 день)**
- Файл: `httpserver/server.go:359`
- Конфигурируемые origins вместо wildcard
- Добавить в config.yaml

### 5.3 Критерии приёмки
- SQL injection тесты не проходят
- Rate limiting работает при 1000+ rps
- CORS настроен корректно

---

## 6. MEDIUM: SQL Completeness

### 6.1 Текущее состояние
- Parser: ~70% coverage
- Executor: ~80% coverage
- **ПРОБЛЕМА**: Много функций не реализованы в executor (parser есть, execution нет)

### 6.2 Необходимые изменения

**6.2.1 EXISTS/NOT EXISTS (2-3 дня)**
- Файл: `executor/eval.go`
- Парсинг уже есть (ast.go)
- Нужна реализация в evalOperand

**6.2.2 Correlated subqueries (3-4 дня)**
- Файл: `executor/eval.go:451-476`
- Подзапросы ссылаются на внешние столбцы
- Нужно передавать outerRow/outerSchema

**6.2.3 INSERT ... SELECT (2-3 дня)**
- Файл: `executor/commands_dml.go`
- Парсинг уже есть (ast.go)
- Нужна реализация executeImmediate

**6.2.4 UPDATE ... FROM execution (2-3 дня)**
- Файл: `executor/commands_dml.go`
- Парсинг уже есть (parser.go)
- Нужна реализация с JOIN

**6.2.5 MERGE execution (3-4 дня)**
- Файл: `executor/commands_new.go`
- Текущая реализация — заглушка
- Нужна полная реализация с WHEN MATCHED/NOT MATCHED

### 6.3 Критерии приёмки
- EXISTS подзапросы работают
- Correlated subqueries работают
- INSERT ... SELECT работает
- UPDATE ... FROM работает
- MERGE работает

---

## 7. MEDIUM: Monitoring & Observability

### 7.1 Текущее состояние
- `metrics/collector.go`: ** реализован ** (Prometheus format)
- `httpserver/server.go`: /metrics, /dashboard — ** OK **
- **ПРОБЛЕМА**: Нет structured logging для всех компонентов
- **ПРОБЛЕМА**: Нет alerting

### 7.2 Необходимые изменения

**7.2.1 Structured logging (2-3 дня)**
- Файл: `cmd/vaultdb-server/main.go`
- Единый slog handler для всех компонентов
- Log levels: DEBUG, INFO, WARN, ERROR
- Correlation ID для запросов

**7.2.2 Query performance logging (1-2 дня)**
- Файл: `executor/executor.go`
- Логирование медленных запросов (> 1s)
- EXPLAIN ANALYZE автоматически для медленных запросов

**7.2.3 Health check improvements (1 день)**
- Файл: `httpserver/server.go`
- Детальный health check: storage, WAL, connections
- Readiness probe vs Liveness probe

### 7.3 Критерии приёмки
- Все логи в JSON формате
- Медленные запросы логируются
- Health check возвращает детальную информацию

---

## 8. MEDIUM: Performance optimizations

### 8.1 Текущее состояние
- Query Optimizer: ** реализован **
- B-tree Index: ** реализован **
- Buffer Pool: ** реализован, но не интегрирован **
- **ПРОБЛЕМА**: Нет prepared statement cache
- **ПРОБЛЕМА**: Нет parallel query execution

### 8.2 Необходимые изменения

**8.2.1 Prepared statement cache (2-3 дня)**
- Файл: `executor/session.go`
- Кэш скомпилированных запросов
- LRU eviction для кэша

**8.2.2 Parallel query execution (4-5 дней)**
- Файл: `executor/executor.go`
- Parallel seq scan для больших таблиц
- Parallel hash aggregate

**8.2.3 Memory-mapped files (3-4 дня)**
- Файл: `storage/heap/heapfile.go`
- mmap для heap файлов
- Copy-on-write для dirty pages

### 8.3 Критерии приёмки
- Prepared statements работают быстрее
- Parallel scan ускоряет SELECT на больших таблицах
- Memory usage снижается на 30-50%

---

## 9. LOW: Additional SQL features

### 9.1 Необходимые изменения

**9.1.1 CREATE VIEW (2-3 дня)**
- Новый файл: `executor/commands_view.go`
- Хранение view как saved SELECT statement
- Поддержка в SELECT

**9.1.2 ALTER TABLE ADD CONSTRAINT (2-3 дня)**
- Файл: `executor/commands_ddl.go`
- NOT NULL, DEFAULT, UNIQUE, PRIMARY KEY
- Проверка при INSERT/UPDATE

**9.1.3 JSON functions (2-3 дня)**
- Файл: `executor/eval.go`
- JSON_EXTRACT, JSON_SET, JSON_LENGTH
- JSON path expressions

**9.1.4 Additional string functions (1-2 дня)**
- Файл: `executor/eval.go`
- REGEXP_REPLACE, REGEXP_MATCHES
- SPLIT_PART, TRANSLATE

### 9.2 Критерии приёмки
- CREATE VIEW работает
- Constraints проверяются при INSERT/UPDATE
- JSON функции работают

---

## 10. LOW: DevOps & Operations

### 10.1 Необходимые изменения

**10.1.1 CI/CD pipeline (2-3 дня)**
- Файл: `.github/workflows/ci.yml`
- Go build + test + lint
- Docker build + push
- Release automation

**10.1.2 Docker Compose (1-2 дня)**
- Файл: `docker-compose.yml`
- VaultDB + Prometheus + Grafana
- Health checks

**10.1.3 Configuration documentation (1-2 дня)**
- Файл: `docs/configuration.md`
- Все параметры конфигурации
- Examples

**10.1.4 API documentation (2-3 дня)**
- Файл: `docs/api.md`
- REST API endpoints
- TCP protocol
- Error codes

### 10.2 Критерии приёмки
- CI/CD работает
- Docker Compose поднимается одной командой
- Документация полная и актуальная

---

## Приоритеты реализации

### Неделя 1-2: CRITICAL
1. WAL-интеграция с DML
2. Исправление redo/undo
3. WAL-интеграция executor
4. Buffer Pool integration

### Неделя 3-4: HIGH
5. Connection Pooling (реальный пул)
6. Query Optimizer (Hash Join, Merge Join)
7. SQL injection prevention

### Неделя 5-6: MEDIUM
8. Structured logging
9. Prepared statement cache
10. EXISTS/NOT EXISTS
11. Correlated subqueries

### Неделя 7-8: MEDIUM
12. INSERT ... SELECT
13. UPDATE ... FROM execution
14. MERGE execution
15. Rate limiting

### Неделя 9-10: LOW
16. CREATE VIEW
17. ALTER TABLE ADD CONSTRAINT
18. JSON functions
19. CI/CD pipeline

---

## Критические файлы

| Файл | Изменения |
|------|-----------|
| `storage/page_engine.go` | WAL integration, Buffer Pool |
| `executor/commands_tx.go` | WAL commit |
| `executor/commands_dml.go` | WAL writes for DML |
| `executor/eval.go` | EXISTS, correlated subqueries |
| `executor/optimizer.go` | Hash Join, Merge Join |
| `internal/pool/pool.go` | Connection pool (новый) |
| `httpserver/ratelimit.go` | Rate limiting (новый) |
| `httpserver/server.go` | CORS hardening |
| `cmd/vaultdb-server/main.go` | Connection pool, logging |
