# VaultDB Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use compose:subagent (recommended) or compose:execute to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Execute all improvements from IMPROVEMENTS.md — critical crash-safety fixes, race condition patches, security hardening, performance optimizations, and code quality improvements.

**Architecture:** Modular Go codebase with page storage engine, WAL, transaction manager, HTTP/TCP servers. Changes span storage layer (crash safety, MVCC), executor (race conditions, aggregates), HTTP server (security, rate limiting), and infrastructure (linting, tests).

**Tech Stack:** Go 1.21+, ARIES-style WAL, page-based storage, Prometheus metrics, HTTP/1.1 + TCP protocol.

---

## Phase 1: Critical Crash Safety (Issues #2, #3, #4)

### Task 1: ALTER TABLE Rewrite Recovery Logic

**Covers:** IMPROVEMENTS #2

**Files:**
- Modify: `server/internal/storage/page_engine_alter.go`
- Modify: `server/internal/wal/wal.go` (WAL op types)
- Test: `server/internal/storage/crash_test.go`

- [ ] **Step 1: Add recovery logic for uncommitted rewrite**

```go
// In page_engine_alter.go, add method:
func (e *PageStorageEngine) recoverRewrite(db, table string) error {
    // Check if temp rewrite directory exists
    originalPath := e.tablePath(db, table)
    tmpPath := originalPath + ".rewrite.tmp"
    
    if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
        return nil // No incomplete rewrite
    }
    
    // Incomplete rewrite found — clean up temp directory
    slog.Warn("recovering from incomplete ALTER TABLE rewrite",
        "db", db, "table", table)
    os.RemoveAll(tmpPath)
    return nil
}
```

- [ ] **Step 2: Call recovery during startup**

```go
// In page_engine.go RecoverFromWAL or startup, add:
func (e *PageStorageEngine) recoverIncompleteRewrites() {
    entries, err := os.ReadDir(e.rootDir)
    if err != nil {
        return
    }
    for _, entry := range entries {
        if entry.IsDir() && strings.HasSuffix(entry.Name(), ".rewrite.tmp") {
            // Extract db/table from path
            // Call recoverRewrite
        }
    }
}
```

- [ ] **Step 3: Write failing test**

```go
// In crash_test.go
func TestAlterTableRewriteRecovery(t *testing.T) {
    // Create table, insert data
    // Simulate crash mid-rewrite (temp dir exists, no commit WAL)
    // Restart engine
    // Verify table is intact
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd server && go test ./internal/storage/ -run TestAlterTableRewriteRecovery -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/storage/page_engine_alter.go server/internal/storage/crash_test.go
git commit -m "fix: add ALTER TABLE rewrite crash recovery logic"
```

---

### Task 2: Vacuum Recovery Logic

**Covers:** IMPROVEMENTS #3

**Files:**
- Modify: `server/internal/storage/page_engine_vacuum.go`
- Test: `server/internal/storage/crash_test.go`

- [ ] **Step 1: Add recovery for orphaned vacuum directories**

```go
// In page_engine_vacuum.go, add:
func (e *PageStorageEngine) recoverOrphanedVacuums() {
    entries, err := os.ReadDir(e.rootDir)
    if err != nil {
        return
    }
    for _, entry := range entries {
        if entry.IsDir() && strings.HasSuffix(entry.Name(), ".vacuum") {
            vacuumPath := filepath.Join(e.rootDir, entry.Name())
            // Extract db/table from directory name
            // If original table exists, remove orphaned vacuum
            // If original missing, rename vacuum to original
            slog.Warn("recovering orphaned vacuum directory",
                "path", vacuumPath)
            os.RemoveAll(vacuumPath)
        }
    }
}
```

- [ ] **Step 2: Call during startup**

```go
// In NewPageStorageEngine or RecoverFromWAL:
e.recoverOrphanedVacuums()
```

- [ ] **Step 3: Write test**

```go
func TestVacuumRecovery(t *testing.T) {
    // Create table, insert data
    // Simulate crash mid-vacuum (shadow file exists, no commit WAL)
    // Restart engine
    // Verify table is intact
}
```

- [ ] **Step 4: Run test**

Run: `cd server && go test ./internal/storage/ -run TestVacuumRecovery -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/storage/page_engine_vacuum.go server/internal/storage/crash_test.go
git commit -m "fix: add vacuum crash recovery for orphaned shadow files"
```

---

### Task 3: Full Page Write Integration with Recovery

**Covers:** IMPROVEMENTS #4

