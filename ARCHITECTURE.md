# VaultDB Architecture

This document provides a visual overview of the VaultDB system architecture, including the latest additions to the core engine and storage layers.

## System Map

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
            
            HTTPSrv --- RateLimit
            HTTPSrv --- TLS
            TCPSrv --- TLS
        end

        WebUI["Embedded Web UI<br/>(internal/httpserver/web)"]
        
        subgraph Core ["Core Engine"]
            Auth["Auth Manager<br/>(internal/auth)"]
            Lexer["Lexer<br/>(internal/lexer)"]
            Parser["Parser (AST)<br/>(internal/parser)"]
            Optimizer["Query Optimizer<br/>(internal/executor/optimizer)"]
            Stats["Statistics<br/>(internal/executor/statistics)"]
            Executor["Executor (Engine)<br/>(internal/executor)"]
            CTE["CTE Handler<br/>(internal/executor/cte)"]
            TxMgr["Tx Manager<br/>(internal/txmanager)"]
            Pool["Worker Pool<br/>(internal/pool)"]
            
            Lexer --> Parser
            Parser --> Optimizer
            Optimizer --- Stats
            Optimizer --> Executor
            Executor --- CTE
            Executor --> TxMgr
            Executor --> Pool
        end

        subgraph StorageLayer ["Storage & Indexing"]
            IndexMgr["Index Manager<br/>(internal/index)"]
            BTree["B-Tree Index<br/>(internal/index/btree)"]
            HashIndex["Hash Index<br/>(internal/index/hash_index)"]
            
            Storage["Storage Engine<br/>(internal/storage)"]
            BufferPool["Buffer Pool (LRU)<br/>(internal/storage/buffer_pool)"]
            PageLock["Page Locking<br/>(internal/storage/page_lock)"]
            WAL["WAL/Recovery<br/>(internal/wal)"]
            
            IndexMgr --> BTree
            IndexMgr --> HashIndex
            Storage --> BufferPool
            BufferPool --- PageLock
            Storage --> WAL
        end

        subgraph Utils ["Observability & Utilities"]
            Audit["Audit Logger<br/>(internal/audit)"]
            Logging["Log Rotator<br/>(internal/logging)"]
            Metrics["Metrics Collector<br/>(internal/metrics)"]
        end

        HTTPSrv --- Auth
        TCPSrv --- Auth
        HTTPSrv --- WebUI
        
        HTTPSrv --> Lexer
        TCPSrv --> Lexer
        
        Executor --> IndexMgr
        Executor --> Storage
        
        Executor -.-> Audit
        Auth -.-> Audit
        Storage --- Metrics
    end

    subgraph Data ["Persistence"]
        DBFiles[(".json DB Files<br/>(data/databases)")]
        WALFile[(".wal File<br/>(data/wal)")]
        Storage --> DBFiles
        WAL --> WALFile
    end

    Lib -- "mTLS/TCP (5432)" --> TCPSrv
    WebUI -- "REST API (8080)" --> HTTPSrv
```

## Component Overview

### 1. Server (Go)
- **SQL Pipeline**: Lexer -> Parser -> **Optimizer** -> Executor.
  - **Optimizer**: Implements cost-based decisions for access methods (SeqScan vs IndexScan) using table statistics.
  - **CTEs**: Support for Common Table Expressions and recursive queries.
- **Storage Engine**: JSON-based storage with versioned rows (Time Travel).
  - **Buffer Pool**: LRU caching layer for efficient page management and reduced disk I/O.
  - **Concurrency**: Page-level locking and optimistic concurrency control.
- **Indexing**: Support for both Hash and B-Tree indexes for efficient data retrieval.
- **Reliability**: WAL (Write-Ahead Log) for crash recovery.
- **Security**: Built-in mTLS support and API rate limiting.

### 2. Clients (C++)
- **libvaultdb**: Communication layer for custom binary protocol.
- **TUI/Shell**: Interactive interfaces for database management.

### 3. Web UI
- Built with React/Vite, embedded into the Go binary.
- Communicates via REST API.

### 4. Observability
- **Audit Logging**: Structured logging for DDL, DML, and Auth events.
- **Metrics**: Integration with Prometheus for real-time monitoring.
