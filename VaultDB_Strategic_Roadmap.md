# VaultDB — Strategic Decisions and Roadmap
## Decision Protocol + Development Plan toward Enterprise-grade DBMS

---

| Attribute | Value |
|---|---|
| Document | VaultDB Strategic Roadmap v1.0.0 |
| Status | Approved — replaces `strategic-decisions.md` |
| Product positioning | Enterprise-first, applicable everywhere |
| Timeline constraint | None. Priority is correct sequencing and quality |
| Protocol | Custom, no PostgreSQL wire protocol compatibility |

---

## Preamble: What "Enterprise-first" Means

This decision defines everything else in the document, so it is stated explicitly
at the very beginning.

VaultDB is not targeting the niche of a "lightweight embeddable SQLite alternative" but rather
the niche of a "DBMS that a large organization installs instead of PostgreSQL/Oracle,
because VaultDB provides what they lack": Time Travel out of the box, TDE
with envelope encryption and KMS, WAL with guaranteed crash recovery, an honest
custom protocol without borrowed compromises.

This implies:

- We do not optimize for minimal binary size (SQLite-class).
- We do not sacrifice correctness for faster feature delivery.
- We design every subsystem as if it will be audited
  by external security engineers and DBAs with years of experience on Oracle/PostgreSQL.
- Compatibility with the existing ecosystem (psql, ORM) is not the goal.
  The goal is that clients who install VaultDB do so deliberately,
  for specific capabilities, not by inertia.

---

## Table of Contents

