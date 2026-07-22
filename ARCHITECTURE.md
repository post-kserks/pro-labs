# VaultDB Architecture

This document describes the architecture of VaultDB — a SQL database engine with a Go server and multi-language client ecosystem (C++, Go, Python, TypeScript/JS). It reflects the modular engine design, page-based binary storage layer, and all real modules in the codebase (verified against source tree).

---

## 1. Project Structure

```
├── server/                    # Go server
│   ├── cmd/
│   │   ├── vaultdb-server/    # Main server entry point
│   │   ├── vaultdb-backup/    # Backup & restore CLI utility
│   │   ├── vaultdb-encrypt/   # TDE encryption key management CLI utility
│   │   └── check-index/       # Index diagnostic tool
│   ├── internal/
│   │   ├── auth/              # HMAC-SHA256 token auth + rate limiter
│   │   ├── backup/            # Backup and recovery infrastructure
│   │   ├── cluster/           # Distributed Raft consensus and replication
│   │   │   └── raft/          # Raft consensus node & log replication
│   │   ├── config/            # YAML + env config loader
│   │   ├── core/              # Core SQL database engine modules
│   │   │   ├── ai/            # AI embedding provider (SEMANTIC_MATCH/AI_EMBED)
│   │   │   ├── audit/         # Audit logging engine
│   │   │   ├── crypto/        # Cryptographic operations, TDE page/WAL encryption
│   │   │   ├── executor/      # Command-pattern execution engine & CBO Optimizer
│   │   │   │   ├── eval/      # AST & VM JIT expression evaluation
│   │   │   │   │   └── vm/    # Bytecode VM & expression compiler
│   │   │   │   └── optimizer/ # DP Join Reordering & GlobalStatsRegistry
│   │   │   ├── fts/           # Full-text search engine
│   │   │   ├── index/         # B-Tree, GIN, GiST, Hash indexes
│   │   │   ├── lexer/         # Hand-written SQL lexer
│   │   │   ├── metrics/       # Prometheus-style metrics collector
│   │   │   ├── parser/        # Recursive-descent SQL parser
│   │   │   ├── security/      # Dynamic Data Masking policies
│   │   │   ├── storage/       # Page engine + HOT + AutoVacuum + Checkpointer
│   │   │   ├── txmanager/     # MVCC transaction manager
│   │   │   ├── wal/           # Write-Ahead Log with ARIES & Group Commit
│   │   │   └── wasmudf/       # WebAssembly user-defined functions (Wasm UDF)
│   │   ├── httpserver/        # HTTP/REST + embedded Web UI + ratelimit
│   │   ├── iputil/            # IP utility and CIDR validation
│   │   ├── logging/           # General logging infrastructure
│   │   ├── osdisk/            # OS-level disk monitoring and I/O utilities
│   │   ├── pool/              # Connection pool / tracker
│   │   ├── protocol/          # Native TCP wire protocol v2
│   │   │   └── pgwire/        # PostgreSQL Wire Protocol handler
│   │   ├── security/          # Security utilities and TLS helpers
│   │   ├── tls/               # TLS/mTLS config loader
│   │   └── websocket/         # WebSocket bridge for live queries
│   ├── vaultdb.go             # Embedded engine facade
│   ├── go.mod / go.sum
│   └── benchmark/             # Server-side benchmark suite
├── client/                    # Client ecosystem (C++ & multi-language SDKs)
│   ├── go/                    # Go client SDK
│   ├── python/                # Python 3 client SDK
│   ├── js/                    # TypeScript/Node.js client SDK
│   ├── lib/                   # libvaultdb shared library
│   │   ├── include/vaultdb/   # Public headers (connection, result, json_utils)
│   │   └── src/               # Implementation
│   ├── shell/                 # Interactive REPL shell
│   ├── tui/                   # Terminal UI (panel-based)
│   └── tests/                 # Client unit tests
├── tools/
│   ├── benchmark/             # Go benchmark tool
│   ├── sqlfuzz/               # SQL fuzzing utilities
│   ├── security/              # Security scan scripts (tls_scan.sh, generate-sbom.sh)
│   └── benchstat-gate.sh      # Benchmark performance regression gate script
├── data/                      # Runtime data directory
├── docs/                      # Documentation
├── build.sh / run.sh          # Build/run scripts
├── Dockerfile / docker-compose.yml
├── vaultdb.yaml / vaultdb.yaml.example
├── VERSION / Makefile
└── ARCHITECTURE.md            # ← this file
```

