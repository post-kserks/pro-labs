# Storage Engine

VaultDB's storage engine is a page-based system inspired by PostgreSQL, using 8KB pages with slotted layout, WAL-based durability, and MVCC versioning.

## Disk Layout

```
data_dir/
├── pagedb/
│   ├── _catalog.json              # Database/table metadata
│   ├── <database>/
│   │   └── <table>/
│   │       ├── _schema.json       # Column definitions
│   │       ├── _indexes.json      # Index metadata
│   │       ├── seg_0000.heap      # Heap file (segment 0)
│   │       ├── seg_0001.heap      # Heap file (segment 1)
│   │       ├── seg_0000.fsm       # Free Space Map
│   │       ├── idx_<name>.json    # Index data (B-tree, GIN, etc.)
│   │       └── idx_<name>.meta    # Index metadata
├── wal/
│   └── vaultdb.wal               # Write-ahead log
└── .generated-token               # Auto-generated auth token
```

## Page Structure

Each page is 8KB (8192 bytes) with the following layout:

```
┌─────────────────────────────────────────────┐
│              Page Header (24 bytes)          │
│  - Lower (offset to free space start)       │
│  - Upper (offset to tuple area start)       │
│  - FreeSpace                                │
│  - PageID (tableID, segmentNo, pageNo)      │
├─────────────────────────────────────────────┤
│          Item Pointers (growing →)           │
│  Each pointer: {offset uint16, length uint16│
│                 flag uint16}                │
├─────────────────────────────────────────────┤
│              Free Space                     │
├─────────────────────────────────────────────┤
│          Tuples (← growing from end)        │
│  [createdTx | deletedTx | colCount | data]  │
└─────────────────────────────────────────────┘
```

### Tuple Header

Each tuple has a 16-byte header:

| Offset | Size | Field | Description |
|--------|------|-------|-------------|
| 0-7 | 8 bytes | `createdTx` | Transaction ID that created this tuple (uint64 LE) |
| 8-15 | 8 bytes | `deletedTx` | Transaction ID that deleted this tuple (0 = live) (uint64 LE) |
| 16-17 | 2 bytes | `colCount` | Number of columns (uint16 LE) |
| 18+ | 2 bytes each | `colOffsets` | Start offset of each column |

After the offsets, column values follow as type-tagged variable-length data.

### Binary Tuple Encoding

Column values are encoded with type tags:

| Tag | Type | Encoding |
|-----|------|----------|
| `'i'` | INT | 8 bytes, uint64 LE (bit-cast from int64) |
| `'f'` | FLOAT | 8 bytes, uint64 LE (bit-cast from float64) |
| `'s'` | TEXT/VARCHAR | 2-byte length + UTF-8 bytes |
| `'j'` | JSON/JSONB | 2-byte length + JSON bytes |
| `'v'` | VECTOR | 2-byte length + float64 array |
| `'1'` | BOOL TRUE | 0 bytes |
| `'0'` | BOOL FALSE | 0 bytes |

## Heap Files

Heap files store table data as a sequence of 8KB pages.

### Segmentation

Each heap file is split into segments of 65,536 pages each (512 MB per segment):

```
seg_0000.heap  → pages 0-65535
seg_0001.heap  → pages 65536-131071
...
```

### Page Allocation

When inserting a tuple:
1. Check the last page of the heap file
2. If the page has space, insert directly
3. If the page is full, allocate a new page
4. New pages are appended to the current segment
5. When a segment is full, a new segment file is created

### Page Full Check

```
FreeSpace = Upper - Lower - (N * ItemPointerSize)
If FreeSpace < tupleSize + ItemPointerSize → page is full
```

## Table Partitioning

VaultDB supports RANGE and HASH partitioning.

### RANGE Partitioning

```sql
CREATE TABLE orders (
    id INT,
    order_date DATE,
    amount FLOAT
) PARTITION BY RANGE (order_date) (
    PARTITION p2023 VALUES LESS THAN ('2024-01-01'),
    PARTITION p2024 VALUES LESS THAN ('2025-01-01'),
    PARTITION p2025 VALUES LESS THAN ('2026-01-01')
);
```

Rows are automatically routed to the correct partition based on the partition key.

### HASH Partitioning

```sql
CREATE TABLE sessions (
    user_id INT,
    data TEXT
) PARTITION BY HASH (user_id) PARTITIONS 4;
```

Rows are distributed across partitions using FNV-32a hash.

### Partition Pruning

