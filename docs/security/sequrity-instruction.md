# VaultDB Security Assurance — Technical Specification
## Security Verification Automation + Self-Audit Algorithms

---

| Attribute | Value |
|---|---|
| Document | VaultDB Security Assurance TZ v1.0.0 |
| Relationship to other documents | Implements Phase 0 and Phase 3 from VaultDB Strategic Roadmap |
| Status | Active |
| Important caveat | This document is internal hygiene, NOT a replacement for independent external security audits |

---

## 1. Scope and Threat Model

### 1.1 What the Document Covers

Two parallel tracks:

**Track A — Automation.** Tools built into CI/CD that
verify code and the running server without human involvement: static
analysis, fuzzing, dependency scanning, dynamic testing
of the running server.

**Track B — Manual Algorithms.** Step-by-step procedures for engineers
that cannot (yet) be fully automated: analysis of architectural
decisions, checking cryptographic primitives for logical
errors, reviewing privilege escalation scenarios.

### 1.2 VaultDB Threat Model (STRIDE adaptation for DBMS)

| Threat Category | Applied to VaultDB | Priority |
|---|---|---|
| Injection | SQL injection via PREPARE/EXECUTE/CREATE FUNCTION, protocol injection via custom protocol | Critical |
| Broken Authentication | Token bypass, HMAC forgery, timing attacks on token comparison | Critical |
| Broken Access Control | RLS policy bypass, privilege escalation between roles | Critical |
| Cryptographic Failures | Weak DEK, key leakage to logs/memory dumps, incorrect nonce reuse in AES-GCM | Critical |
| Data Integrity | Heap page tampering, WAL record tampering, hash-chain audit log bypass | High |
| Denial of Service | Memory exhaustion via large queries, zip-bomb in COPY, connection exhaustion | High |
| Insecure Deserialization | Incorrect parsing of protocol messages leading to RCE/panic | High |
| Supply Chain | Vulnerable dependencies (Go modules, npm for Web UI), compromised Docker base image | Medium |
| Security Misconfiguration | TLS disabled by default, auth disabled in dev config that ends up in production | Medium |
| Information Disclosure | Leakage of details through error messages, timing side-channel in password/token comparison | Medium |

---

## 2. Contents

3. Track A — Automated Security Pipeline
4. SAST — Static Analysis
5. Dependency and Supply Chain Scanning
6. Secret Scanning
7. Fuzzing — Extended Set
8. DAST — Dynamic Testing of the Running Server
9. Docker Image Scanning
10. Track B — Manual Self-Audit Algorithms
11. Severity Classification and SLA
12. Verification Schedule
13. Reporting
14. Task Distribution
15. Acceptance Checklist

---

## 3. Track A — Automated Security Pipeline

### 3.1 Overall Pipeline Diagram

```
pre-commit hook (locally, before commit)
  - gitleaks (secrets)
  - gofmt + go vet
        |
        v
PR gate (required for merge)
  - gosec (SAST)
  - govulncheck (known CVEs in dependencies)
  - go test -race (concurrency)
  - semgrep custom rules (VaultDB-specific patterns)
        |
        v
Nightly (full run, does not block development)
  - FuzzParse, FuzzProtocol, FuzzEncryption (2 hours each)
  - DAST against test instance (SQLi, auth bypass attempts)
  - Trivy Docker image scan
        |
        v
Weekly
  - Manual algorithm from Track B (subsystem rotation)
  - Regression benchmark full run
        |
        v
Before release (major/minor version)
  - Full run of all Track B manual algorithms
  - testssl.sh against TLS configuration
  - Update security report in docs/security/
```

---

## 4. SAST — Static Analysis

### 4.1 gosec — Basic Go Static Analysis

```yaml
# .github/workflows/security.yml

sast-gosec:
  runs-on: ubuntu-latest
  steps:
    - uses: actions/checkout@v4
    - name: Run gosec
      run: |
        go install github.com/securego/gosec/v2/cmd/gosec@latest
        gosec -fmt=sarif -out=gosec-results.sarif -severity=medium ./...
    - name: Upload SARIF
      uses: github/codeql-action/upload-sarif@v3
      with:
        sarif_file: gosec-results.sarif
```

