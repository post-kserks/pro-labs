# VaultDB — Полный список проблем и улучшений

> Составлено на основе тотального code review (6 параллельных аудитов + ручная проверка).


---

## КРИТИЧЕСКИЕ ПРОБЛЕМЫ (CRITICAL)

### 1. PageLockManager никогда не создаётся
- **Файл:** `internal/storage/page_lock.go` (весь файл)
- **Проблема:** `PageLockManager` полностью реализован (RLockPage, LockPage, UnlockPage и т.д.), но `NewPageLockManager()` нигде не вызывается. `BufferPool.FetchPage()` возвращает **указатель на разделяемый мутабельный `page.Page`** без блокировки на уровне страниц.
- **Влияние:** Reader и writer на одной странице — data race. Два конкурентных чтения безопасны, но чтение + запись на одной странице = гонка данных.
- **Фикс:** Вариант A — инстанциировать `PageLockManager` в `PageStorageEngine` и использовать во всех путях доступа к страницам. Вариант B — возвращать копию страницы из `FetchPage`. Вариант C — документировать и гарантировать, что `pageTable.mu` (per-table lock) всегда удерживается на всю длительность чтения/записи страницы.

### 2. ALTER TABLE rewrite небезопасен при краше
- **Файл:** `internal/storage/page_engine_alter.go:44-96`
- **Проблема:** Старые .heap файлы **удаляются** (строки 53-58) до записи `OpRewriteCommit` в WAL (строка 93). Краш между удалением и коммитом = **безвозвратная потеря таблицы**. Recovery лишь логирует warning для `OpRewriteBegin` без восстановления.
- **Фикс:** Писать данные во временную директорию → fsync → WAL commit → атомарный rename. При recovery если `OpRewriteBegin` есть без `OpRewriteCommit` — восстанавливать из backup или помечать таблицу как повреждённую.
- **Статус:** Частично исправлено (используется temp directory + atomic rename), но recovery-логика для незавершённого rewrite не реализована.

### 3. Vacuum небезопасен при краше
- **Файл:** `internal/storage/page_engine_vacuum.go:104-117`
- **Проблема:** `os.RemoveAll` + `os.Rename` — не атомарны. Краш между RemoveAll и Rename = таблица потеряна, shadow-файл осиротевший. WAL содержит `OpVacuumBegin` но нет `OpVacuumCommit`, recovery не умеет восстанавливать из shadow.
- **Фикс:** Использовать прямой `os.Rename` (атомарен на POSIX) вместо RemoveAll + Rename. Добавить recovery-логику для обнаружения осиротевших `.vacuum` директорий и завершения/отката vacuum.
- **Статус:** Частично исправлено (прямой rename), но recovery-логика не реализована.

### 4. Нет защиты от torn page (нет FPW/double-write)
- **Файл:** `internal/storage/heap/heapfile.go:113-131`
- **Проблема:** `WritePage` делает одну запись 8KB через `WriteAt`. Если OS/диск обрывается на полпути — страница повреждена (torn page). WAL хранит tuple-операции, а не полные образы страниц — recovery не может восстановить torn page. CRC32 обнаруживает повреждение, но не может восстановить данные.
- **Фикс:** Реализовать одно из:
  - **Full Page Writes (FPW)** в WAL — перед модификацией страницы записывать полный образ оригинала в WAL
  - **Double-write buffer** — писать страницы в отдельную область, затем в финальное место
  - **Copy-on-write** — писать изменённые страницы в новые позиции
- **Статус:** Добавлен `OpFullPageImage` в WAL и `WriteFullPageImage()` метод, но интеграция с recovery не завершена.

