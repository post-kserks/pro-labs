# VaultDB — План подготовки к продакшену

## Текущее состояние проекта

### Что есть (полная картина)

| Компонент | Статус | Файлы |
|-----------|--------|-------|
| SQL Parser | ✅ Полный | `parser/parser.go`, `parser/ast.go` (403 строки) |
| Lexer | ✅ Полный | `lexer/lexer.go` |
| Executor | ✅ Рабочий | `executor/` (17 файлов) |
| JSON Storage | ✅ Рабочий | `storage/file_storage.go` (2510 строк) |
| Page Storage | ⚠️ Частично | `storage/page_engine.go` (1437 строк) |
| Hash Index | ✅ Рабочий | `index/hash_index.go`, `index/manager.go` |
| WAL | ⚠️ Частично | `wal/wal.go` (440 строк) |
| Transactions | ⚠️ Базовый | `txmanager/manager.go` (194 строки) |
| Auth | ✅ HMAC-SHA256 | `auth/manager.go` |
| HTTP Server | ✅ Рабочий | `httpserver/server.go` |
| TCP Server | ✅ Рабочий | `cmd/vaultdb-server/main.go` |
| Web UI | ✅ React | `httpserver/web/` |
| Live Queries | ✅ SSE | `executor/broadcaster.go` |
| AI Integration | ✅ Embeddings | `ai/embedder.go` |
| Metrics | ✅ Prometheus | `metrics/collector.go` |

### Что отсутствует для продакшена

| Категория | Проблема | Критичность |
|-----------|----------|-------------|
| Crash Recovery | WAL recovery частично реализован | 🔴 CRIT |
| Connection Pooling | Нет пула соединений | 🔴 CRIT |
| Graceful Shutdown | Нет корректного завершения | 🔴 CRIT |
| Query Optimizer | Только Nested Loop Join | 🟡 HIGH |
| B-tree Index | Только Hash Index | 🟡 HIGH |
| Buffer Pool | Нет кэша страниц | 🟡 HIGH |
| Backup/Restore | Нет бэкапов | 🟡 HIGH |
| Replication | Нет репликации | 🟡 HIGH |
| CTEs | Нет WITH clause | 🟡 HIGH |
| UPSERT | Нет MERGE/INSERT ON CONFLICT | 🟡 HIGH |
| TLS | Нет шифрования для TCP | 🟡 HIGH |
| Audit Logging | Нет аудита | 🟡 HIGH |
| Connection Limits | Нет лимита соединений | 🟡 HIGH |
| Monitoring | Нет dashboard | 🟠 MED |
| Log Rotation | Нет ротации логов | 🟠 MED |
| Config Hot-reload | Нет горячей перезагрузки | 🟠 MED |
| Online Backup | Нет online бэкапов | 🟠 MED |
| Role-Based Access | Нет RBAC | 🟠 MED |
| Encryption at Rest | Нет шифрования данных | 🟠 MED |

---

## План реализации по фазам

### Фаза 1: Data Integrity & Reliability (недели 1-4)

**Цель:** Гарантия целостности данных при крашах и перезапусках.

#### 1.1 WAL Recovery (недели 1-2)
**Приоритет:** 🔴 CRIT
**Задачи:**
- Довести `RecoverFromWAL()` до рабочего состояния
- Реализовать полный ARIES (Analysis → Redo → Undo)
- Добавить checkpointing с flush dirty pages
- Тесты на crash recovery

**Файлы:**
- `storage/page_engine.go:100-192` — RecoveryFromWAL, redoPhase, undoPhase
- `wal/wal.go` — AnalyzeTransactions, Replay, ReplayTransaction

**Критерии:**
- Kill -9 во время INSERT → после restart 0 строк (неатомарность)
- Kill -9 после COMMIT → после restart строки на месте
- Kill -9 во время VACUUM → после restart данные целы

#### 1.2 Connection Pooling (недели 2-3)
**Приоритет:** 🔴 CRIT
**Задачи:**
- Реализовать `sync.Pool` для соединений
- Добавить `max_connections` параметр
- Connection timeout и idle timeout
- Метрики.active_connections

**Файлы:**
- `cmd/vaultdb-server/main.go:264-288` — accept loop
- `cmd/vaultdb-server/main.go:325-386` — handleConnection

**Критерии:**
- При 1000+ соединений — memory usage стабильно
- При превышении лимита — клиент получает ошибку
- Idle connections закрываются по таймауту

#### 1.3 Graceful Shutdown (неделя 3)
**Приоритет:** 🔴 CRIT
**Задачи:**
- Дождаться завершения всех активных транзакций
- Записать финальный checkpoint в WAL
- Закрыть все соединения
- Flush dirty pages

**Файлы:**
- `cmd/vaultdb-server/main.go:290-305` — shutdown logic

**Критерии:**
- SIGTERM → все транзакции завершаются
- Нет потери данных при graceful shutdown
- Нет orphan connections