Mandatory gosec rules for VaultDB (do not disable without explicit justification
in a comment like `#nosec G-XXX -- reason`):

| Rule | Checks | Criticality for VaultDB |
|---|---|---|
| G101 | Hardcoded secrets in code | Critical — direct intersection with TDE |
| G201/G202 | SQL queries via string concatenation | Critical — direct intersection with injection |
| G401/G402/G403 | Weak cryptography (MD5, DES, small RSA) | Critical — intersection with encryption TZ |
| G404 | math/rand instead of crypto/rand in security context | Critical — nonce, DEK, tokens must use crypto/rand |
| G304 | Path traversal when working with files | High — relevant for COPY FROM/TO, data_dir |

### 4.2 semgrep — Custom Rules for VaultDB Architecture

Generic rules don't catch VaultDB-specific patterns. We write our own.

```yaml
# .semgrep/vaultdb-sql-injection.yml

rules:
  - id: vaultdb-sql-string-concat-reparse
    languages: [go]
    severity: ERROR
    message: >
      User input concatenation detected into a string that is then
      passed to parser.Parse(). This is a potential SQL injection
      even with an AST parser — if concatenation happens BEFORE
      parsing, not after (bind parameters).
    patterns:
      - pattern: |
          $STR := $A + $USERINPUT + $B
          ...
          parser.Parse($STR)
      - pattern-not: |
          parser.Parse($LITERAL_STRING)

  - id: vaultdb-crypto-rand-required
    languages: [go]
    severity: ERROR
    message: >
      math/rand is used in a cryptographic context (nonce/DEK/token).
      crypto/rand is required.
    patterns:
      - pattern-either:
          - pattern: math/rand.Read(...)
      - pattern-inside: |
          func $FUNC(...) {
            ...
          }
      - metavariable-regex:
          metavariable: $FUNC
          regex: (?i)(nonce|dek|token|key|salt)

  - id: vaultdb-dek-not-zeroized
    languages: [go]
    severity: WARNING
    message: >
      Function works with DEK/passphrase but does not call Zeroize()
      before exiting scope. The key may remain in memory.
    patterns:
      - pattern-inside: |
          func $FUNC(...) {
            ...
          }
      - metavariable-regex:
          metavariable: $FUNC
          regex: (?i)(decrypt|dek|passphrase)
      - pattern-not-regex: Zeroize\(\)
```

```bash
# Run in CI
semgrep --config .semgrep/ --error --json --output semgrep-results.json ./server
```

### 4.3 staticcheck — Additional Level

```bash
staticcheck ./...
# catches ignored errors (_ = err), which is directly related
# to issues found in code review (sendError)
```

---

## 5. Dependency and Supply Chain Scanning

### 5.1 govulncheck — Known CVEs in Go Dependencies

```yaml
supply-chain-go:
  runs-on: ubuntu-latest
  steps:
    - name: Install govulncheck
      run: go install golang.org/x/vuln/cmd/govulncheck@latest
    - name: Scan
      run: govulncheck ./...
      # Required for PR gate — fails on found exploitable vulnerability
      # (govulncheck can distinguish "vulnerability exists in dependency" from
      # "vulnerable code is actually called" — fewer false positives)
```

### 5.2 npm audit for Web UI

```yaml
supply-chain-npm:
  runs-on: ubuntu-latest
  steps:
    - working-directory: server/internal/httpserver/web
      run: |
        npm audit --audit-level=high
        npm audit signatures
```

### 5.3 Dependabot / Automatic Updates

```yaml
# .github/dependabot.yml

version: 2
updates:
  - package-ecosystem: "gomod"
    directory: "/server"
    schedule:
      interval: "weekly"
    open-pull-requests-limit: 5

  - package-ecosystem: "npm"
    directory: "/server/internal/httpserver/web"
    schedule:
      interval: "weekly"

  - package-ecosystem: "docker"
    directory: "/"
    schedule:
      interval: "weekly"
```

### 5.4 SBOM (Software Bill of Materials)

Enterprise customers often require SBOM for compliance (Executive Order
14028 in the US, similar requirements in other jurisdictions).

