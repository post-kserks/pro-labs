# VaultDB Documentation

**Version 1.1.0**

VaultDB is a SQL-compatible database engine with a custom page-based storage engine, WAL-based crash recovery, MVCC transactions, multiple index types, and both TCP and HTTP interfaces.

---

## Table of Contents

### Getting Started

- [Introduction](introduction.md) — What VaultDB is and what it does
- [Installation](installation.md) — Building from source, Docker, Docker Compose
- [Quick Start](quickstart.md) — Your first queries in under 5 minutes

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
- [User-Defined Functions](udf.md) — Creating custom functions and procedures
- [Row-Level Security](rls.md) — Per-user data access policies

### Architecture

- [Architecture Overview](architecture.md) — System design, component interaction
- [Storage Engine](storage.md) — Page-based storage, heap files, tuple format
- [WAL and Recovery](wal.md) — Write-ahead logging, ARIES-style crash recovery
- [MVCC and Concurrency](mvcc.md) — Multi-version concurrency control, locking hierarchy
- [Query Optimizer](optimizer.md) — Cost-based optimization, index selection, join strategies

### Administration

- [Backup and Restore](backup.md) — Backup format, procedures, CLI tool
- [Monitoring and Metrics](monitoring.md) — Prometheus metrics, health checks, dashboards
- [Log Rotation](logging.md) — Structured logging, audit trail
- [Security](security.md) — Authentication, TLS, mTLS, brute-force protection

### Reference

- [HTTP API Reference](api-reference.md) — REST endpoints, request/response formats
- [TCP Protocol](tcp-protocol.md) — Wire protocol for native clients
- [C++ Client](client.md) — Building and using the C++ client library
- [AI and Semantic Search](ai.md) — Embedding providers, vector operations
- [Glossary](glossary.md) — Terminology reference
