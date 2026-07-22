# VaultDB Documentation

**Version 1.2.0**

VaultDB is an enterprise-first, embeddable SQL database engine featuring PostgreSQL Wire Protocol (`pgwire`) and native JSON v2 protocol support, HOT (Heap-Only Tuples), Cost-Based Optimization (`ANALYZE` & `system.pg_statistic`), Raft Consensus HA, Bytecode VM / JIT compilation, Transparent Data Encryption (TDE), Dynamic Data Masking, AutoVacuum & Checkpointer background workers, System Views (`pg_stat_activity`, `pg_locks`), and `KILL QUERY` controls.

---

## Table of Contents

### Getting Started

- [Introduction](introduction.md) — What VaultDB is and what it does
- [Installation](installation.md) — Building from source, Docker, Docker Compose
- [Quick Start](quickstart.md) — Your first queries in under 5 minutes
- [Deployment](deployment.md) — Production deployment guide
- [Deployment (Enterprise)](deployment-enterprise.md) — Enterprise deployment with TDE and audit logging

### User Guide

- [Configuration](configuration.md) — All configuration options, environment variables, CLI flags
- [SQL Reference](sql-reference.md) — Complete SQL syntax reference
- [Data Types](data-types.md) — Supported column types and their properties
- [Functions and Operators](functions.md) — All built-in functions, operators, and expressions
- [Indexes](indexes.md) — B-tree, Hash, GIN, GiST, Composite indexes
- [Transactions](transactions.md) — BEGIN/COMMIT/ROLLBACK, SAVEPOINT, isolation
- [Views](views.md) — Creating and using views
- [Triggers](triggers.md) — AFTER triggers for INSERT, UPDATE, DELETE
- [Sequences](sequences.md) — AUTO_INCREMENT and sequence management
- [User-Defined Functions](udf.md) — Creating custom functions and WASM UDFs
- [Security](security.md) — Authentication, TLS, mTLS, RLS, audit logging, token revocation

### Architecture

- [Architecture Overview](architecture.md) — System design, component interaction
- [Storage Engine](storage.md) — Page-based storage, heap files, tuple format
- [WAL and Recovery](wal.md) — Write-ahead logging, ARIES-style crash recovery
- [MVCC and Concurrency](mvcc.md) — Multi-version concurrency control, locking hierarchy
- [Query Optimizer](optimizer.md) — Cost-based optimization, index selection, join strategies

### Administration

- [Backup and Restore](backup.md) — Backup format, procedures, CLI tool
- [Monitoring and Metrics](monitoring.md) — Prometheus metrics, health checks, dashboards
- [Encryption](encryption.md) — Transparent Data Encryption, key management, SQL syntax
- [Hardening Checklist](hardening-checklist.md) — Production security hardening steps
- [Security Instructions](security/security-instruction.md) — Security policies and guidelines
- [Benchmarks Summary](benchmarks/baseline-summary.md) — Performance baselines and stress testing

### Reference

- [HTTP API Reference](api-reference.md) — REST endpoints, request/response formats
- [TCP Protocol](tcp-protocol.md) — Protocol v2 wire format overview
- [Protocol v2 Specification](protocol/PROTOCOL_v2.md) — Detailed wire protocol v2 specification
- [C++ Client](client.md) — Building and using the C++ client library
- [AI and Semantic Search](ai.md) — Embedding providers, vector operations
- [Glossary](glossary.md) — Terminology reference
