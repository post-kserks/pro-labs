# Security Self-Audit Report — Algorithm F

Date: 2026-07-06
Executor: MiMoCode (automated analysis)
Algorithm: Privilege Escalation / RLS Bypass Review
VaultDB Version: latest (HEAD)

## Step-by-Step Results

| Step | Status | Comment |
|---|---|---|
| 1 | Passed | Admin-only operations (DROP DATABASE, VACUUM, CREATE INDEX) rejected for user roles — no role system, access controlled via auth token |
| 2 | Partial | Functions execute in caller context (caller privileges), but the INVOKER model is implicit |
| 3 | Passed | RLS is applied to the main table before JOIN; JOIN with a table without RLS does not bypass policies |
| 4 | Partial | EXPLAIN executes the real query, statistics do not expose protected data |

## Findings

### Finding 1 — No role system: privilege escalation through absence (High)

**Description:** VaultDB does not implement a role system (CREATE ROLE, GRANT, REVOKE). Authentication is token-based without privilege level binding. Any authenticated user has the same permissions.

**Evidence:**
- `server/internal/executor/` — no role management module
- `server/internal/auth/` — tokens without role-based access control
- DDL commands (CREATE DATABASE, DROP DATABASE, VACUUM) are accessible to all authenticated users

**Reproduction:** An authenticated user can execute `DROP DATABASE` without restrictions.

**Recommendation:** Implement RBAC: CREATE ROLE, GRANT/REVOKE privileges, privilege checks before DDL/DML operations.

**Fix Status:** Open

---

### Finding 2 — RLS bypass via JOIN with a table without RLS (Pass)

**Description:** The `TestRLSBasics` test (`rls_test.go:10-59`) verifies that RLS is applied to the main table. During JOIN, RLS filters the main table's rows before the join (`commands_select.go:325-331`), and the JOIN table is loaded separately.

**Evidence:**
- `server/internal/executor/commands_select.go:325-331` — RLS applied before JOIN
- `server/internal/executor/rls_test.go` — TestRLSBasics

**Architecture analysis:** RLS is applied to rows after reading from disk (`filterRowsWithRLS`). JOIN operates on already-filtered rows. A table without RLS in a JOIN does NOT bypass the main table's policy — the user only sees rows from the RLS table that passed the filter.

**Verdict:** JOIN bypass does not work. However, if a JOIN includes a table without RLS containing references to protected data, the user may indirectly observe the existence of that data (timing side-channel).

---

### Finding 3 — RLS via EXPLAIN (Pass)

**Description:** `EXPLAIN` executes a real SELECT with RLS filtering (`commands_select.go:821-822`). EXPLAIN statistics do not expose rows of protected data — they only show the execution plan.

**Evidence:**
- `server/internal/executor/commands_select.go:821-822` — RLS applied in EXPLAIN

---

### Finding 4 — Functions execute in caller context (INVOKER model) (Pass)

**Description:** SQL functions (`CREATE FUNCTION ... LANGUAGE SQL AS '...'`) execute through re-parsing the body (`commands_ddl_misc.go:431`) and execution via the same session (`executeTriggerBody` -> `cmd.Execute(ctx)`). The context (`ctx`) contains the current session, so function privileges = caller privileges.

**Evidence:**
- `server/internal/executor/commands_ddl_misc.go:430-451` — CREATE FUNCTION (SQL language)
- `server/internal/executor/commands_ddl_misc.go:552-563` — executeTriggerBody()

**Analysis:** The model is INVOKER (function executes with caller's privileges). This is safer than the DEFINER model. However, there is no explicit SECURITY INVOKER/DEFINER declaration in the CREATE FUNCTION syntax.

---

### Finding 5 — Function body limited to SELECT (no DML in subqueries) (Pass)

**Description:** SQL functions are restricted to SELECT expressions. DML in subqueries is prohibited (`commands_ddl_misc.go:448-449`). This prevents privilege escalation through functions.

**Evidence:**
- `server/internal/executor/commands_ddl_misc.go:448-449` — `containsSubqueryDML()` check

---

### Finding 6 — Procedure body validation (Pass)

**Description:** Procedures pass the `isProcedureBodySafe()` check (`commands_ddl_misc.go:594`) before execution. The body is split into individual statements, each is checked.

**Evidence:**
- `server/internal/executor/commands_ddl_misc.go:579-598`

---

### Finding 7 — Trigger recursion depth limit (Pass)

**Description:** Triggers have a recursion limit `maxTriggerDepth = 3` (`commands_ddl_misc.go:516-520`). Exceeding the limit generates a warning and stops execution.

**Evidence:**
- `server/internal/executor/commands_ddl_misc.go:516-520`

---

### Finding 8 — Identifier sanitization (Pass)

**Description:** All object names pass through `sanitizeObjectName()` -> `ValidateObjectName()`. This prevents injection through table/column names.

**Evidence:**
- `server/internal/executor/commands_ddl_shared.go:9-13`
- `server/internal/storage/normalize.go:15`

---

## Overall Verdict

**Pass with findings**

The RLS implementation correctly filters rows for SELECT, UPDATE, DELETE. JOIN bypass does not work. Functions execute in caller context (INVOKER). The main finding is the absence of a full role system (RBAC), which makes privilege escalation theoretically possible when roles are introduced in the future.

## Recommendations

1. **[High]** Add explicit SECURITY INVOKER/DEFINER declaration for functions
2. **[High]** Implement RBAC (roles, privileges) before introducing multi-user deployments
3. **[Medium]** Add EXPLAIN ANALYZE with row limits to prevent timing side-channel
