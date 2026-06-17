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

### 31. Static file serving без аутентификации
- **Файл:** `internal/httpserver/server.go:192`
- **Проблема:** Web UI (`/`) обслуживается без auth middleware. Утечка информации.
- **Фикс:** Обернуть в auth middleware или разрешить только для определённых путей.

### 32. parseSelect — 320 строк
- **Файл:** `internal/parser/parse_select.go`
- **Проблема:** Слишком длинная функция, сложная для поддержки.
- **Фикс:** Разбить на подфункции по типам подзапросов.

### 33. main() — 260 строк
- **Файл:** `cmd/vaultdb-server/main.go`
- **Проблема:** Слишком много логики в main.
- **Фикс:** Вынести инициализацию сервера, storage, auth в отдельные функции.

### 34. SelectStatement — 19 полей
- **Файл:** `internal/parser/ast.go`
- **Проблема:** Слишком много опциональных полей в одной структуре.
- **Фикс:** Использовать composition или builder pattern.

### 35. page_engine redo catalog inconsistency
- **Файл:** `internal/storage/page_engine.go`
- **Проблема:** При recovery catalog может быть не согласован с WAL.
- **Фикс:** Пересчитывать catalog из WAL entries.

### 36. evaluateCheckExpr слишком прост
- **Файл:** `internal/executor/eval.go` (или аналог)
- **Проблема:** CHECK constraint поддерживает только одно сравнение, без boolean логики.
- **Фикс:** Реализовать полный парсинг boolean expressions для CHECK constraints.

---

## НИЗКИЕ ПРОБЛЕМЫ (LOW)

### 37. Нет .golangci.yml
- **Корень проекта**
- **Проблема:** Нет конфигурации линтера.
- **Фикс:** Добавить `.golangci.yml` с `errcheck`, `gosec`, `ineffassign`, `staticcheck`, `unused`, `gosimple`.

### 38. Нет тестов для internal/protocol
- **Файл:** `internal/protocol/`
- **Проблема:** Нулевое покрытие тестами.
- **Фикс:** Добавить round-trip тесты для Request/Response сериализации.

### 39. autovacuum.go без unit-тестов
- **Файл:** `internal/storage/autovacuum.go`
- **Проблема:** Background service не протестирован.
- **Фикс:** Добавить тест с моком StorageEngine.

### 40. Нет concurrent access тестов для storage engine
- **Файл:** `internal/storage/`
- **Проблема:** Нет тестов на безопасность при конкурентном доступе.
- **Фикс:** Добавить `t.Parallel()` тесты с `sync.WaitGroup` или `errgroup`.

### 41. Нет edge-case тестов для NULL значений
- **Файл:** `internal/executor/`
- **Проблема:** Трёхзначная логика (three-valued logic) не протестирована.
- **Фикс:** Добавить тесты для `IS NULL`/`IS NOT NULL`, `COALESCE`, агрегатов с NULL.

### 42. 12 файлов > 500 строк
- **Файлы:** eval_functions.go (1029), parse_ddl.go (997), page_engine.go (848), commands_ddl.go (842), lexer.go (824), server.go (789), parse_utils.go (765), commands_dml.go (729), parse_select.go (699), ast.go (601), wal.go (592), page_engine_io.go (570)
- **Проблема:** Слишком большие файлы, сложные для поддержки.
- **Фикс:** Разбивать на подмодули по зонам ответственности.

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

## АРХИТЕКТУРНЫЕ ОГРАНИЧЕНИЯ (требуют долгосрочного плана)

### A1. Client не поддерживает TLS/mTLS
- Это feature, не bug. Требует реализации в C++ клиенте.

### A2. Client threading data race
- Зависит от архитектуры FTXUI. Требует рефакторинга клиента.

### A3. Auth token в URL для SSE
- Ограничение EventSource API. Невозможно передать заголовок Authorization.

### A4. numberValue precision > 2^53
- Требует int64 типа в JSON Value. Сломает обратную совместимость.

### A5. Live query видит uncommitted state писателя
- `broadcaster.go:188-191` — запрос выполняется в контексте писателя.
- Требует реализации snapshot isolation для live queries.

### A6. TRUNCATE не атомарен
- `commands_new.go` — рекомендовано использовать транзакции.

### A7. Objects хранятся вне WAL
- Views, triggers, functions, procedures — raw JSON файлы.
- Требует транзакционного DDL.

### A8. Optimizer缺乏advanced оптимизации
- Нет predicate pushdown, join reordering, subquery decorrelation.
- Долгосрочная задача.

### A9. Connection pool — metadata only
- `pool.go` отслеживает метаданные соединений, но не управляет реальными TCP соединениями.
- По сути semaphore pattern, не connection pool.

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
| 26 | ALTER TABLE rewrite crash safety | PARTIAL (temp dir + rename, no recovery) |
| 27 | Vacuum crash safety | PARTIAL (atomic rename, no recovery) |
| 28 | Torn page protection | PARTIAL (OpFullPageImage added, not integrated) |

---

## ПРИОРИТЕТЫ ДЛЯ СЛЕДУЮЩЕЙ СЕССИИ

### Неделя 1 — Критические
1. Завершить recovery-логику для ALTER TABLE rewrite
2. Завершить recovery-логику для Vacuum
3. Завершить интеграцию Full Page Images с recovery
4. Реализовать MVCC visibility check (требует txmanager.IsCommitted)

### Неделя 2 — Высокие
5. Исправить UPSERT TOCTOU race
6. Исправить INSERT/UPDATE/DELETE RETURNING stale data
7. Исправить Undo type assertions panic
8. Оптимизировать UPSERT (чтение всей таблицы N раз)
9. Добавить rate limiting на все HTTP эндпоинты
10. Добавить per-connection rate limiting на TCP

### Неделя 3 — Средние
11. Добавить .golangci.yml
12. Разбить большие файлы (>500 строк)
13. Добавить тесты для protocol, autovacuum, concurrent access, NULL values
14. Реализовать pushdown оптимизации в optimizer
15. Объединить evalFtsMatch/evalFullTextMatch