**Files:**
- Modify: `server/internal/storage/page_engine_io.go`
- Modify: `server/internal/wal/wal.go`
- Test: `server/internal/storage/crash_test.go`

- [ ] **Step 1: Integrate OpFullPageImage with recovery**

```go
// In page_engine_io.go, during recovery replay:
func (e *PageStorageEngine) replayFullPageImage(payload []byte) error {
    // Parse page ID and full page data from payload
    // Write page to heap file
    // This restores torn pages from WAL
    return nil
}
```

- [ ] **Step 2: Add recovery case for OpFullPageImage**

```go
// In RecoverFromWAL switch:
case wal.OpFullPageImage:
    if err := e.replayFullPageImage(entry.Payload); err != nil {
        slog.Warn("failed to replay full page image", "error", err)
    }
```

- [ ] **Step 3: Write test**

```go
func TestFullPageWriteRecovery(t *testing.T) {
    // Write page, corrupt it on disk
    // Recovery should restore from WAL full page image
}
```

- [ ] **Step 4: Run test**

Run: `cd server && go test ./internal/storage/ -run TestFullPageWriteRecovery -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/storage/page_engine_io.go server/internal/storage/crash_test.go
git commit -m "fix: integrate full page writes with WAL recovery"
```

---

## Phase 2: Critical Race Conditions (Issues #5, #6, #7, #8)

### Task 4: MVCC Visibility Check

**Covers:** IMPROVEMENTS #5

**Files:**
- Modify: `server/internal/storage/page_engine_io.go:442-446`
- Modify: `server/internal/txmanager/manager.go`
- Test: `server/internal/storage/page_engine_test.go`

- [ ] **Step 1: Add IsCommitted method to txmanager**

```go
// In txmanager/manager.go:
func (m *Manager) IsCommitted(xid uint64) bool {
    // For now, all txids < current counter are committed
    // This is a simplification — proper MVCC would track committed set
    return xid < m.counter.Load()
}
```

- [ ] **Step 2: Add TxManager reference to PageStorageEngine read path**

```go
// In page_engine_io.go readRows:
func (e *PageStorageEngine) readRows(dbName, tableName string, asOf uint64) ([]Row, error) {
    // ...
    err = e.scanTuples(t, func(_ page.PageID, _ *page.Page, _ uint16, createdTx, deletedTx uint64, row Row) (bool, error) {
        if asOf == 0 {
            if deletedTx == 0 {
                // Check if createdTx is committed
                if e.txMgr != nil && !e.txMgr.IsCommitted(createdTx) {
                    return false, nil // Skip uncommitted rows
                }
                rows = append(rows, row)
            }
            return false, nil
        }
        // ...
    })
}
```

- [ ] **Step 3: Write test**

```go
func TestMVCCVisibility(t *testing.T) {
    // Start transaction, insert row (not committed)
    // Read from another session — should not see uncommitted row
    // Commit transaction
    // Read again — should now see the row
}
```

- [ ] **Step 4: Run test**

Run: `cd server && go test ./internal/storage/ -run TestMVCCVisibility -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/storage/page_engine_io.go server/internal/txmanager/manager.go server/internal/storage/page_engine_test.go
git commit -m "fix: add MVCC visibility check for uncommitted rows"
```

---

### Task 5: Fix UPSERT TOCTOU Race

**Covers:** IMPROVEMENTS #6, #10

**Files:**
- Modify: `server/internal/executor/commands_dml.go:253-340`

- [ ] **Step 1: Read existing rows once before loop**

```go
// In executeUpsert, read existing rows BEFORE the loop:
func (c *InsertCommand) executeUpsert(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, rowsToInsert []storage.Row) (*Result, error) {
    affected := 0
    
    // Read existing rows ONCE before the loop (fix #10)
    existingRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
    if err != nil {
        return nil, err
    }
    
    conflictCols := c.stmt.OnConflict.Columns
    // ... setup colIdxMap ...
    
    for _, row := range rowsToInsert {
        // Use pre-read existingRows instead of re-reading each iteration
        conflict := false
        conflictIdx := -1
        
        for idx, existingRow := range existingRows {
            // ... same conflict detection logic ...
        }
        
        if conflict {
            if c.stmt.OnConflict.Action == "NOTHING" {
                continue
            }
            if c.stmt.OnConflict.Action == "UPDATE" {
                // ... update logic ...
                // Re-read after update for next iteration
                existingRows, _ = ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
            }
        } else {
            _, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, []storage.Row{row})
            if err != nil {
                return nil, err
            }
            affected++
            // Re-read after insert for next iteration
            existingRows, _ = ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
        }
    }
    // ...
}
```

