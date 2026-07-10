# VaultDB — Full Code Review

**Date**: 2026-06-28
**Method**: Manual static analysis of all server code (Go), excludes `client/` and `audit.md`

---

## Overall Assessment

The project is impressive: a full-featured SQL engine with WAL, MVCC, optimizer, indexes (BTree/GIN/GiST/Hash), buffer pool, page storage, and transactional overlay. The architecture is modular, layers are cleanly separated. Tests >50, good coverage, all tests pass.

Below are all issues found, grouped by criticality.

---

## Critical (Must Fix)

### 1. Lock ordering WAL ↔ PageEngine — real deadlock vector

**File**: `server/internal/storage/page_engine.go:541-575`
**File**: `server/internal/wal/wal.go:498-525`

**Problem**: `doCheckpoint()` acquires `e.mu.Lock()`, then calls `e.wal.Append()` (acquires `w.mu`). Recovery callbacks (`redoPhase`/`undoPhase`) acquire `e.mu` while `w.mu` is already held — **reverse order**.

This is NOT theoretical: when the server starts, `RecoverFromWAL()` → `redoPhase()` → `redoInsert()` acquires `e.mu.Lock()` **after** `wal.Replay()` has acquired `w.mu`. And `doCheckpoint()` does `e.mu.Lock()` → `w.mu` (via `wal.Append`). If recovery and checkpoint run simultaneously — deadlock.

**Status in audit.md**: ✅ described as "potential". I confirm — this is a **real** deadlock vector under certain conditions.

**Recommendation**: Add a documented invariant: lock order is always WAL → PageEngine. Or restructure checkpoint so that `w.mu` is not held when calling `e.mu`-protected methods.

---

### 2. `context.Background()` in executor — queries not cancelled on shutdown

**File**: `server/internal/executor/executor.go:205-209`

```go
queryCtx := context.Background()
if queryTimeout > 0 {
    queryCtx, cancel = context.WithTimeout(context.Background(), queryTimeout)
}
```

**Problem**: The query context is NOT bound to the server context. On `SIGTERM`/`SIGKILL`, long queries (large SELECT, heavy DDL) continue executing without interruption. The server waits `ShutdownTimeoutSec` (30s default), but the query may run longer.

**Status in audit.md**: ✅ described. Confirmed — this is high-priority for production.

**Recommendation**: Pass `context.Context` from `handleConnection` through `Session.Execute` → `Executor.Run`.

---

### 3. WAL silent error swallowing — data loss on corrupt WAL

**File**: `server/internal/wal/wal.go:413-425`, `466-473`, `508-514`

```go
if err == io.EOF || err == io.ErrUnexpectedEOF {
    break
}
break  // other errors also silently break
```

**Problem**: In `scanAndTruncate`, `AnalyzeTransactions`, `Replay`, all errors except `io.EOF/ErrUnexpectedEOF` are handled as `break` **without logging**. A corrupted record in the middle of the WAL leads to silent loss of ALL subsequent records.

**Status in audit.md**: ✅ described. Agreed — this is a serious data loss risk.

**Recommendation**: Add `slog.Warn` with offset and error type. Better yet — return an error and abort recovery with an explicit message.

---

## Important (Should Fix)

### 4. ConnectionPool — not a pool, but a connection counter

**File**: `server/internal/pool/pool.go:156-173`
**File**: `server/cmd/vaultdb-server/main.go:486-534`

**Problem**: `AcquireConn()` does not reuse idle connections. Each incoming connection creates a new wrapper. The pool does not return connections — it only limits the maximum.

**Status in audit.md**: ✅ described. Agreed — for production, a real pool with reuse is needed, or rename to `ConnectionTracker`.

**Recommendation**: Rename to `ConnectionTracker` or implement a real pool (accept loop → pool → handler goroutine).

---

### 5. `isHealthy` — data race in pool

**File**: `server/internal/pool/pool.go:228-243`

```go
func (p *Pool) isHealthy(conn *Connection) bool {
    _ = conn.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
    _, err := conn.conn.Read(make([]byte, 0))
```

**Problem**: The method calls `conn.conn.SetReadDeadline` and `conn.conn.Read` **without locking `conn.mu`**, while `Read`/`Write` in `Connection` hold the same mutex. Race condition on concurrent read/write.

**Status in audit.md**: ✅ described as data race. Confirmed.

**Recommendation**: Acquire `conn.mu.Lock()` in `isHealthy`, or use `conn.conn` directly (bypass the wrapper).