---

## 2. System Map

```mermaid
graph TB

  subgraph Client ["Client Side (C++)"]

    TUI["TUI Client<br/>(client/tui)"]
    Shell["Shell Client<br/>(client/shell)"]
    Lib["libvaultdb<br/>(client/lib)"]

    TUI --> Lib
    Shell --> Lib

  end

  subgraph Server ["VaultDB Server (Go)"]

    subgraph Net ["Networking & Security"]

      HTTPSrv["HTTP Server<br/>(internal/httpserver)"]
      TCPSrv["TCP Server<br/>(cmd/vaultdb-server)"]
      TLS["TLS Provider<br/>(internal/tls)"]
      RateLimit["Rate Limiter<br/>(internal/httpserver/ratelimit)"]
      Auth["Auth Manager<br/>(internal/auth)"]
      Proto["Wire Protocol<br/>(internal/protocol)"]
      ConnPool["Connection Pool<br/>(internal/pool)"]
      WS["WebSocket Bridge<br/>(internal/websocket)"]

      TCPSrv --- Proto
      TCPSrv --- Auth
      TCPSrv --- TLS
      TCPSrv --- ConnPool
      HTTPSrv --- RateLimit
      HTTPSrv --- TLS
      HTTPSrv --- Auth
      WS --- HTTPSrv

    end

    WebUI["Embedded Web UI<br/>(internal/httpserver/web)"]

    subgraph Core ["Core SQL Engine"]

      subgraph Parser_Module ["Parser"]

        Lexer["Hand-written Lexer<br/>(internal/core/lexer)"]
        Parser["Parser Dispatcher<br/>(internal/core/parser)"]
        P_Select["SELECT Parser"]
        P_DML["DML Parser<br/>(Ins/Upd/Del/Merge)"]
        P_DDL["DDL Parser<br/>(Create/Drop/Alter)"]
        P_Expr["Expression Parser"]

        Lexer --> Parser
        Parser --- P_Select
        Parser --- P_DML
        Parser --- P_DDL
        Parser --- P_Expr

      end

      subgraph Executor_Module ["Executor"]

        Exec["Executor Engine<br/>(internal/core/executor)"]
        E_Select["SELECT Command<br/>+ Join / Aggregate / Window"]
        E_DML["DML Commands<br/>(Insert/Update/Delete/Merge)"]
        E_DDL["DDL Commands<br/>(Create/Drop/Alter/Vacuum)"]
        E_Tx["Tx Commands<br/>(Begin/Commit/Rollback)"]
        E_SOp["Set Operations<br/>(Union/Intersect/Except)"]
        E_CTE["CTE / WITH<br/>(internal/core/executor/cte)"]
        E_Prep["Prepared Stmts<br/>(Prepare/Execute)"]

        Exec --- E_Select
        Exec --- E_DML
        Exec --- E_DDL
        Exec --- E_Tx
        Exec --- E_SOp
        Exec --- E_CTE
        Exec --- E_Prep

      end

      subgraph Evaluator ["Expression Engine"]

        Eval["Eval Dispatcher"]
        Ev_Func["Built-in Functions<br/>+ Math / String / Array"]
        Ev_Json["JSON / JSONB / FTS"]
        Ev_Time["DateTime Logic"]
        Ev_Like["LIKE / Pattern Match<br/>(internal/core/executor/like)"]
        Ev_Aggr["Aggregate Functions<br/>(Welford, Count, Sum)"]

        Eval --- Ev_Func
        Eval --- Ev_Json
        Eval --- Ev_Time
        Eval --- Ev_Like
        Eval --- Ev_Aggr

      end

      Optimizer["Query Optimizer<br/>(internal/core/executor/optimizer)"]
      Stats["Table Statistics<br/>(internal/core/executor/statistics)"]
      PlanCache["Plan Cache<br/>(internal/core/executor/plan_cache)"]
      ResCache["Result Cache<br/>(internal/core/executor/result_cache)"]
      BR["Live Query Broadcaster<br/>(internal/core/executor/broadcaster)"]

      Parser --> Optimizer
      Optimizer --- Stats
      Optimizer --> Exec
      Exec --- PlanCache
      Exec --- ResCache
      Exec --- BR

    end

    subgraph Evaluator2 ["Expression Engine (cont.)"]

      E_Eval["Expression Evaluator<br/>(internal/core/executor/eval)"]
      E_Eval_Ops["Operators (=, !=, <, >, IN, IS NULL)"]
      E_Eval_Func["Functions<br/>(COALESCE, CAST, CASE)"]
      E_Eval_Json["JSON Path/Operators<br/>(@>, <@, ?, ||)"]
      E_Eval_Subq["Subquery Exprs<br/>(ANY, ALL, EXISTS)"]

      E_Eval --- E_Eval_Ops
      E_Eval --- E_Eval_Func
      E_Eval --- E_Eval_Json
      E_Eval --- E_Eval_Subq

    end

    subgraph AI_Module ["AI & Semantic"]

      AI["AI Embedder<br/>(internal/core/ai)"]
      AI_ONNX["ONNX Runtime<br/>(optional provider)"]
      AI_HTTP["HTTP Embedding API<br/>(OpenAI-compatible)"]

      AI --- AI_ONNX
      AI --- AI_HTTP

    end

    TxMgr["Tx Manager<br/>(internal/core/txmanager)"]
    TxMgr --- Exec

    E_Eval --- Eval

    subgraph StorageLayer ["Storage & Indexing"]

      subgraph PageEngine ["Page Storage Engine<br/>(internal/core/storage)"]

        PE_Core["Engine Core<br/>(page_engine.go)"]
        PE_Tuple["Binary Tuple Encoding<br/>(binary_encoding.go)"]
        PE_Heap["Heap File Manager<br/>(internal/core/storage/heap)"]
        PE_Page["Page Data Structures<br/>(internal/core/storage/page)"]
        PE_FSM["Free Space Map<br/>(internal/core/storage/fsm)"]
        PE_IO["Low-level I/O<br/>(page_engine_io.go)"]
        PE_Index["Index Integration<br/>(page_engine_index.go)"]
        PE_Alter["Alter / Rewrite Logic<br/>(page_engine_alter.go)"]
        PE_Vac["Vacuum Cleaner<br/>(page_engine_vacuum.go)"]
        PE_Lock["Page Lock Manager<br/>(page_lock.go)"]

        PE_Core --- PE_Tuple
        PE_Core --- PE_Heap
        PE_Core --- PE_Page
        PE_Core --- PE_FSM
        PE_Core --- PE_IO
        PE_Core --- PE_Index
        PE_Core --- PE_Alter
        PE_Core --- PE_Vac
        PE_Core --- PE_Lock

      end

      BufPool["Buffer Pool (LRU)<br/>(buffer_pool.go)"]
      JSONDec["Legacy JSON Decoder<br/>(json_decode.go)"]
      Normalize["Name Normalizer<br/>(normalize.go)"]

      WAL["Write-Ahead Log<br/>(internal/core/wal)"]

      PE_IO --- BufPool
      PE_IO --- JSONDec
      PE_Core --> WAL

      subgraph Indexes ["Index Implementations<br/>(internal/core/index)"]

        BTree["B-Tree Index"]
        Gin["GIN Index<br/>(JSONB / Full-text)"]
        GiST["GiST Index<br/>(vector / geospatial)"]
        Hash["Hash Index"]
        Composite["Composite Index"]

        BTree --- Gin
        BTree --- GiST
        BTree --- Hash
        BTree --- Composite

      end

      PE_Index --- Indexes

    end

    subgraph Utils ["Observability & Infrastructure"]

      Audit["Audit Engine<br/>(internal/core/audit)"]
      Log["Audit Logger<br/>(internal/logging)"]
      Metrics["Metrics Collector<br/>(internal/core/metrics)"]
      Cfg["Config Loader<br/>(internal/config)"]

    end

    AI --- Exec
    HTTPSrv --- WebUI
    Exec -.-### 3.1 SQL Pipeline (Lexer → Parser → Optimizer → Bytecode VM → Executor)

| Component | Package | Responsibility |
|-----------|---------|----------------|
| **Lexer** | `internal/core/lexer` | Hand-written rune-based tokenizer. Supports single-character operators (`=`, `!=`, `<`, `>`, `->`, `->>`, `@>`, `<@`, `?`, `||`), string escape sequences, negative number literals, `$N` param references. Returns line/col positions for error reporting. |
| **Parser** | `internal/core/parser` | Recursive-descent parser dispatching to modular sub-parsers (DDL, DML, SELECT, expressions). AST nodes for all statement types including CTE/WITH, MERGE, TRIGGER, VIEW, FUNCTION, PROCEDURE, WINDOW, SET operations, LATERAL subqueries. |
| **Optimizer** | `internal/core/executor/optimizer` | Cost-based optimizer (CBO). Uses `optimizer.GlobalStatsRegistry` (`system.pg_statistic` histograms & MCV) to choose between SeqScan and IndexScan. Dynamic programming (DP) Join Reordering (`join_reorder.go`) selects optimal physical join trees. |
| **Bytecode VM & JIT Compiler** | `internal/core/executor/eval/vm` | AST-to-bytecode expression compiler and zero-reflection Virtual Machine (VM) for high-performance zero-allocation predicate evaluation. |
| **Executor** | `internal/core/executor` | Command-pattern execution engine. Within `internal/core/executor`, `types/types.go` handles DDL object catalog (`_objects`), foreign keys, and sequence auto-increment, while `commands/` contains subpackages `ddl`, `dml`, `sel`, `tx`, `audit`, and `auth`. Each statement maps to a `Command` via `reflect`-based registry (initialized in `init()`). Supports transactions, prepared statements, live query broadcasting, plan/result caching, `KILL QUERY` command, and system views (`pg_stat_activity`, `pg_locks`). |
| **Evaluator** | `internal/core/executor/eval*.go` | Expression evaluation engine with recursive descent. Handles math, string ops, JSONB operators, LIKE/FTS/Full-text, CASE/COALESCE/CAST, subqueries (ALL/ANY/EXISTS), window functions, aggregate functions (Welford online algorithm). |

### 3.2 Storage Layer & Background Workers

| Component | Package | Responsibility |
|-----------|---------|----------------|
| **Page Engine** | `internal/core/storage` | Full storage engine on top of page/heap layer. 16-byte tuple header (created_tx + deleted_tx LE uint64), JSON-encoded catalog with current TxID, last-modified tracking, row counts. |
| **Heap-Only Tuples (HOT)** | `internal/core/storage` | In-page tuple versioning chains. Eliminates Write Amplification & index bloat on non-indexed column updates. |
| **AutoVacuum Worker** | `internal/core/storage/vacuum.go` | Background worker for dead tuple reclamation based on active transaction IDs (`MinAge`). |
| **Checkpointer Worker** | `internal/core/storage/checkpointer.go` | Asynchronous batch dirty page flusher to smooth disk I/O throughput. |
| **Heap File** | `internal/core/storage/heap` | Low-level file manager: allocate/read/write 8KB pages. Pages are tracked in segments. Linked free page chain. |
| **Page** | `internal/core/storage/page` | Page data structures: header with check/compression flags, tuple slot array, tuple data area. |
| **FSM** | `internal/core/storage/fsm` | Free Space Map — tracks available space on pages for efficient INSERT placement. |
| **Binary Encoding** | `internal/core/storage/binary_encoding.go` | Compact binary row format: header + col-count + offset array + typed values. Type tags: `i` int64, `f` float64, `b` bool, `s` string, `j` JSONB, `v` float64 vector, 0xFF null. |
| **Buffer Pool** | `internal/core/storage/buffer_pool.go` | LRU-k page cache. Page pin/unpin with dirty tracking. Flush dirty pages up to a given LSN for checkpoint integration. |

### 3.3 Indexes

| Type | File | Use Case |
|------|------|----------|
| **B-Tree** | `internal/core/index/btree.go` | Primary key and unique constraint indexes. Balanced tree with split/merge. |
| **GIN** | `internal/core/index/gin_index.go` | Generalized Inverted Index for JSONB paths and full-text search. |
| **GiST** | `internal/core/index/gist_index.go` | Generalized Search Tree for vector similarity and geospatial. |
| **Hash** | `internal/core/index/hash_index.go` | Hash index for exact-match equality lookups. |
| **Composite** | `internal/core/index/composite.go` | Multi-column index combining B-Tree entries. |
| **Manager** | `internal/core/index/manager.go` | Central index manager per table — coordinates index creation, lookup, and lifecycle. |

### 3.4 Transaction Manager & Clustering

**Package**: `internal/core/txmanager` & `internal/cluster/raft`

MVCC-inspired transaction system & Raft consensus:
- **Snapshot isolation**: each transaction records table versions at first access; Commit checks for conflicts.
- **Raft Consensus**: Multi-node Raft state machine and WAL replication (`internal/cluster/raft`) with configurable `synchronous_commit = off|on`.
- **Per-table commit locks**: serializes commits touching the same table.
- **Savepoints**: named markers within a transaction buffer with ROLLBACK TO support.
- **Spill-to-disk**: when `SpillThreshold` (default 10000) ops accumulate, pending operations are serialized to a file.

### 3.5 Write-Ahead Log & TDE Encryption

**Package**: `internal/core/wal` & `internal/core/crypto`

ARIES-inspired WAL with Transparent Data Encryption:
- **Fixed record format**: magic(4) + txID(8) + opType(1) + payloadLen(4) + payload + CRC32(4)
- **TDE Encryption**: AES-256-GCM envelope encryption (`internal/core/crypto/tde.go`) for pages and WAL records.
- **Operation types**: Insert/Update/Delete, Page operations (insert/delete/XMax), Schema writes, Full-page images, Rewrite (ALTER), Vacuum, Commit/Abort, Checkpoint
- **Three-phase recovery**: Analysis → Redo → Undo
- **Batch fsync**: configurable `synchronous_commit` (group commit / write-behind batching) to amortize fsync cost

### 3.6 Networking & Protocols

| Component | Package / Path | Details |
|-----------|----------------|---------|
| **PostgreSQL Wire Protocol** | `internal/protocol/pgwire` | PG wire server on port 5433 / 5432. Simple query protocol, row descriptions, and authentication compatible with standard PostgreSQL drivers (`pgx`, `lib/pq`, `psql`). |
| **Native TCP Server** | `cmd/vaultdb-server/main.go` | Custom binary/JSON-over-TCP protocol on port 5432. Per-connection goroutine with rate limiter. |
| **HTTP Server** | `internal/httpserver` | REST API on port 8080 + health/monitor on port 5433. CORS configurable, request size limits. Embedded Web UI (`web/`) and rate limiter (`ratelimit/`). |
| **Wire Protocol** | `internal/protocol` | Request/Response JSON types shared between TCP and HTTP. |
| **Connection Pool** | `internal/pool` | Tracks active connections with max limit, idle cleanup, health-check. |
| **TLS/mTLS** | `internal/tls` | Loads TLS config with optional mTLS (CA verification). |
| **Auth & Data Masking** | `internal/auth` & `internal/core/security` | HMAC-SHA256 token hashing, per-IP rate limiter, and dynamic column data masking (`masking.go`). |. |
| **HTTP Server** | `internal/httpserver` | REST API on port 8080 + health/monitor on port 5433. CORS configurable, request size limits. Embedded Web UI (`web/`) and rate limiter (`ratelimit/`). |
| **Wire Protocol** | `internal/protocol` | Request/Response JSON types shared between TCP and HTTP. |
| **Connection Pool** | `internal/pool` | Tracks active connections with max limit, idle cleanup, health-check. |
| **TLS/mTLS** | `internal/tls` | Loads TLS config with optional mTLS (CA verification). |
| **Auth** | `internal/auth` | HMAC-SHA256 token hashing with server secret. Per-IP rate limiter blocks after N failures in a window. |
| **WebSocket** | `internal/websocket` | WebSocket bridge for live query subscriptions over HTTP. |
| **Backup Service** | `internal/backup` | Server-side backup and recovery infrastructure supporting hot backups and point-in-time restore. |
| **Security Utils** | `internal/security` | General security utilities, TLS helpers, and cryptographic integration wrappers. |
| **System Utilities** | `internal/osdisk`, `internal/iputil` | OS-level disk monitoring (`osdisk`) and IP/CIDR validation utilities (`iputil`). |

### 3.7 AI, Crypto & Specialized Core Engines

| Component | Package | Details |
|-----------|---------|---------|
| **AI Embedder** | `internal/core/ai` | Pluggable embedding provider for `SEMANTIC_MATCH` and `AI_EMBED` SQL operations. Calls OpenAI-compatible embedding API endpoints or returns descriptive fallback errors when unconfigured. Injected via `SetEmbedder()`. |
| **Crypto & TDE** | `internal/core/crypto` | Cryptographic primitives and Transparent Data Encryption (TDE) key management. |
| **Full-Text Search** | `internal/core/fts` | Full-text search indexing and query processing engine. |
| **Wasm UDF** | `internal/core/wasmudf` | WebAssembly execution runtime for user-defined functions. |

### 3.8 Observability & Logging Infrastructure

| Component | Package | Details |
|-----------|---------|---------|
| **Metrics Collector** | `internal/core/metrics` | Query counters (by type/status), connection counters, active connections, storage row counts. Background updater syncs metrics every 30s. |
| **Audit Engine** | `internal/core/audit` | Core database audit logging engine tracking execution and security-sensitive DDL/DML operations. |
| **Logging Service** | `internal/logging` | General server logging infrastructure with structured audit log rotation (`internal/logging`). |
| **Config Loader** | `internal/config` | Hierarchical YAML config (`internal/config`) with env overrides. Validates all fields including port ranges, known values, and conflict detection. |

### 3.9 Client Ecosystem

- **Go SDK** (`client/go`): Go client SDK for connecting to VaultDB servers.
- **Python SDK** (`client/python`): Python 3 client SDK for integration with Python applications and data pipelines.
- **TypeScript/Node.js SDK** (`client/js`): TypeScript/Node.js client SDK (`client/js`) for web and backend applications.
- **libvaultdb** (`client/lib`): C++17 shared library (`client/lib`). OpenSSL-based TCP/TLS socket management, JSON request/response formatting. RAII connection, cross-platform (POSIX + Win32).
- **Shell** (`client/shell`): Interactive REPL (`client/shell`) with syntax highlighting and tab completion.
- **TUI** (`client/tui`): Panel-based terminal UI (`client/tui`). Screens for database browser, query editor, table viewer, settings. Built with panel-based architecture (screens, components, dialogs).
- **Client Tests** (`client/tests`): Unit and integration tests for client libraries (`client/tests`).
- **Web UI** (`internal/httpserver/web`): Embedded React dashboard (`internal/httpserver/web`) for real-time monitoring and query execution.

### 3.10 Tools & Utilities

- **Go Benchmark Suite** (`tools/benchmark`): Benchmark tool (`tools/benchmark/`) for stress-testing and evaluating engine throughput.
- **SQL Fuzzing** (`tools/sqlfuzz`): Fuzz testing utilities (`tools/sqlfuzz/`) for SQL parser and query evaluation.
- **Security Utilities** (`tools/security`): Scripts for security auditing, including `tls_scan.sh` and `generate-sbom.sh`.
- **Benchmark Gate Script** (`tools/benchstat-gate.sh`): Performance regression gate script (`tools/benchstat-gate.sh`) for CI workflows.

---

## 4. Internal Module Dependencies

```
cmd/vaultdb-server
  ├── internal/config
  ├── internal/core/storage (engine)
  │     ├── internal/core/storage/heap
  │     ├── internal/core/storage/page
  │     ├── internal/core/storage/fsm
  │     ├── internal/core/wal
  │     ├── internal/core/txmanager
  │     └── internal/core/index
  ├── internal/core/lexer
  ├── internal/core/parser
  ├── internal/core/executor
  │     ├── internal/core/metrics
  │     ├── internal/core/ai
  │     ├── internal/core/wal
  │     ├── internal/core/txmanager
  │     └── internal/core/storage
  ├── internal/httpserver
  │     ├── internal/websocket
  │     └── web/ (embedded)
  ├── internal/pool
  ├── internal/protocol
  ├── internal/auth
  ├── internal/tls
  └── internal/logging