```bash
# Generate SBOM in CycloneDX format at each release
go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest
cyclonedx-gomod mod -json -output sbom.json ./server

# Attached to each GitHub Release as an artifact
```

---

## 6. Secret Scanning

### 6.1 gitleaks — pre-commit + CI

```yaml
# .pre-commit-config.yaml

repos:
  - repo: https://github.com/gitleaks/gitleaks
    rev: v8.18.0
    hooks:
      - id: gitleaks
```

```yaml
# CI — re-check in case pre-commit was bypassed
secret-scan:
  runs-on: ubuntu-latest
  steps:
    - uses: gitleaks/gitleaks-action@v2
      env:
        GITLEAKS_LICENSE: ${{ secrets.GITLEAKS_LICENSE }}
```

### 6.2 Custom Rules for VaultDB-Specific Secrets

```toml
# .gitleaks.toml — additional rules

[[rules]]
id = "vaultdb-api-token"
description = "VaultDB API token"
regex = '''vdb_sk_[a-f0-9]{32}'''
tags = ["vaultdb", "token"]

[[rules]]
id = "vaultdb-encryption-passphrase-env"
description = "Possible hardcoded encryption passphrase"
regex = '''VAULTDB_ENCRYPTION_PASSPHRASE\s*=\s*["'][^"']{4,}["']'''
tags = ["vaultdb", "encryption"]
```

### 6.3 Verification that Secrets Don't Leak into Logs (Runtime Check)

```go
// tools/security/log_secret_test.go

// TestNoSecretsInLogs starts the server, performs operations with tokens
// and keys, captures all log output and verifies the absence of
// known secret patterns.
func TestNoSecretsInLogs(t *testing.T) {
    var logBuf bytes.Buffer
    srv := startTestServer(t, &logBuf)

    passphrase := "test-super-secret-passphrase-12345"
    token := srv.GenerateToken()

    srv.Execute(fmt.Sprintf("CREATE DATABASE secure_test ENCRYPTED WITH KEY '%s';", passphrase))
    srv.ExecuteWithToken(token, "SELECT 1;")

    logs := logBuf.String()

    assert.NotContains(t, logs, passphrase, "passphrase leaked into logs")
    assert.NotContains(t, logs, token, "auth token leaked into logs")
}
```

---

## 7. Fuzzing — Extended Set

Supplements FuzzParse from Strategic Roadmap Phase 0 with
security-specific fuzz targets.

### 7.1 FuzzProtocol — Protocol Fuzzing

