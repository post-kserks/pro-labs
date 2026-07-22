# MVCC and Concurrency Control

VaultDB implements Multi-Version Concurrency Control (MVCC) to enable concurrent reads and writes without blocking.

## Versioning Model

Each tuple carries a version header:

```
[createdTx: uint64] [deletedTx: uint64] [colCount: uint16] [colOffsets...] [data...]
```

| Field | Size | Description |
|-------|------|-------------|
| `createdTx` | 8 bytes | Transaction ID that created this tuple version |
| `deletedTx` | 8 bytes | Transaction ID that deleted this tuple (0 = live) |

This is analogous to PostgreSQL's XMin/XMax pair.

## Write Operations

### INSERT

```
1. Allocate new txID
2. Set createdTx = txID, deletedTx = 0
3. Write new tuple to heap page
4. Write OpPageInsert to WAL
```

### DELETE

```
1. Allocate new txID
2. Find matching tuples
3. Set deletedTx = txID IN-PLACE (8-byte overwrite at offset 8-16)
4. Write OpPageDelete to WAL
```

No tuple is physically removed during DELETE. The tuple is simply marked as dead.

### UPDATE

When updating a row, VaultDB uses Heap-Only Tuples (HOT) if possible:

```
1. Allocate new txID
2. Set deletedTx = txID on old tuple (in-place)
3. If non-indexed columns updated AND same page has space:
   a. Create new tuple on SAME page with createdTx = txID, deletedTx = 0
   b. Set item pointer flag to ItemFlagRedirect (HOT chain created)
   c. Skip updating secondary indexes (index pointers remain unchanged)
4. Else (standard UPDATE):
   a. Create new tuple with createdTx = txID, deletedTx = 0 on heap target page
   b. Update secondary index entries to point to new tuple location
5. Write WAL entries for operations
```

The old tuple version remains on disk until vacuum reclaims the space.

## Read Visibility Rules

### Current Read (within transaction)

A tuple is visible if:
```
deletedTx == 0
AND createdTx is committed (confirmed by txManager)
```

### Snapshot Read (AS OF)

A tuple is visible at snapshot `asOf` if:
```
createdTx <= asOf
AND (deletedTx == 0 OR deletedTx > asOf)
```

### Time-Travel Query

```sql
-- Read table as it was at transaction 1000
SELECT * FROM users AS OF 1000;

-- Full row history
HISTORY users 1;  -- shows all versions of row with id=1
```

## Transaction Manager

### Transaction Lifecycle

```
Begin()
  → Allocate monotonically increasing txID
  → Create Transaction struct with empty Ops buffer

AddOp(op)
  → Buffer INSERT/UPDATE/DELETE operations
  → If > 10,000 ops: spill to disk (tx_<id>.tmp)

Commit()
  → Acquire per-table commit locks (sorted to prevent deadlock)
  → Check OCC conflicts (compare table versions against snapshots)
  → Apply buffered operations to storage
  → Write OpCommit to WAL
  → Release locks

Rollback()
  → Discard all buffered operations
  → Write OpAbort to WAL
```

### Optimistic Concurrency Control (OCC)

**RecordAccess**: On first access to a table, the current table version is snapshotted.

**Conflict detection at Commit**: Each accessed table's current version is compared against the snapshot. If the version changed (another transaction committed writes to the same table), the transaction is aborted with `ErrTxConflict`.

**Conflict example**:
```
Tx A: BEGIN → READ table X (version=100)
Tx B: BEGIN → WRITE table X → COMMIT (version becomes 101)
Tx A: WRITE table X → COMMIT → FAILS (version changed from 100 to 101)
```

### Autocommit Serialization

Single statements outside explicit transactions are auto-committed. Each auto-committed write acquires per-table commit locks via `mutateUnderTableLock`, ensuring serialization with concurrent transactions.

## Concurrency Hierarchy

```
Level 1: e.mu (global RWMutex)
  → Write-locked for DDL (CREATE/DROP/ALTER TABLE)
  → Read-locked for most read operations
  → Released as early as possible

Level 2: t.mu (per-table RWMutex)
  → Write-locked for DML (INSERT/UPDATE/DELETE)
  → Read-locked for SELECT
  → Taken AFTER e.mu, released BEFORE re-acquiring e.mu

Level 3: pageLock (per-page RWMutex)
  → Taken for individual page modifications during inserts
  → Sorted acquisition for multi-page locks

Level 4: txmgr.commitLocks (per-table mutex)
  → Serializes autocommit writes with transaction commits
  → Sorted acquisition to prevent deadlocks

Level 5: wal.mu (WAL mutex)
  → Serializes all WAL append operations
```

**Deadlock prevention**: Lock ordering is enforced (e.mu → t.mu → pageLock). Commit locks are acquired in sorted order by table name.

## Transaction Overlay

Within an active transaction, reads must see the transaction's own uncommitted writes. This is implemented via `applyTxOverlay()`:

```
1. Read committed rows from storage
2. Replay buffered operations in order:
   - INSERT: append new rows
   - UPDATE: apply column assignments to matching rows
   - DELETE: filter out matching rows
   - TRUNCATE: empty the result set
3. Return modified result set
```

### Volatile Function Freezing

Functions like `NOW()`, `CURRENT_TIMESTAMP`, `UUID()` are evaluated once at buffer time and replaced with literal values in the AST. This ensures overlay and commit-apply produce identical results.

## Spill-to-Disk

Large transactions (> 10,000 pending operations) are automatically spilled to temporary files:

```
tx_<id>.tmp
```

The spill file uses a custom wire format encoding `parser.Expression` types as tagged JSON. Subsequent operations append to the file. On commit, the file is read back and operations are applied.

## Savepoints

| Operation | Behavior |
|-----------|----------|
| `SAVEPOINT sp1` | Records current opCounter |
| `ROLLBACK TO sp1` | Truncates ops to savepoint position, removes later savepoints |
| `RELEASE sp1` | Removes savepoint marker (operations remain) |

Cascading rollback: rolling back to a savepoint also undoes all savepoints created after it.

## Background Storage Maintenance & MVCC

### AutoVacuum Worker (`AutoVacuumWorker`)

The `AutoVacuumWorker` (`internal/core/storage/vacuum.go`) runs periodically in the background to reclaim space occupied by dead tuples (`deletedTx != 0` where `deletedTx < MinActiveTxID`):

```
1. Periodically triggers RunVacuumAll() based on configured ticker interval
2. Creates shadow directory for safe atomic rebuilds
3. Scans heap pages and collects live tuples (deletedTx == 0 or active in transaction)
4. Compacts pages and resolves HOT redirect chains
5. Atomically replaces original table files with shadow tables
6. Updates catalog row counts and updates Free Space Map (FSM)
```

The shadow file approach ensures crash safety: if a crash occurs before atomic replacement, the original table remains intact.

### Checkpointer Worker (`CheckpointerWorker`)

The `CheckpointerWorker` (`internal/core/storage/checkpointer.go`) operates alongside MVCC and the WAL to ensure dirty pages in the Buffer Pool are written to disk:

- **Periodic Dirty Page Flushing**: Scans unpinned pages in the buffer pool at configured intervals (`interval`, default 30s) and flushes them in batches (`batchSize`, default 64 pages).
- **LSN Ordering**: Ensures Write-Ahead Log (WAL) records are written before corresponding page dirty flushes (Write-Ahead Logging protocol).
- **Checkpoint Records**: Writes checkpoint LSNs into the catalog (`_catalog.json`) to truncate WAL replay logs during crash recovery.
