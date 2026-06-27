# VaultDB High-Load Optimization Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use compose:subagent (recommended) or compose:execute to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make VaultDB production-ready for high-concurrency workloads with large tables (100K+ rows, 100+ concurrent connections).

**Architecture:** Optimize hot paths (upsert, merge, insert, vacuum), add resource limits (live query worker pool, auth rate limiting), and improve concurrency (per-table vacuum locking, batched page inserts).

**Tech Stack:** Go 1.21+, existing page storage engine, existing WAL, existing index system.

---

## Task 1: Upsert — hash-based conflict detection

**Covers:** Upsert O(n²) bottleneck

**Files:**
- Modify: `server/internal/executor/commands_dml.go:302-378`
- Test: `server/internal/executor/commands_dml_test.go`

**Problem:** `executeUpsert` calls `ReadCurrentRows` (loads ALL rows into memory), then checks conflicts one-by-one per inserted row. For 10K existing rows + 1K inserts = 10M comparisons.

**Fix:** Build a hash index on conflict columns from existing rows once, then O(1) lookup per insert.

- [ ] **Step 1: Write test for upsert performance**

```go
// In commands_dml_test.go — add test
func TestUpsertHashConflictDetection(t *testing.T) {
    store := newMockStorage()
    ctx := &ExecutionContext{
        Storage:   store,
        Session:   NewSession(store, nil, nil, nil),
        TxManager: txmanager.NewManager(),
    }

    // Create table with 1000 rows
    schema := &storage.TableSchema{
        Name:    "users",
        Columns: []storage.ColumnSchema{{Name: "id", Type: "INTEGER"}, {Name: "name", Type: "TEXT"}},
    }
    store.CreateDatabase("test")
    store.CreateTable("test", schema)

    rows := make([]storage.Row, 1000)
    for i := range rows {
        rows[i] = storage.Row{int64(i), fmt.Sprintf("user_%d", i)}
    }
    store.InsertRows("test", "users", rows)

    // Upsert 100 rows — 50 conflicts, 50 new
    insertRows := make([]*parser.ValueExpr, 100)
    for i := range insertRows {
        id := int64(i) // first 50 conflict, next 50 new
        insertRows[i] = &parser.InsertRow{
            Values: []parser.Expression{
                &parser.LiteralExpr{Value: id},
                &parser.LiteralExpr{Value: fmt.Sprintf("upserted_%d", i)},
            },
        }
    }

    stmt := &parser.InsertStatement{
        TableName: "users",
        Columns:   []string{"id", "name"},
        Rows:      insertRows,
        OnConflict: &parser.OnConflictClause{
            Action: "UPDATE",
            Assignments: []parser.Assignment{
                {Column: "name", Value: &parser.ColumnRef{Name: "name"}},
            },
        },
    }

    cmd := &InsertCommand{stmt: stmt}
    result, err := cmd.Execute(ctx)
    require.NoError(t, err)
    assert.Equal(t, 100, result.Affected)
}
```

- [ ] **Step 2: Run test — verify it passes (existing behavior, just slower)**

Run: `cd server && go test ./internal/executor/ -run TestUpsertHashConflictDetection -v`
Expected: PASS (but slow with large data)

- [ ] **Step 3: Implement hash-based conflict detection**

Replace the `executeUpsert` method in `commands_dml.go`:

