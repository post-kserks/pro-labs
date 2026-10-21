# Transactions

VaultDB supports ACID transactions with BEGIN/COMMIT/ROLLBACK semantics, SAVEPOINTs, and optimistic concurrency control.

## Transaction Lifecycle

```
BEGIN
  ├── INSERT / UPDATE / DELETE (buffered)
  ├── COMMIT → writes WAL, applies to storage
  └── ROLLBACK → discards all buffered operations
```

## Asynchronous & Group Commit (`synchronous_commit`)

VaultDB supports configurable transaction durability modes via session variables:

- **`SET synchronous_commit = 'on'` (Default)**:
  - `COMMIT` calls `ctx.WAL.AppendWithTx(tx.ID, wal.OpCommit, nil)`.
  - Performs a synchronous write and immediate `fsync` syscall, guaranteeing on-disk durability before returning to the caller.
- **`SET synchronous_commit = 'off'`**:
  - `COMMIT` calls `ctx.WAL.AppendWithWriteBehind(tx.ID, wal.OpCommit, nil)`.
  - Queues commit records into the `WriteBehindBuffer` and `GroupCommit` worker (`internal/core/wal/group_commit.go`).
  - Returns immediately to the client without waiting for disk sync. The background `flushWorker` flushes pending batches when the batch size threshold (`batchSize`) is met or when the timer (`batchTime`) expires, amortizing `fsync` cost across multiple transactions.

## Distributed Raft Consensus Replication

For high-availability clusters, transactions can be replicated using Raft log consensus (`internal/cluster/raft`):

- **Raft State Engine (`node.go`)**: Each cluster node (`RaftNode`) operates as a `Follower`, `Candidate`, or `Leader`. Leaders maintain term counters, issue heartbeats, and process client mutations.
- **Quorum Commit Protocol (`replication.go`)**: `Replicator.AppendWithTx` coordinates multi-node replication:
  1. **Local Append**: Writes transaction records to the leader's local WAL.
  2. **Peer Replication**: Concurrently dispatches `AppendEntries` RPCs to all registered peer nodes.
  3. **Quorum Wait**: Waits until a strict majority (`(totalNodes / 2) + 1`) of cluster nodes acknowledge log entry persistence before committing and returning control to the caller.

## Basic Transactions

### Starting a Transaction

```sql
BEGIN;
```

### Committing

```sql
COMMIT;
```

### Rolling Back

```sql
ROLLBACK;
```

### Example

```sql
BEGIN;
INSERT INTO accounts (id, balance) VALUES (1, 1000);
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
UPDATE accounts SET balance = balance + 100 WHERE id = 2;
COMMIT;
```

## SAVEPOINTs

SAVEPOINTs allow partial rollback within a transaction.

```sql
BEGIN;
INSERT INTO logs VALUES (1, 'first');
SAVEPOINT sp1;
INSERT INTO logs VALUES (2, 'second');
ROLLBACK TO SAVEPOINT sp1;  -- discards second insert only
INSERT INTO logs VALUES (3, 'third');
COMMIT;  -- commits first and third inserts
```

### SAVEPOINT Commands

| Command | Description |
|---------|-------------|
| `SAVEPOINT name` | Create a savepoint |
| `ROLLBACK TO SAVEPOINT name` | Undo to savepoint, keep transaction open |
| `RELEASE SAVEPOINT name` | Remove savepoint (operations remain) |

### Cascading Rollback

Rolling back to a savepoint also undoes all savepoints created after it.

```sql
BEGIN;
SAVEPOINT sp1;
SAVEPOINT sp2;
ROLLBACK TO SAVEPOINT sp1;  -- sp2 is also removed
COMMIT;
```

## HTTP Transaction Support

HTTP API now supports multi-statement transactions via session tracking:

1. Include a `session_id` field in your requests
2. Execute `BEGIN` to start a transaction
3. Execute subsequent queries with the same `session_id`
4. Execute `COMMIT` or `ROLLBACK` to end the transaction

```json
{"database": "mydb", "query": "BEGIN", "session_id": "sess-123"}
{"database": "mydb", "query": "INSERT INTO users VALUES (1, 'Alice')", "session_id": "sess-123"}
{"database": "mydb", "query": "COMMIT", "session_id": "sess-123"}
```

Sessions expire after 5 minutes of inactivity. If a session expires, any active transaction is automatically rolled back.

**Without session_id:** Requests are auto-committed (stateless mode, previous behavior).

## Transaction over TCP

TCP connections support full transaction management:

```json
{"id": "1", "query": "BEGIN;"}
{"id": "2", "query": "INSERT INTO t VALUES (1);"}
{"id": "3", "query": "COMMIT;"}
```

## Optimistic Concurrency Control (OCC)

VaultDB uses OCC for conflict detection:

1. **RecordAccess**: On first read or write to a table, the current table version is snapshotted.
2. **Conflict detection at Commit**: Under per-table commit locks, each accessed table's current version is compared against the snapshot.
3. **Conflict resolution**: If the table version changed since the snapshot, the transaction is aborted with `ErrTxConflict`.

```sql
-- Transaction A
BEGIN;
SELECT * FROM accounts WHERE id = 1;  -- snapshots accounts version

-- Transaction B (concurrent)
BEGIN;
UPDATE accounts SET balance = 500 WHERE id = 1;  -- bumps accounts version
COMMIT;  -- succeeds

-- Transaction A continues
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
COMMIT;  -- FAILS: accounts version changed (conflict detected)
```

## Autocommit

Single statements outside an explicit transaction are auto-committed. Each auto-committed write acquires per-table commit locks to serialize with concurrent transactions.

## Automatic Rollback on Disconnect

When a TCP connection drops with an active transaction, VaultDB automatically rolls back the transaction to prevent orphaned locks.

## Isolation Levels

VaultDB supports multiple transaction isolation levels:

### Snapshot Isolation (Default)

The default isolation level provides snapshot isolation for reads within transactions:

- Reads see a consistent snapshot from the time the transaction started
- Writes are buffered until COMMIT
- Conflicts are detected at COMMIT time via OCC

### READ COMMITTED

Each statement within a transaction sees data committed by other transactions before the statement started.

```sql
BEGIN TRANSACTION ISOLATION LEVEL READ COMMITTED;
SELECT * FROM accounts WHERE id = 1;  -- sees committed data at statement start
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
COMMIT;
```

### REPEATABLE READ

All reads within a transaction see a snapshot from the start of the transaction.

```sql
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ;
SELECT * FROM accounts WHERE id = 1;  -- sees snapshot at transaction start
-- Even if other transactions modify this row, we see the same data
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
COMMIT;
```

### SERIALIZABLE (Serializable Snapshot Isolation / SSI)

The strictest isolation level. Transactions appear to execute serially, even if run concurrently.

VaultDB implements full Serializable Snapshot Isolation (SSI) via the `PredicateLockManager` (`internal/core/storage/predicate.go`):

- **SIREAD Locks**: Reads acquire SIREAD predicate locks on individual pages and index key ranges.
- **RW-Conflict Graph**: Tracks `rw-anti-dependencies` (when transaction $T_1$ reads a tuple version that is subsequently modified by transaction $T_2$).
- **Serialization Failure Detection**: If a cycle of two consecutive `rw-antidependencies` ($T_1 \to T_2 \to T_3$) is detected, the transaction is aborted with a serialization failure (`CheckSerializationFailure`).

```sql
BEGIN TRANSACTION ISOLATION LEVEL SERIALIZABLE;
SELECT * FROM accounts WHERE id = 1;
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
COMMIT;
```

## Two-Phase Commit (2PC)

For distributed multi-node transactions, VaultDB provides a 2PC engine (`internal/cluster/tx2pc/2pc.go`):

- **Coordinator & Participant**: Manages atomic commits across distributed cluster nodes.
- **Phase 1 (Prepare)**: Coordinator sends `PREPARE` request to participants, which validate transaction invariants and lock required resources.
- **Phase 2 (Commit / Abort)**: If all participants vote `PREPARED`, Coordinator sends `COMMIT`; otherwise sends `ABORT`.

## Spill-to-Disk

Large transactions (more than 10,000 pending operations) are automatically spilled to temporary files on disk to avoid excessive memory usage.

```go
// Internal: transactions with >10,000 ops spill to tx_<id>.tmp
// The spill file uses a custom wire format encoding parser.Expression types
```

## Savepoint Spill

Savepoints can reference spilled operations. Rolling back to a savepoint truncates the spill file to the savepoint's operation counter.

## WAL Integration

All write operations within a transaction are logged to the WAL:

1. Each INSERT/UPDATE/DELETE writes an operation record to the WAL
2. On COMMIT, a commit record is written
3. On ROLLBACK, an abort record is written
4. On crash, uncommitted transactions are automatically undone during WAL recovery

## Timeout

Queries can have a configurable timeout (default 30 seconds). When a query exceeds its timeout, it is cancelled via context cancellation.

```yaml
server:
  query_timeout_sec: 30  # Set to 0 for no timeout
```