```

Layer isolation (from high to low):
```
Client (C++, Go, Python, JS) → TCP/HTTP → Auth/Protocol → Parser → Optimizer → Executor → Storage/WAL
```

No circular dependencies between packages.

---

## 5. Key Data Flows

### Query Execution (SELECT)
```
SQL String
  → Lexer.NextToken() * N → []Token
  → Parser.Parse() → AST (SelectStatement)
  → Optimizer.FormatPlan() → Plan
  → Executor.Run() → Command.Execute()
     → Storage.SelectRows() / IndexLookup()
     → Evaluator.Eval() for WHERE/HAVING/ORDER BY
     → Result (Columns + Rows)
```

### Write Transaction (INSERT/UPDATE/DELETE)
```
SQL
  → Parser → AST
  → TxManager.Begin() → Transaction
  → For each row:
       WAL.Append(OpPageInsert, payload)  ← before page modification
       BufferPool.PinPage(pid)
       Page.InsertTuple(data)
       BufferPool.UnpinPage(pid, dirty=true)
  → TxManager.Commit()
       LockTables()
       Check snapshot versions for conflicts
       Apply ops to storage
       WAL.Append(OpCommit)
       Release locks
  → On crash recovery:
       WAL.AnalyzeTransactions() → committed / in-progress
       WAL.Replay() → redo all
       WAL.ReplayTransaction(xid) → undo in-progress
