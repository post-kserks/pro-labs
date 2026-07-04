# VaultDB Performance Optimization Plan

> **Goal:** Improve VaultDB performance to be competitive with PostgreSQL for common workloads.

**Reference:** PostgreSQL 16 documentation and source code.

**Current State:** VaultDB is 40-200x slower than PostgreSQL on INSERT, 2000x on full scans, 65x on UPDATE.

---

## Phase 1: WAL Group Commit (2-3x INSERT improvement)

### Problem
Every transaction triggers a separate fsync. PostgreSQL batches fsyncs via group commit.

### PostgreSQL Reference
PostgreSQL groups multiple committed transactions into a single WAL write + fsync cycle. The `wal_sync_method` setting controls this (default: `fdatasync`).

### Implementation

**File:** `server/internal/wal/wal.go`

1. Add batch buffer for pending commits
2. Implement group commit with configurable batch size and timeout
3. Add metrics for batch efficiency

```go
type GroupCommit struct {
    pending   []*WALRecord
    mu        sync.Mutex
    batchSize int           // default: 64
    batchTime time.Duration // default: 10ms
    flushCh   chan struct{}
}

func (gc *GroupCommit) Append(rec *WALRecord) error {
    gc.mu.Lock()
    gc.pending = append(gc.pending, rec)
    shouldFlush := len(gc.pending) >= gc.batchSize
    gc.mu.Unlock()
    
    if shouldFlush {
        gc.flushCh <- struct{}{}
    }
    return nil
}

func (gc *GroupCommit) flushWorker() {
    ticker := time.NewTicker(gc.batchTime)
    for {
        select {
        case <-gc.flushCh:
            gc.doFlush()
        case <-ticker.C:
            gc.doFlush()
        }
    }
}
```

**Tests:**
- Benchmark group commit vs individual commits
- Verify no data loss under concurrent writes
- Measure latency impact

**Expected Impact:** 2-3x INSERT throughput

---

## Phase 2: Row-Level Locks (5-10x concurrent write improvement)

### Problem
VaultDB uses per-table locks, blocking all writes to a table. PostgreSQL uses row-level locks.

### PostgreSQL Reference
PostgreSQL uses `tuple locks` (xmax field in tuple header) for row-level locking. Only conflicting rows are blocked.

### Implementation

**Files:**
- `server/internal/storage/page_lock.go`
- `server/internal/storage/page_engine_io.go`

1. Add row-level lock tracking in tuple headers
2. Implement lock upgrade: row → page → table
3. Add lock conflict detection

```go
// Add to tuple header (currently 16 bytes)
type TupleHeader struct {
    CreatedTx   uint64
    DeletedTx   uint64
    LockTx      uint64 // NEW: transaction holding row lock
    LockType    uint8  // NEW: 0=none, 1=share, 2=exclusive
}

// Row-level lock manager
type RowLockManager struct {
    locks map[string]*RowLock // key = "table:rowID"
    mu    sync.RWMutex
}

type RowLock struct {
    TxID    uint64
    Type    LockType
    Held    time.Time
    Timeout time.Duration
}
```

**Tests:**
- Concurrent updates to different rows (should succeed)
- Concurrent updates to same row (one should wait/retry)
- Lock timeout behavior

**Expected Impact:** 5-10x concurrent writes to same table

---

## Phase 3: Query Plan Caching (2-3x repeated query improvement)

### Problem
Every query is parsed and optimized from scratch. PostgreSQL caches plans.

### PostgreSQL Reference
PostgreSQL uses `plan_cache` to store prepared statement plans. Plans are invalidated on DDL changes.

### Implementation

**Files:**
- `server/internal/executor/plan_cache.go` (extend existing)
- `server/internal/executor/executor.go`

1. Extend existing plan cache to store full execution plans
2. Add cache key generation (query hash + schema version)
3. Add invalidation on DDL changes

```go
type PlanCache struct {
    cache    map[uint64]*CachedPlan
    mu       sync.RWMutex
    maxSize  int
    ttl      time.Duration
}

type CachedPlan struct {
    Plan       *ExecutionPlan
    SchemaVer  uint64
    CreatedAt  time.Time
    HitCount   int64
}

func (pc *PlanCache) Get(queryHash uint64, schemaVer uint64) (*CachedPlan, bool) {
    pc.mu.RLock()
    defer pc.mu.RUnlock()
    
    plan, ok := pc.cache[queryHash]
    if !ok || plan.SchemaVer != schemaVer {
        return nil, false
    }
    plan.HitCount++
    return plan, true
}
```