Queries with WHERE conditions on the partition key may skip irrelevant partitions. Currently, partition pruning is conservative and may return all partitions for complex predicates. Full predicate pushdown into partition pruning is planned for a future release.

## Memory Pool (sync.Pool)

VaultDB uses `sync.Pool` for hot `Row` allocations to reduce GC pressure. This is automatic — no configuration needed.

- Pool warms up within seconds of startup
- Works best with consistent query patterns
- Rows with capacity > 256 are discarded to prevent pool bloat

## Buffer Pool

The buffer pool is an in-memory cache of 8KB pages using Clock-Sweep eviction. Default size is 16384 pages (128 MB), configurable via `buffer_pool_pages`.

### Key Operations

| Operation | Description |
|-----------|-------------|
| `GetPage(pid)` | Returns a pinned page (from cache or disk) |
| `PinPage(pid)` | Increments pin count (prevents eviction) |
| `UnpinPage(pid, dirty)` | Decrements pin count, optionally marks dirty |
| `UnpinPageDirty(pid, lsn)` | Unpin + mark dirty + record LSN |
| `FlushPage(pid)` | Write a single page to disk |
| `FlushAll()` | Write all dirty pages to disk |
| `EvictOne()` | Evict unpinned page using Clock-Sweep algorithm |

### Eviction Policy

- Pages with `pinCount > 0` cannot be evicted
- Clock-Sweep algorithm scans pages and evicts unpinned, clean pages
- Dirty pages are flushed before eviction

## Free Space Map (FSM)

A max-binary-tree data structure for O(log n) free-space searches.

### Structure

- **Granularity**: 32 bytes per category (0-255 categories, covering 0-8128 free bytes)
- **Tree**: Complete binary tree stored as an array
  - `nodes[1]` = root
  - `nodes[2i]` / `nodes[2i+1]` = children
  - Leaves at `nodes[capacity..]` where capacity is next power-of-2 >= nPages
- Internal nodes hold `max(left, right)`

### Operations

| Operation | Complexity | Description |
|-----------|-----------|-------------|
| `Search(minFree)` | O(log n) | Find first page with >= minFree bytes |
| `Update(pageNo, freeBytes)` | O(log n) | Update page's free space category |
| `Grow(nPages)` | O(n) | Extend tree for more pages |

### Persistence

Stored as: `"VFSM" magic (4B) | nPages (4B LE) | leafCategories[nPages]`

Atomic write via temp+rename pattern.

## Catalog

The catalog (`_catalog.json`) tracks:

```json
{
  "current_tx_id": 12345,
  "last_modified": {
    "mydb/users": 12345,
    "mydb/orders": 12340
  },
  "row_counts": {
    "mydb/users": 1000,
    "mydb/orders": 5000
  },
  "tx_times": [
    {"tx_id": 1, "timestamp": "2026-07-01T10:00:00Z"},
    {"tx_id": 12345, "timestamp": "2026-07-01T14:30:00Z"}
  ],
  "checkpoint_lsn": 99999
}
```

- `tx_times` is capped at 10,000 entries (trimmed to 5,000)
- Catalog saves are deferred: mutations set `catalogDirty = true`
- Auto-save triggers every 100 mutations
- Final flush at checkpoint and Close()

## Schema Management

Each table has a `_schema.json`:

```json
{
  "name": "users",
  "columns": [
    {"name": "id", "type": "INT", "primary_key": true, "auto_increment": true},
    {"name": "name", "type": "TEXT", "not_null": true},
    {"name": "email", "type": "VARCHAR", "varchar_len": 255, "unique": true}
  ],
  "constraints": [
    {"type": "UNIQUE", "columns": ["email"]}
  ],
  "rls_enabled": false,
  "rls_policies": []
}
```

## Value Normalization

Incoming values are normalized to declared column types:

| Type | Coercion |
|------|----------|
| INT | Any numeric → `int64` (rejects non-integer floats) |
| FLOAT | Any numeric → `float64` |
| BOOL | Must be Go `bool` |
| TEXT | Must be Go `string` |
| VARCHAR | Must be `string`, checked against VarcharLen (rune count) |
| VECTOR | `[]float64` or `[]interface{}` → `[]float64` |
| FLEXIBLE | `map[string]interface{}` or JSON string |
| DATE/TIME/TIMESTAMP | `fmt.Sprintf("%v", value)` → string |

## Object Name Validation

Database and table names must:
- Be non-empty
- Max 128 characters
- Match `[a-zA-Z_][a-zA-Z0-9_]*`
- Not contain path separators (`/`, `\`)
- Not contain `..`
- Not contain null bytes