```go
func (c *InsertCommand) executeUpsert(ctx *ExecutionContext, dbName string, schema *storage.TableSchema, rowsToInsert []storage.Row) (*Result, error) {
	affected := 0

	conflictCols := c.stmt.OnConflict.Columns
	if len(conflictCols) == 0 {
		conflictCols = nil
		for _, col := range schema.Columns {
			conflictCols = append(conflictCols, col.Name)
		}
	}
	colIdxMap := make(map[string]int, len(schema.Columns))
	for i, col := range schema.Columns {
		colIdxMap[strings.ToLower(col.Name)] = i
	}

	// Build hash index on conflict columns for O(1) lookup
	type conflictKey struct {
		vals string // concatenated string representation of conflict column values
	}
	existingRows, err := ctx.Storage.ReadCurrentRows(dbName, c.stmt.TableName)
	if err != nil {
		return nil, err
	}

	// conflictMap: hash key → index in existingRows
	conflictMap := make(map[string]int, len(existingRows))
	for idx, row := range existingRows {
		key := buildConflictKey(row, conflictCols, colIdxMap)
		conflictMap[key] = idx
	}

	for _, row := range rowsToInsert {
		key := buildConflictKey(row, conflictCols, colIdxMap)
		conflictIdx, conflict := conflictMap[key]

		if conflict {
			if c.stmt.OnConflict.Action == "NOTHING" {
				continue
			}
			if c.stmt.OnConflict.Action == "UPDATE" {
				updates := make(map[string]storage.Value)
				for _, assign := range c.stmt.OnConflict.Assignments {
					val, err := evalOperand(assign.Value, row, schema, ctx)
					if err != nil {
						return nil, err
					}
					updates[assign.Column] = val
				}
				_, err := ctx.Storage.UpdateRows(dbName, c.stmt.TableName, []int{conflictIdx}, updates)
				if err != nil {
					return nil, err
				}
				affected++
				// Update the cached row in conflictMap
				existingRows[conflictIdx] = row
			}
		} else {
			_, err := ctx.Storage.InsertRows(dbName, c.stmt.TableName, []storage.Row{row})
			if err != nil {
				return nil, err
			}
			affected++
			// Add new row to conflict map
			newIdx := len(existingRows)
			existingRows = append(existingRows, row)
			conflictMap[key] = newIdx
		}
	}

	return &Result{Type: "affected", Affected: affected}, nil
}

// buildConflictKey creates a hash key from conflict column values.
func buildConflictKey(row storage.Row, conflictCols []string, colIdxMap map[string]int) string {
	var b strings.Builder
	for i, colName := range conflictCols {
		if i > 0 {
			b.WriteByte(0)
		}
		ci, ok := colIdxMap[strings.ToLower(colName)]
		if !ok {
			continue
		}
		if ci < len(row) {
			b.WriteString(valueToString(row[ci]))
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run test — verify it passes**

Run: `cd server && go test ./internal/executor/ -run TestUpsertHashConflictDetection -v`
Expected: PASS

- [ ] **Step 5: Run all executor tests**

Run: `cd server && go test ./internal/executor/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/executor/commands_dml.go server/internal/executor/commands_dml_test.go
git commit -m "perf: optimize upsert with hash-based conflict detection (O(n) → O(1) lookup)"
```

---

## Task 2: MERGE — hash join for matched rows

**Covers:** MERGE O(n×m) bottleneck

**Files:**
- Modify: `server/internal/executor/commands_new.go:100-289`
- Test: `server/internal/executor/commands_new.go` (add test)

**Problem:** For each source row, scans ALL target rows to evaluate ON condition. O(source × target).

**Fix:** Build a hash map from target rows keyed on the ON condition's equality columns. This requires extracting equality predicates from the ON condition.

- [ ] **Step 1: Write test for MERGE performance**

```go
func TestMergeHashJoin(t *testing.T) {
    store := newMockStorage()
    ctx := &ExecutionContext{
        Storage:   store,
        Session:   NewSession(store, nil, nil, nil),
        TxManager: txmanager.NewManager(),
    }

    store.CreateDatabase("test")
    targetSchema := &storage.TableSchema{
        Name:    "target",
        Columns: []storage.ColumnSchema{{Name: "id", Type: "INTEGER"}, {Name: "val", Type: "TEXT"}},
    }
    sourceSchema := &storage.TableSchema{
        Name:    "source",
        Columns: []storage.ColumnSchema{{Name: "id", Type: "INTEGER"}, {Name: "val", Type: "TEXT"}},
    }
    store.CreateTable("test", targetSchema)
    store.CreateTable("test", sourceSchema)

    // 1000 target rows
    targetRows := make([]storage.Row, 1000)
    for i := range targetRows {
        targetRows[i] = storage.Row{int64(i), "old"}
    }
    store.InsertRows("test", "target", targetRows)

    // 500 source rows
    sourceRows := make([]storage.Row, 500)
    for i := range sourceRows {
        sourceRows[i] = storage.Row{int64(i), "new"}
    }
    store.InsertRows("test", "source", sourceRows)

    // MERGE ... ON target.id = source.id WHEN MATCHED UPDATE
    stmt := &parser.MergeStatement{
        TargetTable: "target",
        SourceTable: "source",
        OnCondition: &parser.BinaryExpr{
            Left:  &parser.ColumnRef{Table: "target", Name: "id"},
            Right: &parser.ColumnRef{Table: "source", Name: "id"},
            Operator: "=",
        },
        WhenMatched: &parser.WhenClause{
            Action: "UPDATE",
            Assignments: []parser.Assignment{
                {Column: "val", Value: &parser.ColumnRef{Table: "source", Name: "val"}},
            },
        },
    }

    cmd := &MergeCommand{stmt: stmt}
    start := time.Now()
    result, err := cmd.Execute(ctx)
    duration := time.Since(start)
    require.NoError(t, err)
    assert.Equal(t, 500, result.Affected)
    t.Logf("MERGE 500x1000 took %v", duration)
    assert.Less(t, duration, 5*time.Second, "MERGE should complete within 5s")
}
```

- [ ] **Step 2: Run test — verify it passes (but is slow)**

Run: `cd server && go test ./internal/executor/ -run TestMergeHashJoin -v`
Expected: PASS (but >5s)

- [ ] **Step 3: Implement hash-based MERGE**

Replace the `Execute` method of `MergeCommand` in `commands_new.go`. Add a helper to extract equality columns from the ON condition:

```go
// extractEqualityColumns extracts column pairs from ON condition for hash join.
// Returns (target col name, source col name) pairs.
func extractEqualityColumns(cond parser.Expression) [][2]string {
	be, ok := cond.(*parser.BinaryExpr)
	if !ok || be.Operator != "=" {
		return nil
	}
	leftCol, lok := be.Left.(*parser.ColumnRef)
	rightCol, rok := be.Right.(*parser.ColumnRef)
	if !lok || !rok {
		return nil
	}
	// Determine which is target and which is source
	if leftCol.Table != "" && rightCol.Table != "" {
		return [][2]string{{leftCol.Name, rightCol.Name}}
	}
	return nil
}
```

Then in `Execute`, after reading both tables, check if ON condition is a simple equality:

```go
// Try hash join for simple equality conditions
eqCols := extractEqualityColumns(c.stmt.OnCondition)
if len(eqCols) > 0 && c.stmt.WhenMatched != nil {
    return c.executeMergeHashJoin(ctx, dbName, targetRows, sourceRows, targetSchema, sourceSchema, eqCols, combinedSchema)
}
// Fall back to nested loop for complex conditions
```

The `executeMergeHashJoin` method builds a `map[string][]int` from target rows (key = concatenated equality column values, value = indices), then for each source row, computes the key and looks up matching target indices in O(1).

- [ ] **Step 4: Run test — verify it passes and is fast**

Run: `cd server && go test ./internal/executor/ -run TestMergeHashJoin -v`
Expected: PASS in <1s

- [ ] **Step 5: Run all tests**

Run: `cd server && go test ./internal/executor/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/executor/commands_new.go
git commit -m "perf: optimize MERGE with hash join for simple equality ON conditions"
```

---

## Task 3: InsertRows — cache PageCount per batch

**Covers:** InsertRows O(n) PageCount syscalls

**Files:**
- Modify: `server/internal/storage/page_engine_io.go:135-250`

**Problem:** For each tuple in a batch insert, `t.heap.PageCount()` is called — a syscall per tuple. For 10K inserts, that's 10K syscalls.

**Fix:** Cache the page count before the loop and update it when a new page is allocated.

- [ ] **Step 1: Modify InsertRows to cache PageCount**

In `page_engine_io.go`, replace the inner loop:

```go
// Cache page count — avoid syscall per tuple
cachedPageCount, err := t.heap.PageCount()
if err != nil {
    return 0, err
}