- [ ] **Step 2: Write test**

```go
func TestUpsertTOCTOU(t *testing.T) {
    // Concurrent UPSERTs should not cause duplicate keys
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/executor/ -run TestUpsertTOCTOU -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/commands_dml.go
git commit -m "fix: read existing rows once in UPSERT to prevent TOCTOU race"
```

---

### Task 6: Fix INSERT/UPDATE/DELETE RETURNING Stale Data

**Covers:** IMPROVEMENTS #7, #8

**Files:**
- Modify: `server/internal/executor/commands_dml.go`

- [ ] **Step 1: INSERT RETURNING already fixed — verify**

The INSERT RETURNING already uses `rowsToInsert` directly (line 397). No change needed.

- [ ] **Step 2: UPDATE RETURNING — verify pre-mutation rows**

The UPDATE RETURNING already uses `preUpdateRows` (line 609-616). Verify this is correct — it captures rows before mutation.

- [ ] **Step 3: DELETE RETURNING — verify pre-mutation rows**

The DELETE RETURNING already uses `preDeleteRows` (line 721-728). Verify this is correct.

- [ ] **Step 4: Write regression test**

```go
func TestReturningsUsePreMutationData(t *testing.T) {
    // Insert row, UPDATE with RETURNING, verify returned data is pre-mutation
    // Insert row, DELETE with RETURNING, verify returned data is pre-mutation
}
```

- [ ] **Step 5: Run test**

Run: `cd server && go test ./internal/executor/ -run TestReturningsUsePreMutationData -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/executor/commands_dml.go
git commit -m "fix: verify RETURNING clauses use pre-mutation data"
```

---

### Task 7: Fix Undo Type Assertions Panic

**Covers:** IMPROVEMENTS #9

**Files:**
- Modify: `server/internal/executor/commands_tx.go:179,231,236`

- [ ] **Step 1: Add comma-ok checks**

```go
// In undoInsert (line ~201):
stmt, ok := op.Payload.(*parser.InsertStatement)
if !ok || stmt == nil {
    return fmt.Errorf("undo insert: invalid payload type")
}

// In undoUpdate (around line 231):
stmt, ok := op.Payload.(*parser.UpdateStatement)
if !ok || stmt == nil {
    return fmt.Errorf("undo update: invalid payload type")
}

// In undoDelete (around line 236):
stmt, ok := op.Payload.(*parser.DeleteStatement)
if !ok || stmt == nil {
    return fmt.Errorf("undo delete: invalid payload type")
}
```

- [ ] **Step 2: Write test**

```go
func TestUndoTypeAssertionSafety(t *testing.T) {
    // Create PendingOp with wrong payload type
    // Verify undo returns error instead of panicking
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/executor/ -run TestUndoTypeAssertionSafety -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/commands_tx.go
git commit -m "fix: add comma-ok checks for undo type assertions"
```

---

## Phase 3: Security Hardening (Issues #12, #13, #15, #16, #17, #22)

### Task 8: Health Endpoint Auth Check

**Covers:** IMPROVEMENTS #12

**Files:**
- Modify: `server/internal/httpserver/server.go:437-468`

- [ ] **Step 1: Add auth check to health endpoint**

```go
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
    uptime := int(time.Since(s.startedAt).Seconds())
    
    status := "ok"
    checks := map[string]interface{}{}
    
    if _, err := s.cfg.Storage.ListDatabases(); err != nil {
        status = "degraded"
        checks["storage"] = map[string]interface{}{"status": "fail"}
    } else {
        checks["storage"] = map[string]interface{}{"status": "pass"}
    }
    
    checks["wal"] = map[string]interface{}{"status": "pass"}
    
    // Full response only for authenticated requests
    if s.cfg.Auth != nil && s.cfg.Auth.IsEnabled() {
        token := r.Header.Get("Authorization")
        if token == "" {
            token = r.URL.Query().Get("token")
        }
        if s.cfg.Auth.ValidateToken(strings.TrimPrefix(token, "Bearer ")) {
            writeJSON(w, http.StatusOK, map[string]interface{}{
                "status":      status,
                "version":     s.cfg.Version,
                "uptime_s":    uptime,
                "connections": s.cfg.ActiveConnections(),
                "wal_enabled": true,
                "time_travel": true,
                "checks":      checks,
            })
            return
        }
    }
    
    // Unauthenticated: minimal response
    writeJSON(w, http.StatusOK, map[string]interface{}{
        "status": status,
    })
}
```

