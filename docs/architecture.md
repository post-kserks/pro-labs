# Architecture Overview

## System Architecture

VaultDB is a monolithic SQL database engine with the following major components:

```
┌─────────────────────────────────────────────────────────┐
│                     Client Layer                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐  │
│  │ TCP:5432 │  │ HTTP:8080│  │ Monitor  │  │ Go API │  │
│  │ Protocol │  │ REST API │  │  :5433   │  │Library │  │
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
│  │  │ Engine   │  │  (LRU)   │  │               │   │  │
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
│  └────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

## Component Descriptions

### Client Layer

| Component | Port | Protocol | Purpose |
|-----------|------|----------|---------|
| TCP Server | 5432 | JSON over TCP | Native protocol for C++ and Go clients |
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

The executor uses the **Command Pattern** — each SQL statement type is a `Command` with an `Execute()` method. Commands are registered via a reflect-based factory map.

**Query pipeline**:
1. **Parse**: SQL text → AST (via Lexer + Parser)
2. **Optimize**: Cost-based optimization (access method selection, join reordering, predicate pushdown)
3. **Execute**: Command.Execute() with ExecutionContext
4. **Return**: Uniform Result type

### Storage Layer

- **Page Storage Engine**: Manages 8KB pages with PostgreSQL-style slotted layout
- **Buffer Pool**: LRU page cache with dirty-page tracking and LSN-aware flushing
- **WAL**: Write-ahead log with ARIES-style three-phase recovery

### Disk Layer

- **Heap Files**: Segment-based page storage (each segment = 65,536 pages)
- **Index Files**: B-tree, Hash, GIN, GiST, Composite (stored as JSON)
- **Catalog**: JSON file tracking databases, tables, row counts, and transaction timestamps

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

| Package | Purpose |
|---------|---------|
| `cmd/vaultdb-server` | Server binary entry point |
| `cmd/vaultdb-backup` | Backup/restore CLI tool |
| `cmd/check-index` | Index integrity checker |
| `internal/executor` | SQL execution engine (36 statement types) |
| `internal/parser` | SQL lexer and parser |
| `internal/storage` | Page storage engine, buffer pool, catalog |
| `internal/storage/heap` | Heap file management |
| `internal/storage/page` | Page layout (headers, tuples, item pointers) |
| `internal/storage/fsm` | Free Space Map |
| `internal/wal` | Write-ahead log with ARIES recovery |
| `internal/txmanager` | Transaction manager with OCC |
| `internal/index` | B-tree, Hash, GIN, GiST, Composite indexes |
| `internal/httpserver` | HTTP API server |
| `internal/websocket` | WebSocket support |
| `internal/auth` | HMAC token authentication |
| `internal/config` | YAML configuration loader |
| `internal/tls` | TLS/mTLS support |
| `internal/ai` | AI embedding providers |
| `internal/metrics` | Prometheus metrics collector |
| `internal/logging` | Log rotation and audit logging |
| `internal/pool` | TCP connection pool |
| `internal/protocol` | Wire protocol definitions |
| `internal/backup` | Backup/restore implementation |
