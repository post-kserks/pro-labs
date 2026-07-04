# VaultDB Storage Engine Rewrite Plan

> **Goal:** Rewrite storage engine to match PostgreSQL-level performance and reliability.

**Reference:** PostgreSQL 16 source code (heapam, smgr, bufmgr, wal).

**Current State:** 30-65x slower than PostgreSQL due to fundamental architectural issues.

---

## Critical Issues Found (Top 10)

| # | Issue | Impact | Difficulty |
|---|-------|--------|------------|
| 1 | Global `e.mu` serializes ALL DML across ALL tables | Single-writer bottleneck | High |
| 2 | Double full-table scan in UPDATE/DELETE | 2x read amplification | Medium |
| 3 | BTree index is sorted slice (O(n) insert/delete) | Index degradation at scale | High |
| 4 | Per-batch `t.heap.Sync()` on every write | Synchronous fsync kills throughput | Medium |
| 5 | Buffer pool too small (1024 pages = 8MB) | Constant evictions | Low |
| 6 | WAL payloads serialized as JSON | 3-5x WAL bloat | Medium |
| 7 | No read-ahead for sequential scans | One-syscall-per-page | Medium |
| 8 | `PageCount()`/`AllocatePage()` call `Stat()` syscalls | Kernel round-trip per operation | Low |
| 9 | Table-level write lock prevents concurrent page writes | Page-level locking wasted | High |
| 10 | Row lock map never cleaned up | Unbounded memory growth | Low |

---

## Phase 1: Eliminate Global Lock Contention (Week 1)

### Problem
`e.mu` is acquired twice per DML operation (txID allocation + catalog update), serializing ALL writes across ALL tables.

### Solution

**1.1 Replace txID allocation with atomic counter**

```go
type PageStorageEngine struct {
    // Replace: CurrentTxID uint64 + e.mu for allocation
    txCounter atomic.Uint64
}

func (e *PageStorageEngine) nextTxID() uint64 {
    return e.txCounter.Add(1)
}
```

**1.2 Move catalog updates to per-table atomic counters**

```go
type pageTable struct {
    // ... existing fields
    rowCount   atomic.Int64  // replaces catalog.RowCounts[key]
    lastTxID   atomic.Uint64 // replaces catalog.LastModified[key]
}
```

**1.3 Use RWMutex for catalog reads only**

```go
// Before: e.mu.Lock() for every DML
// After: e.mu.RLock() for reads, no lock for writes (atomic counters)
```

**Expected Impact:** 3-5x throughput for multi-table workloads.

---

## Phase 2: Fix Double Scan in UPDATE/DELETE (Week 1)

### Problem
`mutateRows` scans entire table twice: once to find slots, once to apply mutations.

### Solution

**Single-pass scan with pre-allocated positions:**

```go
func (e *PageStorageEngine) mutateRows(...) (int, error) {
    // Single scan that records both slot positions and row data
    type locatedTuple struct {
        pid       page.PageID
        slot      uint16
        createdTx uint64
        row       Row
    }
    
    var located []locatedTuple
    e.scanTuples(t, func(pid page.PageID, pg *page.Page, slot uint16,
        createdTx, deletedTx uint64, row Row) (bool, error) {
        if deletedTx != 0 { return false, nil }
        if wanted[pos] {
            located = append(located, locatedTuple{pid, slot, createdTx, row})
        }
        pos++
        return false, nil
    })
    
    // Use `located` for both WAL recording AND mutation
    for _, loc := range located {
        // Apply mutation directly
    }
}
```

**Expected Impact:** 2x reduction in read amplification for UPDATE/DELETE.

---

## Phase 3: Replace Sorted-Slice BTree with Real B-Tree (Week 2-3)

### Problem
Current BTree is a sorted slice with O(n) insert/delete. Unusable at scale.

### Solution

**3.1 Use Google's B-Tree library (in-memory)**