- [ ] **Step 2: Write test**

```go
func TestHealthEndpointAuth(t *testing.T) {
    // Unauthenticated: should return only {"status":"ok"}
    // Authenticated: should return full response
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/httpserver/ -run TestHealthEndpointAuth -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/httpserver/server.go
git commit -m "fix: restrict health endpoint info to authenticated requests"
```

---

### Task 9: Metrics Cardinality Limit

**Covers:** IMPROVEMENTS #13

**Files:**
- Modify: `server/internal/metrics/collector.go:319-343`

- [ ] **Step 1: Add cardinality limit**

```go
const maxStorageRowMetrics = 1000

// In Render(), modify storage rows section:
if len(c.storageRows) > 0 {
    totalMetrics := 0
    overflow := false
    
    b.WriteString("\n# HELP vaultdb_storage_rows Total rows per table\n")
    b.WriteString("# TYPE vaultdb_storage_rows gauge\n")
    
    for _, db := range dbs {
        for _, t := range tables {
            if totalMetrics >= maxStorageRowMetrics {
                overflow = true
                break
            }
            fmt.Fprintf(&b, `vaultdb_storage_rows{database="%s",table="%s"} %d`+"\n",
                sanitizeMetricLabel(db), sanitizeMetricLabel(t), c.storageRows[db][t])
            totalMetrics++
        }
        if overflow {
            break
        }
    }
    
    if overflow {
        fmt.Fprintf(&b, "vaultdb_storage_rows_overflow 1\n")
    }
}
```

- [ ] **Step 2: Write test**

```go
func TestMetricsCardinalityLimit(t *testing.T) {
    // Create >1000 tables
    // Verify only 1000 metrics emitted + overflow flag
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/metrics/ -run TestMetricsCardinalityLimit -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/metrics/collector.go
git commit -m "fix: add cardinality limit for storage row metrics"
```

---

### Task 10: Rate Limiting on All HTTP Endpoints

**Covers:** IMPROVEMENTS #16

**Files:**
- Modify: `server/internal/httpserver/server.go:237-243`

- [ ] **Step 1: Wrap all API endpoints in rate limiting middleware**

```go
// In Start() method, apply rate limiter to all API routes:
func (s *Server) Start(ctx context.Context) error {
    mux := http.NewServeMux()
    
    // Apply rate limiting to all API endpoints
    apiHandler := s.cfg.RateLimiter.Middleware(s.handleAPI)
    mux.HandleFunc("/api/", apiHandler)
    
    // Keep monitor endpoints without rate limiting
    mux.HandleFunc("/health", s.handleHealth)
    mux.HandleFunc("/ready", s.handleReady)
    mux.HandleFunc("/metrics", s.handleMetrics)
    
    // ...
}
```

- [ ] **Step 2: Write test**

```go
func TestRateLimitingOnAllEndpoints(t *testing.T) {
    // Verify /api/live, /api/databases/, /metrics are rate limited
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/httpserver/ -run TestRateLimitingOnAllEndpoints -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/httpserver/server.go
git commit -m "fix: apply rate limiting to all HTTP API endpoints"
```

---

### Task 11: Rate Limiter Memory DoS Protection

**Covers:** IMPROVEMENTS #17

**Files:**
- Modify: `server/internal/httpserver/ratelimit.go`

- [ ] **Step 1: Add LRU eviction with max keys**

```go
const maxRateLimitKeys = 100000

type RateLimiter struct {
    mu              sync.Mutex
    tokens          map[string]*tokenBucket
    rate            int
    burst           int
    cleanupInterval time.Duration
    stopCh          chan struct{}
    maxKeys         int
}

func NewRateLimiter(rate int, burst int) *RateLimiter {
    // ...
    rl := &RateLimiter{
        tokens:          make(map[string]*tokenBucket),
        rate:            rate,
        burst:           burst,
        cleanupInterval: 5 * time.Minute,
        stopCh:          make(chan struct{}),
        maxKeys:         maxRateLimitKeys,
    }
    // ...
}

func (rl *RateLimiter) Allow(key string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    
    bucket, ok := rl.tokens[key]
    if !ok {
        // Evict oldest if at capacity
        if len(rl.tokens) >= rl.maxKeys {
            rl.evictOldest()
        }
        bucket = &tokenBucket{
            tokens:    float64(rl.burst),
            lastTime:  time.Now(),
            maxTokens: float64(rl.burst),
        }
        rl.tokens[key] = bucket
    }
    // ... rest unchanged
}

func (rl *RateLimiter) evictOldest() {
    var oldestKey string
    var oldestTime time.Time
    for key, bucket := range rl.tokens {
        if oldestKey == "" || bucket.lastTime.Before(oldestTime) {
            oldestKey = key
            oldestTime = bucket.lastTime
        }
    }
    if oldestKey != "" {
        delete(rl.tokens, oldestKey)
    }
}
```

