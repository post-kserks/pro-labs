# WAL and Crash Recovery

VaultDB uses a Write-Ahead Logging (WAL) protocol based on the ARIES algorithm to ensure transaction durability and enable crash recovery.

## WAL Record Format

Each WAL record has a fixed header plus variable-length payload:

```
┌─────────────────────────────────────────┐
│ Magic: "VDB1"          (4 bytes)        │
│ Transaction ID         (8 bytes, LE)    │
│ Operation Type         (1 byte)         │
│ Payload Length         (4 bytes, LE)    │
│ Payload                (variable)       │
│ CRC32 Checksum         (4 bytes, LE)    │
└─────────────────────────────────────────┘
```

**Fixed overhead**: 17 bytes header + 4 bytes CRC = 21 bytes per record.

## Operation Types

| Code | Name | Purpose |
|------|------|---------|
| `0x20` | `PageInsert` | Tuple insertion on a page |
| `0x21` | `PageDelete` | Mark tuple dead (set XMax) |
| `0x22` | `PageUpdateXMax` | XMax update on DELETE/UPDATE |
| `0x23` | `PageNewPage` | New page allocation |
| `0x30` | `VacuumBegin` | Vacuum operation started |
| `0x31` | `VacuumCommit` | Vacuum operation completed |
| `0x40` | `Abort` | Transaction aborted |
| `0x50` | `Commit` | Transaction committed |
| `0x60` | `SchemaWrite` | Schema file persistence |
| `0x61` | `RewriteBegin` | ALTER TABLE rewrite started |
| `0x62` | `RewriteData` | ALTER TABLE rewrite data |
| `0x63` | `RewriteCommit` | ALTER TABLE rewrite committed |
| `0x64` | `TruncateTable` | Bulk table reset |
| `0x70` | `FullPageImage` | Complete 8KB page image |
| `0xF0` | `Checkpoint` | Checkpoint marker |

## Fsync Batching

WAL appends are batched for performance:

- **Batch size**: 64 records (configurable)
- **Behavior**: Records are buffered in memory; `fsync` is called every 64 appends
- **Trade-off**: At most 64 unwritten records may be lost on crash, but throughput is significantly improved

## Corruption Recovery

On startup, `scanAndTruncate()` validates the WAL:

1. Stream through the WAL, validating each record's CRC32
2. On corruption, attempt resync by scanning byte-by-byte for the next `"VDB1"` magic
3. If resync succeeds, continue reading
4. If resync fails, truncate the corrupt tail
5. If truncation fails, rename the corrupt WAL and create a fresh one

## Recovery Protocol (ARIES-style)

`RecoverFromWAL()` executes three phases:

### Phase 1: Analysis

Scan the entire WAL stream:

- `OpCommit` entries mark their txID as **committed**
- `OpAbort` entries remove txIDs from the committed set
- All other non-zero txIDs are classified as **in-progress** (uncommitted)
- The txManager counter is bumped to at least `maxCommitted + 1`

### Phase 2: Redo

Replay ALL WAL entries (both committed AND in-progress):

| Operation | Redo Action |
|-----------|-------------|
| `PageInsert` | Restore tuple data onto the target page (allocate if needed) |
| `PageDelete` / `PageUpdateXMax` | Set the tuple's XMax field |
| `SchemaWrite` | Rewrite the schema JSON file |
| `TruncateTable` | Invalidate cache, remove segments, create fresh heap |
| `FullPageImage` | Overwrite the entire 8KB page |

### Phase 3: Undo

For each in-progress txID, replay that transaction's entries in **reverse** order and **invert** them:

| Operation | Undo Action |
|-----------|-------------|
| `PageInsert` | Set XMax = txID (marks the inserted tuple as dead) |
| `PageDelete` | Zero XMax (restores the deleted tuple) |

After undoing, an `OpAbort` record is appended to the WAL.

### Post-Recovery

1. Fsync all heap files
2. Recalculate catalog row counts by scanning all live tuples
3. Write a checkpoint record
4. Save the catalog with the checkpoint LSN
5. Truncate the WAL

## Checkpoint Protocol

`doCheckpoint()` follows a strict 5-step sequence:

```
1. Flush WAL to disk          → obtain current LSN
2. Flush dirty pages          → write pages with LSN <= current LSN to heap files
3. Write checkpoint record    → to WAL (before saving catalog)
4. Save catalog               → with checkpoint LSN to _catalog.json (atomic rename)
5. Truncate WAL               → all dirty pages are durable, WAL can be emptied
```

**Crash safety**: A crash at ANY point leaves the system recoverable because:
- Checkpoint record references the LSN of the flushed WAL
- Catalog is saved AFTER the checkpoint record
- WAL is truncated AFTER catalog is saved

### Checkpoint Loop

A background goroutine runs checkpoints at a configurable interval (default: 30 seconds). A final checkpoint is also written during graceful shutdown.

## Vacuum and WAL

Vacuum operations are logged to the WAL:

```
1. OpVacuumBegin  → marks vacuum start
2. (live tuples rewritten to shadow file)
3. OpVacuumCommit → marks vacuum end
```

If a crash occurs during vacuum, the recovery process:
- Sees the uncommitted vacuum
- The original heap file is still intact (shadow file is orphaned)
- `recoverOrphanedVacuums()` removes leftover `.vacuum` directories

## ALTER TABLE and WAL

Table rewrites (ADD COLUMN, DROP COLUMN) are logged as:

```
1. OpRewriteBegin
2. OpRewriteData  → (per-tuple transformation data)
3. OpRewriteCommit
```

Crash recovery: `recoverIncompleteRewrites()` removes leftover `.rewrite.tmp` directories.

## WAL Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| SyncBatchSize | 64 | Records between fsync calls |
| MaxTxTimesEntries | 10,000 | Max transaction timestamp entries |
| KeepTxTimesEntries | 5,000 | Entries to keep after trimming |

## Durability Guarantees

| Scenario | Behavior |
|----------|----------|
| Crash after COMMIT record | Transaction is durable (redo during recovery) |
| Crash before COMMIT record | Transaction is undone during recovery |
| Crash during batch fsync | Up to 64 recent records may be lost |
| Corrupt WAL record | CRC32 detection + resync or truncation |
| Corrupt heap page | Full page images in WAL enable restoration |