for _, tuple := range tuples {
    var pid page.PageID
    var pg *page.Page
    havePage := false

    if cachedPageCount > 0 {
        pid = pageIDAt(t.tableID, cachedPageCount-1)
        e.pageLock.RLockPage(pid)
        pg, err = e.getPage(pid, t.heap)
        e.pageLock.UnlockPage(pid)
        if err != nil {
            return 0, err
        }
        havePage = true
    }

    for {
        if !havePage {
            newPid, newPg, err := t.heap.AllocatePage(page.PageTypeHeap)
            if err != nil {
                return 0, err
            }
            pid, pg, havePage = newPid, newPg, true
            cachedPageCount++ // track new page
        }

        e.pageLock.LockPage(pid)
        slot, err := pg.InsertTuple(tuple)
        if err == nil {
            // ... (WAL write, mark dirty, unlock, break)
            break
        }
        e.pageLock.UnlockPage(pid)
        // Page full — try next page
        havePage = false
    }
}
```

- [ ] **Step 2: Run storage tests**

Run: `cd server && go test ./internal/storage/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add server/internal/storage/page_engine_io.go
git commit -m "perf: cache PageCount in InsertRows batch loop to avoid per-tuple syscall"
```

---

## Task 4: Live query worker pool

**Covers:** Unbounded goroutine spawn in live queries

**Files:**
- Modify: `server/internal/executor/broadcaster.go:176-222`

**Problem:** Each table mutation spawns N goroutines (one per subscription), each creating a full session. Under load, this can overwhelm the system.

**Fix:** Add a buffered worker pool. Subscriptions are dispatched to a fixed pool of workers.

- [ ] **Step 1: Add worker pool to Broadcaster**

```go
type Broadcaster struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription
	workerPool    chan struct{} // buffered channel = max concurrent workers

	logger        *slog.Logger
	defaultPolicy DropPolicy
	blockTimeout  time.Duration
	bufferSize    int
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscriptions: make(map[string]*Subscription),
		workerPool:    make(chan struct{}, 64), // max 64 concurrent live query evaluations
		logger:        slog.Default(),
		defaultPolicy: PolicyDrop,
		blockTimeout:  5 * time.Second,
		bufferSize:    256,
	}
}
```

- [ ] **Step 2: Modify NotifyTableChanged to use worker pool**

```go
func (b *Broadcaster) NotifyTableChanged(dbName, tableName string, ctx *ExecutionContext) {
	b.mu.RLock()
	matched := make([]*Subscription, 0, len(b.subscriptions))
	for _, s := range b.subscriptions {
		if s.DB == dbName && s.Query.TableName == tableName {
			matched = append(matched, s)
		}
	}
	b.mu.RUnlock()

	for _, s := range matched {
		sub := s
		// Acquire worker slot (blocks if pool is full)
		select {
		case b.workerPool <- struct{}{}:
		default:
			// Pool full — skip this notification to avoid overload
			b.logger.Warn("live query worker pool exhausted, skipping notification",
				"subscription", sub.ID)
			continue
		}

		go func() {
			defer func() { <-b.workerPool }() // release worker slot
			defer func() {
				if r := recover(); r != nil {
					b.logger.Error("panic in live query notification",
						"db", dbName, "table", tableName, "panic", r)
				}
			}()

			sess := NewSession(ctx.Storage, ctx.Metrics, ctx.TxManager, ctx.Broadcaster)
			sess.SetCurrentDatabase(sub.DB)
			if ctx.WAL != nil {
				sess.SetWAL(ctx.WAL)
			}
			if ctx.Embedder != nil {
				sess.SetEmbedder(ctx.Embedder)
			}
			if sub.snapshotTxID > 0 {
				sess.SetSnapshotTxID(sub.snapshotTxID)
			}

			res, err := sess.Execute(sub.Query)
			sess.Close()

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

- [ ] **Step 3: Run broadcaster tests**

Run: `cd server && go test ./internal/executor/ -run TestBroadcaster -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/executor/broadcaster.go
git commit -m "perf: add worker pool to live query broadcaster (max 64 concurrent evaluations)"
```

---

## Task 5: Vacuum per-table locking

**Covers:** Vacuum blocks ALL tables

**Files:**
- Modify: `server/internal/storage/page_engine_vacuum.go:17-19`

**Problem:** `Vacuum` acquires `e.mu.Lock()` for the entire operation, blocking ALL tables in ALL databases.

**Fix:** Acquire only the per-table lock (`t.mu`) instead of the global engine lock. Use `e.mu` only briefly to look up the table reference.

- [ ] **Step 1: Refactor Vacuum to use per-table lock**

```go
func (e *PageStorageEngine) Vacuum(dbName, tableName string) (*VacuumStats, error) {
	// Brief global lock to get table reference
	e.mu.RLock()
	t, err := e.getTableLocked(dbName, tableName, false)
	e.mu.RUnlock()
	if err != nil {
		return nil, err
	}

	// Per-table lock for the duration of vacuum
	t.mu.Lock()
	defer t.mu.Unlock()

	start := time.Now()

	// ... rest of vacuum logic (shadow file, WAL, atomic rename) ...
	// Use e.mu briefly only for catalog updates
}
```

**Important:** The `getTableLocked` method expects to be called under `e.mu`. We need to add a `getTableForRead` variant that doesn't require the lock, or restructure the lookup. The safest approach:

1. Under `e.mu.RLock()`: look up `t` in `e.tables` map
2. Release `e.mu`
3. Acquire `t.mu.Lock()`
4. Proceed with vacuum

- [ ] **Step 2: Run storage tests**

Run: `cd server && go test ./internal/storage/... -count=1 -timeout 120s`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add server/internal/storage/page_engine_vacuum.go
git commit -m "perf: vacuum uses per-table lock instead of global engine lock"
```

---

## Task 6: Auth rate limiting on failed attempts

**Covers:** Brute-force protection for auth tokens

**Files:**
- Modify: `server/internal/auth/manager.go`
- Test: `server/internal/auth/manager_test.go`

**Problem:** No rate limiting on auth failures. An attacker can try tokens at full speed.

**Fix:** Add per-IP rate limiting on failed auth attempts. After N failures in a time window, temporarily block the IP.

- [ ] **Step 1: Write test for auth rate limiting**

```go
func TestAuthRateLimiting(t *testing.T) {
    mgr, err := New(true, map[string]string{"valid-token": "admin"}, nil)
    require.NoError(t, err)

    // 10 failed attempts should trigger rate limit
    for i := 0; i < 10; i++ {
        mgr.ValidateToken("wrong-token-" + string(rune('0'+i)))
    }

    // 11th attempt from same IP should be rate-limited
    blocked := mgr.IsRateLimited("127.0.0.1")
    assert.True(t, blocked, "IP should be rate limited after 10 failures")
}
```

- [ ] **Step 2: Implement rate limiting**

Add to `auth/manager.go`:

```go
type authRateLimiter struct {
    mu       sync.Mutex
    attempts map[string][]time.Time // IP → timestamps of failed attempts
    window   time.Duration
    maxFails int
    blockFor time.Duration
    blocked  map[string]time.Time // IP → unblock time
}

func newAuthRateLimiter() *authRateLimiter {
    return &authRateLimiter{
        attempts: make(map[string][]time.Time),
        blocked:  make(map[string]time.Time),
        window:   1 * time.Minute,
        maxFails: 10,
        blockFor: 5 * time.Minute,
    }
}

func (rl *authRateLimiter) recordFailure(ip string) {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    now := time.Now()
    rl.attempts[ip] = append(rl.attempts[ip], now)
    // Cleanup old attempts
    cutoff := now.Add(-rl.window)
    filtered := rl.attempts[ip][:0]
    for _, t := range rl.attempts[ip] {
        if t.After(cutoff) {
            filtered = append(filtered, t)
        }
    }
    rl.attempts[ip] = filtered
    if len(filtered) >= rl.maxFails {
        rl.blocked[ip] = now.Add(rl.blockFor)
    }
}

func (rl *authRateLimiter) isBlocked(ip string) bool {
    rl.mu.Lock()
    defer rl.mu.Unlock()
    until, ok := rl.blocked[ip]
    if !ok {
        return false
    }
    if time.Now().After(until) {
        delete(rl.blocked, ip)
        delete(rl.attempts, ip)
        return false
    }
    return true
}
```

Integrate into `Middleware`:

```go
func (m *Manager) Middleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if !m.enabled { ... }

        ip := extractIP(r)
        if m.rateLimiter.isBlocked(ip) {
            // Return 429
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusTooManyRequests)
            json.NewEncoder(w).Encode(map[string]interface{}{
                "status": "error", "message": "rate limit exceeded, try again later",
            })
            return
        }

        token := tokenFromRequest(r)
        if token == "" || !m.ValidateToken(token) {
            m.rateLimiter.recordFailure(ip)
            // Return 401
            ...
            return
        }
        next(w, r)
    }
}
```

- [ ] **Step 3: Run auth tests**

Run: `cd server && go test ./internal/auth/... -v`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/auth/manager.go server/internal/auth/manager_test.go
git commit -m "sec: add per-IP rate limiting on failed auth attempts (10/min → 5min block)"
```

