# VaultDB

Enterprise SQL database engine with Go server, native TCP protocol, and official clients in Go, Python, and JavaScript/TypeScript.

**Version: 2.0.0** | **License: MIT**

---

## Why VaultDB?

VaultDB is not another lightweight embedded database. It's a full-featured SQL engine designed for organizations that need PostgreSQL-level capabilities with a unique set of built-in features:

| Advantage | What it means |
|-----------|---------------|
| **Time Travel out of the box** | Query data as it was at any point in the past — no separate tooling needed |
| **Transparent Data Encryption** | AES-256-GCM with envelope encryption (DEK/KEK), no config overhead |
| **Built-in RBAC** | CREATE ROLE / GRANT / REVOKE with dynamic permissions via SQL — no external identity provider required |
| **Crash-safe WAL** | Group commit, binary payloads, guaranteed recovery — no silent data loss |
| **Native protocol** | Own TCP protocol with v2 handshake — no PostgreSQL wire compatibility baggage |
| **WASM UDFs** | Run custom functions in a sandboxed WASM runtime with memory limits and export whitelist |
| **Partition pruning** | RANGE and HASH partitioning with predicate-based partition elimination |
| **Full-text search** | BM25 ranking with snippet highlighting built into the engine |
| **Audit log with hash-chain** | SHA-256 chained audit trail with `VERIFY AUDIT LOG` for tamper detection |
| **Production-grade security** | Token revocation, rate limiting, TLS enforcement, path sandboxing, 8-algorithm security audit |

---

## Features

### SQL Engine

- **DML**: INSERT, UPDATE, DELETE, UPSERT (ON CONFLICT), MERGE, TRUNCATE, COPY FROM/TO
- **DQL**: SELECT with JOIN, CTE (recursive), window functions, subqueries, DISTINCT ON
- **DDL**: CREATE/DROP/ALTER DATABASE/TABLE/INDEX/VIEW/TRIGGER/FUNCTION/PROCEDURE, UNIQUE constraints, UNIQUE indexes, FULLTEXT indexes
- **Types**: INT, BIGINT, FLOAT, BOOL, TEXT, VARCHAR, NUMERIC, JSONB, VECTOR, TIMESTAMPTZ, BLOB, ARRAY
- **JSONB operators**: `->`, `->>`, `@>`, `<@`, `||`, `?`
- **Table partitioning**: RANGE and HASH partitioning with predicate-based pruning
- **Full-text search**: BM25 ranking, snippet highlighting, stop words
- **Stored functions**: PL/pgSQL interpreter (DECLARE, BEGIN/END, RETURN, RETURN QUERY)
- **FTS operators**: FTS_MATCH, @@, bm25_score() relevance scoring
- **MERGE**: MERGE with table, subquery, and VALUES sources
- **HISTORY**: Query row version history with KEY and WHERE filters
- **RBAC**: CREATE ROLE, DROP ROLE, GRANT, REVOKE with dynamic permissions

### Security

- **TDE**: Transparent Data Encryption with AES-256-GCM, envelope encryption (DEK/KEK)
- **Authentication**: HMAC-SHA256 token-based auth with constant-time comparison
- **Token revocation**: Revoke compromised tokens via SQL or HTTP API (persisted to disk)
- **RBAC**: Role-based access control with SQL-managed roles (admin, writer, reader + custom)
- **Audit log**: Hash-chain integrity, SHA-256, periodic verification, `VERIFY AUDIT LOG` command
- **TLS**: Configurable enforcement, min version (1.2/1.3), mTLS support
- **Path sandboxing**: COPY commands restricted to data directory
- **WASM security**: Memory limits, export whitelist, no host filesystem access
- **Rate limiting**: Per-IP token bucket with configurable RPS and burst
- **Anti-replay**: Handshake nonce validation for protocol v2

### Protocol

- **Protocol v2**: Handshake negotiation, versioning, feature detection, anti-replay
- **Backward compatible**: v1 clients work without changes
- **Official clients**: Go, Python, JavaScript/TypeScript, C++

### Performance

- **WAL**: Group commit, binary payloads, crash recovery
- **Buffer pool**: Clock-Sweep eviction, configurable size (default 128MB)
- **Query plan caching**: LRU cache with schema-aware invalidation
- **Parallel queries**: Multi-goroutine execution for complex queries
- **Partition pruning**: Predicate extraction eliminates irrelevant partitions early
- **sync.Pool**: Hot Row allocation reuse

---

## Quick Start

### Docker Compose (recommended)

```bash
echo 'VAULTDB_API_TOKENS=vdb_my_token_123' > .env
echo 'VAULTDB_AUTH_SECRET=my-secret-key' >> .env
docker compose up -d --build
curl http://localhost:8080/health
```

### Docker

