# Introduction

## What is VaultDB?

VaultDB is a lightweight, embeddable SQL database engine written in Go. It provides a familiar SQL interface with support for transactions, multiple index types, time-travel queries, and both programmatic (Go library) and network (TCP/HTTP) access.

VaultDB is designed for:

- **Embedded use** — Embed a full SQL database in your Go application with a single `vaultdb.Open()` call
- **Standalone server** — Run as a network-accessible database service with authentication, TLS, and connection pooling
- **Development and testing** — A zero-dependency database that starts in milliseconds

## Key Features

### SQL Compatibility

VaultDB supports a comprehensive subset of SQL including:

- **DML**: `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`, `TRUNCATE`
- **DDL**: `CREATE/DROP/ALTER TABLE`, `CREATE/DROP INDEX`, `CREATE/DROP VIEW`, `CREATE/DROP DATABASE`
- **DCL**: `CREATE POLICY`, `ENABLE RLS` (row-level security)
- **TCL**: `BEGIN`, `COMMIT`, `ROLLBACK`, `SAVEPOINT`
- **Set operations**: `UNION`, `UNION ALL`, `INTERSECT`, `EXCEPT`
- **Subqueries**: Correlated and uncorrelated, in SELECT/WHERE/FROM
- **Common Table Expressions**: Including recursive CTEs
- **Window functions**: `ROW_NUMBER`, `RANK`, `DENSE_RANK`, `LAG`, `LEAD`, `FIRST_VALUE`, `LAST_VALUE`, `NTH_VALUE`, aggregate OVER
- **130+ built-in functions**: String, math, date/time, JSON, array, aggregate
- **Prepared statements** with parameter binding (`$1`, `$2`, ...)
- **EXPLAIN** and **EXPLAIN ANALYZE** for query plan inspection

### Storage Engine

- **Page-based storage** with 8KB pages and PostgreSQL-style slotted layout
- **Write-Ahead Logging (WAL)** with ARIES-style three-phase crash recovery
- **MVCC** (Multi-Version Concurrency Control) enabling snapshot isolation and time-travel queries
- **Buffer pool** with LRU eviction and dirty-page tracking
- **Free Space Map** for O(log n) page allocation
- **Auto-vacuum** for dead tuple reclamation
- **Binary tuple encoding** for compact on-disk representation

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

- **TCP** (port 5432): Native JSON wire protocol for C++ and Go clients
- **HTTP** (port 8080): REST API with SSE streaming, batch queries, and live subscriptions
- **Monitor** (port 5433): Health checks and Prometheus metrics
- **Go library**: Direct `vaultdb.Open()` / `vaultdb.Query()` for embedding

### Security

- HMAC-SHA256 token authentication
- TLS 1.2+ encryption
- Mutual TLS (mTLS) for client certificate verification
- Per-IP rate limiting and brute-force protection
- Row-Level Security (RLS) policies

### AI Integration

- Pluggable embedding providers (OpenAI-compatible, Ollama)
- `SEMANTIC_MATCH` and `AI_EMBED` functions for vector-based search
- LRU embedding cache for performance

## Comparison with Other Databases

| Feature | VaultDB | SQLite | PostgreSQL |
|---------|---------|--------|------------|
| Language | Go | C | C |
| Embedded mode | Yes | Yes | No |
| TCP protocol | Yes | No | Yes |
| HTTP API | Built-in | No | Extension |
| MVCC | Yes | No | Yes |
| Time travel | Yes (`AS OF`) | No | Extension |
| WAL recovery | ARIES | Rollback | ARIES |
| Index types | 5 | 5 | Many |
| Window functions | Yes | 3.25+ | Yes |
| JSON support | Native | Limited | Native |
| mTLS | Yes | No | Yes |
| Deployment | Single binary | Single binary | Multi-process |

## License

VaultDB is released under the MIT License.