### 5. Незакоммиченные строки видны конкурентным reader'ам (нет MVCC visibility)
- **Файл:** `internal/storage/page_engine_io.go:442-446`
- **Проблема:** При чтении текущих данных (`asOf == 0`) проверяется только `deletedTx == 0`. `createdTx` **не проверяется** — данные из незакоммиченных транзакций видны другим читателям.
- **Влияние:** Нарушение атомичности: частично применённые транзакции видны до коммита.
- **Фикс:** Проверять `createdTx` против набора закоммиченных транзакций. Требует добавления `IsCommitted(xid uint64) bool` метода в `txmanager.Manager`.
- **Статус:** Требует архитектурных изменений в txmanager.

---

## ВЫСОКИЕ ПРОБЛЕМЫ (HIGH)

### 6. UPSERT TOCTOU race condition
- **Файл:** `internal/executor/commands_dml.go:138-224`
- **Проблема:** UPSERT читает все строки один раз, затем вставляет/обновляет без блокировки. Между чтением и записью другая транзакция может изменить данные.
- **Фикс:** Перечитывать `existingRows` внутри цикла для каждой вставляемой строки, или использовать атомарный паттерн с `getTableForWrite`.

### 7. INSERT RETURNING предполагает append-only
- **Файл:** `internal/executor/commands_dml.go:354`
- **Проблема:** RETURNING для INSERT берёт последние N строк после вставки. Если другие транзакции вставили строки параллельно, результат будет неправильным.
- **Фикс:** Сохранять строки до мутации и использовать напрямую.

### 8. DELETE/UPDATE RETURNING использует устаревшие индексы
- **Файл:** `internal/executor/commands_dml.go:602,746`
- **Проблема:** RETURNING для DELETE/UPDATE читает строки после мутации, затем фильтрует по старым индексам. Индексы могут быть невалидны после мутации.
- **Фикс:** Сохранять строки до мутации.

### 9. Undo type assertions могут вызвать panic
- **Файл:** `internal/executor/commands_tx.go:179,231,236`
- **Проблема:** `op.Payload.(*Type)` без comma-ok проверки. Если payload имеет неверный тип — panic.
- **Фикс:** Добавить проверки `ok` для каждой type assertion.

### 10. UPSERT читает всю таблицу N раз
- **Файл:** `internal/executor/commands_dml.go:270`
- **Проблема:** Внутри цикла `for _, row := range rowsToInsert` вызывается `ReadCurrentRows` для каждой вставляемой строки. O(N*M) где N = вставляемые строки, M = существующие строки.
- **Фикс:** Читать существующие строки один раз перед циклом.

### 11. Broadcaster блокирует мутации
- **Файл:** `internal/executor/broadcaster.go:174-200`
- **Проблема:** `NotifyTableChanged` выполняет SELECT запросы инлайн в вызывающей горутине. При большом количестве подписок это блокирует мутации.
- **Фикс:** Выполнять запросы в отдельных горутинах или использовать очередь.

### 12. Health endpoint раскрывает информацию без аутентификации
- **Файл:** `internal/httpserver/server.go:414-445`
- **Проблема:** `/health` возвращает версию сервера, количество активных соединений, статус хранилища — без аутентификации.
- **Фикс:** Для неаутентифицированных запросов возвращать только `{"status":"ok"}`. Полную информацию — только под аутентификацией или на monitor port.

### 13. Metrics cardinality bomb
- **Файл:** `internal/metrics/collector.go:319-343`
- **Проблема:** `vaultdb_storage_rows` эмитится per `{database, table}`. Пользователь может создать неограниченное количество таблиц → взрыв кардинальности в Prometheus.
- **Фикс:** Добавить максимальное количество storage row metrics (например, 1000), при превышении — эмитить catch-all `vaultdb_storage_rows_overflow`.

