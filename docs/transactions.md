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

## Transaction over HTTP

The HTTP API does not support multi-statement transactions. Each HTTP request is a single auto-committed statement. Use the TCP protocol for transactional work.

```json
POST /api/transaction
{
  "action": "begin",
  "database": "mydb"
}
```

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

## Isolation Level

VaultDB provides **snapshot isolation** for reads within transactions:

- Reads see a consistent snapshot from the time the transaction started
- Writes are buffered until COMMIT
- Conflicts are detected at COMMIT time via OCC

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