```bash
docker build -t vaultdb .
docker run -d \
  -p 5432:5432 -p 8080:8080 -p 5433:5433 \
  -e VAULTDB_API_TOKENS=vdb_my_token_123 \
  -e VAULTDB_AUTH_SECRET=my-secret-key \
  -v vaultdb-data:/data \
  vaultdb
```

### Native build

```bash
cd server && go build -o ../vaultdb-server ./cmd/vaultdb-server
./vaultdb-server --host 127.0.0.1 --port 5432 --http-port 8080 --data ./data
```

---

## Ports

| Port | Protocol | Purpose |
|------|----------|---------|
| 5432 | TCP | Native protocol (Go/Python/JS/C++ clients) |
| 8080 | HTTP | REST API + Web UI |
| 5433 | HTTP | Health/metrics/security dashboard |

---

## Clients

### Go

```go
import vaultdb "github.com/post-kserks/vaultdb/client/go"

client, _ := vaultdb.TCPDial("localhost:5432", "vdb_sk_...")
defer client.Close()

result, _ := client.Query("mydb", "SELECT * FROM users WHERE id = $1", "42")
fmt.Println(result.Rows)
```

### Python

```python
from vaultdb import Client

with Client("localhost", 5432, "vdb_sk_...") as client:
    client.connect()
    result = client.query("SELECT * FROM users WHERE id = $1", [42])
    print(result["rows"])
```

### JavaScript/TypeScript

```typescript
import { Client } from '@vaultdb/client';

const client = new Client('localhost', 5432, 'vdb_sk_...');
await client.connect();
const result = await client.query('SELECT * FROM users WHERE id = $1', [42]);
console.log(result.rows);
```

---

## SQL Examples

### RBAC — create roles and grant permissions

```sql
CREATE ROLE analyst WITH PASSWORD 'secure_password';
GRANT SELECT ON users TO analyst;
GRANT SELECT, INSERT ON logs TO analyst;
REVOKE INSERT ON logs FROM analyst;
DROP ROLE analyst;
```

### Table with partitioning

```sql
CREATE TABLE orders (
    id INT,
    order_date DATE,
    amount FLOAT
) PARTITION BY RANGE (order_date) (
    PARTITION p2023 VALUES LESS THAN ('2024-01-01'),
    PARTITION p2024 VALUES LESS THAN ('2025-01-01')
);

-- Partition pruning: only p2024 is scanned
SELECT * FROM orders WHERE order_date >= '2024-06-01';
```

### COPY data

```sql
COPY users FROM '/data/users.csv' WITH (FORMAT CSV, HEADER true);
COPY users TO '/data/export.json' WITH (FORMAT JSON);
```

### JSONB queries

```sql
SELECT data->>'name' FROM users WHERE data @> '{"active": true}';
SELECT * FROM users WHERE data ? 'email';
```

### Audit log

```sql
SELECT * FROM vaultdb_audit_log WHERE action = 'ALTER TABLE' ORDER BY occurred_at DESC;
VERIFY AUDIT LOG;
ARCHIVE AUDIT LOG TO '/backup/audit_export.json' KEEP 1000;
```

### Token management

```sql
REVOKE TOKEN 'vdb_sk_compromised_token_here';
```

### Full-text search

```sql
SELECT content FROM docs WHERE content FTS_MATCH 'database performance';
```

### UNIQUE constraints

```sql
CREATE TABLE users (
    id INT PRIMARY KEY,
    email VARCHAR(255) UNIQUE
);
CREATE UNIQUE INDEX idx_docs_number ON documents (doc_number);
```

### MERGE with VALUES

```sql
MERGE INTO target t
USING (VALUES (1, 'Alice'), (2, 'Bob')) AS src(id, name)
ON t.id = src.id
WHEN MATCHED THEN UPDATE SET t.name = src.name
WHEN NOT MATCHED THEN INSERT (id, name) VALUES (src.id, src.name);
```

### Stored functions

```sql
CREATE FUNCTION get_stats(min_age INT) RETURNS TABLE(name TEXT, age INT) AS $$
BEGIN
  RETURN QUERY SELECT name, age FROM users WHERE age >= min_age;
END;
$$ LANGUAGE plpgsql;
```

---

## Configuration

### vaultdb.yaml

```yaml
server:
  host: "0.0.0.0"
  port: 5432
  http_port: 8080
  monitor_port: 5433

tls:
  enabled: true
  cert_file: "/etc/ssl/certs/server.crt"
  key_file: "/etc/ssl/certs/server.key"
  min_version: "1.2"
  enforce: true

storage:
  engine: "page"
  data_dir: "/data"
  buffer_pool_pages: 16384  # 128 MB

auth:
  enabled: true
  localhost_bypass: false   # require tokens even from localhost
  require_tls_for_token: true
```

### Environment variables

