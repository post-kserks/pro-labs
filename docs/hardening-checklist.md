# VaultDB Phase 7 — Hardening Checklist

> **Purpose**: Track crash testing, benchmark regression gates, edge cases, security hardening, documentation validation, fuzz testing, and test coverage for Phase 7 readiness.
>
> **Legend**: ✅ = done, ⬜ = TODO

---

## 1. Crash Testing

| # | Scenario | Status | Notes |
|---|----------|--------|-------|
| 1.1 | Kill server mid-write (SIGKILL) | ⬜ | |
| 1.2 | Kill server mid-transaction commit | ⬜ | |
| 1.3 | Kill server during WAL flush | ⬜ | |
| 1.4 | Kill server during compaction | ⬜ | |
| 1.5 | Kill server during index rebuild | ⬜ | |
| 1.6 | Disk full during write | ⬜ | |
| 1.7 | OOM during large query | ⬜ | |
| 1.8 | Power loss during checkpoint | ⬜ | |
| 1.9 | Kill during replication sync | ⬜ | |
| 1.10 | Kill during backup creation | ⬜ | |

---

## 2. Benchmark Regression Gate

| # | Metric | Baseline (Phase 6) | Current | Status | Notes |
|---|--------|--------------------|---------|--------|-------|
| 2.1 | Insert throughput (rows/s) | TBD | TBD | ⬜ | |
| 2.2 | Select throughput (rows/s) | TBD | TBD | ⬜ | |
| 2.3 | Update throughput (rows/s) | TBD | TBD | ⬜ | |
| 2.4 | Delete throughput (rows/s) | TBD | TBD | ⬜ | |
| 2.5 | Point query latency (p50, ms) | TBD | TBD | ⬜ | |
| 2.6 | Point query latency (p99, ms) | TBD | TBD | ⬜ | |
| 2.7 | Range scan latency (p50, ms) | TBD | TBD | ⬜ | |
| 2.8 | Range scan latency (p99, ms) | TBD | TBD | ⬜ | |
| 2.9 | Concurrent connections (max) | TBD | TBD | ⬜ | |
| 2.10 | Memory footprint (RSS, MB) | TBD | TBD | ⬜ | |
| 2.11 | Startup time (s) | TBD | TBD | ⬜ | |
| 2.12 | Recovery time after crash (s) | TBD | TBD | ⬜ | |

---

## 3. Edge Cases

| # | Scenario | Status | Notes |
|---|----------|--------|-------|
| 3.1 | Empty table operations | ⬜ | |
| 3.2 | Single-row table operations | ⬜ | |
| 3.3 | Max-size VARCHAR/TEXT insert | ⬜ | |
| 3.4 | NULL value handling across types | ⬜ | |
| 3.5 | Unicode/emoji in strings | ⬜ | |
| 3.6 | Zero-precision DECIMAL | ⬜ | |
| 3.7 | Negative timestamps | ⬜ | |
| 3.8 | Boundary integer values (MIN/MAX) | ⬜ | |
| 3.9 | Nested JSON structures | ⬜ | |
| 3.10 | Empty JSON object/array | ⬜ | |
| 3.11 | Concurrent DDL on same table | ⬜ | |
| 3.12 | Transaction with zero statements | ⬜ | |
| 3.13 | Connection with empty SQL | ⬜ | |
| 3.14 | SQL with only whitespace | ⬜ | |
| 3.15 | Table with maximum column count | ⬜ | |
| 3.16 | Table with maximum index count | ⬜ | |
| 3.17 | Very long table/column names | ⬜ | |
| 3.18 | Reserved word as identifier | ⬜ | |
| 3.19 | Cross-database reference | ⬜ | |
| 3.20 | Self-referencing foreign key | ⬜ | |

---

## 4. Security Hardening

