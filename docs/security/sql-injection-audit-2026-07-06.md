# SQL Injection Audit Report — Round 2

| Attribute | Value |
|---|---|
| Date | 2026-07-06 |
| Scope | All Go source under `server/internal/` — new features + re-verification |
| Previous audit | 2026-07-05 (8 findings, 5 fixed) |

---

## Executive Summary

本轮审计覆盖新增功能 (COPY, partitioning, JSONB, DISTINCT ON, WASM UDF, protocol v2) 并重新验证5个已修复发现。

**新发现**: 4 Critical, 6 High, 6 Medium, 4 Low — 共 20 条
**已修复发现**: 5/5 仍然安全，无回归
**本轮修复**: 11 条 (4 Critical, 5 High, 2 Medium)

### Fix Status (2026-07-06)

| Finding | Status | Fix |
|---|---|---|
| C1 COPY FROM path traversal | **FIXED** | `validateCopyPath()` — rejects absolute, traversal, escapes data dir |
| C2 COPY TO path traversal | **FIXED** | Same `validateCopyPath()` applied to COPY TO |
| C3 WASM memory limit dead code | **FIXED** | `NewRuntimeWithLimits()` enforces page cap via wazero config |
| C4 WASI unrestricted filesystem | **FIXED** | WASI instantiation removed — WASM gets zero host access |
| H1 WASM no default timeout | **FIXED** | 30s default timeout always applied |
| H2 WASM file:// traversal | **FIXED** | `validateWASMPath()` — resolves and contains within DataDir |
| H3 COPY delimiter DoS | **FIXED** | Max 20 chars validated in parser |
| H4 Migration DROP TABLE | **FIXED** | Removed from `isMigrationSafe()` allowed list |
| H5 Procedure semicolon split | **FIXED** | `splitSQLStatements()` — state-machine respecting quotes |
| H6 Function subquery DML | **FIXED** | `containsSubqueryDML()` — recursive AST walk |
| M5 Migration ALTER TABLE | **FIXED** | `isAlterTableSafe()` — whitelist ADD COLUMN/CONSTRAINT only |

---

## Part 1: Original Findings — Re-verification

| # | Finding | Verdict | Evidence |
|---|---------|---------|----------|
| 1 | HTTP API AST-level binding | **STILL SECURE** | `applyParams` deleted; `bindHTTPParams` at server_handlers.go |
| 2 | Triggers re-entrant guard | **STILL SECURE** | `triggerDepth` field checked, max=3 |
| 3 | Migration body validation | **STILL SECURE** | `isMigrationSafe()` enforced at CREATE time |
| 4 | Function SELECT-only check | **STILL SECURE** | Type assertion to `*parser.SelectStatement` |
| 5 | Procedure allowed-statement check | **STILL SECURE** | `isProcedureBodySafe()` type-switch |

**结论**: 无回归。所有验证在 CREATE 时基于 AST 节点执行，非字符串匹配。

---

## Part 2: New Findings

### CRITICAL

#### C1 — COPY FROM: Arbitrary File Read

- **File**: `server/internal/executor/commands_copy.go`
- **Mitigation status**: **FIXED (2026-07-06)** — `validateCopyPath()` rejects absolute paths, traversal, and escapes data directory boundary.

#### C2 — COPY TO: Arbitrary File Write / Data Exfiltration

- **File**: `server/internal/executor/commands_copy.go`
- **Mitigation status**: **FIXED (2026-07-06)** — Same `validateCopyPath()` applied to COPY TO.

#### C3 — WASM Memory Limit is Dead Code

- **File**: `server/internal/core/wasmudf/runtime.go`
- **Mitigation status**: **FIXED (2026-07-06)** — `NewRuntimeWithLimits()` enforces page cap via `wazero.NewRuntimeConfig().WithMemoryLimitPages()`. Default 256 pages (16 MB).

#### C4 — WASI Gives Unrestricted Filesystem Access

- **File**: `server/internal/core/wasmudf/runtime.go`
- **Mitigation status**: **FIXED (2026-07-06)** — WASI instantiation removed entirely. WASM UDFs get zero host filesystem/process access.

---

### HIGH

#### H1 — WASM: No Default Execution Deadline

- **File**: `server/internal/core/wasmudf/runtime.go`
- **Mitigation status**: **FIXED (2026-07-06)** — 30s default timeout always applied via `DefaultTimeout` constant.

#### H2 — WASM `file://` Path Allows Traversal

- **File**: `server/internal/executor/commands_ddl_misc.go`
- **Mitigation status**: **FIXED (2026-07-06)** — `validateWASMPath()` resolves relative to DataDir and verifies containment.

#### H3 — COPY Delimiter Unbounded Length (DoS)

- **File**: `server/internal/parser/parse_copy.go`
- **Mitigation status**: **FIXED (2026-07-06)** — Max 20 characters validated in parser.

#### H4 — Migration Allows Unrestricted DROP TABLE

- **File**: `server/internal/executor/commands_ddl_misc.go`
- **Mitigation status**: **FIXED (2026-07-06)** — DROP TABLE and DROP INDEX removed from `isMigrationSafe()` allowed list.