| Variable | Description |
|----------|-------------|
| VAULTDB_HOST | Server host |
| VAULTDB_PORT | TCP port |
| VAULTDB_HTTP_PORT | HTTP port |
| VAULTDB_DATA_DIR | Data directory |
| VAULTDB_AUTH_ENABLED | Enable auth (true/false) |
| VAULTDB_API_TOKENS | API tokens |
| VAULTDB_AUTH_SECRET | HMAC secret |
| VAULTDB_ENCRYPTION_PASSPHRASE | TDE passphrase |
| VAULTDB_LOG_LEVEL | Log level (info/debug) |

---

## Architecture

```
Client (Go/Python/JS/C++) → TCP → Protocol v2 Handshake (anti-replay nonce)
                                       ↓
                              Lexer → Parser → Optimizer → Executor
                                       ↓                    ↓
                              Transaction Manager    Audit Log (hash-chain)
                                       ↓                    ↓
                              WAL (group commit)    Storage Engine
                                       ↓                    ↓
                              Buffer Pool (Clock-Sweep)   Heap Files
```

---

## Project Structure

```
├── server/                     # Go server
│   ├── cmd/vaultdb-server/     # Entry point
│   ├── cmd/vaultdb-backup/     # Backup utility
│   ├── cmd/vaultdb-encrypt/    # Encryption utility
│   ├── internal/               # Core (28 packages)
│   │   ├── executor/           # Query execution + optimizer pushdown
│   │   ├── parser/             # SQL parser
│   │   ├── storage/            # Storage engine + buffer pool + partitioning
│   │   ├── wal/                # Write-Ahead Log
│   │   ├── txmanager/          # MVCC transactions
│   │   ├── crypto/             # Encryption (AES-256-GCM) + DPAPI
│   │   ├── auth/               # Authentication + RBAC + revocation
│   │   ├── audit/              # Audit log with hash-chain
│   │   ├── wasmudf/            # WASM UDF runtime
│   │   ├── fts/                # Full-text search (BM25)
│   │   ├── iputil/             # Shared IP extraction utility
│   │   └── ...                 # index, metrics, config, tls, etc.
│   ├── benchmarks/             # Regression benchmarks
│   └── go.mod
├── client/                     # Official clients
│   ├── go/                     # Go TCP client
│   ├── python/                 # Python TCP client
│   ├── js/                     # JavaScript/TypeScript TCP client
│   └── lib/                    # C++ client library
├── tools/                      # Security & dev tools
│   ├── sqlfuzz/                # SQL random query generator
│   ├── security/               # Security scripts (SBOM, TLS scan)
│   └── benchstat-gate.sh       # Benchmark regression gate
├── docs/                       # Documentation (55+ files)
│   ├── security/               # Audit reports, self-audits (8 algorithms)
│   ├── benchmarks/             # Baseline metrics
│   ├── hardening/              # Coverage & crash reports
│   └── protocol/               # Protocol v2 specification
├── .github/workflows/          # CI/CD (7 workflows)
├── docker-compose.yml
└── Dockerfile
```

---

## Development

```bash
# Run tests
cd server && go test ./... -v

# Race detector
go test -race ./...

# Benchmarks
go test -bench=. -benchmem ./benchmarks/

# Fuzz testing
go test -fuzz=FuzzParseSQL -fuzztime=30s ./internal/parser/

# Security audit
semgrep --config .semgrep/ ./server
```

---

## Security Pipeline

| Component | Frequency | Blocks |
|-----------|-----------|--------|
| gosec (SAST) | Every PR | PR merge |
| govulncheck | Every PR | PR merge |
| semgrep custom rules | Every PR | PR merge |
| gitleaks | Every commit | Local commit |
| Race tests | Every PR | PR merge |
| Fuzz tests | Nightly (2h each) | Alert |
| DAST (injection, auth, timing) | Nightly | Alert |
| Trivy Docker scan | Every PR + nightly | PR merge |
| testssl.sh | Weekly + pre-release | Release |
| Manual audits A-H | Weekly rotation | Report |

---

## Documentation

| Category | Files |
|----------|-------|
| Getting started | introduction, quickstart, installation |
| SQL reference | sql-reference, functions, data-types, indexes |
| Features | encryption, transactions, triggers, views, udf, wal, mvcc, storage |
| Security | security, sql-injection-audit (2 reports), self-audits (8 algorithms) |
| Infrastructure | configuration, deployment, deployment-enterprise, architecture |
| Clients | client (Go/Python/JS/C++), tcp-protocol, api-reference |
| Operations | monitoring, backup, hardening-checklist |

Full documentation: [`docs/`](docs/)

---

## Enterprise Deployment

See [Enterprise Deployment Guide](docs/deployment-enterprise.md) for:
- GOGC/GOMEMLIMIT tuning
- Resource sizing (small/medium/large)
- Kubernetes deployment with Helm
- Security checklist
- TLS enforcement configuration

---

## License

MIT