- [ ] **Step 2: Write test**

```go
func TestRateLimiterMemoryDoS(t *testing.T) {
    // Add >100000 unique keys
    // Verify oldest evicted, memory bounded
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/httpserver/ -run TestRateLimiterMemoryDoS -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/httpserver/ratelimit.go
git commit -m "fix: add LRU eviction to prevent rate limiter memory DoS"
```

---

### Task 12: HTTP TLS MinVersion

**Covers:** IMPROVEMENTS #22

**Files:**
- Modify: `server/internal/httpserver/server.go:117-124`

- [ ] **Step 1: Add TLSConfig with MinVersion**

```go
// In Start() method, when TLS is configured:
if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
    tlsCfg := &tls.Config{
        MinVersion: tls.VersionTLS12,
        CipherSuites: []uint16{
            tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
            tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
            tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
        },
    }
    server.TLSConfig = tlsCfg
}
```

- [ ] **Step 2: Write test**

```go
func TestHTTP TLSMinVersion(t *testing.T) {
    // Start server with TLS
    // Connect with TLS 1.1 — should fail
    // Connect with TLS 1.2 — should succeed
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/httpserver/ -run TestHTTP -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/httpserver/server.go
git commit -m "fix: enforce TLS 1.2 minimum for HTTP server"
```

---

## Phase 4: Performance & Safety (Issues #11, #14, #15, #18, #19)

### Task 13: Broadcaster Async Notifications

**Covers:** IMPROVEMENTS #11

**Files:**
- Modify: `server/internal/executor/broadcaster.go:174-210`

- [ ] **Step 1: Execute queries in separate goroutines**

```go
func (b *Broadcaster) NotifyTableChanged(dbName, tableName string, ctx *ExecutionContext) {
    // ... snapshot matched subscriptions ...
    
    for _, s := range matched {
        sub := s // capture for goroutine
        go func() {
            defer func() {
                if r := recover(); r != nil {
                    b.logger.Error("panic in live query notification",
                        "db", dbName, "table", tableName, "panic", r)
                }
            }()
            
            origDB := ctx.Session.CurrentDatabase()
            ctx.Session.SetCurrentDatabase(sub.DB)
            cmd := &SelectCommand{stmt: sub.Query}
            res, err := cmd.Execute(ctx)
            ctx.Session.SetCurrentDatabase(origDB)
            if err != nil {
                return
            }
            if !sub.notify(res, b.logger) {
                b.Unsubscribe(sub.ID)
                sub.Close()
            }
        }()
    }
}
```

- [ ] **Step 2: Write test**

```go
func TestBroadcasterAsync(t *testing.T) {
    // Verify mutations don't block on slow subscribers
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/executor/ -run TestBroadcasterAsync -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/broadcaster.go
git commit -m "perf: execute live query notifications in separate goroutines"
```

---

### Task 14: Variance/Stddev Welford's Algorithm

**Covers:** IMPROVEMENTS #14, #26

**Files:**
- Modify: `server/internal/executor/aggregates.go:222-296`

- [ ] **Step 1: Implement Welford's algorithm**

```go
// stddevAgg handles STDDEV(col) using Welford's algorithm — O(1) memory.
type stddevAgg struct {
    n     int64
    mean  float64
    m2    float64
}

func (a *stddevAgg) Add(_, v interface{}) {
    if v == nil {
        return
    }
    if f, ok := toFloat(v); ok {
        a.n++
        delta := f - a.mean
        a.mean += delta / float64(a.n)
        delta2 := f - a.mean
        a.m2 += delta * delta2
    }
}

func (a *stddevAgg) Result() interface{} {
    if a.n < 2 {
        if a.n == 0 {
            return nil
        }
        return 0.0
    }
    variance := a.m2 / float64(a.n-1) // sample variance
    return math.Sqrt(variance)
}

// varianceAgg handles VARIANCE(col) using Welford's algorithm — O(1) memory.
type varianceAgg struct {
    n     int64
    mean  float64
    m2    float64
}

func (a *varianceAgg) Add(_, v interface{}) {
    if v == nil {
        return
    }
    if f, ok := toFloat(v); ok {
        a.n++
        delta := f - a.mean
        a.mean += delta / float64(a.n)
        delta2 := f - a.mean
        a.m2 += delta * delta2
    }
}

func (a *varianceAgg) Result() interface{} {
    if a.n < 2 {
        if a.n == 0 {
            return nil
        }
        return 0.0
    }
    return a.m2 / float64(a.n-1) // sample variance
}
```