#### H5 — Procedure Body: Semicolon Split Bypass

- **File**: `server/internal/executor/commands_ddl_misc.go`
- **Mitigation status**: **FIXED (2026-07-06)** — `splitSQLStatements()` — state-machine respecting quotes and escapes replaces naive `strings.Split`.

#### H6 — SQL Function: Subquery DML Bypass

- **File**: `server/internal/executor/commands_ddl_misc.go`
- **Mitigation status**: **FIXED (2026-07-06)** — `containsSubqueryDML()` — recursive AST walk detects DML in any subquery position.

---

### MEDIUM

#### M1 — Partition Names: Implicit Validation Only

- **File**: `server/internal/parser/parse_ddl.go:1208`
- **Description**: Partition names validated only via lexer constraints and storage-layer check. No explicit validation in parser.
- **Mitigation**: Partial (implicit via lexer)

#### M2 — WASM: No Binary Validation Beyond Format

- **File**: `server/internal/core/wasmudf/runtime.go:55`
- **Description**: `CompileModule` checks structural validity but not disallowed imports, excessive code sections, or suspicious exports.
- **Mitigation**: None

#### M3 — WASM: Silent `passArgs` Error Swallowing

- **File**: `server/internal/core/wasmudf/runtime.go`
- **Mitigation status**: **FIXED (2026-07-06)** — Error now propagates to callers instead of being silently discarded.

#### M4 — Protocol v2: Handshake Not Implemented Server-Side

- **File**: `server/internal/protocol/protocol.go`
- **Description**: Handshake structs exist but server has no TCP handler for them. v2 feature negotiation is client-side only.
- **Mitigation**: N/A (feature incomplete)

#### M5 — Migration Allows Unrestricted ALTER TABLE

- **File**: `server/internal/executor/commands_ddl_misc.go`
- **Mitigation status**: **FIXED (2026-07-06)** — `isAlterTableSafe()` — whitelist: ADD COLUMN, ADD CONSTRAINT only.

#### M6 — COPY FROM STDIN: Future Risk

- **File**: `server/internal/executor/commands_copy.go:37-39`
- **Description**: Currently returns error (safe). When implemented, ensure input size bounds.
- **Mitigation**: N/A (not implemented)

---

### LOW

#### L1 — Protocol: Auth Token in Plaintext

- **File**: `client/go/tcp_client.go:107`
- **Description**: API key sent as plaintext JSON over unencrypted TCP. TLS exists but not mandatory.
- **Mitigation**: TLS available, not enforced

#### L2 — Protocol: Predictable Request IDs

- **File**: `client/go/tcp_client.go:106`
- **Description**: Query IDs use `time.Now().UnixNano()` — sequential and predictable.
- **Mitigation**: None (minor, no direct exploit)

#### L3 — Protocol: No Anti-Replay on Handshake

- **File**: `server/internal/protocol/protocol.go:4-9`
- **Description**: Handshake has no nonce/timestamp. Captured payloads could be replayed.
- **Mitigation**: N/A (server-side handshake not implemented)

#### L4 — WASM: No Export Restriction

- **File**: `server/internal/core/wasmudf/runtime.go`
- **Description**: Any WASM export is accepted. No restriction on dangerous exports like `memory.grow` loops.
- **Mitigation**: None

---

## Part 3: Recommendations

| Priority | Finding | Action | Status |
|----------|---------|--------|--------|
| **P0** | C1, C2 | Path sandboxing for COPY | **DONE** |
| **P0** | C3, C4 | WASM memory limits + WASI restriction | **DONE** |
| **P0** | H1 | WASM default timeout | **DONE** |
| **P0** | H2 | WASM file:// path validation | **DONE** |
| **P1** | H3 | COPY delimiter length cap | **DONE** |
| **P1** | H4 | Migration: remove DROP TABLE | **DONE** |
| **P1** | H5 | Procedure: SQL-aware semicolon split | **DONE** |
| **P1** | H6 | Function: recursive AST DML walk | **DONE** |
| **P2** | M5 | Migration: restrict ALTER TABLE | **DONE** |
| **P2** | M3 | WASM: propagate passArgs errors | **DONE** |
| **P2** | M1, M2, M4, M6 | See individual findings | Open |
| **P2** | L1-L4 | See individual findings | Open |

---

## Comparison with Previous Audit

| Metric | Round 1 (2026-07-05) | Round 2 (2026-07-06) |
|--------|---------------------|---------------------|
| Total findings | 8 | 20 |
| Critical | 0 | 4 (4 fixed) |
| High | 3 | 6 (5 fixed) |
| Medium | 3 | 6 (2 fixed) |
| Low | 2 | 4 |
| Fixed (original) | 5 | 5 (all still secure) |
| Fixed (new) | — | 11 |
| Remaining open | 2 | 9 |

**注**: 新发现主要来自 COPY (无路径验证) 和 WASM UDF (无内存/WASI限制) — 这两个功能在上次审计时尚未实现。11/20 已修复，9 条低风险/Medium findings 留待后续。