| # | Check | Status | Notes |
|---|-------|--------|-------|
| 4.1 | SQL injection prevention (all inputs) | ⬜ | |
| 4.2 | Path traversal prevention | ⬜ | |
| 4.3 | Command injection prevention | ⬜ | |
| 4.4 | Buffer overflow testing | ⬜ | |
| 4.5 | Integer overflow testing | ⬜ | |
| 4.6 | Authentication bypass testing | ⬜ | |
| 4.7 | Privilege escalation testing | ⬜ | |
| 4.8 | TLS certificate validation | ⬜ | |
| 4.9 | Secrets not logged | ⬜ | |
| 4.10 | Secrets not in error messages | ⬜ | |
| 4.11 | Rate limiting on auth endpoints | ⬜ | |
| 4.12 | Session timeout enforcement | ⬜ | |
| 4.13 | Input length limits enforced | ⬜ | |
| 4.14 | Memory-safe deserialization | ⬜ | |
| 4.15 | Denial-of-service resilience | ⬜ | |

---

## 5. Documentation

| # | Task | Status | Notes |
|---|------|--------|-------|
| 5.1 | API reference reflects current interface | ⬜ | |
| 5.2 | SQL reference covers all supported syntax | ⬜ | |
| 5.3 | Configuration docs match code defaults | ⬜ | |
| 5.4 | Deployment guide tested end-to-end | ⬜ | |
| 5.5 | Quickstart guide produces working install | ⬜ | |
| 5.6 | Security docs cover all hardening measures | ⬜ | |
| 5.7 | Architecture doc reflects current design | ⬜ | |
| 5.8 | Protocol spec matches implementation | ⬜ | |
| 5.9 | Glossary is consistent with codebase | ⬜ | |
| 5.10 | All code examples in docs compile/run | ⬜ | |

---

## 6. Fuzz Testing

| # | Target | Corpus Size | Iterations | Crashes Found | Status | Notes |
|---|--------|-------------|------------|---------------|--------|-------|
| 6.1 | SQL parser | TBD | TBD | TBD | ⬜ | |
| 6.2 | Expression evaluator | TBD | TBD | TBD | ⬜ | |
| 6.3 | Binary protocol decoder | TBD | TBD | TBD | ⬜ | |
| 6.4 | JSON parser | TBD | TBD | TBD | ⬜ | |
| 6.5 | WAL replay | TBD | TBD | TBD | ⬜ | |
| 6.6 | Storage engine (page reader) | TBD | TBD | TBD | ⬜ | |
| 6.7 | Backup/restore parser | TBD | TBD | TBD | ⬜ | |

---

## 7. Test Coverage

| # | Package | Current % | Target % | Status | Notes |
|---|---------|-----------|----------|--------|-------|
| 7.1 | `core` | TBD | ≥80% | ⬜ | |
| 7.2 | `storage` | TBD | ≥80% | ⬜ | |
| 7.3 | `sql` | TBD | ≥80% | ⬜ | |
| 7.4 | `network` | TBD | ≥80% | ⬜ | |
| 7.5 | `auth` | TBD | ≥85% | ⬜ | |
| 7.6 | `wal` | TBD | ≥80% | ⬜ | |
| 7.7 | `backup` | TBD | ≥80% | ⬜ | |
| 7.8 | `replication` | TBD | ≥80% | ⬜ | |
| 7.9 | `optimization` | TBD | ≥75% | ⬜ | |
| 7.10 | `transaction` | TBD | ≥85% | ⬜ | |

---

## 8. Summary

| Category | Total | Done | TODO | Progress |
|----------|-------|------|------|----------|
| Crash Testing | 10 | 0 | 10 | 0% |
| Benchmark Regression Gate | 12 | 0 | 12 | 0% |
| Edge Cases | 20 | 0 | 20 | 0% |
| Security Hardening | 15 | 0 | 15 | 0% |
| Documentation | 10 | 0 | 10 | 0% |
| Fuzz Testing | 7 | 0 | 7 | 0% |
| Test Coverage | 10 | 0 | 10 | 0% |
| **Total** | **84** | **0** | **84** | **0%** |

---

*Last updated: 2026-07-05*
