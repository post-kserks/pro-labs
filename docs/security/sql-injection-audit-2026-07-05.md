# SQL Injection Audit Report

| Attribute | Value |
|---|---|
| Date | 2026-07-05 |
| Auditor | Automated (MiMoCode explore agent) |
| Scope | All Go source under `server/internal/` |
| Method | Static analysis of `parser.Value.StrVal` usage, string concatenation into SQL, re-parsing contexts |

---

## Executive Summary

8 findings identified across prepared statements, stored procedure/function/trigger bodies, view queries, migrations, RLS policies, CHECK constraints, and the HTTP API parameter substitution layer.

**Critical**: 0 | **High**: 3 (3 fixed) | **Medium**: 3 (2 fixed) | **Low**: 2

### Fix Status (2026-07-05)

| Finding | Status | Fix |
|---|---|---|
| #1 HTTP API text-substitution | **FIXED** | Migrated to AST-level binding via `BindParams()` |
| #4 Triggers re-entrant guard | **FIXED** | Added `triggerDepth` counter, max depth 3 |
| #3 Migrations validation | **FIXED** | Body validated at CREATE time, system tables rejected |
| #5 Functions validation | **FIXED** | Body validated at CREATE time, SELECT-only for SQL functions |
| #6 Procedures validation | **FIXED** | Body validated at CREATE time, DML-only for SQL procedures |

---

## Findings

### 1. HTTP API Text-Substitution Params

- **File**: `server/internal/httpserver/server_handlers.go:1078-1143`
- **Risk**: **High**
- **Description**: `applyParams()` performs string-level `$N` replacement BEFORE parsing. It escapes `'` to `\'` and wraps non-numeric values in single quotes. The VaultDB lexer treats `\'` as an escaped quote (lexer.go:469), so the escaping is technically correct for this parser. The parser also rejects extra tokens after `;` (parser.go:265-266), preventing multi-statement injection.
- **Mitigation status**: **FIXED (2026-07-05)** — Migrated to AST-level binding. `applyParams()` replaced with `bindHTTPParams()` which parses first (creating `ParamRef` nodes) then uses exported `BindParams()` to replace them with typed `Value` nodes. Text-substitution eliminated.
- **Recommendation**: ~~Migrate HTTP API to use AST-level parameter binding~~ **DONE**

### 2. PREPARE/EXECUTE — Proper AST Binding

- **File**: `server/internal/executor/commands_tx.go:428,456-542`
- **Risk**: **Low / Already mitigated**
- **Description**: `bindParams()` replaces `ParamRef` nodes with `Value` nodes directly in the AST tree. Parameters are never serialized back to text or re-parsed. This is the correct approach.
- **Coverage**: Limited to SELECT, UPDATE, INSERT, DELETE — DDL and other statements are rejected at line 502.
- **Mitigation status**: Fully mitigated for supported statement types.

### 3. Migrations Re-parse Stored SQL

- **File**: `server/internal/executor/commands_ddl_misc.go:128-133`
- **Risk**: **High**
- **Description**: `MigrationCommand.Execute()` stores raw SQL strings in `_migrations.sql`. On APPLY, the string is read back and `parser.Parse(sqlToApply)` is called followed by `ctx.Session.Execute(innerStmt)`. Any user with CREATE MIGRATION privileges can write arbitrary SQL that gets re-parsed and executed later.
- **Mitigation status**: **FIXED (2026-07-05)** — Body validated at CREATE time via `isMigrationSafe()`. Rejects DDL on system tables (`_`-prefixed, `vaultdb_audit_log`). Only allows DML + safe DDL (CREATE TABLE/INDEX/VIEW, ALTER TABLE, DROP TABLE/INDEX).
- **Recommendation**: ~~Validate migration body at CREATE time~~ **DONE**

### 4. Triggers: Body Stored and Re-parsed

