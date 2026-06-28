# VaultDB

SQL-compatible database server with Go backend, C++ clients, and interactive web lab.

## Interactive SQL Lab

A web-based interface for exploring VaultDB's capabilities in real time.

### Quick Start

```bash
# Build and start VaultDB server
cd server && go build -o ../vaultdb-server ./cmd/vaultdb-server
cd .. && ./vaultdb-server --host 127.0.0.1 --port 5432 --http-port 8080 --data ./data --config vaultdb.yaml.example

# Start the web lab
cd site && npm install && npm run dev
# Open http://localhost:3000
```

### Features

- **SQL Playground** — write and execute queries with syntax highlighting
- **Schema Explorer** — browse databases, tables, columns and indexes
- **Transaction Lab** — test BEGIN/COMMIT/ROLLBACK with step-by-step execution
- **Time Travel** — query historical data using AS OF and row versioning
- **Feature Gallery** — 10 interactive demos covering JOIN, CTE, Window Functions, JSONB, UPSERT, MERGE, Indexes, Aggregates, LIKE/Full-Text
- **Performance Dashboard** — real-time metrics (query counts, latency percentiles, connections)

### Running the Lab

The lab requires two processes:

1. **VaultDB server** (port 5432 TCP, 8080 HTTP, 5433 monitor)
2. **Lab web server** (port 3000, proxies to VaultDB)

```bash
# Terminal 1: VaultDB
./vaultdb-server --host 127.0.0.1 --port 5432 --http-port 8080 --data ./data

# Terminal 2: Lab
cd site && npm install && npm run dev
```

---

## Features

- **SQL Support**: SELECT, INSERT, UPDATE, DELETE, JOIN, CTE, MERGE, TRUNCATE, UPSERT, window functions
- **Storage Engine**: Binary page engine with Buffer Pool and page-level locking
- **WAL**: Write-Ahead Logging for crash recovery (ARIES protocol, streaming)
- **MVCC**: Multi-Version Concurrency Control with time travel
- **Query Optimizer**: Cost-based optimizer with statistics and selectivity estimation
- **Indexes**: Hash, B-tree, GIN, GiST, multi-column, auto PK indexes
- **Buffer Pool**: LRU page cache with per-table locking
- **Concurrent Writes**: Per-table locking (no global mutex contention)
- **Transactions**: BEGIN/COMMIT/ROLLBACK with conflict detection and spill to disk
- **Result Cache**: LRU cache with TTL and auto-invalidation
- **Binary Encoding**: Fast tuple serialization (no JSON overhead)
- **Authentication**: HMAC-SHA256 hashed tokens (server-secret keyed), with login rate-limiting
- **TLS Support**: mTLS, self-signed certificate generation
- **Rate Limiting**: Token bucket with trusted proxy support
- **Monitoring**: Prometheus metrics (p50/p95/p99), health checks, dashboard
- **AI/Embeddings**: Pluggable HTTP embedder for SEMANTIC_MATCH and AI_EMBED

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

The easiest way to run a query is the HTTP API:

```bash
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"query": "SELECT 1 + 1;"}'
```

> Note: TCP port 5432 does **not** speak the PostgreSQL wire protocol — `psql` cannot connect.
> It uses a custom newline-delimited JSON protocol consumed by the bundled C++ client in `client/`.

## Configuration

### vaultdb.yaml

```yaml
server:
  host: "0.0.0.0"
  port: 5432
  http_port: 8080
  monitor_port: 5433
  max_request_size_bytes: 67108864
  rate_limit_rps: 100
  rate_limit_burst: 200

storage:
  engine: "page"
  data_dir: "/data"
  result_cache_size: 256
  result_cache_ttl_seconds: 30

auth:
  enabled: true
  rate_window_seconds: 60
  max_fails: 10
  block_for_seconds: 300

ai:
  provider: ""
  endpoint: ""
  model: ""
  api_key: ""
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
| VAULTDB_LOG_LEVEL | Log level (debug/info) |

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
- `SELECT * FROM t AS OF TIMESTAMP '2024-01-01 00:00:00'` (time travel)

### Transactions (TCP client only)
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
- Other: `COALESCE`, `NULLIF`, `CASE WHEN ... THEN ... END`, `CAST(x AS type)`, `UUID`

### Data Types
- `INT` — 64-bit integer
- `FLOAT` — 64-bit float
- `BOOL` — boolean
- `TEXT` — text string
- `VARCHAR(n)` — variable-length string
- `JSONB` — JSON binary
- `VECTOR(n)` — float vector for similarity search

## Architecture

```
Client (C++) → TCP/HTTP → Lexer → Parser → Optimizer → Executor → Storage Engine
                                                ↓
                                          Transaction Manager
                                                ↓
                                          WAL (crash recovery)
                                                ↓
                                          Buffer Pool (LRU cache)
                                                ↓
                                          Heap Files (disk)
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full system map with module dependencies.

## Project Structure

```
├── server/                    # Go server
│   ├── cmd/vaultdb-server/    # Main entry point
│   ├── internal/              # Core modules (18 packages)
│   │   ├── ai/               # AI embedding provider
│   │   ├── auth/             # HMAC-SHA256 token auth
│   │   ├── config/           # YAML + env config loader
│   │   ├── executor/         # Command-pattern execution engine
│   │   ├── httpserver/       # HTTP/REST + embedded Web UI
│   │   ├── index/            # B-Tree, GIN, GiST, Hash indexes
│   │   ├── lexer/            # Hand-written SQL lexer
│   │   ├── parser/           # Recursive-descent SQL parser
│   │   ├── storage/          # Page engine + binary encoding
│   │   ├── txmanager/        # MVCC transaction manager
│   │   ├── wal/              # Write-Ahead Log with ARIES recovery
│   │   └── websocket/        # WebSocket bridge for live queries
│   └── vaultdb.go            # Embedded engine facade
├── client/                    # C++ client (libvaultdb, shell, TUI)
├── site/                      # Interactive SQL Lab (Node.js + Express)
├── tools/                     # Benchmark tools
├── data/                      # Runtime data directory
├── docs/                      # Documentation
└── docker-compose.yml         # Docker deployment
```

## Development

```bash
# Run tests
cd server && go test ./... -v

# Run with race detector
go test ./... -race

# Build
go build ./cmd/vaultdb-server

# Docker
docker compose up -d
```

## License

MIT
