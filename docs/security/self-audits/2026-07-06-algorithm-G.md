# Security Self-Audit Report — Algorithm G

Дата: 2026-07-06
Исполнитель: MiMoCode (автоматический анализ)
Алерготм: Denial of Service / Resource Exhaustion Review
Версия VaultDB: latest (HEAD)

## Результаты по шагам

| Шаг | Статус | Комментарий |
|---|---|---|
| 1 | Пройден | Query timeout реализован через context.WithTimeout |
| 2 | Пройден | Max request size лимит через http.MaxBytesReader |
| 3 | Пройден | Rate limiting через token bucket |
| 4 | Частично | COPY FROM не поддерживает STDIN, файловый ввод без row limit |
| 5 | Частично | Parser recursion limit отсутствует, trigger depth limited до 3 |
| 6 | Пройден | Connection limits через конфигурацию |

## Findings

### Finding 1 — Query Timeout: context.WithTimeout реализован (Pass)

**Описание:** Каждый запрос выполняется с таймаутом через `context.WithTimeout` (`executor.go:252-255`). Таймаут конфигурируется через `QueryTimeoutSec` (default: 30 сек).

**Доказательства:**
- `server/internal/executor/executor.go:251-255` — `context.WithTimeout(queryCtx, queryTimeout)`
- `server/internal/config/config.go:93` — `DefaultQueryTimeoutSec = 30`

**Вердикт:** CORRECT — таймаут применяется к каждому запросу.

---

### Finding 2 — Max Request Size: http.MaxBytesReader (Pass)

**Описание:** HTTP-запросы ограничены `MaxRequestSizeBytes` (default: 64MB). Используется `http.MaxBytesReader` перед декодированием JSON.

**Доказательства:**
- `server/internal/httpserver/server_handlers.go:75` — `r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))`
- `server/internal/config/config.go:89` — `DefaultMaxRequestSize = 64 * 1024 * 1024`

**Вердикт:** CORRECT — oversized body отклоняется с 413.

---

### Finding 3 — Rate Limiting: Token Bucket (Pass)

**Описание:** Реализован token bucket rate limiter (`ratelimit.go`). Ключ — клиентский IP. По умолчанию: 100 RPS, burst 200. При превышении — 429 Too Many Requests.

**Доказательства:**
- `server/internal/httpserver/ratelimit.go:33-87` — RateLimiter implementation
- `server/internal/config/config.go:102-103` — `DefaultRateLimitRPS = 100`, `DefaultRateLimitBurst = 200`
- `server/internal/httpserver/ratelimit.go:62-63` — maxKeys = 100000 (prevents memory exhaustion)

**Дополнительно:** Rate limiter имеет LRU eviction при превышении 100k ключей (`ratelimit.go:62-64`), предотвращая memory exhaustion через spoofed IPs.

**Вердикт:** CORRECT — multiple protection layers.

---

### Finding 4 — HTTP Server Timeouts (Pass)

**Описание:** HTTP-сервер настроен с timeouts:
- ReadHeaderTimeout: 5s
- ReadTimeout: 15s
- WriteTimeout: 60s
- IdleTimeout: 120s
- MaxHeaderBytes: 1MB

**Доказательства:**
- `server/internal/httpserver/server.go:213-221`

**Вердикт:** CORRECT — slowloris-style attacks mitigated.

---

### Finding 5 — Connection Limits: Configurable (Pass)

**Описание:** `MaxConnections` конфигурируется (default: 1000). TCP keepalive и idle timeout также настраиваются.

**Доказательства:**
- `server/internal/config/config.go:94` — `DefaultMaxConnections = 1000`
- `server/internal/config/config.go:97-98` — TCP keepalive/idle timeout

---

### Finding 6 — COPY FROM: отсутствует лимит на количество строк (Medium)

**Описание:** `COPY FROM` загружает все строки из файла без ограничения количества. Огромный файл CSV может исчерпать память.

**Доказательства:**
- `server/internal/executor/commands_copy.go:160-202` — readCopyData() загружает все строки в `[]storage.Row`

**Воспроизведение:** Создать CSV-файл с 10M строк и выполнить `COPY FROM`.

**Рекомендация:** Добавить `max_copy_rows` параметр или лимит на размер CSV-файла.

**Статус исправления:** Open

---

### Finding 7 — Parser: отсутствует лимит на глубину рекурсии (Medium)

**Описание:** Парсер не имеет явного лимита на глубину вложенных выражений (подзапросы, CTE, вложенные括ировки). Глубоко вложенные запросы могут вызвать stack overflow.

**Доказательства:**
- `server/internal/parser/` — отсутствует depth limit в парсере
- Trigger depth ограничен до 3 (`commands_ddl_misc.go:516`), но парсер — нет

**Воспроизведение:** Создать запрос с 1000+ уровней вложенности подзапросов.

**Рекомендация:** Добавить `maxParseDepth` параметр в парсер.

**Статус исправления:** Open

---

### Finding 8 — Prepared Statements: лимит 1000 (Pass)

**Описание:** Максимальное количество prepared statements на сессию — 1000 (default). При превышении — ошибка.

**Доказательства:**
- `server/internal/executor/session.go:189` — `len(s.PreparedStatements) >= s.maxPreparedStmts`

---

### Finding 9 — Live Query Subscriptions: лимит 1000 (Pass)

**Описание:** Максимальное количество активных live query подписок — 1000 (default).

**Доказательства:**
- `server/internal/httpserver/server_middleware.go:28` — `DefaultMaxLiveQuerySubscriptions = 1000`

---

### Finding 10 — Max Rows: лимит 1M строк (Pass)

**Описание:** SELECT результат ограничен 1M строк (default). Защита от unbounded result sets.

**Доказательства:**
- `server/internal/config/config.go:96` — `DefaultMaxRows = 1000000`

---

## Общий вердикт

**Pass with findings**

VaultDB имеет многоуровневую защиту от DoS:
- Query timeout, max request size, rate limiting
- HTTP server timeouts, connection limits
- Prepared statement limits, live query limits
- Max rows limit

Находки — отсутствие row limit для COPY и parser recursion limit. Обе — Medium severity.

## Рекомендации

1. **[Medium]** Добавить `max_copy_rows` параметр для COPY FROM
2. **[Medium]** Добавить `maxParseDepth` в парсер
3. **[Low]** Добавить метрики для мониторинга rate limiting и connection exhaustion