---

## Task 7: Statistics — bounded sample without full table scan

**Covers:** Statistics reads all rows then truncates

**Files:**
- Modify: `server/internal/storage/storage.go` (add interface method)
- Modify: `server/internal/storage/page_engine_io.go` (implement)
- Modify: `server/internal/executor/statistics.go` (use new method)

**Problem:** `ReadCurrentRows` loads ALL rows into memory, then statistics truncates to 1000. For a 1M-row table, this allocates 1M Row objects unnecessarily.

**Fix:** Add `ReadSampleRows(dbName, tableName string, limit int) ([]Row, error)` to the storage interface.

- [ ] **Step 1: Add method to storage interface**

In `storage.go`:

```go
type StorageEngine interface {
    // ... existing methods ...
    ReadSampleRows(dbName, tableName string, limit int) ([]Row, error)
}
```

- [ ] **Step 2: Implement in PageStorageEngine**

In `page_engine_io.go`:

```go
func (e *PageStorageEngine) ReadSampleRows(dbName, tableName string, limit int) ([]Row, error) {
    e.mu.RLock()
    t, err := e.getTableLocked(dbName, tableName, false)
    e.mu.RUnlock()
    if err != nil {
        return nil, err
    }

    t.mu.RLock()
    defer t.mu.RUnlock()

    var rows []Row
    snap := e.txMgr.CurrentSnapshot()
    pageCount, err := t.heap.PageCount()
    if err != nil {
        return nil, err
    }

    // Sample from evenly-spaced pages
    step := uint32(1)
    if pageCount > uint32(limit) {
        step = pageCount / uint32(limit)
    }

    for g := uint32(0); g < pageCount && len(rows) < limit; g += step {
        pid := pageIDAt(t.tableID, g)
        var pg page.Page
        if err := t.heap.ReadPage(pid, &pg); err != nil {
            continue
        }
        h := pg.Header()
        for slot := uint16(0); slot < h.NItems && len(rows) < limit; slot++ {
            tuple := pg.GetTuple(slot)
            if tuple == nil { continue }
            createdTx := binary.LittleEndian.Uint64(tuple[0:8])
            deletedTx := binary.LittleEndian.Uint64(tuple[8:16])
            if deletedTx != 0 && deletedTx <= snap { continue }
            if createdTx > snap { continue }
            row := decodeRowFromTuple(tuple[16:], t.schema)
            rows = append(rows, row)
        }
    }
    return rows, nil
}
```

