# Transactions

VaultDB supports ACID transactions with BEGIN/COMMIT/ROLLBACK semantics, SAVEPOINTs, and optimistic concurrency control.

## Transaction Lifecycle

```
BEGIN
  ├── INSERT / UPDATE / DELETE (buffered)
  ├── COMMIT → writes WAL, applies to storage
  └── ROLLBACK → discards all buffered operations
```

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

### SERIALIZABLE

The strictest isolation level. Transactions appear to execute serially, even if run concurrently.

```sql
BEGIN TRANSACTION ISOLATION LEVEL SERIALIZABLE;
SELECT * FROM accounts WHERE id = 1;
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
COMMIT;
```

**Note:** All isolation levels use Optimistic Concurrency Control (OCC) for conflict detection. Conflicts are detected at COMMIT time, and transactions are aborted if conflicts are found.

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
