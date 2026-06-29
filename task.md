# VaultDB — Production Readiness Assessment

**Дата**: 2026-06-28 (обновлено: 2026-06-29)
**Оценка**: ~90% production-ready

---

## 1. Ядро СУБД — 90%

### Работает
- SELECT, INSERT, UPDATE, DELETE с WHERE/ORDER BY/LIMIT/OFFSET
- JOIN (INNER, LEFT, RIGHT, FULL, CROSS)
- CTE (WITH) — non-recursive и recursive (UNION ALL)
- Window Functions (ROW_NUMBER, RANK, LAG, LEAD, SUM OVER, etc.)
- JSONB операторы (->, ->>, @>, <@, ?, ||)
- UPSERT (ON CONFLICT DO NOTHING/UPDATE)
- MERGE (WHEN MATCHED/NOT MATCHED)
- Временной旅行 (AS OF TIMESTAMP, HISTORY)
- LIKE и full-text search через GIN
- Математика агрегатов (AVG(x) + 1, MAX(x) - MIN(y))
- Foreign keys с referential integrity и ON DELETE CASCADE
- AUTO_INCREMENT для PRIMARY KEY колонок
- CHECK constraints (AND/OR/NOT/IN/BETWEEN)

### Не работает
- Stored procedures — только CREATE/DROP, нет EXECUTE
- RETURNING clause — ограничена

---

## 2. Надёжность — 85%

### Плюсы
- WAL + checkpoint (ARIES protocol)
- Crash recovery с redo/undo фазами
- Buffer Pool с LRU и per-table locking
- Buffer Pool write-back с dirty page tracking
- Deadlock guard для autocommit операций
- Spill-to-disk для транзакций при нехватке памяти
- Retry с exponential backoff для disk I/O ошибок

### Минусы
- Нет replication
- Нет alerting интеграции

---

## 3. Безопасность — 90%

### Плюсы
- HMAC-SHA256 токены с серверным секретом
- mTLS поддержка
- Rate limiting (token bucket per-IP)
- Sanitize error messages (whitelist подход)
- CORS настройки
- Parameterized queries через HTTP ($1, $2, ...)
- Audit log для DDL операций

### Минусы
- Нет row-level security (только заготовка)

---

## 4. API — 90%

### Плюсы
- HTTP REST API + custom TCP protocol
- Health checks (/health, /ready)
- Prometheus метрики (p50/p95/p99)
- WebSocket для live queries
- Batch queries (/api/batch)
- Streaming results (/api/query/stream — SSE)
- Транзакции через HTTP (/api/transaction)

### Минусы
- Нет prepared statements через HTTP (параметризация через $N)

---

## 5. DevOps — 90%

### Плюсы
- Docker + docker-compose
- Healthcheck в Dockerfile
- Graceful shutdown с таймаутом
- Конфигурация через YAML + environment variables
- Auto-vacuum
- Backup/restore утилита (gzip tar archive)

### Минусы
- Нет replication

---

## 6. Клиенты — 60%

### Плюсы
- C++ libvaultdb (shared library)
- Interactive shell с подсветкой синтаксиса
- TUI (panel-based)
- Interactive SQL Lab (веб-интерфейс)

### Минусы
- Нет ORM интеграций

---

## Закрытые задачи (2026-06-29)

| # | Задача | Статус |
|---|--------|--------|
| 1 | Recursive CTE | Закрыто — архитектурный баг исправлен, anchor/recursive split |
| 2 | Foreign keys | Закрыто — referential integrity + ON DELETE CASCADE |
| 3 | Backup/restore | Закрыто — gzip tar утилита |
| 4 | Parameterized queries | Закрыто — HTTP API поддерживает $1, $2, ... |
| 5 | Sequences/auto-increment | Закрыто — AUTO_INCREMENT для PK |
| 6 | Streaming results | Закрыто — SSE endpoint /api/query/stream |
| 7 | Buffer pool write-back | Закрыто — dirty page tracking |
| 8 | Disk error retry | Закрыто — exponential backoff |
| 9 | DDL audit logging | Закрыто — LogDDL для всех DDL команд |
| 10 | HTTP batching | Закрыто — /api/batch endpoint |
| 11 | HTTP transactions | Закрыто — /api/transaction endpoint |

---

## Оставшиеся задачи

| Приоритет | Задача | Влияние |
|-----------|--------|---------|
| ~~1~~ | ~~Row-level security~~ | ~~Безопасность~~ |
| 2 | Stored procedures EXECUTE | Функциональность |
| 3 | RETURNING clause расширение | Удобство |
| 4 | Replication | Надёжность |
| 5 | ORM интеграции | Удобство |

## Закрытые задачи (2026-06-29)

| # | Задача | Статус |
|---|--------|--------|
| 12 | **Row-level security** | Закрыто — USING-фильтр для SELECT/UPDATE/DELETE, тесты |

(приоритет 1 перемещён в закрытые)
