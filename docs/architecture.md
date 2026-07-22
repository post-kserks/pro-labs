# Architecture Overview

## System Architecture

VaultDB is a monolithic SQL database engine with the following major components:

```
┌─────────────────────────────────────────────────────────┐
│                     Client Layer                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────┐  │
│  │ PGWire   │  │ TCP Native│ │ HTTP:8080│  │ Go API │  │
│  │ (psql)   │  │ (v1+v2)  │  │ REST API │  │Library │  │
│  │ :5432    │  │ :5432/3  │  │          │  │        │  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘  └───┬────┘  │
│       │              │             │             │        │
│  ┌────┴──────────────┴─────────────┴─────────────┴────┐  │
│  │              Session Layer                          │  │
│  │  ┌──────────┐  ┌────────────┐  ┌───────────────┐  │  │
│  │  │ Executor │  │  Parser    │  │  Optimizer    │  │  │
│  │  │ (Command │  │  (Lexer +  │  │  (CBO + DP    │  │  │
│  │  │  Pattern)│  │   Parser)  │  │  JoinReorder) │  │  │
│  │  └────┬─────┘  └────────────┘  └───────────────┘  │  │
│  │       │                                             │  │
│  │  ┌────┴──────────────────────────────────────────┐  │  │
│  │  │           Execution Engine                    │  │  │
│  │  │  ┌──────────┐  ┌──────────┐  ┌───────────┐  │  │  │
│  │  │  │Bytecode  │  │ Aggregat.│  │  Window   │  │  │  │
│  │  │  │ VM & JIT │  │ (GROUP   │  │  Functions│  │  │  │
│  │  │  │ (eval/vm)│  │  BY)     │  │           │  │  │  │
│  │  │  └──────────┘  └──────────┘  └───────────┘  │  │  │
│  │  └──────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Storage Layer                          │  │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐   │  │
│  │  │ Page (HOT│  │  Buffer  │  │     WAL       │   │  │
│  │  │ Chains)  │  │  Pool    │  │  (ARIES)      │   │  │
│  │  └────┬─────┘  └──────────┘  └───────────────┘   │  │
│  │       │                                            │  │
│  │  ┌────┴────────────────────────────────────────┐   │  │
│  │  │ Background Workers & Disk Layer            │   │  │
│  │  │  ┌──────────┐  ┌──────────┐  ┌──────────┐ │   │  │
│  │  │  │AutoVacuum│  │ Checkptr │  │ Catalog  │ │   │  │
│  │  │  │ Worker   │  │ Worker   │  │ & Stats  │ │   │  │
│  │  │  └──────────┘  └──────────┘  └──────────┘ │   │  │
│  │  └────────────────────────────────────────────┘   │  │
│  └────────────────────────────────────────────────────┘  │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Supporting & HA Services               │  │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐   │  │
│  │  │   Auth   │  │ Raft HA  │  │   Broadcaster │   │  │
│  │  │  (HMAC)  │  │Consensus │  │  (Live Queries│   │  │
│  │  └──────────┘  └──────────┘  └───────────────┘   │  │
│  │  ┌──────────┐  ┌──────────┐  ┌───────────────┐   │  │
│  │  │  Audit   │  │  WASM    │  │  Full-Text    │   │  │
│  │  │ (Hash-   │  │  UDF     │  │  Search       │   │  │
│  │  │  Chain)  │  │ Runtime  │  │  (Enterprise) │   │  │
│  │  └──────────┘  └──────────┘  └───────────────┘   │  │
│  └────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

## Component Descriptions

### Client Layer

| Component | Port | Protocol | Purpose |
|-----------|------|----------|---------|
| PGWire Server | 5432 | PostgreSQL Wire Protocol v3 | Standard PostgreSQL client compatibility (`psql`, `pgx`, `libpq`, ORMs) |
| TCP Server | 5432 / 5433 | JSON over TCP | Native protocol with v2 handshake (Go, Python, JS, C++ clients) |
| HTTP Server | 8080 | REST/JSON | REST API, SSE streaming, web dashboard |
| Monitor | 5433 | HTTP | Health checks, Prometheus metrics |
| Go API | (in-process) | Go function calls | Embedded database access |

### Session Layer

Each TCP or PGWire connection or HTTP request creates a **Session** with:
- Isolated transaction state
- Prepared statement cache
- Plan cache
- Result cache

### Execution Engine

The executor (`internal/core/executor/`) uses the **Command Pattern** — each SQL statement type is a `Command` with an `Execute()` method. Commands are registered via a reflect-based factory map.

The executor is organized into specialized subsystems:
- **`internal/core/executor/types/types.go`**: Manages DDL objects (`_objects`), foreign key enforcement, and sequences (`_sequences`), along with uniform command execution results (`Result`).
- **`internal/core/executor/commands/`**: Contains subpackages for domain-specific statement execution (`ddl`, `dml`, `sel`, `tx`, `audit`, `auth`).
- **`internal/core/executor/eval/vm/`**: Bytecode VM & Expression Compiler. Compiles AST filter expressions into compact bytecode instructions (`OpPushInt`, `OpLoadColumn`, `OpEq`, `OpAnd`, etc.) for zero-reflection, high-throughput predicate evaluation.
- **`internal/core/executor/optimizer/`**: Cost-Based Optimizer (CBO) with dynamic programming join reordering (`join_reorder.go`), histogram & MCV selectivity estimation (`statistics.go`), and `GlobalStatsRegistry` backed by `system.pg_statistic`.

**Query pipeline**:
1. **Parse**: SQL text → AST via Lexer (`internal/core/lexer`) and Parser (`internal/core/parser`)
2. **Optimize**: Cost-based optimization (access method selection, DP join reordering, predicate pushdown, bytecode compilation)
3. **Execute**: Command.Execute() with ExecutionContext and Bytecode VM evaluation
4. **Return**: Uniform Result type or PostgreSQL wire message response

### Storage Layer

The storage layer (`internal/core/storage/`) is composed of several decomposed subsystems:

- **Page Storage Engine (`internal/core/storage/page/`)**: Manages 8KB pages with PostgreSQL-style slotted layout and Heap-Only Tuples (HOT) support to eliminate index updates on non-indexed column updates.
- **Buffer Pool (`internal/core/storage/`)**: Clock-Sweep page cache (PostgreSQL-style) with dirty-page tracking and LSN-aware flushing.
- **AutoVacuum Worker (`internal/core/storage/vacuum.go`)**: Background worker (`AutoVacuumWorker`) for periodic dead tuple reclamation and shadow table compaction.
- **Checkpointer Worker (`internal/core/storage/checkpointer.go`)**: Background worker (`CheckpointerWorker`) for periodic flushing of dirty unpinned pages to disk.
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
| `internal/core/executor/eval/vm/` | Bytecode VM and JIT expression compiler for fast zero-reflection predicate evaluation |
| `internal/core/executor/optimizer/` | Cost-Based Optimizer (CBO), DP Join Reordering (`join_reorder.go`), Histograms/MCV (`statistics.go`), and `GlobalStatsRegistry` |
| `internal/core/executor/types/types.go` | Manages DDL objects (`_objects`), foreign key enforcement, sequences (`_sequences`), and `Result` types |
| `internal/core/executor/commands/` | Domain-specific statement execution (`ddl`, `dml`, `sel`, `tx`, `audit`, `auth` subpackages) |
| `internal/core/parser` | SQL lexer and parser for AST generation |
| `internal/core/lexer` | SQL lexical analyzer and tokenizer |
| `internal/core/storage` | Page storage engine (with Heap-Only Tuples), buffer pool, background workers (`vacuum.go`, `checkpointer.go`), catalog |
| `internal/core/storage/fsm` | Free Space Map subdirectory tracking available space across heap pages |
| `internal/core/storage/heap` | Heap file management subdirectory (segment-based storage) |
| `internal/core/storage/page` | Page layout subdirectory (headers, tuples, item pointers, `ItemFlagRedirect`) |
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

### Cluster & HA Modules (`internal/cluster/`)

| Package / Path | Purpose |
|----------------|---------|
| `internal/cluster/raft/` | Raft consensus node replication (`RaftNode`), leader election, state machine log replication |

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
| `internal/protocol` | Native wire protocol definitions (v1 + v2 with handshake) |
| `internal/protocol/pgwire` | PostgreSQL wire protocol server (`PGWireServer`, extended & simple query handler) |
| `internal/security` | Security hardening tests and verification |
| `internal/tls` | TLS/mTLS support |
| `internal/websocket` | WebSocket support |