**Tests:**
- Cache hit rate measurement
- Invalidation on DDL
- Memory usage under load

**Expected Impact:** 2-3x for repeated queries

---

## Phase 4: Binary Catalog (2x DDL improvement)

### Problem
Catalog stored as JSON, parsed on every access. PostgreSQL uses binary format.

### PostgreSQL Reference
PostgreSQL stores catalog in `pg_catalog` tables with binary tuple format.

### Implementation

**Files:**
- `server/internal/storage/catalog.go`
- `server/internal/storage/page_engine.go`

1. Replace JSON catalog with binary format
2. Memory-map catalog for fast access
3. Add versioning for concurrent reads

```go
// Binary catalog format
type BinaryCatalog struct {
    Magic       [4]byte  // "VDBC"
    Version     uint32
    TableCount  uint32
    Tables      []BinaryTableEntry
}

type BinaryTableEntry struct {
    NameLen     uint16
    Name        []byte
    ColumnCount uint16
    Columns     []BinaryColumnEntry
    RowCount    uint64
    Flags       uint32
}

// Memory-mapped catalog
type MappedCatalog struct {
    data   []byte
    header *BinaryCatalog
    tables map[string]*BinaryTableEntry
}
```

**Tests:**
- Read/write performance comparison
- Concurrent access safety
- Migration from JSON format

**Expected Impact:** 2x DDL operations, 1.5x metadata reads

---

## Phase 5: Index-Only Scans (2-3x indexed SELECT improvement)

### Problem
Index lookups always fetch full rows. PostgreSQL can return data directly from index.

### PostgreSQL Reference
PostgreSQL's `IndexOnlyScan` returns columns stored in the index, avoiding heap fetch.

### Implementation

**Files:**
- `server/internal/index/btree.go`
- `server/internal/executor/optimizer_core.go`

1. Add column storage to B-tree index entries
2. Implement index-only scan in optimizer
3. Add visibility map to track all-visible pages

```go
// Extended B-tree entry with stored columns
type BTreeEntry struct {
    Key       string
    RowIDs    []uint64
    // NEW: stored columns for index-only scan
    StoredCols map[string]interface{}
}

// Visibility map
type VisibilityMap struct {
    allVisible map[uint64]bool // pageID -> all tuples visible
}
```

**Tests:**
- Index-only scan vs regular scan performance
- Visibility map accuracy
- Fallback when visibility unknown

**Expected Impact:** 2-3x for queries covered by index

---

## Phase 6: Connection Pooling (2x concurrent connection improvement)

### Problem
Each connection creates new session. PostgreSQL reuses connections via pooler.

### PostgreSQL Reference
PostgreSQL uses `pgbouncer` or built-in connection pooling for connection reuse.

### Implementation

**Files:**
- `server/internal/pool/pool.go` (extend existing)

1. Implement connection/ session reuse
2. Add connection health checks
3. Add idle connection cleanup

```go
type SessionPool struct {
    sessions    chan *Session
    maxIdle     int
    maxOpen     int
    idleTimeout time.Duration
    healthCheck func(*Session) bool
}

func (p *SessionPool) Get(ctx context.Context) (*Session, error) {
    select {
    case sess := <-p.sessions:
        if p.healthCheck(sess) {
            return sess, nil
        }
        // Session unhealthy, create new
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
        // Pool empty, create new if under limit
    }
    return p.createNew()
}
```

**Tests:**
- Connection reuse rate
- Health check effectiveness
- Pool exhaustion behavior

**Expected Impact:** 2x concurrent connections

---

## Phase 7: Buffer Pool Optimization (1.5x general improvement)

### Problem
Buffer pool uses basic LRU. PostgreSQL uses clock-sweep with usage counts.

### PostgreSQL Reference
PostgreSQL's shared buffers use clock-sweep algorithm with reference counts for better eviction decisions.

### Implementation

**Files:**
- `server/internal/storage/buffer_pool.go`

1. Replace LRU with clock-sweep algorithm
2. Add usage counts for better eviction
3. Implement buffer pins for hot pages