```go
// internal/server/fuzz_protocol_test.go

// FuzzProtocol checks that the server does not crash or hang
// on arbitrary bytes sent as a protocol message.
func FuzzProtocol(f *testing.F) {
    f.Add([]byte(`{"id":"1","token":"x","query":"SELECT 1;"}` + "\n"))
    f.Add([]byte(`{`))
    f.Add([]byte(strings.Repeat("A", 1<<20)))
    f.Add([]byte("\x00\x01\x02\xff\xfe"))

    f.Fuzz(func(t *testing.T, data []byte) {
        conn := connectToTestServer(t)
        defer conn.Close()

        conn.SetDeadline(time.Now().Add(2 * time.Second))
        conn.Write(data)

        assertServerStillAlive(t)
    })
}
```

### 7.2 FuzzEncryption — Decryption Fuzzing

```go
// internal/crypto/fuzz_decrypt_test.go

// FuzzDecryptPage checks that attempting to decrypt arbitrary
// (corrupted/tampered) bytes as an encrypted page
// correctly returns an error instead of panicking.
func FuzzDecryptPage(f *testing.F) {
    em, _ := crypto.NewEncryptionManager(testDEK, "v1")

    validNonce, validCiphertext, _ := em.EncryptPage([]byte("test data"), testPageID)
    f.Add(validNonce, validCiphertext)
    f.Add(make([]byte, 12), make([]byte, 100))
    f.Add([]byte{}, []byte{})

    f.Fuzz(func(t *testing.T, nonce, ciphertext []byte) {
        defer func() {
            if r := recover(); r != nil {
                t.Fatalf("DecryptPage panicked on malformed input: %v", r)
            }
        }()
        _, _ = em.DecryptPage(nonce, ciphertext, testPageID)
    })
}
```

### 7.3 FuzzWALRecovery — Recovery Fuzzing on Corrupted WAL

```go
// internal/wal/fuzz_recovery_test.go

func FuzzWALRecovery(f *testing.F) {
    validWAL := generateValidWALFile()
    f.Add(validWAL)
    f.Add(corruptByteAt(validWAL, 50))
    f.Add(truncateAt(validWAL, len(validWAL)/2))

    f.Fuzz(func(t *testing.T, walBytes []byte) {
        tmpDir := t.TempDir()
        os.WriteFile(filepath.Join(tmpDir, "vaultdb.wal"), walBytes, 0644)

        defer func() {
            if r := recover(); r != nil {
                t.Fatalf("WAL recovery panicked: %v", r)
            }
        }()

        engine, err := storage.NewPageStorageEngine(tmpDir)
        _ = err
        if engine != nil {
            engine.Close()
        }
    })
}
```

---

## 8. DAST — Dynamic Testing of the Running Server

### 8.1 Automated Attacks via Custom Protocol

```go
// tools/security/dast/injection_attempts.go

var injectionPayloads = []string{
    `SELECT * FROM users WHERE id = 1; DROP TABLE users;--`,
    `SELECT * FROM users WHERE name = 'x' OR '1'='1'`,
    `SELECT * FROM users WHERE id = (SELECT 1 UNION SELECT password FROM admin)`,
    `PREPARE p AS SELECT * FROM users WHERE id = $1; EXECUTE p('1 OR 1=1')`,
    `CREATE FUNCTION x() RETURNS INT LANGUAGE SQL AS 'SELECT 1; DROP DATABASE mydb;'`,
}

func TestDASTSQLInjection(t *testing.T) {
    client := connectToTestServer(t)

    for _, payload := range injectionPayloads {
        result := client.Execute(payload)
        assertNoUnauthorizedSideEffects(t, client, payload, result)
    }
}
```

### 8.2 Authentication Bypass Testing

```go
// tools/security/dast/auth_bypass.go

func TestDASTAuthBypass(t *testing.T) {
    client := connectToTestServerNoAuth(t)

    cases := []struct {
        name  string
        token string
    }{
        {"empty token", ""},
        {"malformed token", "not-a-real-token"},
        {"sql-injection-like token", "' OR '1'='1"},
        {"null byte token", "vdb_sk_\x00\x00\x00"},
        {"extremely long token", strings.Repeat("a", 1<<20)},
        {"valid-looking but wrong token", "vdb_sk_" + strings.Repeat("0", 32)},
    }

    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            result := client.ExecuteWithToken(c.token, "SELECT 1;")
            assert.Equal(t, 2001, result.ErrorCode, "expected ERR_UNAUTHORIZED for: %s", c.name)
        })
    }
}
```

### 8.3 Timing Attack on Token Comparison

```go
// tools/security/dast/timing_attack_test.go

func TestTokenComparisonTiming(t *testing.T) {
    validToken := generateValidToken()

    measureTime := func(candidate string) time.Duration {
        start := time.Now()
        for i := 0; i < 1000; i++ {
            _ = authManager.ValidateToken(candidate)
        }
        return time.Since(start)
    }

    farOff := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
    closeMatch := validToken[:30] + "zz"

    tFarOff := measureTime(farOff)
    tCloseMatch := measureTime(closeMatch)

    ratio := float64(tCloseMatch) / float64(tFarOff)
    assert.Less(t, ratio, 1.15,
        "possible timing side-channel in token comparison: ratio=%.3f", ratio)
}
```

Implementation requirement (consequence of this test): token verification
must use `subtle.ConstantTimeCompare` or equivalent,
not direct string/hash comparison via `==`.

### 8.4 TLS Configuration — testssl.sh

```bash
#!/usr/bin/env bash
# tools/security/dast/tls_scan.sh

docker run --rm -it drwetter/testssl.sh \
    --severity HIGH \
    --protocols \
    --vulnerable \
    "${VAULTDB_HOST}:${VAULTDB_TLS_PORT}"

# Mandatory pass criteria:
#   - TLS 1.0/1.1 disabled
#   - Weak cipher suites absent
#   - No vulnerability to Heartbleed/POODLE/BEAST
```

### 8.5 Rate Limiting / DoS Resilience

```go
// tools/security/dast/dos_test.go

func TestConnectionExhaustion(t *testing.T) {
    const attemptConnections = 10000

    var success, refused atomic.Int64
    var wg sync.WaitGroup

    for i := 0; i < attemptConnections; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            conn, err := net.DialTimeout("tcp", testServerAddr, 2*time.Second)
            if err != nil {
                refused.Add(1)
                return
            }
            defer conn.Close()
            success.Add(1)
        }()
    }
    wg.Wait()

    assertServerStillAlive(t)
    t.Logf("accepted: %d, refused: %d", success.Load(), refused.Load())
}

func TestLargePayloadDoS(t *testing.T) {
    client := connectToTestServer(t)

    hugeString := strings.Repeat("A", 2<<30)
    result := client.Execute(fmt.Sprintf(
        `INSERT INTO t VALUES ('%s');`, hugeString))

    assert.NotNil(t, result)
    assertServerStillAlive(t)
}
```

---

## 9. Docker Image Scanning

```yaml
# CI — Trivy scanning of the final image

docker-security-scan:
  runs-on: ubuntu-latest
  steps:
    - name: Build image
      run: docker build -t vaultdb-scan-target .
    - name: Trivy scan
      uses: aquasecurity/trivy-action@master
      with:
        image-ref: vaultdb-scan-target
        format: sarif
        output: trivy-results.sarif
        severity: CRITICAL,HIGH
        exit-code: 1
    - name: Upload results
      uses: github/codeql-action/upload-sarif@v3
      with:
        sarif_file: trivy-results.sarif
```

```bash
# tools/security/verify_minimal_image.sh
docker run --rm vaultdb-scan-target sh -c "echo test" 2>&1 | grep -q "executable file not found" \
    && echo "OK: no shell in image" \
    || (echo "FAIL: shell present in production image" && exit 1)
```

---

## 10. Track B — Manual Self-Audit Algorithms

Each algorithm has a fixed structure: Preconditions -> Steps ->
Expected Result -> Failure Criteria. Executed by an engineer on
a schedule (section 12), results recorded in a report (section 13).

---

### Algorithm A — SQL Injection Manual Review

**Preconditions:** Access to source code of internal/parser, internal/executor.

**Steps:**

1. Build a list of all places where a string passes through parser.Parse()
   more than once during a query lifecycle (migrations, CREATE FUNCTION,
   EXECUTE with parameters, CALL).
   ```bash
   grep -rn "parser.Parse(" server/internal/ | grep -v "_test.go"
   ```
2. For each found location — verify: the string passed to
   Parse() is built from a code constant OR from a value that passed
   through a typed Value (bind parameter), not through direct
   concatenation with a user string.
3. Explicitly test PREPARE/EXECUTE with a payload containing SQL
   meta-characters in the parameter value (not in the query itself):
   ```sql
   PREPARE p AS SELECT * FROM users WHERE name = $1;
   EXECUTE p('''; DROP TABLE users; --');
   ```
   Expected: the string is searched literally as a value, DROP TABLE does
   not execute.
4. Check CREATE FUNCTION ... LANGUAGE SQL AS '...' — the function body
   must not allow execution of additional statements beyond
   those declared at call time.
5. Check identifier handling (table/column names) separately
   from values — identifiers are not parameterized as Value, therefore
   require separate injection checks via information_schema-like
   system queries, if any exist.

**Expected Result:** no test case from step 3 results in
execution of an undeclared operation.

**Failure Criteria:** any unintended execution of DDL/DML beyond
the originally declared statement — Critical severity, blocks release.

---

### Algorithm B — Authentication & Authorization Review

**Preconditions:** Running test instance with authentication configured.

**Steps:**

1. Check token storage format — verify that tokens.json
   (or its equivalent for the page engine) contains no plaintext values:
   ```bash
   grep -E "vdb_sk_[a-f0-9]{32}" data/auth/tokens.json && echo "FAIL: plaintext token found"
   ```
2. Verify that ValidateToken uses constant-time comparison:
   ```bash
   grep -A5 "func.*ValidateToken" server/internal/auth/manager.go | grep -q "subtle.ConstantTimeCompare\|hmac.Equal" \
       || echo "FAIL: possible non-constant-time comparison"
   ```
3. Verify that VAULTDB_AUTH_SECRET (HMAC secret) is mandatory in
   production mode and has no hardcoded default value.
4. If RLS is implemented (CREATE POLICY) — check policy bypass via:
   - direct admin access without a role
   - SQL injection in the USING (...) policy expression
   - bypass via JOIN with a table without RLS, exposing data
     from the protected table indirectly
5. Check token lifetime — is there a revocation mechanism
   for compromised tokens without server restart.

**Expected Result:** all tokens in storage are hashes only; comparison
is constant-time; RLS cannot be bypassed by any of the three vectors in step 4.

**Failure Criteria:** detection of a plaintext token, timing difference
greater than 15% between matching and non-matching candidates, or
any working RLS bypass — Critical severity.

---

### Algorithm C — Encryption at Rest Review

**Preconditions:** Database with TDE enabled (ENCRYPTED), access to raw
heap files on disk.

**Steps:**

1. Create an encrypted database, insert a deliberately recognizable string
   ("UNIQUE_MARKER_STRING_12345"), perform a checkpoint.
2. Run hexdump/strings on the .heap file on disk:
   ```bash
   strings data/databases/secure_test/tables/*/0000.heap | grep "UNIQUE_MARKER"
   ```
   Expected: nothing found — the string is not visible in plaintext.
3. Similarly for the WAL file:
   ```bash
   strings data/wal/vaultdb.wal | grep "UNIQUE_MARKER"
   ```
4. Verify that when a single byte is modified in the middle of an encrypted
   page, the server detects the integrity violation (GCM auth tag
   failure) instead of silently returning corrupted data.
5. Verify that DEK does not remain in the swap file/core dump —
   obtain a core dump and search for DEK bytes:
   ```bash
   gcore <pid>
   strings core.<pid> | grep -f <(echo -n "$KNOWN_DEK_HEX")
   ```
6. Verify KEK rotation — confirm that after rotate-kek the old
   KEK is not physically required to read data.

**Expected Result:** steps 2, 3 — marker not found anywhere on disk.
Step 4 — explicit authentication error. Step 5 — DEK not found in dump
after explicit Zeroize() call.

**Failure Criteria:** detection of readable data on disk in any form
(heap, WAL, core dump) with encryption enabled — Critical severity,
blocks any TDE claims.

---

### Algorithm D — Network / Transport Review

**Preconditions:** Server with TLS enabled and disabled (two runs).

**Steps:**

1. With TLS disabled — capture traffic via tcpdump/Wireshark between
   client and server, verify that data is visible in plaintext.
2. With TLS enabled — repeat the capture, verify that data
   is unreadable in captured traffic.
3. Run testssl.sh (section 8.4), check for absence of TLS 1.0/1.1,
   insecure cipher suites, self-signed certificates in production.
4. Check handling of incorrect/expired client
   certificates with mTLS enabled.

**Expected Result:** TLS 1.3 (or minimum 1.2 with strong cipher
suites), correct mTLS operation when enabled.

**Failure Criteria:** active TLS 1.0/1.1, weak cipher suites in
production profile — High severity.

---

### Algorithm E — WAL / Recovery Tamper Review

**Preconditions:** Server with WAL, ability to stop the process at any point.

**Steps:**

1. Start a transaction with N operations, stop the server (kill -9)
   after operation N/2, before COMMIT.
2. Restart, verify that none of the N/2 operations are applied.
3. Repeat with stopping AFTER the COMMIT write in WAL, but before applying
   to heap — verify that recovery completes the transaction (redo).
4. Manually tamper with a byte in the middle of a WAL record (after
   checksum), restart — recovery should detect corruption via CRC32.
5. Check behavior with corruption in encrypted WAL —
   decryption with incorrect GCM tag should produce a clear error.

**Expected Result:** in all scenarios — either complete rollback
or complete redo, never partial/undefined state.

**Failure Criteria:** any partial transaction application after
a crash — Critical severity, direct violation of ACID guarantees.

---

### Algorithm F — Privilege Escalation / RLS Bypass Review

**Preconditions:** Minimum two roles configured (admin, user) with different RLS policies.

**Steps:**

1. Connect with user role token, attempt to execute operations
   reserved for admin (DROP DATABASE, VACUUM, CREATE INDEX
   on another user's table) — all should be rejected.
2. Check bypass via CREATE FUNCTION ... LANGUAGE SQL — a function
   created by the user role must not execute with creator
   privileges exceeding the caller's own privileges (explicitly decide which
   model is adopted — DEFINER or INVOKER).
3. Check RLS bypass via compound queries (JOIN, subquery,
   aggregate function).
4. Check bypass via EXPLAIN — the execution plan must not
   expose data from protected rows through statistics estimates.

**Expected Result:** no vector provides access beyond
what the policy permits.

**Failure Criteria:** any working bypass — Critical severity.

---

### Algorithm G — Denial of Service / Resource Exhaustion Review

**Preconditions:** Test instance with configured limits.

**Steps:**

1. Run automated tests from section 8.5 (connection exhaustion, large payload).
2. Check EXPLAIN ANALYZE on a deliberately expensive query — the server should
   have the ability to abort long queries by timeout.
3. Check COPY FROM behavior with an incorrect/infinite data stream.
4. Check recursion depth limits in the parser.

**Expected Result:** all scenarios produce a controlled failure.

**Failure Criteria:** OOM-kill of the server or hang exceeding the
configured timeout — High severity.

---

### Algorithm H — Audit Log Tamper Review

**Preconditions:** Audit log with hash-chain enabled.

**Steps:**

1. Execute a series of DDL operations, record the audit log state.
2. Directly (bypassing the SQL interface) modify the value of one record in
   the middle of the log.
3. Execute VERIFY AUDIT LOG — verify that tampering is detected.
4. Verify that the audit log is not writable via regular
   INSERT/UPDATE/DELETE from any role.
5. Check audit log rotation/archiving — the hash chain should
   continue across archive file boundaries.

**Expected Result:** any tampering is detected.

**Failure Criteria:** undetected record tampering — Critical
severity.

---

## 11. Severity Classification and SLA

| Severity | Definition | Fix SLA | Blocks Release |
|---|---|---|---|
| Critical | Authentication bypass, SQL injection, encryption key leakage, RLS bypass, data corruption on crash | Immediately | Yes |
| High | DoS without authentication, weak TLS, missing rate limiting, timing attack | Within current iteration | Yes, for minor/major |
| Medium | Missing best practices, excessive information in errors | Within a month | No |
| Low | Stylistic issues | As possible | No |

---

## 12. Verification Schedule

| Verification | Frequency | Blocks |
|---|---|---|
| gitleaks (pre-commit) | Every commit | Local commit |
| gosec, govulncheck, race-tests | Every PR | PR merge |
| semgrep custom rules | Every PR | PR merge |
| FuzzParse, FuzzProtocol, FuzzEncryption, FuzzWALRecovery | Nightly, 2 hours each | Alerts |
| DAST (injection, auth bypass, timing) | Nightly | Alerts |
| Trivy Docker scan | Nightly + release | Release on Critical/High |
| Manual algorithms A-H (rotation) | Weekly | Recorded in report |
| Full run of all algorithms A-H | Before major/minor release | Yes |
| testssl.sh | Before release | Yes, on weak TLS |
| SBOM generation | Every release | No, mandatory artifact |
| Independent external security audit | Once a year / before major deal | Separate decision |

---

## 13. Reporting

### 13.1 Manual Algorithm Report Format

```markdown
# Security Self-Audit Report — Algorithm [A-H]

Date: 2025-XX-XX
Executor: [name]
Algorithm: [name]
VaultDB Version: X.Y.Z

## Step-by-Step Results

| Step | Status | Comment |
|---|---|---|
| 1 | Passed | |
| 2 | Passed | |
| 3 | Partial | Found X, details in Findings |
| 4 | Failed | Critical — see Findings |

## Findings

### Finding 1 — [Severity]
Description: ...
How to reproduce: ...
Recommendation: ...
Fix Status: Open / In Progress / Fixed / Accepted Risk

## Overall Verdict
[Pass / Pass with findings / Fail]
```

Reports are accumulated in docs/security/self-audits/YYYY-MM-DD-algorithm-X.md.

### 13.2 Security Status Dashboard

```json
GET /admin/security-status

{
  "last_full_audit": "2025-06-15",
  "open_findings": {
    "critical": 0,
    "high": 1,
    "medium": 3,
    "low": 7
  },
  "automated_checks": {
    "last_gosec_run": "2025-08-15T02:00:00Z",
    "last_fuzz_run": "2025-08-15T02:00:00Z",
    "last_trivy_scan": "2025-08-15T02:00:00Z",
    "all_passing": true
  },
  "manual_algorithms_coverage": {
    "A_sql_injection": "2025-08-01",
    "B_auth": "2025-08-08",
    "C_encryption": "2025-08-15",
    "D_network": "2025-07-25",
    "E_wal_recovery": "2025-07-18",
    "F_rls_bypass": "2025-07-11",
    "G_dos": "2025-07-04",
    "H_audit_log": "2025-06-27"
  }
}
```

---

## 14. Task Distribution

| Task | Owner | Priority |
|---|---|---|
| CI integration of gosec + govulncheck + semgrep | Dev3 | Immediately |
| gitleaks pre-commit + CI | Dev4 | Immediately |
| FuzzProtocol, FuzzEncryption, FuzzWALRecovery | Dev2 + Dev3 | Immediately |
| DAST scripts (injection, auth bypass, timing) | Dev3 | High |
| testssl.sh integration + TLS review | Dev3 | High |
| Trivy Docker scan in CI | Dev4 | High |
| SBOM generation | Dev4 | Medium |
| Constant-time token comparison (if not already done) | Dev3 | Critical, before release |
| Manual algorithms A-H — first full run | Entire team | High |
| Security dashboard endpoint | Dev3 | Medium |
| Process documentation + report templates | Dev1 | Medium |

---

## 15. Acceptance Checklist

### Automation (Track A)

| # | Criterion | Verification |
|---|---|---|
| 1 | gosec integrated into PR gate, fails on Critical/High findings | Test PR with intentional vulnerability |
| 2 | semgrep custom rules catch SQL string concat pattern | Test commit with vulnerable code |
| 3 | govulncheck blocks PR on known exploitable CVE | Test with vulnerable dependency |
| 4 | gitleaks blocks commit with test secret | Test commit |
| 5 | FuzzProtocol runs 2+ hours nightly without server crash | CI log |
| 6 | FuzzEncryption finds no panics on arbitrary bytes | CI log |
| 7 | Trivy scan fails on Critical vulnerability in image | Test image with vulnerable dependency |
| 8 | DAST timing-attack test passes (ratio less than 1.15) | CI log |
| 9 | testssl.sh finds no TLS 1.0/1.1/weak cipher suites | testssl.sh report |

### Manual Algorithms (Track B)

| # | Criterion | Verification |
|---|---|---|
| 10 | Algorithm A completed, no injection payload succeeded | Report |
| 11 | Algorithm B completed, tokens stored as hashes only, constant-time comparison | Report |
| 12 | Algorithm C completed, data not found in plaintext on disk | Report |
| 13 | Algorithm D completed, TLS 1.3 confirmed | Report |
| 14 | Algorithm E completed, transactions atomic at any crash point | Report |
| 15 | Algorithm F completed, RLS not bypassable by any vector | Report |
| 16 | Algorithm G completed, DoS scenarios produce controlled failure | Report |
| 17 | Algorithm H completed, audit log tampering detected | Report |

### Process

| # | Criterion |
|---|---|
| 18 | All 8 manual algorithm reports saved in docs/security/self-audits/ |
| 19 | Security dashboard endpoint serves current data |
| 20 | No open Critical findings before declaring release |
