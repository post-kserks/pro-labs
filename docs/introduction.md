# Introduction

## What is VaultDB?

VaultDB is an enterprise-first, embeddable SQL database engine written in Go. It provides a familiar SQL interface with support for transactions, multiple index types, time-travel queries, full-text search, and both programmatic (Go/Python/JS library) and network (TCP/HTTP) access.

VaultDB is designed for:

- **Embedded use** — Embed a full SQL database in your Go, Python, or JavaScript application with a single client call
- **Standalone server** — Run as a network-accessible database service with authentication, TLS, audit logging, and connection pooling
- **Development and testing** — A zero-dependency database that starts in milliseconds

## Key Features

### SQL Compatibility

VaultDB supports a comprehensive subset of SQL including:

- **DML**: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE`, `COPY FROM/TO`
- **DDL**: `CREATE/DROP/ALTER TABLE`, `CREATE/DROP INDEX`, `CREATE/DROP VIEW`, `CREATE/DROP DATABASE`, `CREATE/DROP FUNCTION`, `PARTITION BY RANGE/HASH`
- **DCL**: `CREATE POLICY`, `ENABLE RLS` (row-level security)
- **TCL**: `BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`
- **Set operations**: `UNION`, `UNION ALL`, `INTERSECT`, `EXCEPT`
- **Subqueries**: Correlated and uncorrelated, in SELECT/WHERE/FROM
- **Common Table Expressions**: Including recursive CTEs
- **Window functions**: `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, `FIRST_VALUE`, `LAST_VALUE`, `NTH_VALUE`, aggregate OVER
- **`DISTINCT ON`** for deduplication on specific columns
- **JSONB operators**: Containment (`@>`), reverse containment (`<@`), merge (`||`)
- **130+ built-in functions**: String, math, date/time, JSON, array, aggregate
- **Prepared statements** with parameter binding (`$1`, `$2`, ...)
- **EXPLAIN** and **EXPLAIN ANALYZE** for query plan inspection
- **COPY FROM/TO** for bulk CSV and JSON import/export
- **Table partitioning** with RANGE and HASH strategies, automatic partition pruning
- **BM25 full-text search** with tokenization, corpus management, and snippet generation

### Storage Engine

- **Page-based storage** with 8KB pages and PostgreSQL-style slotted layout
- **Write-Ahead Logging (WAL)** with ARIES-style three-phase crash recovery
- **MVCC** (Multi-Version Concurrency Control) enabling snapshot isolation and time-travel queries
- **Buffer pool** with Clock-Sweep eviction and dirty-page tracking
- **Free Space Map** for O(log n) page allocation
- **Auto-vacuum** for dead tuple reclamation
- **Binary tuple encoding** for compact on-disk representation
- **Transparent Data Encryption (TDE)** with AES-256-GCM page-level encryption

### Index Types

| Type | Use Case |
|------|----------|
| **B-tree** | Equality and range queries, ordering |
| **Hash** | Fast equality lookups |
| **GIN** | Full-text search, JSONB containment |
| **GiST** | Numeric range/overlap queries |
| **Composite** | Multi-column index |

### Concurrency

- Three-level locking hierarchy (global → per-table → per-page)
- Optimistic Concurrency Control (OCC) for transaction conflict detection
- Per-page read/write locks for fine-grained concurrency
- Automatic rollback on connection drop

### Interfaces

- **TCP** (port 5432): Protocol v2 with JSON-based handshake for Go, Python, and JS clients
- **HTTP** (port 8080): REST API with SSE streaming, batch queries, and live subscriptions
- **Monitor** (port 5433): Health checks and Prometheus metrics
- **Go library**: Direct `vaultdb.Open()` / `vaultdb.Query()` for embedding
- **Python client**: `vaultdb` package with Protocol v2 TCP support
- **JS/TS client**: `vaultdb` package with Protocol v2 TCP support

### Security

- HMAC-SHA256 token authentication
- TLS 1.2+ encryption
- Mutual TLS (mTLS) for client certificate verification
- Per-IP rate limiting and brute-force protection
- Row-Level Security (RLS) policies
- **Transparent Data Encryption (TDE)** with AES-256-GCM and envelope encryption (KEK/DEK)
- **Audit log** with hash-chain integrity (`SHA-256` chained entries) stored in `vaultdb_audit_log`
- **Token revocation** via admin endpoint (`/admin/revoke-token`)
- **VERIFY AUDIT LOG** command to check chain integrity

### User-Defined Functions

- **WASM UDF**: Create custom functions in WebAssembly with `CREATE FUNCTION ... LANGUAGE WASM`
- Configurable memory limits and execution timeouts
- Standard SQL UDFs with `LANGUAGE SQL`

### AI Integration

- Pluggable embedding providers (OpenAI-compatible, Ollama)
- `SEMANTIC_MATCH` and `AI_EMBED` functions for vector-based search
- Embedding cache for performance

## Comparison with Other Databases

| Feature | VaultDB | SQLite | PostgreSQL |
|---------|---------|--------|------------|
| Language | Go | C | C |
| Embedded mode | Yes | Yes | No |
| TCP protocol | Yes (v2) | No | Yes |
| HTTP API | Built-in | No | Extension |
| MVCC | Yes | No | Yes |
| Time travel | Yes (`AS OF`) | No | Extension |
| WAL recovery | ARIES | Rollback | ARIES |
| Index types | 5 | 5 | Many |
| Window functions | Yes | 3.25+ | Yes |
| JSON support | Native (JSONB ops) | Limited | Native |
| Full-text search | BM25 | FTS5 | tsvector |
| Table partitioning | RANGE, HASH | No | Yes |
| Audit log | Hash-chain | No | Extension |
| TDE | AES-256-GCM | No | Extension |
| COPY FROM/TO | CSV, JSON | No | Yes |
| mTLS | Yes | No | Yes |
| Clients | Go, Python, JS | C | Multi-lang |
| Deployment | Single binary | Single binary | Multi-process |

## License

VaultDB is released under the MIT License.