```go
import "github.com/google/btree"

type BTreeIndex struct {
    tree *btree.BTree
    mu   sync.RWMutex
}

type btreeEntry struct {
    key       string
    positions []int
}

func (e *btreeEntry) Less(than btree.Item) bool {
    return e.key < than.(*btreeEntry).key
}
```

**3.2 Add on-disk B-Tree for large indexes**

For indexes that don't fit in memory:
```go
type DiskBTree struct {
    rootPageID uint32
    heapFile   *heap.HeapFile
    fanout     int  // typically 200-400
}
```

**Expected Impact:** O(log n) insert/delete instead of O(n).

---

## Phase 4: Remove Per-Batch Sync (Week 2)

### Problem
`t.heap.Sync()` called after every batch insert/mutate, forcing synchronous fsync.

### Solution

**Durability via WAL only, sync only at checkpoints:**

```go
func (e *PageStorageEngine) InsertRows(...) (int, error) {
    // ... write tuples, update WAL ...
    
    // REMOVE: t.heap.Sync()
    // Durability guaranteed by WAL
    
    return affected, nil
}

// Checkpoint handles actual sync
func (e *PageStorageEngine) doCheckpoint() {
    e.bufPool.FlushDirtyPagesUpToLSN(...)
    // Sync happens here, not per-batch
}
```

**Expected Impact:** 2-3x INSERT throughput.

---

## Phase 5: Optimize Buffer Pool (Week 2)

### Problem
Default 1024 pages (8MB) causes constant evictions.

### Solution

**5.1 Make buffer pool configurable**

```go
type Config struct {
    BufferPoolPages int `yaml:"buffer_pool_pages"` // default: 16384 (128MB)
}
```

**5.2 Add sequential scan awareness**

```go
type SequentialScanTracker struct {
    recentScans []uint32 // tableIDs of recent sequential scans
    ringBuffer  *RingBuffer // separate buffer for seq scans
}
```

**5.3 Cache page counts**

```go
type HeapFile struct {
    // ... existing fields
    cachedPageCount uint32
    pageCountValid  bool
}

func (hf *HeapFile) PageCount() uint32 {
    if hf.pageCountValid {
        return hf.cachedPageCount
    }
    // ... compute and cache
}
```

**Expected Impact:** 1.5-2x for cache-bound workloads.

---

## Phase 6: Binary WAL Payloads (Week 3)

### Problem
WAL payloads are JSON-encoded, causing 3-5x bloat.

### Solution

**Binary WAL record format:**

```go
type WALPageInsertBinary struct {
    DB         string
    Table      string
    SegmentNo  uint16
    PageNo     uint32
    SlotNo     uint16
    XID        uint64
    TupleData  []byte // raw binary tuple
}

func encodeWALPageInsert(p *WALPageInsertPayload) []byte {
    var buf bytes.Buffer
    writeLengthPrefixedString(&buf, p.DB)
    writeLengthPrefixedString(&buf, p.Table)
    binary.Write(&buf, binary.LittleEndian, uint16(p.SegmentNo))
    binary.Write(&buf, binary.LittleEndian, uint32(p.PageNo))
    binary.Write(&buf, binary.LittleEndian, uint16(p.SlotNo))
    binary.Write(&buf, binary.LittleEndian, uint64(p.XID))
    buf.Write(p.TupleData)
    return buf.Bytes()
}
```

**Expected Impact:** 2-3x WAL throughput, 2-3x faster recovery.

---

## Phase 7: Add Read-Ahead for Sequential Scans (Week 3)

### Problem
No prefetching for sequential scans, one syscall per page.

### Solution

**7.1 Implement read-ahead in HeapFile**

