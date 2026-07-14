# Architecture Overview

## System Architecture

VaultDB is a monolithic SQL database engine with the following major components:

```
┌─────────────────────────────────────────────────────────┐
│                     Client Layer                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐  │
│  │ TCP:5432 │  │ HTTP:8080│  │ Monitor  │  │ Go API │  │
│  │ Protocol │  │ REST API │  │  :5433   │  │Library │  │
│  │ (v1+v2)  │  │          │  │          │  │        │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └───┬────┘  │
│       │              │             │             │        │
│  ┌────┴──────────────┴─────────────┴─────────────┴────┐  │
│  │              Session Layer                          │  │
│  │  ┌──────────┐  ┌────────────┐  ┌───────────────┐  │  │
│  │  │ Executor │  │  Parser    │  │  Optimizer    │  │  │
│  │  │ (Command │  │  (Lexer +  │  │  (Cost-based) │  │  │
│  │  │  Pattern)│  │   Parser)  │  │               │  │  │
│  │  └────┬─────┘  └────────────┘  └───────────────┘  │  │
│  │       │                                             │  │
│  │  ┌────┴──────────────────────────────────────────┐  │  │
│  │  │           Execution Engine                    │  │  │
│  │  │  ┌──────────┐  ┌──────────┐  ┌───────────┐  │  │  │
│  │  │  │Evaluator │  │ Aggregat.│  │  Window   │  │  │  │
│  │  │  │(WHERE,   │  │ (GROUP   │  │  Functions│  │  │  │
│  │  │  │ HAVING)  │  │  BY)     │  │           │  │  │  │
│  │  │  └──────────┘  └──────────┘  └───────────┘  │  │  │
│  │  └──────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Storage Layer                          │  │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐   │  │
│  │  │  Page    │  │  Buffer  │  │     WAL       │   │  │
│  │  │ Storage  │  │  Pool    │  │  (ARIES)      │   │  │
│  │  │ Engine   │  │  (Clock- │  │               │   │  │
│  │  │          │  │  Sweep)  │  │               │   │  │
│  │  └────┬─────┘  └──────────┘  └───────────────┘   │  │
│  │       │                                            │  │
│  │  ┌────┴────────────────────────────────────────┐   │  │
│  │  │           Disk Layer                        │   │  │
│  │  │  ┌──────────┐  ┌──────────┐  ┌──────────┐ │   │  │
│  │  │  │Heap Files│  │  Index   │  │  Catalog │ │   │  │
│  │  │  │ (8KB pg) │  │  Files   │  │  (JSON)  │ │   │  │
│  │  │  └──────────┘  └──────────┘  └──────────┘ │   │  │
│  │  └────────────────────────────────────────────┘   │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Supporting Services                    │  │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐   │  │
│  │  │   Auth   │  │  Metrics │  │   Broadcaster │   │  │
│  │  │  (HMAC)  │  │(Prometheu│  │  (Live Queries│   │  │
│  │  │          │  │   s)     │  │   SSE)        │   │  │
│  │  └──────────┘  └──────────┘  └───────────────┘   │  │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐   │  │
│  │  │  Audit   │  │  WASM    │  │  Full-Text    │   │  │
│  │  │(Hash-    │  │  UDF     │  │  Search       │   │  │
│  │  │ Chain)   │  │ Runtime  │  │  (Enterprise) │   │  │
│  │  └──────────┘  └──────────┘  └───────────────┘   │  │
│  └────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

## Component Descriptions

### Client Layer

| Component | Port | Protocol | Purpose |
|-----------|------|----------|---------|
| TCP Server | 5432 | JSON over TCP | Native protocol with v2 handshake (Go, Python, JS, C++ clients) |
| HTTP Server | 8080 | REST/JSON | REST API, SSE streaming, web dashboard |
| Monitor | 5433 | HTTP | Health checks, Prometheus metrics |
| Go API | (in-process) | Go function calls | Embedded database access |

### Session Layer

Each TCP connection or HTTP request creates a **Session** with:
- Isolated transaction state
- Prepared statement cache
- Plan cache
- Result cache

### Execution Engine

The executor (`internal/core/executor/`) uses the **Command Pattern** — each SQL statement type is a `Command` with an `Execute()` method. Commands are registered via a reflect-based factory map.

The executor is organized into specialized subsystems:
- **`internal/core/executor/types/types.go`**: Manages DDL objects (`_objects`), foreign key enforcement, and sequences (`_sequences`), along with uniform command execution results (`Result`).
- **`internal/core/executor/commands/`**: Contains subpackages for domain-specific statement execution (`ddl`, `dml`, `sel`, `tx`, `audit`, `auth`).

**Query pipeline**:
1. **Parse**: SQL text → AST via Lexer (`internal/core/lexer`) and Parser (`internal/core/parser`)
2. **Optimize**: Cost-based optimization (access method selection, join reordering, predicate pushdown)
3. **Execute**: Command.Execute() with ExecutionContext
4. **Return**: Uniform Result type

### Storage Layer

The storage layer (`internal/core/storage/`) is composed of several decomposed subsystems:

- **Page Storage Engine (`internal/core/storage/page/`)**: Manages 8KB pages with PostgreSQL-style slotted layout (subdirectory under `internal/core/storage/`).
- **Buffer Pool (`internal/core/storage/`)**: Clock-Sweep page cache (PostgreSQL-style) with dirty-page tracking and LSN-aware flushing.
- **Heap File Management (`internal/core/storage/heap/`)**: Handles segment-based heap file management across disk pages (subdirectory under `internal/core/storage/`).
- **Free Space Map (`internal/core/storage/fsm/`)**: Tracks available space across heap pages for efficient allocation (subdirectory under `internal/core/storage/`).
- **Partition Manager (`internal/core/storage/partition.go`)**: Implements table partitioning (range, hash, list) directly under `internal/core/storage/` with transparent query routing.
- **WAL (`internal/core/wal/`)**: Write-ahead log with ARIES-style three-phase recovery.

### Disk Layer

- **Heap Files**: Segment-based page storage (`internal/core/storage/heap/`, each segment = 65,536 pages)
- **Index Files**: B-tree, Hash, GIN, GiST, Composite (`internal/core/index/`, stored as JSON)
- **Catalog**: JSON file tracking databases, tables, row counts, and transaction timestamps (`internal/core/storage/catalog.go`)

## Data Flow: SELECT Query

```
1. SQL text arrives (TCP/HTTP)
2. Parser tokenizes and parses → AST
3. Optimizer selects access method (SeqScan/IndexScan)
4. Executor reads rows from storage engine
5. Buffer pool serves pages from cache or disk
6. WHERE clause evaluated (Evaluator)
7. GROUP BY / HAVING applied (Aggregator)
8. Window functions computed (WindowFunc)
9. ORDER BY / LIMIT applied
10. Result returned as stringified rows
```

## Data Flow: INSERT

```
1. SQL text parsed → InsertStatement
2. Transaction begins (if not already)
3. Values normalized to column types
4. Binary tuple encoded (16-byte header + column data)
5. Tuple inserted into last page of heap file
6. WAL entry written (OpPageInsert)
7. Buffer pool page marked dirty
8. Secondary indexes updated
9. Commit record written to WAL
10. Catalog row count incremented
```

## Data Flow: Crash Recovery

```
1. On startup: RecoverFromWAL()
2. Phase 1 - Analysis: Scan WAL, identify committed/uncommitted txns
3. Phase 2 - Redo: Replay ALL WAL entries (committed + uncommitted)
4. Phase 3 - Undo: Reverse uncommitted transactions
5. Post-recovery: fsync all heaps, recalculate catalog, write checkpoint
```

## Concurrency Model

Three-level locking hierarchy:

```
Level 1: e.mu (global)       — DDL operations, catalog mutations
Level 2: t.mu (per-table)    — DML operations (INSERT/UPDATE/DELETE)
Level 3: pageLock (per-page) — Individual page modifications
```

**Lock ordering**: e.mu → t.mu → pageLock (to prevent deadlocks)

**Commit serialization**: Per-table commit locks (sorted acquisition) prevent concurrent commits from conflicting.

## Package Structure

### Server Binaries (`cmd/`)

| Package | Purpose |
|---------|---------|
| `cmd/vaultdb-server` | Server binary entry point |
| `cmd/vaultdb-backup` | Backup/restore CLI tool |
| `cmd/vaultdb-encrypt` | Database encryption management utility (init, status, key generation, migration, rotation) |
| `cmd/check-index` | Index integrity checker utility |

### Core Database Modules (`internal/core/`)

| Package / Path | Purpose |
|----------------|---------|
| `internal/core/executor` | SQL execution engine |
| `internal/core/executor/types/types.go` | Manages DDL objects (`_objects`), foreign key enforcement, sequences (`_sequences`), and `Result` types |
| `internal/core/executor/commands/` | Domain-specific statement execution (`ddl`, `dml`, `sel`, `tx`, `audit`, `auth` subpackages) |
| `internal/core/parser` | SQL lexer and parser for AST generation |
| `internal/core/lexer` | SQL lexical analyzer and tokenizer |
| `internal/core/storage` | Page storage engine, buffer pool, and catalog management |
| `internal/core/storage/fsm` | Free Space Map subdirectory tracking available space across heap pages |
| `internal/core/storage/heap` | Heap file management subdirectory (segment-based storage) |
| `internal/core/storage/page` | Page layout subdirectory (headers, tuples, item pointers) |
| `internal/core/storage/partition.go` | Table partitioning (range, hash, list) directly under `internal/core/storage/` |
| `internal/core/wal` | Write-ahead log with ARIES-style three-phase recovery |
| `internal/core/txmanager` | Transaction manager with Optimistic Concurrency Control (OCC) |
| `internal/core/index` | B-tree, Hash, GIN, GiST, and Composite indexes |
| `internal/core/ai` | AI embedding providers and vector operations |
| `internal/core/audit` | Audit log with hash-chain integrity verification |
| `internal/core/crypto` | Cryptographic utilities and page-level encryption support |
| `internal/core/fts` | Enterprise full-text search engine |
| `internal/core/metrics` | Prometheus metrics collector |
| `internal/core/wasmudf` | WASM UDF runtime for user-defined functions |

### Server Infrastructure (`internal/`)

| Package | Purpose |
|---------|---------|
| `internal/auth` | HMAC token authentication |
| `internal/backup` | Backup/restore implementation |
| `internal/config` | YAML configuration loader |
| `internal/httpserver` | HTTP API server |
| `internal/iputil` | IP address utilities, client IP extraction, and trusted proxy handling |
| `internal/logging` | Log rotation and audit logging |
| `internal/osdisk` | OS-level disk encryption detection (LUKS, FileVault, BitLocker) |
| `internal/pool` | TCP connection pool |
| `internal/protocol` | Wire protocol definitions (v1 + v2 with handshake) |
| `internal/security` | Security hardening tests and verification |
| `internal/tls` | TLS/mTLS support |
| `internal/websocket` | WebSocket support |
