# VaultDB — План покрытия тестами

**Дата**: 2026-07-01
**Текущее покрытие**: 57.9%
**Целевое покрытие**: 80%+
**Непокрытых функций**: 666

---

## Текущее состояние по пакетам

| Пакет | Покрытие | Статус |
|-------|----------|--------|
| storage/page | 89.2% | Отлично |
| storage/fsm | 88.7% | Отлично |
| metrics | 84.8% | Хорошо |
| ai | 79.8% | Хорошо |
| auth | 78.1% | Хорошо |
| pool | 78.0% | Хорошо |
| backup | 72.7% | Хорошо |
| storage/heap | 64.0% | Нормально |
| txmanager | 62.7% | Нормально |
| executor | 61.8% | Нужно улучшить |
| storage | 60.1% | Нужно улучшить |
| websocket | 58.5% | Нужно улучшить |
| index | 56.7% | Нужно улучшить |
| tls | 54.1% | Нужно улучшить |
| logging | 54.1% | Нужно улучшить |
| httpserver | 53.8% | Нужно улучшить |
| wal | 52.4% | Нужно улучшить |
| parser | 51.9% | Нужно улучшить |
| lexer | 51.9% | Нужно улучшить |
| config | 41.6% | Критично |
| websocket | 58.5% | Нужно улучшить |
| cmd/vaultdb-server | 9.2% | Критично |
| cmd/check-index | 0.0% | Не покрыто |
| cmd/vaultdb-backup | 0.0% | Не покрыто |

---

## Приоритет 1: Критичные пакеты (<50%)

### 1.1 cmd/vaultdb-server (9.2% → 40%)

**Непокрытые функции**:
- `loadConfig` — загрузка конфигурации из YAML
- `setupStorage` — инициализация storage engine
- `runHTTPServer` — запуск HTTP сервера
- `handleConnection` — обработка TCP соединения
- `updateStorageMetrics` — обновление метрик storage
- `sendError`, `sanitizeErrorMessage`, `sendResult`, `writeResponse` — TCP protocol helpers

**Тесты**:
- `TestLoadConfig` — загрузка из файла, defaults, env overrides
- `TestHandleConnection` — mock storage + net.Pipe, отправка запроса, получение ответа
- `TestUpdateStorageMetrics` — mock storage, проверка обновления метрик
- `TestSendError` — проверка формата ошибки
- `TestSanitizeErrorMessage` — проверка sanitization
- `TestSendResult` — проверка формата результата

### 1.2 config (41.6% → 70%)

**Непокрытые функции**:
- `ApplyEnvOverrides` — применение env переменных
- `envBoolValue` — парсинг bool из env
- `Reload` — горячая перезагрузка конфигурации

**Тесты**:
- `TestApplyEnvOverrides` — все env переменные (VAULTDB_HOST, VAULTDB_PORT, etc.)
- `TestEnvBoolValue` — true/false/1/0/yes/no
- `TestReload` — изменение файла, проверка перезагрузки
- `TestConfigValidation` — валидация всех полей

### 1.3 parser (51.9% → 70%)

**Непокрытые функции**: Many parse_* functions, expression parsing edge cases

**Тесты**:
- `TestParseMergeComplete` — MERGE со всеми вариантами
- `TestParseWindowFunctions` — все window functions
- `TestParseJSONB` — все JSONB операторы
- `TestParseSubqueries` — scalar, EXISTS, IN, ALL, ANY
- `TestParseCTERecursive` — recursive CTE edge cases
- `TestParseExpressions` — сложные выражения, вложенность
- `TestParseErrorRecovery` — ошибки парсинга

### 1.4 lexer (51.9% → 70%)

**Непокрытые функции**: Token handling edge cases

**Тесты**:
- `TestLexerOperators` — все операторы
- `TestLexerStrings` — escape sequences, unicode
- `TestLexerNumbers` — edge cases (overflow, precision)
- `TestLexerKeywords` — все ключевые слова
- `TestLexerErrorHandling` — невалидный ввод

---

## Приоритет 2: Средние пакеты (50-65%)

### 1.5 wal (52.4% → 70%)

**Непокрытые функции**:
- `Replay` — воспроизведение WAL
- `AnalyzeTransactions` — анализ транзакций
- `scanAndTruncate` — сканирование и обрезка
- `WriteFullPageImage` — запись полного образа страницы

**Тесты**:
- `TestWALReplay` — воспроизведение после краша
- `TestWALAnalyzeTransactions` — committed vs in-progress
- `TestWALScanAndTruncate` — corrupt tail handling
- `TestWALFullPageImage` — запись и чтение образов
- `TestWALBatchFsync` — batch fsync behavior

### 1.6 httpserver (53.8% → 70%)

**Непокрытые функции**: HTTP handlers, middleware

**Тесты**:
- `TestHandleQuery` — основной query handler
- `TestHandleBatch` — batch queries
- `TestHandleTransaction` — BEGIN/COMMIT/ROLLBACK
- `TestHandleStreaming` — SSE streaming
- `TestHandleListDatabases` — list databases
- `TestHandleSchema` — schema explorer
- `TestRateLimitMiddleware` — rate limiting
- `TestAuthMiddleware` — auth check

### 1.7 index (56.7% → 70%)

**Непокрытые функции**:
- `GINIndex` — GIN index operations
- `GiSTIndex` — GiST index operations
- `CompositeIndex` — multi-column index
- `IndexManager` — index management

