# VaultDB

SQL-compatible database server with Go backend and C++ clients.

## Features

- **SQL Support**: SELECT, INSERT, UPDATE, DELETE, JOIN, CTE, MERGE, TRUNCATE, UPSERT
- **Dual Storage**: JSON (file-based) and Page (binary heap pages)
- **WAL**: Write-Ahead Logging for crash recovery (ARIES protocol)
- **MVCC**: Multi-Version Concurrency Control with time travel
- **Query Optimizer**: Cost-based optimizer with statistics
- **Indexes**: Hash and B-tree indexes
- **Buffer Pool**: LRU page cache
- **Concurrent Writes**: Page-level locking
- **Transactions**: BEGIN/COMMIT/ROLLBACK with conflict detection
- **Authentication**: HMAC-SHA256 tokens
- **TLS Support**: Self-signed certificate generation
- **Rate Limiting**: Token bucket algorithm
- **Monitoring**: Prometheus metrics, health checks, dashboard

## Quick Start

### Build from source

```bash
cd server
go build -o vaultdb-server ./cmd/vaultdb-server
```

### Run server

```bash
./vaultdb-server --host 0.0.0.0 --port 5432 --data ./data
```

### Run with TLS

```bash
./vaultdb-server --tls-cert=cert.pem --tls-key=key.pem
```

### Connect

```bash
# TCP connection
psql -h localhost -p 5432

# HTTP API
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"query": "SELECT 1 + 1;"}'
```

## Configuration

### vaultdb.yaml

```yaml
server:
  host: "0.0.0.0"
  port: 5432
  http_port: 8080
  monitor_port: 5433
  max_request_size_bytes: 67108864

storage:
  engine: "json"  # или "page"
  data_dir: "/data"

auth:
  enabled: true

ai:
  enabled: false
  provider: "noop"
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| VAULTDB_HOST | Server host |
| VAULTDB_PORT | TCP port |
| VAULTDB_HTTP_PORT | HTTP port |
| VAULTDB_DATA_DIR | Data directory |
| VAULTDB_AUTH_ENABLED | Enable auth (true/false) |
| VAULTDB_API_TOKENS | API tokens (token:label) |
| VAULTDB_AUTH_SECRET | HMAC secret for tokens |
| VAULTDB_AI_API_KEY | AI embedding API key |

## SQL Reference

### DDL
- `CREATE DATABASE name`
- `DROP DATABASE name`
- `USE name`
- `CREATE TABLE name (col TYPE, ...)`
- `DROP TABLE name`
- `ALTER TABLE name ADD/DROP/RENAME COLUMN`
- `CREATE INDEX name ON table (column)`
- `DROP INDEX name`

### DML
- `INSERT INTO table VALUES (...)`
- `INSERT INTO table SELECT ... FROM ...`
- `INSERT INTO table ON CONFLICT DO NOTHING/UPDATE`
- `UPDATE table SET col = val WHERE ...`
- `UPDATE t1 SET col = val FROM t2 WHERE ...`
- `DELETE FROM table WHERE ...`
- `TRUNCATE TABLE table`

### DQL
- `SELECT [DISTINCT] columns FROM table [WHERE ...] [GROUP BY ...] [HAVING ...] [ORDER BY ...] [LIMIT n] [OFFSET n]`
- `SELECT * FROM t1 LEFT/RIGHT/FULL/INNER/CROSS JOIN t2 ON condition`
- `SELECT * FROM t1 UNION/INTERSECT/EXCEPT SELECT * FROM t2`
- `SELECT * FROM t WHERE EXISTS (SELECT ...)`
- `SELECT * FROM t WHERE col BETWEEN a AND b`
- `WITH cte AS (...) SELECT * FROM cte`

### Transactions
- `BEGIN` / `START TRANSACTION`
- `COMMIT`
- `ROLLBACK`
- `SAVEPOINT name`
- `ROLLBACK TO SAVEPOINT name`
- `RELEASE SAVEPOINT name`

### Functions
- String: `UPPER`, `LOWER`, `LENGTH`, `SUBSTRING`, `TRIM`, `REPLACE`, `LPAD`, `RPAD`, `REVERSE`, `INITCAP`, `LEFT`, `RIGHT`, `POSITION`, `STRING_AGG`
- Numeric: `ABS`, `CEIL`, `FLOOR`, `ROUND`, `MOD`, `POWER`, `SQRT`, `SIGN`, `GREATEST`, `LEAST`
- Date: `NOW`, `CURRENT_DATE`, `CURRENT_TIME`, `CURRENT_TIMESTAMP`, `DATE_TRUNC`, `EXTRACT`, `AGE`, `TO_DATE`, `TO_TIMESTAMP`, `TO_CHAR`
- Aggregate: `COUNT`, `SUM`, `AVG`, `MIN`, `MAX`, `BOOL_AND`, `BOOL_OR`, `STDDEV`, `VARIANCE`
- Window: `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, `FIRST_VALUE`, `LAST_VALUE`, `NTILE`
- Other: `COALESCE`, `NULLIF`, `CASE WHEN ... THEN ... END`, `CAST(x AS type)`

### Data Types
- `INT` — 64-bit integer
- `FLOAT` — 64-bit float
- `BOOL` — boolean
- `TEXT` — text string
- `VARCHAR(n)` — variable-length string

## Architecture

```
Client (C++) → TCP/HTTP → Lexer → Parser → Executor → Storage Engine
                                          ↓
                                    Transaction Manager
                                          ↓
                                    WAL (crash recovery)
                                          ↓
                                    Buffer Pool (LRU cache)
                                          ↓
                                    Heap Files (disk)
```

## Development

```bash
# Run tests
cd server
go test ./... -v

# Run with race detector
go test ./... -race

# Build
go build ./cmd/vaultdb-server
```

## License

MIT