---

### 6. `getTableForRead` / `getTableForWrite` — code duplication

**File**: `server/internal/storage/page_engine.go:797-893`

**Problem**: ~45 lines copied with the only difference being `t.mu.RLock()` vs `t.mu.Lock()`. DRY violation.

**Status in audit.md**: ✅ described. Agreed.

**Recommendation**: Extract into a common method `getTable(db, table, write bool)`.

---

### 7. `encodeColumnValue` fallback — silent type loss

**File**: `server/internal/storage/binary_encoding.go:172-180`

```go
default:
    s := fmt.Sprintf("%v", v)
    // encode as string tag 's'
```

**Problem**: For unknown Go types, `fmt.Sprintf("%v")` is used with tag `'s'`. On deserialization, the result is a string, not the original type. Silent data degradation.

**Status in audit.md**: ✅ described. Agreed.

**Recommendation**: Return an error for unknown types, or add a `'?'` tag with raw JSON.

---

### 8. reflect-based command registry — fragility

**File**: `server/internal/executor/executor.go:23-96`

**Problem**: When adding a new Statement, you must not forget to register the factory in `init()`. If forgotten — `CommandFactory` returns `"unknown statement type"` at runtime.

**Status in audit.md**: ✅ described. Agreed — this is a latent bug source.

**Recommendation**: Use type switch or registration via interface marker.

---

### 9. `validateConfig` duplicates `Default()` — non-obvious fallback

**File**: `server/internal/config/config.go:153-259`

**Problem**: `validateConfig` re-assigns default values that were already set in `Default()`. This is a fallback for when YAML contains explicit `0`/`""`/`false` and `Unmarshal` resets the default. But the code is non-obvious.

**Status in audit.md**: ✅ described as a minor note. I consider this medium — in a production config, this could lead to unexpected behavior.

---

## New Issues (Not in audit.md)

### 10. `isHealthy` in pool — incorrect semantics for idle connections

**File**: `server/internal/pool/pool.go:228-243`

```go
func (p *Pool) isHealthy(conn *Connection) bool {
    _, err := conn.conn.Read(make([]byte, 0))
    if err == io.EOF {
        return true
    }
    return false
}
```

**Problem**: `io.EOF` means the remote side closed the connection. For TCP this is a **dead** connection, but `isHealthy` considers it healthy. On reuse, `io.EOF` will be returned on read.

**Recommendation**: Remove `io.EOF` from "healthy" conditions.

---

### 11. `sanitizeErrorMessage` in protocol.go — incomplete protection

**File**: `server/cmd/vaultdb-server/protocol.go:37-53`

**Problem**: The filter identifies internal errors by patterns (`/go/src/`, `.go:`, `heapfile`), but does not cover:
- Stack traces (contain `.go:` files)
- Errors with ports (`:5432`, `:8080`)
- Data paths (`data/pagedb/`)

Some storage errors may leak to the client.

**Recommendation**: Use a whitelist approach (always return generic "internal error") instead of a blacklist.

---

### 12. `VaultDB.Open()` does not initialize WAL

**File**: `server/vaultdb.go:25-38`

```go
func Open(dataDir string) (*VaultDB, error) {
    s, err := storage.NewPageStorageEngine(dataDir, nil, txm)
```

**Problem**: The Embedded API passes `nil` for WAL. This means `PageStorageEngine` operates **without WAL** — no crash recovery, no durability. Embedded API users may not realize this.

**Recommendation**: Either automatically create a WAL, or explicitly document the absence of durability.

---

### 13. Missing `go.sum` dependency pinning

**File**: `server/go.mod`

```
go 1.23
require gopkg.in/yaml.v3 v3.0.1
```

**Problem**: The only dependency is `yaml.v3`. But CI uses `staticcheck@v0.5.1`, `gosec@v2.21.4`, `govulncheck@v1.1.3` — external tools without version pinning. If their behavior changes, CI may break.

**Recommendation**: Use `go tool` directive (Go 1.24+) or pin versions in Makefile.

---

### 14. `handleConnection` — panic recovery without stack trace

**File**: `server/cmd/vaultdb-server/main.go:177-184`

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

**Problem**: Panic recovery does not log the stack trace. In production, diagnostic information from a panic will be lost.

**Recommendation**: Add `debug.Stack()` to the log.

---

### 15. `RateLimiter` hardcoded values

**File**: `server/cmd/vaultdb-server/main.go:104`

```go
rateLimiter := httpserver.NewRateLimiter(100, 200) // 100 req/s, burst 200
```