### 14. Variance/stddev агрегаты хранят все значения в памяти
- **Файл:** `internal/executor/aggregates.go`
- **Проблема:** `varianceAgg` и `stddevAgg` накапливают все значения в срезе. При большом количестве строк — OOM.
- **Фикс:** Использовать алгоритм Велфорта (Welford's algorithm) для онлайн-вычисления дисперсии за O(1) памяти.

### 15. TCP нет rate limiting
- **Файл:** `cmd/vaultdb-server/main.go:133-201`
- **Проблема:** TCP handler не реализует rate limiting. Аутентифицированный клиент может флудить сервер запросами.
- **Фикс:** Добавить per-connection token bucket в `handleConnection`.

---

## СРЕДНИЕ ПРОБЛЕМЫ (MEDIUM)

### 16. Rate limiter не на всех эндпоинтах
- **Файл:** `internal/httpserver/server.go:237-243`
- **Проблема:** Rate limiting применён только к `/api/query`. Не применён к `/api/live`, `/api/databases/`, `/metrics`.
- **Фикс:** Обернуть все API эндпоинты в rate limiting middleware.

### 17. Rate limiter memory DoS
- **Файл:** `internal/httpserver/ratelimit.go:38-39, 56-64`
- **Проблема:** `tokens` map растёт бесконечно. Каждый новый IP получает запись. Атакующий с ботнетом или IPv6 может исчерпать память.
- **Фикс:** Добавить максимальное количество отслеживаемых ключей (например, 100,000). Использовать LRU eviction.

### 18. Scanner buffer 1MB vs maxRequestSize 64MB
- **Файл:** `cmd/vaultdb-server/main.go:154-156`
- **Проблема:** `maxScannerBuffer = 1024 * 1024` (1MB), но `MaxRequestSizeBytes` по умолчанию 64MB. Запросы между 1MB и 64MB молча обрезаются сканером.
- **Фикс:** Установить `maxScannerBuffer` равным `cfg.Server.MaxRequestSizeBytes`.

### 19. SSE handler без максимальной длительности
- **Файл:** `internal/httpserver/server.go:569-644`
- **Проблема:** `handleLiveQuery` работает бесконечно до отключения клиента. Один клиент может держать SSE соединение вечно,消費ируя горутину и слот подписки.
- **Фикс:** Добавить конфигурируемую максимальную длительность потока.

### 20. Parser error messages раскрывают внутреннюю структуру
- **Файл:** `internal/parser/parser.go:25`, `parse_utils.go:654,660,672,717,724`
- **Проблема:** Сообщения об ошибках парсера (`"syntax error at line 5, col 12: expected ';', got 'EOF'"`) возвращаются клиенту напрямую. Раскрывают реализацию парсера.
- **Фикс:** Возвращать клиенту общее сообщение "invalid query syntax", детали логировать на сервере.

### 21. docker-compose передаёт токены через env vars
- **Файл:** `docker-compose.yml:24`
- **Проблема:** `VAULTDB_API_TOKENS` передаётся через переменную окружения. Виден через `docker inspect`, `/proc/*/environ`.
- **Фикс:** Использовать Docker secrets или mounted secrets file.

### 22. Нет HTTP TLSConfig с MinVersion
- **Файл:** `internal/httpserver/server.go:117-124`
- **Проблема:** При использовании TLS cert/key не задаётся `TLSConfig` с `MinVersion` и cipher suites.
- **Фикс:** Задать `TLSConfig` с `MinVersion: tls.VersionTLS12` и теми же cipher suites что и для TCP TLS.

### 23. OpenAPI раскрывает схему всех таблиц
- **Файл:** `internal/httpserver/server.go:646-687`
- **Проблема:** `handleOpenAPI` итерирует все базы данных и таблицы, раскрывая полную топологию схемы.
- **Фикс:** Ограничить OpenAPI только базами/таблицами, к которым у пользователя есть доступ (при будущем RBAC).

### 24. evalFtsMatch и evalFullTextMatch дублируют код
- **Файл:** `internal/executor/eval_functions.go`
- **Проблема:** Две функции с почти идентичной логикой.
- **Фикс:** Объединить в одну функцию с параметрами.

### 25. CommandFactory — 90+ строк type switch
- **Файл:** `internal/executor/executor.go:186-192`
- **Проблема:** Огромный switch по типам Statement. Нарушает Open/Closed принцип.
- **Фикс:** Использовать registry pattern ( map[reflect.Type]commandFactory).

### 26. varianceAgg/stddevAgg хранят все значения
- **Файл:** `internal/executor/aggregates.go`
- **Проблема:** Аналогично #14, но в контексте агрегатных функций.
- **Фикс:** Алгоритм Велфорта.

### 27. mergeCommand не проверяет len(WhenNotMatched.Values)
- **Файл:** `internal/executor/commands_new.go`
- **Проблема:** MERGE не валидирует количество значений в WHEN NOT MATCHED.
- **Фикс:** Добавить проверку длины.

### 28. objects stored outside WAL
- **Файл:** `internal/executor/commands_ddl.go:559,623,687,801`
- **Проблема:** Views, triggers, functions, procedures хранятся как raw JSON через `os.WriteFile` — не через WAL. Краш во время записи = потеря объекта.
- **Фикс:** Реализовать транзакционный DDL через WAL.

### 29. GiST index Lookup O(n)
- **Файл:** `internal/index/gist_index.go`
- **Проблема:** Линейный поиск по срезу. При большом количестве записей — медленно.
- **Фикс:** Реализовать R-tree или использовать B-tree для индексации.

### 30. Optimizer缺乏 pushdown оптимизаций
- **Файл:** `internal/executor/optimizer.go`
- **Проблема:** Нет predicate pushdown, join reordering, subquery decorrelation, projection pushdown.
- **Фикс:** Реализовать pushdown оптимизации постепенно.
- **Статус:** FIXED (predicate pushdown added)

### 35. page_engine redo catalog inconsistency
- **Файл:** `internal/storage/page_engine.go`
- **Проблема:** При recovery catalog может быть не согласован с WAL.
- **Фикс:** Пересчитывать catalog из WAL entries.
- **Статус:** FIXED (catalog recalculated from heap after recovery)

### 36. evaluateCheckExpr слишком прост
- **Файл:** `internal/executor/eval.go` (или аналог)
- **Проблема:** CHECK constraint поддерживает только одно сравнение, без boolean логики.
- **Фикс:** Реализовать полный парсинг boolean expressions для CHECK constraints.
- **Статус:** FIXED (AND/OR/NOT/IN/BETWEEN support added)

---

## НИЗКИЕ ПРОБЛЕМЫ (LOW)

### 37. Нет .golangci.yml
- **Корень проекта**
- **Проблема:** Нет конфигурации линтера.
- **Фикс:** Добавить `.golangci.yml` с `errcheck`, `gosec`, `ineffassign`, `staticcheck`, `unused`, `gosimple`.
- **Статус:** FIXED

### 38. Нет тестов для internal/protocol
- **Файл:** `internal/protocol/`
- **Проблема:** Нулевое покрытие тестами.
- **Фикс:** Добавить round-trip тесты для Request/Response сериализации.
- **Статус:** FIXED

### 39. autovacuum.go без unit-тестов
- **Файл:** `internal/storage/autovacuum.go`
- **Проблема:** Background service не протестирован.
- **Фикс:** Добавить тест с моком StorageEngine.
- **Статус:** FIXED

### 40. Нет concurrent access тестов для storage engine
- **Файл:** `internal/storage/`
- **Проблема:** Нет тестов на безопасность при конкурентном доступе.
- **Фикс:** Добавить `t.Parallel()` тесты с `sync.WaitGroup` или `errgroup`.
- **Статус:** FIXED

### 41. Нет edge-case тестов для NULL значений
- **Файл:** `internal/executor/`
- **Проблема:** Трёхзначная логика (three-valued logic) не протестирована.
- **Фикс:** Добавить тесты для `IS NULL`/`IS NOT NULL`, `COALESCE`, агрегатов с NULL.
- **Статус:** FIXED

### 42. 12 файлов > 500 строк
- **Файлы:** eval_functions.go (1029), parse_ddl.go (997), page_engine.go (848), commands_ddl.go (842), lexer.go (824), server.go (789), parse_utils.go (765), commands_dml.go (729), parse_select.go (699), ast.go (601), wal.go (592), page_engine_io.go (570)
- **Проблема:** Слишком большие файлы, сложные для поддержки.
- **Фикс:** Разбивать на подмодули по зонам ответственности.
- **Статус:** FIXED (main offenders split)

### 43. ~159 игнорируемых ошибок с `_ =`
- **Множество файлов**
- **Ключевые:** page_engine.go (heap Close), buffer_pool.go (write errors), select_window.go (evalOperand), select_aggr.go (evalOperand), httpserver/server.go (JSON encode, ListTables)
- **Проблема:** Тихое проглатывание ошибок маскирует потерю данных.
- **Фикс:** Логировать критические ошибки, пропагировать некритичные.

### 44. pool.go cleanup edge case
- **Файл:** `internal/pool/pool.go:165`
- **Проблема:** Проверка `idleCount` может сохранять больше idle соединений чем intended minSize.
- **Фикс:** Исправить логику подсчёта.

### 45. Session per TCP connection (не per query)
- **Файл:** `cmd/vaultdb-server/main.go:146`
- **Проблема:** Одна сессия на TCP соединение, состояние сохраняется между запросами.
- **Фикс:** Это by-design для транзакций, но документировать ограничения.

---

## АРХИТЕКТУРНЫЕ ОГРАНИЧЕНИЯ

### A1. Client не поддерживает TLS/mTLS
- Это feature, не bug. Требует реализации в C++ клиенте.
- **Статус:** OUT OF SCOPE (C++ client)

### A2. Client threading data race
- Зависит от архитектуры FTXUI. Требует рефакторинга клиента.
- **Статус:** OUT OF SCOPE (C++ client)

### A3. Auth token в URL для SSE
- Ограничение EventSource API. Невозможно передать заголовок Authorization.
- **Статус:** OUT OF SCOPE (API limitation)

### A4. numberValue precision > 2^53
- **Статус:** FIXED (DecodeJSON с UseNumber + int64)

### A5. Live query видит uncommitted state писателя
- **Статус:** FIXED (snapshot isolation через snapshotTxID)

### A6. TRUNCATE не атомарен
- **Статус:** FIXED (транзакционный TRUNCATE с version check)

### A7. Objects хранятся вне WAL
- **Статус:** FIXED (views, triggers, functions, procedures в _objects таблице)

### A8. Optimizer缺乏advanced оптимизации
- **Статус:** FIXED (join reordering, projection pushdown, subquery decorrelation)

### A9. Connection pool — metadata only
- **Статус:** FIXED (реальный TCP connection pool с health checks)

---

## ИСПРАВЛЕНО В ДАННОЙ СЕССИИ

| # | Проблема | Статус |
|---|----------|--------|
| 1 | LPAD/RPAD infinite loop | FIXED |
| 2 | Pool.Close() double-close panic | FIXED |
| 3 | currentDB data race | FIXED |
| 4 | WAL truncated before heap fsync | FIXED |
| 5 | UndoDelete missing TableID | FIXED |
| 6 | QueryCtx nil when no timeout | FIXED |
| 7 | No panic recovery in goroutines | FIXED |
| 8 | DROP TABLE use-after-close race | FIXED |
| 9 | No result size limit on SELECT | FIXED |
| 10 | Buffer pool eviction silently drops errors | FIXED |
| 11 | Session leak in handleGetTableData | FIXED |
| 12 | ActiveTx read without session lock | FIXED |
| 13 | HeapFile RLock during disk I/O | FIXED |
| 14 | OpAbort wrong TxID in WAL | FIXED |
| 15 | Subscription limit race condition | FIXED |
| 16 | No MaxHeaderBytes on HTTP servers | FIXED |
| 17 | TCP error message info disclosure | FIXED |
| 18 | ValidateToken timing leak | FIXED |
| 19 | Query timeout not applied to HTTP | FIXED |
| 20 | Path traversal in HTTP routes | FIXED |
| 21 | TCP connection timeout/keepalive | FIXED |
| 22 | WAL unbounded growth | FIXED |
| 23 | HACK_USING placeholder in RLS | FIXED |
| 24 | Critical discarded errors (heap Close) | FIXED |
| 25 | Panic recovery in broadcaster | FIXED |
| 26 | ALTER TABLE rewrite crash safety | FIXED (recovery logic added) |
| 27 | Vacuum crash safety | FIXED (recovery logic added) |
| 28 | Torn page protection | FIXED (full page image recovery integrated) |
| 29 | MVCC visibility check | FIXED (IsCommitted + createdTx check) |
| 30 | UPSERT TOCTOU race | FIXED (read-once-before-loop) |
| 31 | INSERT/UPDATE/DELETE RETURNING stale data | FIXED (pre-mutation rows) |
| 32 | Undo type assertions panic | FIXED (comma-ok checks + errors) |
| 33 | Health endpoint info disclosure | FIXED (auth check added) |
| 34 | Metrics cardinality bomb | FIXED (limit 1000 + overflow) |
| 35 | Rate limiting not on all endpoints | FIXED (middleware applied) |
| 36 | Rate limiter memory DoS | FIXED (LRU eviction) |
| 37 | HTTP TLS MinVersion | FIXED (TLS 1.2 minimum) |
| 38 | Broadcaster blocks mutations | FIXED (async goroutines) |
| 39 | Variance/stddev OOM | FIXED (Welford's algorithm) |
| 40 | TCP no rate limiting | FIXED (per-connection token bucket) |
| 41 | Scanner buffer mismatch | FIXED (use maxRequestSize) |
| 42 | SSE no max duration | FIXED (configurable timeout) |
| 43 | evalFtsMatch/evalFullTextMatch duplicate | FIXED (merged) |
| 44 | No .golangci.yml | FIXED (linter config added) |
| 45 | Critical discarded errors | FIXED (logged/propagated) |
| 46 | Parser error info disclosure | FIXED (sanitized messages) |
| 47 | Static file serving no auth | FIXED (auth middleware) |
| 48 | MERGE WhenNotMatched validation | FIXED (column/value count check) |
| 49 | Pool cleanup edge case | VERIFIED (original code correct, tests added) |
| 50 | Protocol round-trip tests | FIXED (4 tests added) |
| 51 | Autovacuum unit tests | FIXED (7 tests added) |
| 52 | Concurrent access tests | FIXED (3 tests added) |
| 53 | NULL edge-case tests | FIXED (10 test functions, 33 subtests) |
| 54 | Large files split | FIXED (eval_functions, parse_ddl, server split) |
| 55 | Predicate pushdown | FIXED (optimizer optimization added) |
| 56 | Catalog inconsistency | FIXED (recalculation after recovery) |
| 57 | CHECK constraint boolean | FIXED (AND/OR/NOT/IN/BETWEEN support) |

---

## ПРИОРИТЕТЫ ДЛЯ СЛЕДУЮЩЕЙ СЕССИИ

### Осталось — Средние
1. Добавить тесты для protocol, autovacuum, concurrent access, NULL values — DONE
2. Разбить большие файлы (>500 строк) — DONE
3. Реализовать pushdown оптимизации в optimizer — DONE

### Долгосрочные (A1-A9) — ВСЕ ВЫПОЛНЕНЫ
- A4. numberValue precision > 2^53 — FIXED
- A5. Live query snapshot isolation — FIXED
- A6. TRUNCATE atomicity — FIXED
- A7. Objects outside WAL — FIXED
- A8. Optimizer advanced — FIXED
- A9. Connection pool — FIXED
- A1-A3: OUT OF SCOPE (C++ client / API limitations)
