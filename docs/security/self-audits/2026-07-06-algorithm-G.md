# Security Self-Audit Report — Algorithm G

Date: 2026-07-06
Executor: MiMoCode (automated analysis)
Algorithm: Denial of Service / Resource Exhaustion Review
VaultDB Version: latest (HEAD)

## Step-by-Step Results

| Step | Status | Comment |
|---|---|---|
| 1 | Passed | Query timeout implemented via context.WithTimeout |
| 2 | Passed | Max request size limited via http.MaxBytesReader |
| 3 | Passed | Rate limiting via token bucket |
| 4 | Partial | COPY FROM does not support STDIN, file input has no row limit |
| 5 | Partial | Parser recursion limit missing, trigger depth limited to 3 |
| 6 | Passed | Connection limits configurable |

## Findings

### Finding 1 — Query Timeout: context.WithTimeout implemented (Pass)

**Description:** Each query is executed with a timeout via `context.WithTimeout` (`executor.go:252-255`). Timeout is configured via `QueryTimeoutSec` (default: 30 seconds).

**Evidence:**
- `server/internal/executor/executor.go:251-255` — `context.WithTimeout(queryCtx, queryTimeout)`
- `server/internal/config/config.go:93` — `DefaultQueryTimeoutSec = 30`

**Verdict:** CORRECT — timeout is applied to each query.

---

### Finding 2 — Max Request Size: http.MaxBytesReader (Pass)

**Description:** HTTP requests are limited by `MaxRequestSizeBytes` (default: 64MB). `http.MaxBytesReader` is used before JSON decoding.

**Evidence:**
- `server/internal/httpserver/server_handlers.go:75` — `r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))`
- `server/internal/config/config.go:89` — `DefaultMaxRequestSize = 64 * 1024 * 1024`

**Verdict:** CORRECT — oversized body is rejected with 413.

---

### Finding 3 — Rate Limiting: Token Bucket (Pass)

**Description:** A token bucket rate limiter is implemented (`ratelimit.go`). Key is client IP. Default: 100 RPS, burst 200. On excess — 429 Too Many Requests.

**Evidence:**
- `server/internal/httpserver/ratelimit.go:33-87` — RateLimiter implementation
- `server/internal/config/config.go:102-103` — `DefaultRateLimitRPS = 100`, `DefaultRateLimitBurst = 200`
- `server/internal/httpserver/ratelimit.go:62-63` — maxKeys = 100000 (prevents memory exhaustion)

**Additional:** Rate limiter has LRU eviction when exceeding 100k keys (`ratelimit.go:62-64`), preventing memory exhaustion through spoofed IPs.

**Verdict:** CORRECT — multiple protection layers.

---

### Finding 4 — HTTP Server Timeouts (Pass)

**Description:** HTTP server is configured with timeouts:
- ReadHeaderTimeout: 5s
- ReadTimeout: 15s
- WriteTimeout: 60s
- IdleTimeout: 120s
- MaxHeaderBytes: 1MB

**Evidence:**
- `server/internal/httpserver/server.go:213-221`

**Verdict:** CORRECT — slowloris-style attacks mitigated.

---

### Finding 5 — Connection Limits: Configurable (Pass)

**Description:** `MaxConnections` is configurable (default: 1000). TCP keepalive and idle timeout are also configurable.

**Evidence:**
- `server/internal/config/config.go:94` — `DefaultMaxConnections = 1000`
- `server/internal/config/config.go:97-98` — TCP keepalive/idle timeout

---

### Finding 6 — COPY FROM: no row count limit (Medium)

**Description:** `COPY FROM` loads all rows from a file without limiting the count. A massive CSV file can exhaust memory.

**Evidence:**
- `server/internal/executor/commands_copy.go:160-202` — readCopyData() loads all rows into `[]storage.Row`

**Reproduction:** Create a CSV file with 10M rows and execute `COPY FROM`.

**Recommendation:** Add a `max_copy_rows` parameter or a CSV file size limit.

**Fix Status:** Open

---

### Finding 7 — Parser: no recursion depth limit (Medium)

**Description:** The parser has no explicit limit on the depth of nested expressions (subqueries, CTEs, nested parentheses). Deeply nested queries can cause a stack overflow.

**Evidence:**
- `server/internal/parser/` — no depth limit in the parser
- Trigger depth is limited to 3 (`commands_ddl_misc.go:516`), but the parser is not

**Reproduction:** Create a query with 1000+ nesting levels of subqueries.

**Recommendation:** Add a `maxParseDepth` parameter to the parser.

**Fix Status:** Open

---

### Finding 8 — Prepared Statements: limit of 1000 (Pass)

**Description:** Maximum number of prepared statements per session is 1000 (default). On excess — error.

**Evidence:**
- `server/internal/executor/session.go:189` — `len(s.PreparedStatements) >= s.maxPreparedStmts`

---

### Finding 9 — Live Query Subscriptions: limit of 1000 (Pass)

**Description:** Maximum number of active live query subscriptions is 1000 (default).

**Evidence:**
- `server/internal/httpserver/server_middleware.go:28` — `DefaultMaxLiveQuerySubscriptions = 1000`

---

### Finding 10 — Max Rows: limit of 1M rows (Pass)

**Description:** SELECT result is limited to 1M rows (default). Protection against unbounded result sets.

**Evidence:**
- `server/internal/config/config.go:96` — `DefaultMaxRows = 1000000`

---

## Overall Verdict

**Pass with findings**

VaultDB has multi-layered DoS protection:
- Query timeout, max request size, rate limiting
- HTTP server timeouts, connection limits
- Prepared statement limits, live query limits
- Max rows limit

Findings — missing row limit for COPY and parser recursion limit. Both are Medium severity.

## Recommendations

1. **[Medium]** Add `max_copy_rows` parameter for COPY FROM
2. **[Medium]** Add `maxParseDepth` to the parser
3. **[Low]** Add metrics for monitoring rate limiting and connection exhaustion
