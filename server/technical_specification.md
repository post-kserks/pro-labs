# VaultDB Technical Specification (v2.0)

## 1. Architectural Overview
VaultDB is an enterprise-grade SQL database engine written in Go. It provides standard PostgreSQL wire compatibility (`pgwire`) and Native TCP Protocol v2, aiming for high performance, high availability, and strong security out of the box.

### Core Subsystems
1. **Parser & Lexer**: Custom SQL parser generating ASTs.
2. **Optimizer**: Cost-Based Optimizer (CBO) leveraging Dynamic Programming for Join Reordering and Equi-depth Histograms (`system.pg_statistic`).
3. **Execution Engine (JIT VM)**: 
   - Zero-reflection stack virtual machine (`internal/core/executor/eval/vm`) that compiles AST predicate expressions into bytecode.
   - Provides up to 5x speedup for `WHERE` clause evaluation.
4. **Storage Engine (Page-Based)**:
   - Append-only heap pages with **HOT (Heap-Only Tuples)** chains to mitigate write amplification and index bloat.
   - **Buffer Pool**: Clock-Sweep eviction policy.
5. **Concurrency Control (MVCC)**:
   - Tuple versioning (`xmin`, `xmax`) managed via a centralized `TxManager`.
   - Read Committed and Snapshot Isolation levels.
6. **Transaction Log (WAL)**:
   - Binary payload format for efficiency.
   - Group commit and Checkpointer workers.
   - **PITR** (Point-in-Time Recovery) and Archiving support.
7. **High Availability (Raft)**:
   - Built-in multi-node Raft consensus (`internal/cluster/raft`).
   - Synchronous quorum-based WAL replication.

## 2. Advanced Features

### 2.1 Security & Compliance
- **Transparent Data Encryption (TDE)**: AES-256-GCM encryption applied at the page and WAL level.
- **Dynamic Data Masking**: Column-level masking policies evaluated at runtime during projection, enforcing the `UNMASK` privilege.
- **RBAC**: Role-Based Access Control via `GRANT/REVOKE`.
- **Audit Log**: SHA-256 hash-chained audit trails preventing tampering.

### 2.2 Storage Optimizations
- **HOT Updates**: Updates that do not modify indexed columns append new tuple versions to the same page, linked via slot chains.
- **B-Tree & FTS / GIN**: Full-Text Search via BM25, and GIN indexing for `JSONB` structures.

## 3. Comparison with PostgreSQL (Where VaultDB Falls Behind)
While VaultDB has modern built-in features (TDE, Raft) that typically require extensions in Postgres, it lacks in several advanced database capabilities:
1. **Advanced Indexing**: No GiST or SP-GiST for Geospatial data. No BRIN indexes for append-heavy time-series data.
2. **Strict Locking & SSI**: No `SELECT ... FOR UPDATE` row-level locks, and no true Serializable Snapshot Isolation (SSI) dependency tracking.
3. **Change Data Capture (CDC)**: No logical decoding framework to stream changes to Kafka/Debezium.
4. **Procedural Capabilities**: Basic PL/pgSQL; missing `EXCEPTION` blocks, triggers, and cursors.

## 4. Current Milestone (Milestone 6)

### Track 1: Concurrency Control & Row-Level Locking
- Introduce `internal/core/storage/lock_manager.go` for in-memory row-level locks.
- Implement `SELECT ... FOR UPDATE` syntax.
- Build foundation for Serializable Snapshot Isolation (SSI) in `TxManager`.

### Track 2: Logical Decoding & CDC
- Implement `internal/core/wal/logical.go` to parse binary WAL payloads into `Insert/Update/Delete` logical events.
- Expose logical replication streams via `pgwire` using PostgreSQL logical replication protocol commands (`START_REPLICATION SLOT`).