- [ ] **Step 3: Update statistics to use ReadSampleRows**

In `statistics.go`:

```go
rows, err := sc.storage.ReadSampleRows(dbName, tableName, defaultSampleSize)
if err != nil {
    return stats
}
// No need to truncate — already limited by ReadSampleRows
```

- [ ] **Step 4: Add mock implementation for tests**

In `mock_storage_test.go`:

```go
func (m *mockStorageEngine) ReadSampleRows(dbName, tableName string, limit int) ([]storage.Row, error) {
    rows, err := m.ReadCurrentRows(dbName, tableName)
    if err != nil {
        return nil, err
    }
    if len(rows) > limit {
        rows = rows[:limit]
    }
    return rows, nil
}
```

- [ ] **Step 5: Run all tests**

Run: `cd server && go test ./... -count=1 -timeout 180s`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/storage/storage.go server/internal/storage/page_engine_io.go server/internal/executor/statistics.go server/internal/executor/mock_storage_test.go
git commit -m "perf: add ReadSampleRows for bounded statistics collection without full table scan"
```

---

## Task 8: WAL sync batching default

**Covers:** WAL fsync per write is slow

**Files:**
- Modify: `server/internal/wal/wal.go:138`

**Problem:** Default `SyncBatchSize=1` means fsync on every write. For bulk inserts, this is the main bottleneck.

**Fix:** Change default to 64. Document the tradeoff.

- [ ] **Step 1: Change default**

```go
const defaultSyncBatchSize = 64
```

- [ ] **Step 2: Add config option**

In `vaultdb.yaml`:

```yaml
wal:
  sync_batch_size: 64  # fsync every N entries (1=safest, 64+=fastest)
```

- [ ] **Step 3: Run WAL tests**

Run: `cd server && go test ./internal/wal/... -count=1 -timeout 60s`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/wal/wal.go
git commit -m "perf: increase default WAL sync batch size from 1 to 64"
```

---

## Execution Order

Tasks are independent and can be parallelized:
- **Batch 1 (parallel):** Task 1 (Upsert), Task 2 (MERGE), Task 4 (Worker pool)
- **Batch 2 (parallel):** Task 3 (InsertRows), Task 5 (Vacuum), Task 6 (Auth rate limit)
- **Batch 3 (parallel):** Task 7 (Statistics), Task 8 (WAL batch)

After all tasks: run full test suite + `go vet ./...` + `go test -race ./...`