**Тесты**:
- `TestGINIndexInsertLookup` — GIN insert и lookup
- `TestGINIndexJSONB` — JSONB path queries
- `TestGiSTIndexVector` — vector similarity
- `TestCompositeIndex` — multi-column queries
- `TestIndexManagerLifecycle` — create/drop/rebuild

### 1.8 storage (60.1% → 70%)

**Непокрытые функции**:
- `Vacuum` — vacuum operations
- `AlterTableModifyColumn` — modify column
- `TruncateTable` — truncate (уже есть, но покрытие низкое)
- `RowHistory` — row history
- `TableVersionStats` — version statistics

**Тесты**:
- `TestVacuumReclaimSpace` — проверка освобождения места
- `TestVacuumConcurrentRead` — concurrent access during vacuum
- `TestTruncateTable` — truncate + recovery
- `TestRowHistory` — история изменений строки
- `TestTableVersionStats` — статистика версий

### 1.9 executor (61.8% → 75%)

**Непокрытые функции**:
- `executeWithGrouping` — GROUP BY + HAVING
- `applyWindowFunctions` — window functions execution
- `executeJoins` — JOIN execution
- `executeReturningGeneric` — RETURNING projection
- `enforceRLSPolicies` — RLS enforcement
- `enforceCheckConstraints` — CHECK constraint enforcement
- `evaluateCheckExpr` — CHECK expression evaluation

**Тесты**:
- `TestGroupByHaving` — GROUP BY с HAVING
- `TestWindowFunctions` — все window functions в execution
- `TestJoinTypes` — LEFT/RIGHT/FULL/CROSS JOIN
- `TestRLSPolicies` — SELECT/UPDATE/DELETE с RLS
- `TestCheckConstraints` — CHECK на INSERT/UPDATE
- `TestCheckExpressions` — сложные CHECK выражения
- `TestReturningOldNew` — old.* / new.* в RETURNING

---

## Приоритет 3: Высокие пакеты (65-80%)

### 1.10 storage/heap (64.0% → 75%)

**Тесты**:
- `TestHeapFileCompaction` — компакция после удалений
- `TestHeapFileConcurrentAccess` — concurrent reads/writes
- `TestHeapFileRecovery` — recovery after corruption

### 1.11 txmanager (62.7% → 75%)

**Тесты**:
- `TestTransactionIsolation` — snapshot isolation
- `TestTransactionConflict` — conflict detection
- `TestSavepointRollback` — savepoint operations
- `TestSpillToDisk` — large transaction spill

### 1.12 tls (54.1% → 70%)

**Тесты**:
- `TestTLSConfigLoad` — загрузка сертификатов
- `TestMTLSVerification` — mTLS verification
- `TestSelfSignedGeneration` — генерация самоподписанных

### 1.13 logging (54.1% → 70%)

**Тесты**:
- `TestAuditLogRotation` — ротация логов
- `TestAuditLogFormat` — формат записей
- `TestDDLLogging` — логирование DDL операций

---

## Приоритет 4: Minor improvements

### 1.14 protocol (no statements → add basic tests)

**Тесты**:
- `TestProtocolEncodeDecode` — encode/decode round-trip
- `TestProtocolErrorHandling` — malformed input

### 1.15 cmd/check-index (0% → 50%)

**Тесты**:
- `TestCheckIndex` — basic index check
- `TestCheckIndexInvalidPath` — error handling

### 1.16 cmd/vaultdb-backup (0% → 50%)

**Тесты**:
- `TestBackupMain` — backup + restore cycle
- `TestBackupInvalidArgs` — error handling

---

## Порядок выполнения

### Фаза 1 (неделя 1): Критичные пакеты
1. cmd/vaultdb-server — protocol helpers, handleConnection mock
2. config — env overrides, reload
3. parser — complex expressions, MERGE, window functions
4. lexer — edge cases

### Фаза 2 (неделя 2): Средние пакеты
5. wal — replay, analysis, truncation
6. httpserver — all HTTP handlers
7. index — GIN, GiST, composite
8. storage — vacuum, truncate, history

### Фаза 3 (неделя 3): Executor и storage
9. executor — GROUP BY, window, JOIN, RLS, CHECK
10. storage/heap — compaction, concurrent
11. txmanager — isolation, conflict, savepoint

### Фаза 4 (неделя 4): Minor + verification
12. tls, logging, protocol
13. cmd/check-index, cmd/vaultdb-backup
14. Финальная верификация покрытия

---

## Ожидаемый результат

| Пакет | Текущее | Цель |
|-------|---------|------|
| cmd/vaultdb-server | 9.2% | 40% |
| config | 41.6% | 70% |
| parser | 51.9% | 70% |
| lexer | 51.9% | 70% |
| wal | 52.4% | 70% |
| httpserver | 53.8% | 70% |
| index | 56.7% | 70% |
| storage | 60.1% | 70% |
| executor | 61.8% | 75% |
| storage/heap | 64.0% | 75% |
| txmanager | 62.7% | 75% |
| tls | 54.1% | 70% |
| logging | 54.1% | 70% |
| **Общее** | **57.9%** | **80%+** |

---

## Стратегия тестирования

### Unit tests (основной фокус)
- Каждая функция — минимум 1 test case
- Edge cases: nil, empty, boundary values
- Error paths: invalid input, missing data

### Integration tests (для executor)
- End-to-end query execution через Session
- Multi-statement transactions
- Concurrent access

### Table-driven tests
- Все парсерные тесты — table-driven
- Все evaluator тесты — table-driven
- Все storage операции — table-driven

### Mock patterns
- StorageEngine interface для executor tests
- net.Pipe для TCP handler tests
- httptest для HTTP handler tests