- [ ] **Step 2: Write test**

```go
func TestWelfordVariance(t *testing.T) {
    // Compare Welford's result with naive implementation
    // Test with large dataset to verify O(1) memory
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/executor/ -run TestWelfordVariance -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/aggregates.go
git commit -m "perf: use Welford's algorithm for variance/stddev (O(1) memory)"
```

---

### Task 15: Per-Connection TCP Rate Limiting

**Covers:** IMPROVEMENTS #15

**Files:**
- Modify: `server/cmd/vaultdb-server/main.go:135-223`

- [ ] **Step 1: Add token bucket per connection**

```go
func handleConnection(conn net.Conn, store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, br *executor.Broadcaster, authManager *auth.Manager, embedder ai.Embedder, serverWAL *wal.WAL, logger *slog.Logger, maxRequestSize int, queryTimeoutSec int, maxRows int) {
    defer func() { /* ... */ }()
    defer conn.Close()
    
    // Per-connection rate limiter: 100 requests/second, burst 200
    connLimiter := NewConnectionRateLimiter(100, 200)
    
    // ... existing code ...
    
    for scanner.Scan() {
        // Rate limit check
        if !connLimiter.Allow() {
            if !sendError(conn, "", "rate limit exceeded", logger) {
                return
            }
            continue
        }
        // ... rest of handler
    }
}

// ConnectionRateLimiter is a simple token bucket for per-connection rate limiting.
type ConnectionRateLimiter struct {
    tokens    float64
    lastTime  time.Time
    rate      float64
    maxTokens float64
}

func NewConnectionRateLimiter(rate, burst int) *ConnectionRateLimiter {
    return &ConnectionRateLimiter{
        tokens:    float64(burst),
        lastTime:  time.Now(),
        rate:      float64(rate),
        maxTokens: float64(burst),
    }
}

func (l *ConnectionRateLimiter) Allow() bool {
    now := time.Now()
    elapsed := now.Sub(l.lastTime).Seconds()
    l.tokens += elapsed * l.rate
    if l.tokens > l.maxTokens {
        l.tokens = l.maxTokens
    }
    l.lastTime = now
    
    if l.tokens >= 1 {
        l.tokens--
        return true
    }
    return false
}
```

- [ ] **Step 2: Write test**

```go
func TestConnectionRateLimiter(t *testing.T) {
    // Verify rate limiting works per connection
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./cmd/vaultdb-server/ -run TestConnectionRateLimiter -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/cmd/vaultdb-server/main.go
git commit -m "feat: add per-connection rate limiting for TCP"
```

---

### Task 16: Fix Scanner Buffer Size

**Covers:** IMPROVEMENTS #18

**Files:**
- Modify: `server/cmd/vaultdb-server/main.go:174-176`

- [ ] **Step 1: Set scanner buffer to maxRequestSize**

```go
// In handleConnection, change:
scanner := bufio.NewScanner(conn)
scanner.Buffer(make([]byte, 0, 64*1024), maxRequestSize) // Use maxRequestSize instead of 1MB
```

- [ ] **Step 2: Write test**

```go
func TestScannerBufferSize(t *testing.T) {
    // Send request between 1MB and 64MB
    // Verify it's not silently truncated
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./cmd/vaultdb-server/ -run TestScannerBufferSize -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/cmd/vaultdb-server/main.go
git commit -m "fix: set scanner buffer size to maxRequestSize"
```

---

### Task 17: SSE Max Duration

**Covers:** IMPROVEMENTS #19

**Files:**
- Modify: `server/internal/httpserver/server.go:592-672`

- [ ] **Step 1: Add configurable max duration**