**Problem**: Rate limiter is hardcoded in code, not configurable via `vaultdb.yaml`. In production, different deployments may need different limits.

**Recommendation**: Add `server.rate_limit_rps` and `server.rate_limit_burst` to config.

---

### 16. `ConnectionRateLimiter` duplicates `httpserver.RateLimiter`

**File**: `server/cmd/vaultdb-server/main.go:140-174`
**File**: `server/internal/httpserver/ratelimit.go`

**Problem**: Two different rate limiters: one for TCP (`ConnectionRateLimiter`), another for HTTP (`httpserver.RateLimiter`). `ConnectionRateLimiter` code was written from scratch and does not reuse the existing one.

**Recommendation**: Reuse `httpserver.RateLimiter` or extract a common token bucket into `internal/ratelimit`.

---

### 17. Missing `VERSION` consistency check

**File**: `VERSION`
**File**: `Makefile:4`
**File**: `server/cmd/vaultdb-server/main.go:41`

**Problem**: The `VERSION` file, `Makefile`, and `main.go` all contain a version. `main.go` defaults to `dev`, but there is no check that the `VERSION` file exists when building via `make build`. If the file is deleted — an empty string will be used.

---

### 18. `InsertRows` — double sync

**File**: `server/internal/storage/page_engine_io.go:249-261`

```go
if err := t.heap.Sync(); err != nil {  // sync inside loop
    return 0, err
}
...
if err := t.heap.Sync(); err != nil {  // sync after loop
    return 0, err
}
```

**Problem**: `t.heap.Sync()` is called both inside the loop (on page overflow) and after the loop. The inner sync is redundant — it occurs during page flush.

**Recommendation**: Remove the inner sync (line 249-251), keep only the final one.

---

## Minor (Nice to Have)

### 19. `generateID()` — dead code
`pool.go:300-304` — `generateID()` is unused, duplicates `randomID()`.

### 20. `PoolStats` — dead type
`pool.go:306-311` — `PoolStats` and `Stats()` are not called externally.

### 21. `VAULTDB_LOG_LEVEL` — no-op in config
`config.go:293-295` — variable is read but immediately discarded.

### 22. `OpUpdate` absent in WAL
At the WAL level, there is no single `OpUpdate`. UPDATE = DELETE + INSERT — two WAL records.

### 23. `distinctRows` — O(n) memory, potential O(n²) strings
`commands_select.go:414-425` — `strings.Join(row, "\x00")` creates a new string for each row. For large results — many allocations.

### 24. `evalOperandRaw` — potential nil dereference
`commands_select.go:311` — if `cmp.Right` is nil, `evalOperandRaw` may crash.

---

## Tests and Coverage

| Package | Status | Time |
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

**All tests pass.** `go vet` is clean.

---

## CI/CD

CI includes: `gofmt`, `go vet`, `staticcheck`, `go test -race`, `gosec`, `govulncheck`, `npm audit`, C++ build+test, Docker smoke test. This is good practice.

---

## Summary

| Category | Found | Priority |
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

**Total**: 20 findings (4 Critical/High, 8 Medium, 8 Low)

---

## Comparison with audit.md

| audit.md # | My Status | Comment |
|------------|-----------|-------------|
| A (deadlock) | ✅ Agree, real risk | Not theoretical — real vector |
| B (pool) | ✅ Agree | Additionally: isHealthy data race |
| C (context) | ✅ Agree | Critical for production |
| D (WAL errors) | ✅ Agree | Critical for durability |
| E (DRY) | ✅ Agree | |
| F (reflect) | ✅ Agree | |
| G (fallback) | ✅ Agree | |
| H (LOG_LEVEL) | ✅ Agree | |
| I (isHealthy race) | ✅ Agree | |
| J (token race) | ✅ Agree | Low priority |
| 3.1-3.5 | ✅ Agree | |

**New findings** (not in audit.md): #10-18, 20-24.

---

## Production Priority Recommendations

1. **Fix deadlock** (#1) — document lock ordering or restructure checkpoint
2. **Fix context** (#2) — pass shutdown context into executor
3. **Fix WAL errors** (#3) — add slog.Warn on corrupt entries
4. **Fix pool race** (#5) — lock conn.mu in isHealthy
5. **Fix isHealthy EOF** (#10) — remove io.EOF from healthy conditions
6. **Fix panic stack trace** (#14) — add debug.Stack()
7. **Fix embedded WAL** (#12) — document or automate