#### 1.4 Backup/Restore (недели 3-4)
**Приоритет:** 🟡 HIGH
**Задачи:**
- `BACKUP DATABASE` команда — консистентный снимок
- `RESTORE DATABASE` команда — восстановление из бэкапа
- Point-in-time recovery через WAL

**Файлы:**
- Новый файл: `executor/commands_backup.go`
- Модификация: `storage/page_engine.go`

**Критерии:**
- BACKUP во время активных записей — консистентный снимок
- RESTORE восстанавливает точное состояние
- Point-in-time recovery работает

---

### Фаза 2: Performance (недели 5-10)

**Цель:** Оптимальная производительность для рабочих нагрузок.

#### 2.1 Query Optimizer (недели 5-7)
**Приоритет:** 🟡 HIGH
**Задачи:**
- Cost-based optimizer (CBO)
- Статистика по таблицам (row count, distinct values, null fraction)
- Hash Join для equi-joins
- Merge Join для отсортированных данных
- Index-only scan

**Файлы:**
- `executor/plan.go` — buildPlan (расширение)
- Новый файл: `executor/optimizer.go`
- Новый файл: `executor/statistics.go`

**Критерии:**
- JOIN на 10K строк — < 100ms
- SELECT с WHERE по индексу — < 10ms
- EXPLAIN показывает оптимальный план

#### 2.2 B-tree Index (недели 6-8)
**Приоритет:** 🟡 HIGH
**Задачи:**
- B-tree индекс для range queries
- Persistent B-tree (на диске)
- Поддержка уникальных индексов
- Composite indexes

**Файлы:**
- Новый файл: `index/btree.go`
- Модификация: `index/manager.go`

**Критерии:**
- Range query `WHERE id BETWEEN 100 AND 200` — < 10ms
- Уникальный индекс — нет дубликатов
- Composite index `CREATE INDEX idx ON t(a, b)` работает

#### 2.3 Buffer Pool (недели 7-9)
**Приоритет:** 🟡 HIGH
**Задачи:**
- LRU cache для страниц
- Dirty page tracking
- Async flush в background
- Page pre-fetching

**Файлы:**
- Новый файл: `storage/buffer_pool.go`
- Модификация: `storage/page_engine.go`

**Критерии:**
- Повторные чтения —命中缓存 (cache hit rate > 80%)
- Dirty pages flush в background — нет блокировки writes
- Memory usage предсказуемо

#### 2.4 Concurrent Writes (недели 8-10)
**Приоритет:** 🟡 HIGH
**Задачи:**
- Multi-writer с lock-free algorithms
- Page-level locking вместо table-level
- Lock escalation
- Deadlock detection

**Файлы:**
- `storage/page_engine.go` — замена `e.mu` на page-level locks
- `txmanager/manager.go` — deadlock detection

**Критерии:**
- 100 параллельных INSERT — все применяются
- Нет deadlock'ов
- Throughput > 10K inserts/sec

---

### Фаза 3: SQL Completeness (недели 11-16)

**Цель:** Полная совместимость со стандартным SQL.

#### 3.1 CTEs (недели 11-12)
**Приоритет:** 🟡 HIGH
**Задачи:**
- `WITH` clause для CTEs
- Recursive CTEs
- Materialized CTEs

**Файлы:**
- `parser/ast.go` — CTEStatement
- `parser/parser.go` — WITH clause
- `executor/commands_select.go` — CTE execution

**Критерии:**
- `WITH cte AS (SELECT ...) SELECT * FROM cte` работает
- Recursive CTE `WITH RECURSIVE` работает

#### 3.2 UPSERT (недели 12-13)
**Приоритет:** 🟡 HIGH
**Задачи:**
- `INSERT ... ON CONFLICT DO UPDATE`
- `INSERT ... ON CONFLICT DO NOTHING`
- Conflict detection по unique index

**Файлы:**
- `parser/ast.go` — OnConflict clause
- `executor/commands_dml.go` — UPSERT logic

**Критерии:**
- `INSERT INTO t VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET name = 'b'` работает
- Conflict detection корректен

#### 3.3 Additional SQL Features (недели 13-16)
**Приоритет:** 🟠 MED
**Задачи:**
- String functions: SUBSTRING, TRIM, LENGTH, REPLACE, POSITION
- Date functions: EXTRACT, DATE_TRUNC, AGE
- JSON functions: JSON_EXTRACT, JSON_ARRAY_LENGTH
- EXISTS/NOT EXISTS подзапросы
- LATERAL joins
- GROUPING SETS

**Файлы:**
- `executor/eval.go` — evalFunctionCall (расширение)

**Критерии:**
- `SELECT SUBSTRING(name, 1, 3) FROM t` работает
- `SELECT * FROM t WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id)` работает

---

### Фаза 4: Security & Compliance (недели 17-20)

**Цель:** Соответствие стандартам безопасности.

#### 4.1 TLS (недели 17-18)
**Приоритет:** 🟡 HIGH
**Задачи:**
- TLS для TCP соединений
- TLS для HTTP соединений
- Certificate management
- TLS termination proxy support