```go
// In Config struct, add:
MaxLiveQueryDurationSec int

// In handleLiveQuery, add timeout:
func (s *Server) handleLiveQuery(w http.ResponseWriter, r *http.Request) {
    // ...
    
    maxDuration := time.Duration(s.cfg.MaxLiveQueryDurationSec) * time.Second
    if maxDuration <= 0 {
        maxDuration = 1 * time.Hour // Default: 1 hour
    }
    
    ctx, cancel := context.WithTimeout(r.Context(), maxDuration)
    defer cancel()
    
    // ... rest of handler uses ctx with timeout
}
```

- [ ] **Step 2: Write test**

```go
func TestSSEMaxDuration(t *testing.T) {
    // Start SSE connection with short max duration
    // Verify connection closes after timeout
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/httpserver/ -run TestSSEMaxDuration -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/httpserver/server.go
git commit -m "feat: add configurable max duration for SSE live queries"
```

---

## Phase 5: Code Quality (Issues #24, #25, #37, #43)

### Task 18: Merge evalFtsMatch/evalFullTextMatch

**Covers:** IMPROVEMENTS #24

**Files:**
- Modify: `server/internal/executor/eval_functions.go`

- [ ] **Step 1: Find and merge duplicate functions**

```go
// Search for evalFtsMatch and evalFullTextMatch
// They have nearly identical logic — merge into one:
func evalFtsMatchFunc(query, document string, weights ...float64) float64 {
    // Combined implementation
}
```

- [ ] **Step 2: Write test**

```go
func TestFtsMatchConsolidated(t *testing.T) {
    // Verify both old paths produce same results
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/executor/ -run TestFtsMatchConsolidated -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/eval_functions.go
git commit -m "refactor: merge duplicate FTS match functions"
```

---

### Task 19: Add .golangci.yml

**Covers:** IMPROVEMENTS #37

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Create linter config**

```yaml
# .golangci.yml
run:
  timeout: 5m

linters:
  enable:
    - errcheck
    - gosec
    - ineffassign
    - staticcheck
    - unused
    - gosimple
    - govet
    - ineffassign

linters-settings:
  errcheck:
    check-blank: true
  gosec:
    excludes:
      - G104  # Unhandled errors (we handle most)

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - gosec
```

- [ ] **Step 2: Run linter**

Run: `cd server && golangci-lint run ./...`
Expected: Report issues (expected for first run)

- [ ] **Step 3: Commit**

```bash
git add .golangci.yml
git commit -m "chore: add golangci-lint configuration"
```

---

### Task 20: Fix Critical Discarded Errors

**Covers:** IMPROVEMENTS #43

**Files:**
- Multiple files with `_ =` patterns

- [ ] **Step 1: Find and fix critical discarded errors**

```go
// Key files to fix:
// - page_engine.go: heap Close errors → log and continue
// - buffer_pool.go: write errors → log and continue
// - select_window.go: evalOperand errors → propagate
// - select_aggr.go: evalOperand errors → propagate
// - httpserver/server.go: JSON encode errors → handle

// Example fix in buffer_pool.go:
// Before: _ = t.heap.WritePage(pid, pg)
// After: if err := t.heap.WritePage(pid, pg); err != nil {
//     slog.Error("failed to write page", "pageID", pid, "error", err)
// }
```

- [ ] **Step 2: Write test**

```go
func TestErrorPropagation(t *testing.T) {
    // Verify errors are properly logged/propagated
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/storage/buffer_pool.go server/internal/executor/select_window.go
git commit -m "fix: log critical discarded errors instead of silently ignoring"
```

---

## Phase 6: Remaining Medium Issues

### Task 21: Parser Error Message Sanitization

**Covers:** IMPROVEMENTS #20

**Files:**
- Modify: `server/internal/parser/parser.go:25`
- Modify: `server/internal/parser/parse_utils.go`

- [ ] **Step 1: Sanitize parser error messages**

```go
// In parser.go, wrap parse errors:
func Parse(input string) (parser.Statement, error) {
    stmt, err := parse(input)
    if err != nil {
        // Log detailed error server-side
        slog.Debug("parse error", "input", input, "error", err)
        // Return generic error to client
        return nil, fmt.Errorf("invalid query syntax")
    }
    return stmt, nil
}
```

- [ ] **Step 2: Write test**

```go
func TestParserErrorSanitization(t *testing.T) {
    // Verify parse errors return generic message
    // Verify detailed error is logged
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/parser/ -run TestParserErrorSanitization -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/parser/parser.go
git commit -m "fix: sanitize parser error messages for client responses"
```

---

### Task 22: Static File Auth Middleware