1. [Decision Protocol (all 10 items)](#1-decision-protocol)
2. [Phase Architecture](#2-phase-architecture)
3. [Phase 0 — Correctness Foundation](#3-phase-0)
4. [Phase 1 — Custom Protocol v2](#4-phase-1)
5. [Phase 2 — Catalog and Next-Generation WAL](#5-phase-2)
6. [Phase 3 — Security and Audit](#6-phase-3)
7. [Phase 4 — Runtime and Performance](#7-phase-4)
8. [Phase 5 — SQL Compatibility](#8-phase-5)
9. [Phase 6 — Extensibility (WASM UDF)](#9-phase-6)
10. [Phase 7 — Hardening and Stabilization](#10-phase-7)
11. [Enterprise-ready Definition](#11-enterprise-ready-definition)
12. [Document Management](#12-document-management)

---

## 1. Decision Protocol

Each decision is recorded in the format: Decision → Rationale → What it changes.
A decision can only be revisited through an explicit process (section 11), not through
silent workarounds in code.

---

### Decision 1 — WAL: Segmentation

**Status: DEFERRED. Synchronous commit — DOING.**

Segmented WAL (16 MB files, rotation after checkpoint) is not being implemented
until recovery time metrics on real data volumes show a problem. Premature optimization
of a non-existent bottleneck.

Instead, `synchronous_commit` with three levels (`on`,
`off`, `remote_write`) is implemented — this gives the client enterprise-grade control over the
durability/throughput trade-off right now, at an order of magnitude lower cost.

**What it changes:** WAL remains a single linearly growing file with checkpoint
and truncate matching the current implementation. The segmentation task is moved to Phase 2
as part of next-generation WAL work, with a clear activation trigger:
recovery time > 30 seconds on real client data, or WAL archiving
for PITR becomes a concrete customer request.

---

### Decision 2 — Catalog: Consistency

**Status: TWO-STEP. Rename — now. Binary catalog — part of Phase 2.**

Immediately: JSON catalog is migrated to write-to-temp + `os.Rename()`.
This eliminates half the desynchronization risk almost for free.

The full solution (binary catalog on dedicated pages inside the page engine,
atomicity via WAL) is not spun off as a separate project — it merges
with the ongoing `storage-engine-rewrite-plan.md`. Two parallel refactorings
of the same storage subsystem is a waste of resources and a conflict risk.

**What it changes:** there is no separate "rewrite the catalog" track.
The catalog migrates to page-based storage synchronously with the migration
of user tables, following a single storage engine rewrite plan.

---

### Decision 3 — Testing: Close the Gaps Immediately

**Status: FULLY APPROVED. Higher priority than any feature in this document.**

This is the only section of the original document that requires no
trade-off evaluation — all four items are cheap and find real bugs.

- Fuzz testing the parser (`go test -fuzz`) — integrated into CI as a
  continuously running job, not a one-off check.
- `go test -race -count=1000` on existing stress tests — becomes
  a mandatory gate before merging into `main` for any PR touching
  concurrency code (executor, txmanager, bufpool, wal).
- Random valid SQL query generator (SQLsmith equivalent) — implemented
  as a separate test utility, run in nightly CI.
- Regression benchmark suite — every PR that could affect
  performance must run existing benchmarks and include
  a comparison with `main`. Automated via `benchstat`.

**What it changes:** Enterprise positioning requires that the client
trusts kernel stability more than the speed of new features.
Testing is not a separate phase but a continuous process from day one.

---

### Decision 4 — Protocol: Custom, No PostgreSQL Wire Compatibility

**Status: APPROVED. Explicitly confirmed by user.**

VaultDB will not implement PostgreSQL wire protocol v3. The reasons are final:

1. Time Travel, TDE metadata, AI features, and future VaultDB features do not
   fit into a protocol designed for a different DBMS — attempting to
   shoehorn them in creates a constantly growing list of "almost compatible but
   not quite" corners, which is worse than an honest separate protocol.
2. The PostgreSQL ecosystem (drivers, ORM, pgBouncer) is optimized for
   PostgreSQL behavior, not VaultDB — protocol-level compatibility
   does not yield behavior-level compatibility, only an illusion.
3. Team resources are directed toward what uniquely differentiates VaultDB,
   not toward copying another protocol.

In its place — a custom protocol v2, designed from scratch for VaultDB's needs,
fully described in Phase 1 of this document.

**What it changes:** the entire "PostgreSQL compatibility" track is closed. Resources
that could have gone there are redirected to Phase 1 (protocol v2) and
Phase 5 (substantive SQL compatibility, not wire format).

---

### Decision 5 — Extension Model: WASM UDF

**Status: APPROVED FOR DEVELOPMENT, but not the top priority.**

WASM extensions (via `wazero`, Go-native runtime without CGo) are the
right enterprise solution: sandbox, any compilation language, security
by design. This is being built, but after the core (protocol, catalog,
security) is stabilized.

The `CREATE FUNCTION` syntax is already designed with future WASM
body support in mind, so no breaking change is needed at implementation time:

```sql
-- Today — SQL body:
CREATE FUNCTION calc_discount(price FLOAT) RETURNS FLOAT
LANGUAGE SQL
AS 'SELECT price * 0.9';

-- Syntax pre-extended for future WASM (Phase 6):
CREATE FUNCTION hash_pii(value TEXT) RETURNS TEXT
LANGUAGE WASM
AS 'file:///plugins/hash_pii.wasm'
WITH (memory_limit = '16MB', timeout = '100ms');
```

**What it changes:** the `CREATE FUNCTION` grammar is extended to support
`LANGUAGE WASM` ahead of time (Phase 5), while the runtime implementation is Phase 6.

---

### Decision 6 — Runtime and Memory

**Status: APPROVED. sync.Pool — now. Arena — monitoring.**

`sync.Pool` for hot structures (`storage.Row`, serialization buffers,
`[]Expression`) is implemented within Phase 4. Cheap, reversible, with a measurable
effect on GC pressure.

Arena allocator (`arena.Arena`) — not implemented while the API is under an
experimental Go flag. An enterprise DBMS cannot depend on an
unstable language API. The decision is revisited when the API stabilizes
in a Go release (tracked as an external dependency, not an internal task).

Additionally to the original document: documented `GOGC` policy and
`GOMEMLIMIT` recommendations for 10GB+ working sets are introduced —
this is part of the Enterprise Deployment Guide (Phase 4).

---

### Decision 7 — Security Beyond Encryption

**Status: SQL injection audit — immediately. Audit log — Phase 3, with tamper-evidence.**

The SQL injection audit is performed out of queue, before any other work
from this document begins. Rationale: the gap between the data protection level
(AES-256-GCM, envelope encryption, KMS) and a potential hole in
`PREPARE`/`CREATE FUNCTION`/`CALL` is not just a bug — it's a reputational
catastrophe when discovered by an external auditor. All locations where
`parser.Value.StrVal` participates in re-parsed
constructs (functions, triggers, migrations, EXECUTE with parameters) are checked.

The audit log is implemented in Phase 3 as a built-in system table
(`vaultdb_audit_log`), not an external log file — because enterprise customers
must be able to query the audit log via SQL, integrate it into
their SIEM systems via the same protocol, and apply RLS policies to the
audit log itself.

An addition missing from the original document: the audit log is designed
as append-only with hash-chain — each entry contains the hash of the previous
entry. Without this property, the audit log is just a regular journal that can be
silently edited and cannot withstand any formal
compliance check (SOC 2, ISO 27001).

Embedded mode without auth (item B of the original document) is documented
as-is, requiring no separate work, since embedded is not the primary
product positioning (see Decision 8).

---

### Decision 8 — Packaging and Distribution

**Status: Shared library — not being built. Docker remains the primary channel.**

A direct consequence of the Enterprise-first positioning. A C-compatible shared
library (`.so`/`.dylib`/`.dll`) only makes sense for an embedded strategy —
and VaultDB is not positioned as a SQLite replacement within a process. Companies
that need TDE, KMS, Time Travel, and WAL crash recovery deploy
VaultDB as a service, not link a library into their binary.

Binary size (~15MB) is not aggressively optimized (UPX and similar) —
this is not a criterion for enterprise deployments, where the score is not in kilobytes
but in reliability guarantees.

Docker remains the primary distribution channel, with strengthening toward
enterprise deployment patterns: Kubernetes Helm chart, readiness/
liveness probe support via the existing `/health`, documented resource
profiles (small/medium/large deployment sizing).

**What it changes:** the "shared library for embedded" task is removed from
the backlog entirely, not deferred — this is a deliberate "no", not "later".

---

### Decision 9 — SQL Compatibility Roadmap

**Status: APPROVED with priority refinement.**

The proposed core set for SQL engine completeness is agreed upon:

| Feature | Decision Status |
|---|---|
| EXPLAIN ANALYZE | Phase 5, high priority (developer UX already required by existing clients) |
| COPY FROM/TO | Phase 5, high priority (ETL — blocker for enterprise migrations from other DBMSs) |
| DEFAULT expressions (CURRENT_TIMESTAMP, gen_random_uuid) | Phase 5 |
| GENERATED ALWAYS AS ... STORED | Phase 5 |
| DISTINCT ON | Phase 5, low cost |
| JSONB operators (->, ->>, @>, ?) | Phase 5, after core set |
| Array types (INT[], TEXT[]) | Phase 5, after core set |
| PARTITION BY RANGE/LIST/HASH | Phase 6+, requires mature storage engine |
| Enterprise-level full-text search | Phase 6+ |
| INSERT ... ON DUPLICATE KEY (MySQL compatibility) | Not a priority — conflicts with the decision not to chase other systems' compatibility. UPSERT/MERGE already covers this need |

An addition to the original document: it is explicitly stated that test
coverage for the RETURNING + UPSERT/MERGE combination is added as part of Phase 5,
since this is a typical boundary where bugs hide in already implemented
features.

---

## 2. Phase Architecture

Phases are ordered by dependencies, not calendar time — timelines are
fundamentally not fixed per the requirement. Each phase may take
an arbitrary amount of time; the transition to the next happens upon readiness,
confirmed by that phase's acceptance checklist.

```
✅ Phase 0 — Correctness Foundation — COMPLETED
✅ Phase 1 — Custom Protocol v2 — COMPLETED
✅ Phase 2 — Catalog and Next-Generation WAL — COMPLETED
✅ Phase 3 — Security and Audit — COMPLETED
✅ Phase 4 — Runtime and Performance — COMPLETED
✅ Phase 5 — SQL Compatibility — COMPLETED
✅ Phase 6 — WASM UDF + Partitioning + FTS — COMPLETED
         |
         v
🔄 Phase 7 — Hardening and Stabilization — IN PROGRESS
   (crash tests: 60%, benchmark: 75%, edge cases: 25%, security: 60%, docs: 30%)
```

Phases 0 and 3 (SQL injection, testing) do not block each other and
are executed in parallel from the very start — this is not a sequential stage
but a continuously active process.

---

## 3. Phase 0 — Correctness Foundation

Goal: eliminate structural risks before building functionality.
This is not a "feature" — it is hygiene, without which the Enterprise claim is hollow.

### 3.1 SQL Injection Audit

Scope of the audit:
- `parser.Value.StrVal` in all locations where the string is re-parsed:
  PREPARE / EXECUTE, CREATE FUNCTION body, CREATE PROCEDURE body,
  CALL with arguments, migrations, triggers (if any)
- Any concatenation of user input into SQL text before re-Parse()
- Verification that parameterization ($1, $2...) in PREPARE is not vulnerable
  to escape via type control at bind time, not text substitution

The audit result is documented as a separate security report listing
found locations and the status of each fix. The report is stored in the repository
(`docs/security/sql-injection-audit-YYYY-MM-DD.md`) as a permanent
compliance artifact.

### 3.2 Parser Fuzz Testing — Continuous CI Process

```go
// internal/parser/fuzz_test.go

func FuzzParse(f *testing.F) {
    // Seed corpus — known valid and edge cases
    f.Add("SELECT * FROM users;")
    f.Add("")
    f.Add("\x00\x00\x00")
    f.Add(strings.Repeat("(", 10000))
    f.Add("SELECT " + strings.Repeat("a,", 50000) + "b FROM t;")

    f.Fuzz(func(t *testing.T, input string) {
        defer func() {
            if r := recover(); r != nil {
                t.Fatalf("parser panicked on input %q: %v", input, r)
            }
        }()
        _, _ = Parse(input) // does not panic — the only requirement
    })
}
```

```yaml
# CI: nightly fuzz job, does not block PR but alerts the team
fuzz-nightly:
  schedule: "0 2 * * *"
  run: go test -fuzz=FuzzParse -fuzztime=2h ./internal/parser/
```

### 3.3 Race Tests as Mandatory Gate

```yaml
# .github/workflows/ci.yml — add mandatory check
race-detector:
  run: go test -race -count=1000 ./internal/executor/... ./internal/txmanager/... ./internal/storage/...
  # Mandatory for PRs touching: executor, txmanager, bufpool, wal, broadcaster
```

### 3.4 SQL Random Query Generator (SQLsmith equivalent)

```go
// tools/sqlfuzz/generator.go

// RandomQueryGenerator generates random valid SQL queries
// based on existing DB schema — to verify the server doesn't crash
// on unexpected but syntactically correct combinations.
type RandomQueryGenerator struct {
    schema *catalog.Schema
    rng    *rand.Rand
}

func (g *RandomQueryGenerator) GenerateSelect() string {
    // Random combinations: JOIN N tables, random WHERE conditions,
    // random window functions, random subquery nesting
    return ""
}

// nightly job: 10000 random queries, only check — server is alive
```

### 3.5 Regression Benchmark Suite

```bash
# tools/benchstat-gate.sh — compare PR benchmarks vs main
go test -bench=. -benchmem -count=10 ./... > new.txt
git stash && go test -bench=. -benchmem -count=10 ./... > old.txt && git stash pop
benchstat old.txt new.txt
# If regression > 10% on key benchmarks — CI marks PR with a warning
```

### Phase 0 Acceptance Checklist

| # | Criterion |
|---|---|
| 0.1 | SQL injection audit completed, report in docs/security/, all findings closed or explicitly accepted as risk |
| 0.2 | FuzzParse runs 2+ hours nightly without panics, found bugs filed in tracker |
| 0.3 | -race -count=1000 green on executor/txmanager/storage, integrated as mandatory CI gate |
| 0.4 | SQL random query generator runs nightly, finds no panics for 2 consecutive weeks |
| 0.5 | Regression benchmark suite integrated in CI, comparison history for at least 4 PRs |

---

## 4. Phase 1 — Custom Protocol v2

Goal: a protocol designed from scratch for VaultDB, documented
as a formal specification, with official client libraries.

### 4.1 Why the Current Protocol Is Insufficient for Enterprise

The current JSON-over-TCP works but is not formalized: no protocol
versioning, no official specification outside code, no generated clients.
An enterprise customer will not connect to a DBMS whose protocol is "what the
code does" rather than a documented contract.

### 4.2 Protocol v2 — Requirements

1. Protocol versioning — Handshake communicates the version, client and server
   can negotiate a compatible version.

2. Formal specification in a separate directory:
   `docs/protocol/vaultdb-protocol-v2.md` — independent of the implementation,
   so third-party developers can write clients without reading Go code.

3. Extensibility for VaultDB's unique features:
   - Time Travel parameters as a first-class part of the query, not a hack on top of SQL
   - Encryption metadata (which DEK version was used) in the response
   - AI modes (if retained) as a separate message type, not an overlay on SQL

4. Binary mode for the hot path (optional, on top of the current NDJSON):
   Protocol Buffers or a custom compact binary format
   for clients where throughput matters.
   NDJSON remains the default — for debugging, simplicity, HTTP compatibility.

5. Official client libraries:
   - Go (reference implementation, used in vaultdb-cli)
   - Python
   - JavaScript/TypeScript (Node + browser via WASM client)
   Each with full test coverage, published as a separate package.

### 4.3 Handshake v2 Format

```json
{
  "type": "handshake",
  "protocol_version": "2.0",
  "server": "VaultDB",
  "server_version": "2.0.0",
  "supported_features": [
    "time_travel", "transactions", "encryption",
    "prepared_statements", "live_queries", "binary_mode"
  ]
}
```

The client checks `protocol_version` and refuses to connect (with a clear
error) on an incompatible major version — instead of unpredictable behavior.

### 4.4 Extended Request/Response Format

```json
// Request v2 — extensible structure
{
  "id": "req_01J9XKZ...",
  "protocol_version": "2.0",
  "auth": { "token": "vdb_sk_..." },
  "query": {
    "sql": "SELECT * FROM users WHERE id = $1;",
    "params": [42],
    "as_of": null,
    "isolation": "read_committed"
  }
}
```

```json
// Response v2
{
  "id": "req_01J9XKZ...",
  "status": "ok",
  "result": {
    "type": "rows",
    "columns": ["id", "name"],
    "rows": [[1, "alice"]],
    "encryption_meta": { "key_version": 2 },
    "duration_ms": 3
  }
}
```

### 4.5 Formal Specification — Document Structure

```
docs/protocol/vaultdb-protocol-v2.md
  1. Transport (TCP framing, versioning)
  2. Handshake
  3. Authentication
  4. Message types (query, subscribe, admin)
  5. Error handling (complete code table)
  6. Time Travel in the protocol
  7. Transactions in the protocol (BEGIN/COMMIT/ROLLBACK semantics)
  8. Live Queries / subscriptions
  9. Binary mode (optional)
  10. Protocol version changelog
```

### Phase 1 Acceptance Checklist

| # | Criterion |
|---|---|
| 1.1 | Formal protocol v2 specification published, independent of implementation code |
| 1.2 | Handshake with versioning implemented, incompatible version produces a clear error |
| 1.3 | Time Travel and transactional parameters are first-class protocol parts, not a text hack |
| 1.4 | Official Go client published with full test coverage |
| 1.5 | Official Python client published |
| 1.6 | Official JS/TS client published |
| 1.7 | Existing vaultdb-cli migrated to protocol v2 without functionality loss |

---

## 5. Phase 2 — Catalog and Next-Generation WAL

Merges with the existing `storage-engine-rewrite-plan.md` — not duplicated
here in detail, only criteria specific to this document are added.

### 5.1 Immediate Step (Does Not Wait for Phase 2, Done Now)

```go
// internal/catalog/catalog.go — atomic write via rename

func (c *Catalog) Save() error {
    data, err := json.Marshal(c)
    if err != nil { return err }

    tmpPath := c.path + ".tmp"
    if err := os.WriteFile(tmpPath, data, 0644); err != nil {
        return fmt.Errorf("write temp catalog: %w", err)
    }
    return os.Rename(tmpPath, c.path) // atomic on the same filesystem
}
```

### 5.2 Long-term Goal of Phase 2

Binary catalog as regular tables in the page engine (modeled after pg_catalog
in PostgreSQL), atomicity via the same WAL mechanism as user
data. WAL segmentation (Decision 1) is activated here if metrics
at the start of Phase 2 show a real need.

### Phase 2 Acceptance Checklist

| # | Criterion |
|---|---|
| 2.1 | JSON catalog saved via write-temp+rename (immediate step completed) |
| 2.2 | Catalog migrated to page-based storage, atomic via WAL |
| 2.3 | Catalog recovery verified by crash test (kill -9 during DDL) |
| 2.4 | WAL segmentation decision made based on real recovery time metrics, documented |

---

## 6. Phase 3 — Security and Audit

### 6.1 Audit Log — Specification

```sql
-- System table, accessible via regular SQL (with access restrictions)
SELECT * FROM vaultdb_audit_log
WHERE actor = 'admin@company.com'
  AND action = 'ALTER TABLE'
ORDER BY occurred_at DESC
LIMIT 50;
```

```go
// internal/audit/log.go

// AuditEntry — a single audit log entry.
// Append-only, hash-chained — each entry contains the hash of the previous one.
type AuditEntry struct {
    ID          uint64
    OccurredAt  time.Time
    Actor       string
    Action      string
    Target      string
    Details     string
    PrevHash    string
    EntryHash   string
}

// Append adds an entry and maintains the hash-chain.
func (a *AuditLog) Append(entry AuditEntry) error {
    prevHash, _ := a.lastHash()
    entry.PrevHash = prevHash

    payload := fmt.Sprintf("%d|%s|%s|%s|%s|%s",
        entry.ID, entry.OccurredAt.Format(time.RFC3339),
        entry.Actor, entry.Action, entry.Target, entry.PrevHash)
    hash := sha256.Sum256([]byte(payload))
    entry.EntryHash = hex.EncodeToString(hash[:])

    return a.storage.InsertRows(ctx, "system", "vaultdb_audit_log", []Row{entry.ToRow()})
}

// VerifyChain checks the integrity of the entire chain — detects entry tampering.
func (a *AuditLog) VerifyChain() (bool, error) {
    entries, _ := a.storage.ReadCurrentRows(ctx, "system", "vaultdb_audit_log")
    prevHash := ""
    for _, e := range entries {
        expected := computeHash(e, prevHash)
        if expected != e.EntryHash {
            return false, fmt.Errorf("audit chain broken at entry %d", e.ID)
        }
        prevHash = e.EntryHash
    }
    return true, nil
}
```

```sql
-- Service command for audit integrity verification (for compliance reports)
VERIFY AUDIT LOG;
-- Output: "Audit chain intact: 48392 entries verified, no tampering detected."
```

### 6.2 What Is Logged

| Event | Must Log |
|---|---|
| CREATE/DROP DATABASE | Yes |
| CREATE/ALTER/DROP TABLE | Yes |
| CREATE/DROP INDEX | Yes |
| Access control changes (RLS policies) | Yes |
| Encryption key rotation | Yes |
| Authentication login/logout (success and failure) | Yes |
| SELECT/INSERT/UPDATE/DELETE on regular data | Optional, configurable per table |

### Phase 3 Acceptance Checklist

| # | Criterion |
|---|---|
| 3.1 | vaultdb_audit_log — system table, accessible via SELECT with access restrictions |
| 3.2 | Hash-chain implemented, VERIFY AUDIT LOG detects entry tampering in test |
| 3.3 | All DDL operations and encryption key changes logged automatically |
| 3.4 | RLS can be applied to the audit log itself |

---

## 7. Phase 4 — Runtime and Performance

### 7.1 sync.Pool for Hot Structures

```go
// internal/storage/pool.go

var rowPool = sync.Pool{
    New: func() interface{} {
        return make(storage.Row, 0, 16)
    },
}

func GetRow() storage.Row {
    return rowPool.Get().(storage.Row)[:0]
}

func PutRow(r storage.Row) {
    if cap(r) > 256 { return }
    rowPool.Put(r)
}
```

Applied in hot paths: ReadCurrentRows, tuple serialization,
WAL write buffers.

### 7.2 Enterprise Deployment Guide — GC Policies

| Data | GOGC | GOMEMLIMIT | Comment |
|---|---|---|---|
| < 1 GB | default (100) | do not set | Standard mode |
| 1-10 GB | 75 | 80% of container RAM | Reduces GC pauses at the cost of CPU |
| 10 GB+ | 50 | 80% of container RAM | Recommended together with sync.Pool optimizations |

### Phase 4 Acceptance Checklist

| # | Criterion |
|---|---|
| 4.1 | sync.Pool integrated for Row/serialization buffers, allocs/op reduced by at least 2x on benchmark |
| 4.2 | Enterprise Deployment Guide published with specific GOGC/GOMEMLIMIT recommendations |
| 4.3 | Arena allocator — tracking status recorded, decision not made prematurely |

---

## 8. Phase 5 — SQL Compatibility

The core set agreed upon in Decision 9 is implemented. Order within the phase:

1. EXPLAIN ANALYZE (developer UX, frequently requested)
2. COPY FROM/TO (blocker for enterprise migrations)
3. DEFAULT expressions + GENERATED ALWAYS AS STORED
4. DISTINCT ON
5. JSONB operators
6. Array types
7. CREATE FUNCTION grammar extension for LANGUAGE WASM (without runtime implementation)
8. Regression test coverage for RETURNING + UPSERT/MERGE

### Phase 5 Acceptance Checklist

| # | Criterion |
|---|---|
| 5.1 | EXPLAIN ANALYZE works for all query types (SELECT/JOIN/aggregates) |
| 5.2 | COPY FROM/TO supports CSV and JSON Lines |
| 5.3 | DEFAULT expressions including gen_random_uuid(), CURRENT_TIMESTAMP |
| 5.4 | JSONB operators tested on par with core SQL |
| 5.5 | CREATE FUNCTION ... LANGUAGE WASM parses (implementation — Phase 6) |

---

## 9. Phase 6 — Extensibility and Enterprise Scale

### 9.1 WASM UDF

Implementation via wazero (Go-native WASM runtime, no CGo — important for
maintaining build simplicity, meets the Enterprise requirement
of predictable delivery).

```go
// internal/wasmudf/runtime.go

type WASMFunction struct {
    Name         string
    ModulePath   string
    MemoryLimit  uint32
    Timeout      time.Duration
    runtime      wazero.Runtime
    compiled     wazero.CompiledModule
}

func (f *WASMFunction) Call(ctx context.Context, args []storage.Value) (storage.Value, error) {
    ctx, cancel := context.WithTimeout(ctx, f.Timeout)
    defer cancel()

    mod, err := f.runtime.InstantiateModule(ctx, f.compiled, wazero.NewModuleConfig())
    if err != nil { return nil, fmt.Errorf("wasm instantiate: %w", err) }
    defer mod.Close(ctx)

    result, err := mod.ExportedFunction("execute").Call(ctx, marshalArgs(args)...)
    if err != nil { return nil, fmt.Errorf("wasm execute (timeout=%s): %w", f.Timeout, err) }

    return unmarshalResult(result), nil
}
```

### 9.2 Partitioning (RANGE/LIST/HASH)

Requires a mature page engine (Phase 2 completed) — a partition is physically
a separate set of heap files with a shared catalog description.
A detailed specification is written as a separate document when approaching this
phase, once the storage engine has stabilized.

### 9.3 Enterprise-Level Full-Text Search

Extension of existing FTS to ranking algorithms (BM25),
multi-language stemming support, snippet highlighting —
competitive with Elasticsearch for cases where a separate
search cluster is not needed.

### Phase 6 Acceptance Checklist

| # | Criterion |
|---|---|
| 6.1 | CREATE FUNCTION ... LANGUAGE WASM fully functional, sandbox verified against memory/time escape attempts |
| 6.2 | Partitioning supports RANGE and HASH, covered by crash tests |
| 6.3 | FTS supports BM25 ranking and at least 3 stemming languages |

---

## 10. Phase 7 — Hardening and Stabilization

Goal: stabilize what has already been built. Not new features — but bringing
existing functionality to production-ready level.

### 10.1 Crash Tests

- Increase crash scenario coverage: kill -9 during DDL, DML, checkpoint
- Verify recovery after each operation type
- Add chaos tests with concurrent operations
- Criterion: 100% of crash scenarios recover without data loss

### 10.2 Benchmark Regression Gate

- Run benchmarks on every PR touching executor/storage/wal
- Compare with baseline on main
- Regression > 10% — blocks merge
- Criterion: benchmark history for 20+ PRs, no undetected regressions

### 10.3 Edge Cases

- Testing on large data (1M+ rows)
- Concurrent mixed workloads (OLTP + DDL simultaneously)
- Unicode/emoji in all text operations
- Empty tables, NULL values, boundary values
- Criterion: no panics or data corruption during 24 hours of continuous load

### 10.4 Security Hardening

- Re-run SQL injection audit after all changes
- Parser fuzz tests: 2+ hours nightly without panics
- Race tests: -race -count=1000 on all concurrency packages
- Criterion: 0 known vulnerabilities, 0 race conditions

### 10.5 Documentation Hardening

- Verify all code examples — they must work
- Verify all SQL queries from documentation — they must execute
- Ensure API reference is current
- Criterion: any developer can run the project from documentation in 15 minutes

### Phase 7 Acceptance Checklist

| # | Criterion |
|---|----------|
| 7.1 | Crash tests: 100% recovery without data loss |
| 7.2 | Benchmark regression gate: 0 undetected regressions across 20+ PRs |
| 7.3 | Edge cases: 24 hours of continuous load without panics |
| 7.4 | Security: 0 known vulnerabilities, 0 race conditions |
| 7.5 | Documentation: all examples work |

---

## 11. Enterprise-ready Definition

VaultDB is considered enterprise-ready when all items below are met —
this is the final criterion, not a single-phase checklist.

| # | Criterion | Source Phase |
|---|---|---|
| 1 | SQL injection audit passed, documented, repeated with every major release | Phase 0 |
| 2 | Fuzz/race/regression testing — continuous CI process | Phase 0 |
| 3 | Formal protocol specification published independently of code | Phase 1 |
| 4 | Official clients for at least 3 languages (Go/Python/JS) | Phase 1 |
| 5 | Catalog and data consistent under any crash — confirmed by crash tests | Phase 2 |
| 6 | Audit log with hash-chain, verifiable for integrity | Phase 3 |
| 7 | Enterprise Deployment Guide with specific resource recommendations | Phase 4 |
| 8 | SQL compatibility covers EXPLAIN ANALYZE, COPY, DEFAULT, JSONB, arrays | Phase 5 |
| 9 | Extensibility via WASM UDF in a secure sandbox | Phase 6 |
| 10 | Independent external security audit passed | Separate, outside phases |
| 11 | Kubernetes Helm chart, readiness/liveness probes documented | Phase 4 |
| 12 | TDE remains part of the base set without changes | Previously completed |
| 13 | Crash tests: 100% recovery | Phase 7 |
| 14 | Benchmark regression gate active | Phase 7 |
| 15 | 24 hours of continuous load without panics | Phase 7 |

Item 10 is the only one that fundamentally cannot be performed
by the team internally. When the project reaches the point of real
enterprise sales, an independent security audit must be commissioned from
an external company — this is standard practice, and without it the formal
"enterprise-ready" claim will not withstand scrutiny from a serious customer.

---

## 12. Document Management

Any decision recorded in section 1 may only be revisited through
an explicit process:

1. The initiator formulates: which decision is being revisited and why
   (new data, changed customer requirements, technical discovery)
2. The decision is updated in this document with a note:
   "Decision N revised [date]: was — X, now — Y. Reason: ..."
3. The old rationale is not deleted — it is preserved in history for context

The document is not quietly rewritten. Decision history is part of enterprise-grade
development discipline, which the product itself (Time Travel, Audit Log)
preaches for user data — it would be strange not to follow
the same principle for its own architectural decisions.
