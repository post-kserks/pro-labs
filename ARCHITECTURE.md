# VaultDB Architecture

This document provides a visual overview of the VaultDB system architecture.

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
        HTTPSrv["HTTP Server<br/>(internal/httpserver)"]
        TCPSrv["TCP Server<br/>(cmd/vaultdb-server)"]
        WebUI["Embedded Web UI<br/>(internal/httpserver/web)"]
        
        Auth["Auth Manager<br/>(internal/auth)"]
        Lexer["Lexer<br/>(internal/lexer)"]
        Parser["Parser (AST)<br/>(internal/parser)"]
        Executor["Executor<br/>(internal/executor)"]
        TxMgr["Tx Manager<br/>(internal/txmanager)"]
        IndexMgr["Index Manager<br/>(internal/index)"]
        Storage["Storage Engine<br/>(internal/storage)"]
        WAL["WAL/Recovery<br/>(internal/wal)"]
        Metrics["Metrics Collector<br/>(internal/metrics)"]

        HTTPSrv --- Auth
        TCPSrv --- Auth
        HTTPSrv --- WebUI
        
        HTTPSrv --> Lexer
        TCPSrv --> Lexer
        Lexer --> Parser
        Parser --> Executor
        Executor --> TxMgr
        Executor --> IndexMgr
        Executor --> Storage
        Storage --> WAL
        Storage --- Metrics
    end

    subgraph Data ["Persistence"]
        DBFiles[(".json DB Files<br/>(data/databases)")]
        WALFile[(".wal File<br/>(data/wal)")]
        Storage --> DBFiles
        WAL --> WALFile
    end

    Lib -- "TCP (5432)" --> TCPSrv
    WebUI -- "REST API (8080)" --> HTTPSrv
    Prometheus["Prometheus"] -- "Metrics (5433)" --> HTTPSrv
```

## Component Overview

### 1. Server (Go)
- **SQL Pipeline**: Lexer -> Parser -> Executor.
- **Query Support**:
  - Full relational algebra: SELECT, FROM, WHERE, JOIN (Nested Loop), GROUP BY, HAVING.
  - Sorting & Pagination: ORDER BY, LIMIT, OFFSET.
  - Aggregates: COUNT, SUM, AVG, MIN, MAX.
  - Set Operations: UNION (ALL), INTERSECT, EXCEPT.
  - Expressions: Arithmetic (+, -, *, /) and projection aliases.
- **Storage Engine**: JSON-based storage with versioned rows (Time Travel).
- **Transaction Management**: Optimistic Concurrency Control.
- **Reliability**: WAL (Write-Ahead Log) for crash recovery.

### 2. Clients (C++)
- **libvaultdb**: Communication layer for custom binary protocol.
- **TUI/Shell**: Interactive interfaces for database management.

### 3. Web UI
- Built with React/Vite, embedded into the Go binary.
- Communicates via REST API.