**Файлы:**
- `cmd/vaultdb-server/main.go` — TLS config
- `httpserver/server.go` — HTTPS

**Критерии:**
- `vaultdb --tls-cert=cert.pem --tls-key=key.pem` — encrypted connections
- curl с https работает

#### 4.2 Audit Logging (недели 18-19)
**Приоритет:** 🟡 HIGH
**Задачи:**
- Логирование всех DDL операций
- Логирование всех DML операций
- Логирование auth событий
- Structured audit log format

**Файлы:**
- Новый файл: `audit/logger.go`
- Модификация: `executor/executor.go`

**Критерии:**
- `CREATE TABLE` → audit log запись
- Failed login → audit log запись
- Audit log в JSON формате

#### 4.3 RBAC (недели 19-20)
**Приоритет:** 🟠 MED
**Задачи:**
- Roles и privileges
- `GRANT` / `REVOKE` команды
- Row-level security (RLS)

**Файлы:**
- Новый файл: `auth/rbac.go`
- `parser/ast.go` — GRANT/REVOKE statements

**Критерии:**
- `GRANT SELECT ON t TO user1` — user1 может читать
- `REVOKE INSERT ON t FROM user1` — user1 не может писать

---

### Фаза 5: Operations & Monitoring (недели 21-24)

**Цель:** Удобство эксплуатации и мониторинга.

#### 5.1 Monitoring Dashboard (недели 21-22)
**Приоритет:** 🟠 MED
**Задачи:**
- Real-time metrics dashboard
- Query performance analytics
- Connection pool stats
- Storage utilization

**Файлы:**
- `httpserver/web/` — dashboard components
- `metrics/collector.go` — additional metrics

**Критерии:**
- Dashboard показывает active connections
- Query latency histogram доступен

#### 5.2 Log Rotation (неделя 22)
**Приоритет:** 🟠 MED
**Задачи:**
- Automatic log rotation
- Log compression
- Log retention policy
- Structured logging

**Файлы:**
- Новый файл: `logging/rotation.go`

**Критерии:**
- Logs ротируются при достижении max size
- Old logs удаляются по retention policy

#### 5.3 Config Hot-reload (недели 23-24)
**Приоритет:** 🟠 MED
**Задачи:**
- SIGHUP для перезагрузки конфига
- Hot-reload без перезапуска
- Validation при reload

**Файлы:**
- `config/config.go` — Reload method
- `cmd/vaultdb-server/main.go` — SIGHUP handler

**Критерии:**
- SIGHUP → конфиг перезагружается без downtime
- Invalid config → error logged, old config used

---

## Приоритеты реализации

### Критические (нужны ASAP)
1. WAL Recovery (Фаза 1.1)
2. Connection Pooling (Фаза 1.2)
3. Graceful Shutdown (Фаза 1.3)

### Высокие (нужны для продакшена)
4. Query Optimizer (Фаза 2.1)
5. B-tree Index (Фаза 2.2)
6. Buffer Pool (Фаза 2.3)
7. Concurrent Writes (Фаза 2.4)
8. CTEs (Фаза 3.1)
9. UPSERT (Фаза 3.2)
10. TLS (Фаза 4.1)
11. Audit Logging (Фаза 4.2)
12. Backup/Restore (Фаза 1.4)

### Средние (удобство эксплуатации)
13. Monitoring Dashboard (Фаза 5.1)
14. Log Rotation (Фаза 5.2)
15. Config Hot-reload (Фаза 5.3)
16. RBAC (Фаза 4.3)
17. Additional SQL Features (Фаза 3.3)

---

## Оценка времени

| Фаза | Недели | Описание |
|------|--------|----------|
| 1 | 1-4 | Data Integrity & Reliability |
| 2 | 5-10 | Performance |
| 3 | 11-16 | SQL Completeness |
| 4 | 17-20 | Security & Compliance |
| 5 | 21-24 | Operations & Monitoring |
| **Итого** | **24 недели (6 месяцев)** | **Полная готовность к продакшену** |

---

## Рекомендации

### Начать с:
1. **WAL Recovery** — без этого данные ненадёжны
2. **Connection Pooling** — без этого нет scalability
3. **Graceful Shutdown** — без этого есть риск потери данных

### Параллельно:
1. **Query Optimizer** — начать с CBO, добавлять планы постепенно
2. **B-tree Index** — критичен для range queries
3. **Buffer Pool** — критичен для производительности

### Позже:
1. **SQL Completeness** — CTEs, UPSERT, string functions
2. **Security** — TLS, audit, RBAC
3. **Operations** — monitoring, logging, hot-reload

---

## Тестирование

### Unit Tests
- Каждый компонент — 80%+ coverage
- Тесты на edge cases
- Тесты на error handling

### Integration Tests
- End-to-end сценарии
- Crash recovery тесты
- Concurrent access тесты

### Performance Tests
- Benchmark для критических путей
- Load testing (1000+ concurrent connections)
- Memory profiling

### Security Tests
- SQL injection тесты
- Auth bypass тесты
- TLS verification