```

### Checkpoint Cycle (every 30s)
```
PageStorageEngine.CheckpointLoop()
  → WAL.Flush() (fsync)
  → BufferPool.FlushDirtyPagesUpToLSN()
  → saveCatalogLocked()
  → WAL.Append(OpCheckpoint)
  → WAL.Checkpoint() (truncate)
```

---

## 6. Code Statistics

| Measure | Count (approx.) |
|---------|----------------|
| Go packages | 12 internal + 13 core + 4 cmd |
| Go source files | ~90 |
| Go test files | ~50 |
| C++ source files | ~25 |
| Lines of Go code | ~25,000 |
| Lines of C++ code | ~5,000 |
| Test files with fixtures | 10+ packages |

---

## 7. Architecture Decisions & Known Issues

### 7.1 Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Command pattern via `reflect`** | Flexible statement→command mapping, but registration must be explicit in `init()`. Missing registration detected only at runtime. |
| **Binary tuple format** | 16-byte fixed header (created_tx + deleted_tx) enables in-place versioning and efficient vacuum. Offset array allows O(1) column access. |
| **Batch WAL fsync** | Default sync every 64 writes trades a small window of potential data loss for ~10-50x throughput improvement on write-heavy workloads. |
| **Page engine + WAL ownership** | Page engine owns both the heap storage and the WAL. This enables ARIES-style recovery where WAL replay directly modifies pages. |
| **Connection pool as counter** | Pool effectively tracks max concurrent connections rather than reusing idle ones. Each connection gets its own goroutine. |

### 7.2 Known Architecture Gaps

Identified during code audit — see `audit.md` for full details:

| Issue | Priority | Description | Status |
|-------|----------|-------------|--------|
| Lock ordering WAL↔PageEngine | Critical | `doCheckpoint()` takes `mu` then `wal.mu`; recovery callbacks take `wal.mu` then `mu`. | Fixed — `mu` released before WAL append in checkpoint |
| Context.Background() in executor | High | Query timeouts use `context.Background()` instead of server shutdown context — long queries aren't cancelled on graceful shutdown. | Verified — no `context.Background()` found in current code |
| WAL silent error swallowing | High | Corrupt WAL entries in the middle of the file cause all subsequent valid entries to be silently lost. | Fixed — resync by scanning for next VDB1 magic bytes after corrupt entries |
| getTableForRead/Write duplication | Medium | ~45 lines duplicated between read/write variants, differing only in `RLock` vs `Lock`. | Acceptable — only 8 lines each, minimal and clear |

---

## 8. Extending VaultDB

### Adding a new statement type
1. Define AST node in `internal/core/parser/ast.go`
2. Add parsing in the appropriate `parse_*.go`
3. Register command in `internal/core/executor/commands/` or `internal/core/executor/executor.go` `init()`
4. Implement `Command` interface (`.Execute()`)
5. Add tests in `internal/core/executor/commands/` (`commands_*.go`) and `parser_test.go`

### Adding a new index type
1. Implement the index in `internal/core/index/<type>.go`
2. Wire it into `internal/core/index/manager.go`
3. Add WAL operation type in `internal/core/wal/wal.go`
4. Integrate in `internal/core/storage/page_engine_index.go`

### Adding a new storage engine
1. Implement `storage.StorageEngine` interface (composes `ReadOnlyEngine` + `WriteEngine` + `AdminEngine`)
2. Register in `internal/config/config.go` validation (optional)
3. Wire in `cmd/vaultdb-server/main.go` `setupStorage()`
