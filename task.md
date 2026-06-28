# VaultDB — Production Readiness Assessment

**Дата**: 2026-06-28
**Оценка**: ~70% production-ready

---

## 1. Ядро СУБД — 70%

### Работает
- SELECT, INSERT, UPDATE, DELETE с WHERE/ORDER BY/LIMIT/OFFSET
- JOIN (INNER, LEFT, RIGHT, FULL, CROSS)
- CTE (WITH) — non-recursive
- Window Functions (ROW_NUMBER, RANK, LAG, LEAD, SUM OVER, etc.)
- JSONB операторы (->, ->>, @>, <@, ?, ||)
- UPSERT (ON CONFLICT DO NOTHING/UPDATE)
- MERGE (WHEN MATCHED/NOT MATCHED)
- Временной旅行 (AS OF TIMESTAMP, HISTORY)
- LIKE и full-text search через GIN
- Математика агрегатов (AVG(x) + 1, MAX(x) - MIN(y))

### Не работает
- Recursive CTE — архитектурный баг: CTE scope не пробрасывается через CommandFactory
- Foreign keys — нет проверки ссылочной целостности
- Sequences / auto-increment
- Stored procedures — только CREATE/DROP, нет EXECUTE
- CHECK constraints — частичная реализация
- RETURNING clause — ограничена

---

## 2. Надёжность — 65%

### Плюсы
- WAL + checkpoint (ARIES protocol)
- Crash recovery с redo/undo фазами
- Buffer Pool с LRU и per-table locking
- Deadlock guard для autocommit операций
- Spill-to-disk для транзакций при нехватке памяти

### Минусы
- Buffer pool не делает write-back — все записи синхронные
- Нет retry логики для disk errors
- Нет валидации данных на уровне storage (CHECK constraints)

---

## 3. Безопасность — 80%

### Плюсы
- HMAC-SHA256 токены с серверным секретом
- mTLS поддержка
- Rate limiting (token bucket per-IP)
- Sanitize error messages (whitelist подход)
- CORS настройки

### Минусы
- Нет parameterized queries (парсер строгий, но SQL injection теоретически возможен)
- Нет audit log для DDL операций
- Нет row-level security (только заготовка)

---

## 4. API — 75%

### Плюсы
- HTTP REST API + custom TCP protocol
- Health checks (/health, /ready)
- Prometheus метрики (p50/p95/p99)
- WebSocket для live queries

### Минусы
- Нет batching для HTTP
- Нет prepared statements через HTTP
- Нет streaming results
- Транзакции только через TCP (не HTTP)

---

## 5. DevOps — 85%

### Плюсы
- Docker + docker-compose
- Healthcheck в Dockerfile
- Graceful shutdown с таймаутом
- Конфигурация через YAML + environment variables
- Auto-vacuum

### Минусы
- Нет backup/restore утилиты
- Нет replication
- Нет alerting интеграции

---

## 6. Клиенты — 60%

### Плюсы
- C++ libvaultdb (shared library)
- Interactive shell с подсветкой синтаксиса
- TUI (panel-based)
- Interactive SQL Lab (веб-интерфейс)

### Минусы
- Нет Python/Node/Go клиентов
- Нет ORM интеграций
- Нет driver для popular frameworks

---

## Приоритеты для доработки

| Приоритет | Задача | Влияние |
|-----------|--------|---------|
| 1 | Recursive CTE | Функциональность |
| 2 | Foreign keys | Целостность данных |
| 3 | Backup/restore | Operability |
| 4 | Parameterized queries | Безопасность |
| 5 | Sequences/auto-increment | Удобство |
| 6 | Streaming results | Производительность |
| 7 | Python/Node клиенты | Экосистема |