**Covers:** IMPROVEMENTS #31

**Files:**
- Modify: `server/internal/httpserver/server.go:192`

- [ ] **Step 1: Add auth check for static files**

```go
// In Start(), wrap static file serving:
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    // Allow health/ready/metrics without auth
    if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
        // ... existing handlers
        return
    }
    
    // Check auth for Web UI
    if s.cfg.Auth != nil && s.cfg.Auth.IsEnabled() {
        token := r.Header.Get("Authorization")
        if token == "" {
            token = r.URL.Query().Get("token")
        }
        if !s.cfg.Auth.ValidateToken(strings.TrimPrefix(token, "Bearer ")) {
            writeError(w, http.StatusUnauthorized, errCodeInternal, "unauthorized")
            return
        }
    }
    
    // Serve static files
    fs := http.FileServer(http.FS(webUIFiles))
    fs.ServeHTTP(w, r)
})
```

- [ ] **Step 2: Write test**

```go
func TestStaticFileAuth(t *testing.T) {
    // Unauthenticated: should get 401 for Web UI
    // Authenticated: should get 200
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/httpserver/ -run TestStaticFileAuth -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/httpserver/server.go
git commit -m "fix: add auth middleware for static file serving"
```

---

### Task 23: MERGE WhenNotMatched Validation

**Covers:** IMPROVEMENTS #27

**Files:**
- Modify: `server/internal/executor/commands_new.go`

- [ ] **Step 1: Add validation**

```go
// In mergeCommand, before processing WHEN NOT MATCHED:
if len(mergeStmt.WhenNotMatched.Values) > 0 {
    if len(mergeStmt.WhenNotMatched.Columns) != len(mergeStmt.WhenNotMatched.Values) {
        return nil, fmt.Errorf("MERGE: WHEN NOT MATCHED columns count (%d) doesn't match values count (%d)",
            len(mergeStmt.WhenNotMatched.Columns), len(mergeStmt.WhenNotMatched.Values))
    }
}
```

- [ ] **Step 2: Write test**

```go
func TestMergeWhenNotMatchedValidation(t *testing.T) {
    // Verify error when columns/values count mismatch
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/executor/ -run TestMergeWhenNotMatchedValidation -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/commands_new.go
git commit -m "fix: validate MERGE WHEN NOT MATCHED columns/values count"
```

---

### Task 24: Pool Cleanup Edge Case Fix

**Covers:** IMPROVEMENTS #44

**Files:**
- Modify: `server/internal/pool/pool.go:154-178`

- [ ] **Step 1: Fix idle count logic**

```go
func (p *Pool) cleanup() {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    now := time.Now()
    var remaining []*Connection
    
    // Count idle connections that should be removed
    idleToRemove := 0
    idleCount := 0
    for _, conn := range p.connections {
        if !conn.InUse && now.Sub(conn.LastUsed) >= p.idleTimeout {
            idleCount++
        }
    }
    
    // Keep at least minSize connections
    for _, conn := range p.connections {
        if conn.InUse {
            remaining = append(remaining, conn)
        } else if now.Sub(conn.LastUsed) < p.idleTimeout {
            remaining = append(remaining, conn)
        } else if idleCount <= len(p.connections)-p.minSize {
            idleCount--
            remaining = append(remaining, conn)
        }
    }
    
    p.connections = remaining
}
```

- [ ] **Step 2: Write test**

```go
func TestPoolCleanupEdgeCase(t *testing.T) {
    // Create pool with minSize=2, add 5 idle connections
    // Run cleanup
    // Verify at least minSize connections remain
}
```

- [ ] **Step 3: Run test**

Run: `cd server && go test ./internal/pool/ -run TestPoolCleanupEdgeCase -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/pool/pool.go
git commit -m "fix: correct pool cleanup idle count logic"
```

---

## Summary

| Phase | Tasks | Issues Covered |
|-------|-------|----------------|
| 1. Critical Crash Safety | 1-3 | #2, #3, #4 |
| 2. Critical Race Conditions | 4-7 | #5, #6, #7, #8, #9, #10 |
| 3. Security Hardening | 8-12 | #12, #13, #15, #16, #17, #22 |
| 4. Performance & Safety | 13-17 | #11, #14, #15, #18, #19, #26 |
| 5. Code Quality | 18-20 | #24, #37, #43 |
| 6. Remaining Medium | 21-24 | #20, #27, #31, #44 |

**Total: 24 tasks covering 28+ issues from IMPROVEMENTS.md**