```go
func (hf *HeapFile) ReadPageAhead(pid page.PageID,-ahead int) ([]*page.Page, error) {
    pages := make([]*page.Page, ahead)
    for i := 0; i < ahead; i++ {
        nextPid := page.PageID{
            TableID:   pid.TableID,
            SegmentNo: pid.SegmentNo,
            PageNo:    pid.PageNo + uint32(i),
        }
        pages[i] = &page.Page{}
        go hf.ReadPage(nextPid, pages[i])
    }
    return pages, nil
}
```

**7.2 Add sequential scan detection**

```go
func (e *PageStorageEngine) detectSequentialScan(table string, startPage uint32) bool {
    // If reading pages in order, enable read-ahead
    // ...
}
```

**Expected Impact:** 2x sequential scan throughput.

---

## Phase 8: Decompose God Object (Week 4)

### Problem
`PageStorageEngine` is 2000+ lines handling everything.

### Solution

**Split into focused subsystems:**

```go
type StorageEngine struct {
    catalog  *CatalogManager    // metadata
    bufPool  *BufferPool        // page caching
    wal      *WAL               // write-ahead log
    txMgr    *TxManager         // transaction management
    pageLock *PageLockManager   // page locking
    rowLock  *RowLockManager    // row locking
    dml      *DMLExecutor       // insert/update/delete
    ddl      *DDLExecutor       // create/alter/drop
    indexer  *IndexManager      // index maintenance
}

type CatalogManager struct {
    databases map[string]*Database
    mu        sync.RWMutex
}

type DMLExecutor struct {
    engine *StorageEngine
}

func (d *DMLExecutor) InsertRows(dbName, tableName string, rows []Row) (int, error) {
    // Focused DML logic only
}
```

**Expected Impact:** Better maintainability, easier optimization.

---

## Implementation Order

| Phase | Impact | Complexity | Dependencies |
|-------|--------|------------|--------------|
| 1. Global Lock | Critical | High | None |
| 2. Double Scan | Critical | Medium | None |
| 3. B-Tree | Critical | High | None |
| 4. Remove Sync | High | Medium | Phase 1 |
| 5. Buffer Pool | High | Low | None |
| 6. Binary WAL | High | Medium | None |
| 7. Read-Ahead | Medium | Medium | Phase 5 |
| 8. Decompose | Medium | High | None |

**Recommended order:** 1 → 2 → 4 → 5 → 6 → 3 → 7 → 8

---

## Target Metrics After Rewrite

| Metric | Current | Wave 1+2 | After Rewrite |
|--------|---------|----------|---------------|
| INSERT (single) | 300 ops/s | ~400 ops/s | 5,000+ ops/s |
| INSERT (batch) | 12,886 rows/s | ~15,000 rows/s | 100,000+ rows/s |
| SELECT (indexed) | 6,000 ops/s | ~8,000 ops/s | 30,000+ ops/s |
| UPDATE | 200 ops/s | ~300 ops/s | 2,000+ ops/s |
| Concurrent txns | 50/100 | ~70/100 | 95+/100 |
| Latency (p99) | 8ms | ~5ms | <1ms |

---

## Risk Assessment

| Phase | Risk | Mitigation |
|-------|------|------------|
| Global Lock | Race conditions | Extensive concurrency testing |
| Double Scan | Data corruption | Verify WAL correctness |
| B-Tree | Memory usage | Disk-backed fallback |
| Remove Sync | Data loss | WAL-only durability |
| Binary WAL | Format migration | Dual-format support |

---

## Estimated Timeline

| Phase | Duration | Milestone |
|-------|----------|-----------|
| Phase 1 | 1 week | No global lock contention |
| Phase 2 | 3 days | Single-pass UPDATE/DELETE |
| Phase 4 | 3 days | No per-batch sync |
| Phase 5 | 3 days | Configurable buffer pool |
| Phase 6 | 1 week | Binary WAL payloads |
| Phase 3 | 2 weeks | Real B-Tree index |
| Phase 7 | 1 week | Sequential scan read-ahead |
| Phase 8 | 1 week | Decomposed architecture |

**Total: ~7 weeks for complete rewrite**