- **File**: `server/internal/executor/commands_ddl_misc.go:311-341, 451-462`
- **Risk**: **High**
- **Description**: `CreateTriggerStatement.Body` is a raw string (ast.go:313). It's stored as JSON in `_objects`. When a matching DML event fires, `fireTriggers()` loads the body and `executeTriggerBody()` calls `parser.Parse(body)` then executes it. The trigger body runs in the same execution context with full privileges. No sandboxing, no re-entrant guard.
- **Mitigation status**: **FIXED (2026-07-05)** — Added `triggerDepth` counter to `ExecutionContext`. Max depth 3 prevents infinite recursion. Warning logged when limit reached.
- **Recommendation**: ~~Validate trigger body at CREATE time. Add re-entrant guard~~ **DONE** (guard added; body validation deferred — triggers commonly need DML including UPDATE on same table)

### 5. Functions: Body Stored and Re-parsed

- **File**: `server/internal/executor/commands_ddl_misc.go:367-397, eval_functions.go:260-316`
- **Risk**: **Medium**
- **Description**: `CreateFunctionStatement.Body` (ast.go:325) is a string stored in `_objects`. At call time, `executeUserDefinedFunction()` loads it and calls `parser.Parse(body)`. Parameter substitution uses AST-level `substituteParam()` (line 318-339), which replaces `ColumnRef` nodes with `Value` nodes — safe.
- **Mitigation status**: **FIXED (2026-07-05)** — Body validated at CREATE time. SQL-language functions must contain SELECT. WASM-language functions skip validation (bodies are file paths).
- **Recommendation**: ~~Validate function body at CREATE time~~ **DONE**

### 6. Procedures: Body Stored and Re-parsed

- **File**: `server/internal/executor/commands_ddl_misc.go:464-578`
- **Risk**: **Medium**
- **Description**: Same pattern as functions — body stored as string, re-parsed at CALL time. Parameters are bound via AST substitution (safe). But procedures can execute arbitrary statements including DDL.
- **Mitigation status**: **FIXED (2026-07-05)** — Body validated at CREATE time via `isProcedureBodySafe()`. SQL-language procedures can only contain SELECT/INSERT/UPDATE/DELETE/BEGIN. DDL and DCL rejected. WASM-language procedures skip validation.
- **Recommendation**: ~~Same as functions — validate body at CREATE time~~ **DONE**

### 7. Views: Body Stored as SQL Text

- **File**: `server/internal/executor/commands_ddl_misc.go` (view creation)
- **Risk**: **Medium**
- **Description**: View definitions store the SELECT query as a string. When the view is queried, the stored SQL is re-parsed. This is standard database behavior, but worth noting that the stored SQL executes with the querying user's privileges, not the view creator's.
- **Mitigation status**: Standard behavior, no specific mitigation needed.
- **Recommendation**: Ensure RLS policies are applied when querying views (check if they are).

### 8. RLS Policies and CHECK Constraints

- **File**: `server/internal/executor/` (RLS evaluation)
- **Risk**: **Low**
- **Description**: RLS policy expressions and CHECK constraints are stored as strings and evaluated at runtime. These are typically set by admins, not regular users.
- **Mitigation status**: Acceptable risk — admin-only operation.
- **Recommendation**: Ensure only users with appropriate privileges can create/modify RLS policies and CHECK constraints.

---

## Recommendations Summary

| Priority | Action | Finding | Status |
|---|---|---|---|
| **P0** | Migrate HTTP API to AST-level parameter binding | #1 | **DONE** |
| **P0** | Add re-entrant guard for trigger execution | #4 | **DONE** |
| **P1** | Validate migration body at CREATE time | #3 | **DONE** |
| **P1** | Validate function/procedure body at CREATE time | #5, #6 | **DONE** |
| **P2** | Verify RLS policies apply to view queries | #7 | Pending |
| **P2** | Audit CHECK constraint expression evaluation | #8 | Pending |

---

## Next Audit

This audit should be repeated:
- Before any major release
- When new features involve storing and re-parsing SQL (e.g., WASM UDF in Phase 6)
- When the parser is modified to accept new syntax