```go
type ClockSweep struct {
    buffers    []*Buffer
    hand       int
    clockHand  int
    numBuffers int
}

type Buffer struct {
    Page       *Page
    UsageCount uint8
    PinCount   int32
    Dirty      bool
    ClockBits  uint8
}

func (cs *ClockSweep) Evict() *Buffer {
    for {
        buf := cs.buffers[cs.clockHand]
        cs.clockHand = (cs.clockHand + 1) % cs.numBuffers
        
        if buf.PinCount > 0 {
            continue
        }
        
        if buf.UsageCount > 0 {
            buf.UsageCount--
            continue
        }
        
        return buf
    }
}
```

**Tests:**
- Hit rate comparison LRU vs clock-sweep
- Eviction behavior under pressure
- Pin/unpin correctness

**Expected Impact:** 1.5x for cache-bound workloads

---

## Phase 8: Parallel Query Execution (2-3x large query improvement)

### Problem
Queries execute single-threaded. PostgreSQL supports parallel queries.

### PostgreSQL Reference
PostgreSQL can parallelize sequential scans, joins, and aggregations across multiple workers.

### Implementation

**Files:**
- `server/internal/executor/commands_select.go`
- `server/internal/executor/select_result.go`

1. Add parallel scan coordinator
2. Implement parallel aggregation
3. Add work distribution for joins

```go
type ParallelCoordinator struct {
    numWorkers int
    results    chan *Result
    wg         sync.WaitGroup
}

func (pc *ParallelCoordinator) ParallelScan(table string, predicate func(*Row) bool) *Result {
    // Divide page ranges among workers
    ranges := pc.splitPageRanges(table, pc.numWorkers)
    
    for _, r := range ranges {
        pc.wg.Add(1)
        go func(rng PageRange) {
            defer pc.wg.Done()
            result := pc.scanRange(table, rng, predicate)
            pc.results <- result
        }(r)
    }
    
    pc.wg.Wait()
    return pc.mergeResults()
}
```

**Tests:**
- Parallel vs sequential scan performance
- Worker scaling efficiency
- Result correctness

**Expected Impact:** 2-3x for large table scans

---

## Implementation Order

| Phase | Impact | Complexity | Dependencies |
|-------|--------|------------|--------------|
| 1. Group Commit | High | Medium | None |
| 2. Row-Level Locks | High | High | None |
| 3. Plan Caching | Medium | Low | None |
| 4. Binary Catalog | Medium | Medium | None |
| 5. Index-Only Scans | Medium | High | None |
| 6. Connection Pooling | Medium | Low | None |
| 7. Buffer Pool | Low | Medium | None |
| 8. Parallel Queries | High | High | Phase 7 |

**Recommended order:** 1 → 3 → 6 → 7 → 4 → 2 → 5 → 8

---

## Success Metrics

| Metric | Current | Target (PostgreSQL-like) |
|--------|---------|-------------------------|
| INSERT (single) | 244 ops/s | 10,000+ ops/s |
| INSERT (batch) | 1,714 rows/s | 50,000+ rows/s |
| SELECT (indexed) | 4,255 ops/s | 20,000+ ops/s |
| UPDATE | 152 ops/s | 5,000+ ops/s |
| Concurrent txns | 1-2/100 | 100+ concurrent |
| Latency (p99) | ~10ms | <1ms |

---

## Estimated Timeline

| Phase | Duration | Milestone |
|-------|----------|-----------|
| Phase 1 | 1 week | Group commit working |
| Phase 3 | 3 days | Plan cache active |
| Phase 6 | 3 days | Session pooling |
| Phase 7 | 1 week | Clock-sweep buffer pool |
| Phase 4 | 1 week | Binary catalog |
| Phase 2 | 2 weeks | Row-level locks |
| Phase 5 | 1 week | Index-only scans |
| Phase 8 | 2 weeks | Parallel queries |

**Total: ~8 weeks for all phases**

---

## Risk Assessment

| Phase | Risk | Mitigation |
|-------|------|------------|
| Group Commit | Data loss if batch fails | Write-ahead batch + fallback to sync |
| Row-Level Locks | Deadlocks | Lock ordering + timeout |
| Plan Caching | Stale plans | Invalidation on DDL |
| Parallel Queries | Race conditions | Immutable data snapshots |
