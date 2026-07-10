# VaultDB Wire Protocol v2 Specification

**Version**: 2.0.0
**Status**: Draft
**Date**: 2025-07-02

---

## Table of Contents

1. [Overview](#1-overview)
2. [Connection and Handshake](#2-connection-and-handshake)
3. [Authentication](#3-authentication)
4. [Query Request/Response](#4-query-requestresponse)
5. [Transaction Support](#5-transaction-support)
6. [Prepared Statements](#6-prepared-statements)
7. [Time Travel](#7-time-travel)
8. [Error Handling](#8-error-handling)
9. [Binary Mode (Future)](#9-binary-mode-future)
10. [Backward Compatibility](#10-backward-compatibility)
11. [Changelog](#11-changelog)

---

## 1. Overview

**Protocol name**: VaultDB Wire Protocol v2
**Transport**: TCP, NDJSON framing (newline-delimited JSON)
**Default port**: 5432
**Versioning**: Semantic versioning (`major.minor`)

All messages are newline-delimited JSON. Each request is a single JSON object terminated by `\n`. Each response is a single JSON object terminated by `\n`. The maximum message size is configurable (default 64 MB).

Key changes from v1:

- Connection handshake with version negotiation
- Parameterized queries (`params` field)
- Explicit database selection (`database` field)
- First-class Time Travel via `as_of` request field
- Transaction isolation level selection via `isolation` field
- Query execution metrics (`duration_ms`)
- Encryption metadata (`encryption_meta`)
- Formal error codes

---

## 2. Connection and Handshake

### 2.1 Handshake

After a TCP connection is established, a v2 client MUST send a handshake message as its first message. The server responds with a handshake response. If the client does not send a handshake within the connection's initial timeout (default 5 seconds), the server MAY assume v1 compatibility mode (see [Section 10](#10-backward-compatibility)).

#### Handshake Request

```json
{
  "type": "handshake",
  "client_version": "2.0",
  "client_name": "vaultdb-go-client",
  "supported_features": ["time_travel", "transactions", "prepared_statements"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | MUST be `"handshake"` |
| `client_version` | string | Yes | Client protocol version in `major.minor` format |
| `client_name` | string | Yes | Human-readable client identifier (e.g. `"vaultdb-go-client"`, `"vaultdb-cpp-client"`) |
| `supported_features` | array of strings | Yes | Features the client supports. MAY be empty `[]`. |

Recognized feature identifiers:

| Feature | Description |
|---------|-------------|
| `time_travel` | Client understands `as_of` request field |
| `transactions` | Client understands `isolation` request field |
| `prepared_statements` | Client uses parameterized queries via `params` |
| `binary_mode` | Client supports binary wire format (future) |

#### Handshake Response

```json
{
  "type": "handshake",
  "protocol_version": "2.0",
  "server": "VaultDB",
  "server_version": "2.0.0",
  "supported_features": ["time_travel", "transactions", "prepared_statements", "binary_mode"]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | MUST be `"handshake"` |
| `protocol_version` | string | Yes | Negotiated protocol version |
| `server` | string | Yes | Server product name |
| `server_version` | string | Yes | Server version in `major.minor.patch` format |
| `supported_features` | array of strings | Yes | Features the server supports for this connection |

#### Version Negotiation

- The client sends its desired `client_version` (e.g. `"2.0"`).
- The server responds with `protocol_version` set to the highest mutually compatible version.
- If the major versions are incompatible (e.g. client sends `"3.0"`, server supports `"2.x"`), the server MUST send an error response and close the connection.

Version negotiation error response:

```json
{
  "type": "handshake",
  "status": "error",
  "error_code": "E001",
  "message": "incompatible protocol version: client 3.0, server supports 2.x"
}
```

After a successful handshake, both parties operate at the negotiated version. All subsequent messages MUST conform to that version.

---

## 3. Authentication

Authentication is token-based. Tokens use the format `vdb_sk_...` where `...` is a Base64-encoded secret key.

### 3.1 Per-Request Auth

The client MAY include a `token` field in each query request:

```json
{
  "id": "req_01J9ABC123",
  "token": "vdb_sk_abcdef1234567890",
  "query": "SELECT * FROM users;"
}
```

The server validates the token on each request. If the token is invalid or missing (when auth is required), the server returns an authentication error (see [Section 8](#8-error-handling)).

### 3.2 Handshake Auth (Optional)

During the handshake, the client MAY include a `token` field. If present, the server authenticates the session at connection time. Subsequent requests on this connection MAY omit the `token` field.

```json
{
  "type": "handshake",
  "client_version": "2.0",
  "client_name": "vaultdb-go-client",
  "supported_features": ["time_travel", "transactions"],
  "token": "vdb_sk_abcdef1234567890"
}
```

### 3.3 Embedded Mode

When VaultDB runs in embedded mode (no separate server process), authentication MAY be disabled. In this case, the `token` field is optional on all messages, and the handshake response will not require authentication.

---

## 4. Query Request/Response

### 4.1 Request

```json
{
  "id": "req_01J9ABC123",
  "token": "vdb_sk_abcdef1234567890",
  "query": "SELECT * FROM users WHERE id = $1;",
  "params": [42],
  "database": "mydb",
  "as_of": null,
  "isolation": "read_committed"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Client-assigned request identifier. MUST be unique within a connection. |
| `token` | string | Conditional | Authentication token. Required if auth is enabled. |
| `query` | string | Yes | SQL statement. Supports `$1`, `$2`, ... parameter placeholders. |
| `params` | array | No | Parameter values to bind to `$1`, `$2`, etc. Elements MAY be typed (see below). If omitted, no parameters are bound. |
| `database` | string | No | Target database name. If omitted, uses the connection's current database. |
| `as_of` | string, integer, or null | No | Time Travel specification (see [Section 7](#7-time-travel)). `null` or omitted means current state. |
| `isolation` | string | No | Transaction isolation level. One of: `"read_committed"`, `"repeatable_read"`, `"serializable"`. If omitted, the server default applies. |

#### Parameter Types

The `params` array elements MAY be:

- **Number**: `42`, `3.14` — bound as integer or float
- **String**: `"hello"` — bound as text
- **Boolean**: `true`, `false` — bound as boolean
- **Null**: `null` — bound as SQL NULL

```json
{
  "id": "req_02",
  "query": "INSERT INTO events (name, payload, active) VALUES ($1, $2, $3);",
  "params": ["click", {"x": 10, "y": 20}, true]
}
```

### 4.2 Response — Success

```json
{
  "id": "req_01J9ABC123",
  "status": "ok",
  "type": "select",
  "columns": ["id", "name"],
  "rows": [[1, "alice"]],
  "affected": 0,
  "message": "",
  "as_of_note": "as of tx 12345",
  "duration_ms": 3,
  "encryption_meta": { "key_version": 2 }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Echoes the request ID |
| `status` | string | `"ok"` for success, `"error"` for failure |
| `type` | string | Statement type: `"select"`, `"insert"`, `"update"`, `"delete"`, `"ddl"`, `"begin"`, `"commit"`, `"rollback"`, etc. |
| `columns` | array of strings | Column names. Present for SELECT queries; empty or absent otherwise. |
| `rows` | array of arrays | Row data. Each row is an array of values (strings or typed). Present for SELECT queries; empty or absent otherwise. |
| `affected` | integer | Number of rows affected by DML statements. `0` for SELECT. |
| `message` | string | Human-readable message (e.g. `"INSERT 0 1"`). Empty string on success for SELECT. |
| `as_of_note` | string | Time Travel annotation. Present when `as_of` was specified; empty or absent otherwise. |
| `duration_ms` | number | Query execution time in milliseconds. |
| `encryption_meta` | object | Encryption metadata. Present when TDE is enabled. |

#### `encryption_meta` Fields

| Field | Type | Description |
|-------|------|-------------|
| `key_version` | integer | Version of the encryption key used to decrypt the result |

### 4.3 Response — Error

```json
{
  "id": "req_01J9ABC123",
  "status": "error",
  "error_code": "E004",
  "message": "syntax error at or near \"SELEC\"",
  "duration_ms": 1
}
```

See [Section 8](#8-error-handling) for the full error response format and error codes.

### 4.4 Backward Compatibility with v1

A v2 request is a superset of a v1 request. A v1 client sends:

```json
{
  "id": "req-1",
  "token": "vdb_sk_...",
  "query": "SELECT * FROM users;"
}
```

This is valid in v2 — all new fields (`params`, `database`, `as_of`, `isolation`) are optional. The server treats it as a v1 request with no handshake context.

A v2 response is a superset of a v1 response. New fields (`duration_ms`, `encryption_meta`) are present only when applicable. A v1 client that ignores unknown fields will function correctly.

---

## 5. Transaction Support

Transactions are managed via standard SQL statements sent as regular queries. The `isolation` field in the request sets the isolation level for the transaction.

### 5.1 Beginning a Transaction

```json
{
  "id": "req_tx_01",
  "token": "vdb_sk_...",
  "query": "BEGIN;",
  "isolation": "repeatable_read"
}
```

Response:

```json
{
  "id": "req_tx_01",
  "status": "ok",
  "type": "begin",
  "columns": [],
  "rows": [],
  "affected": 0,
  "message": ""
}
```

### 5.2 Performing Work

```json
{
  "id": "req_tx_02",
  "query": "UPDATE accounts SET balance = balance - 100 WHERE id = 1;"
}
```

### 5.3 Committing

```json
{
  "id": "req_tx_03",
  "query": "COMMIT;"
}
```

### 5.4 Rolling Back

```json
{
  "id": "req_tx_04",
  "query": "ROLLBACK;"
}
```

### 5.5 Isolation Levels

| Level | Description |
|-------|-------------|
| `read_committed` | Default. Reads see only committed data. |
| `repeatable_read` | Reads see a consistent snapshot for the duration of the transaction. |
| `serializable` | Highest isolation. Transactions execute as if serial. |

The `isolation` field MAY be set on any request. If set on the first statement of a transaction (e.g. `BEGIN`), it applies to the entire transaction. If set on subsequent statements, it is ignored (the isolation level was fixed at `BEGIN`).

### 5.6 Connection Behavior

- If a connection drops with an active transaction, the server automatically rolls back.
- A client MUST NOT send a new query while a transaction is in progress (pipelining is not supported in v2).

---

## 6. Prepared Statements

Prepared statements are managed via standard SQL statements. The `params` array in the request provides parameter values.

### 6.1 Preparing

```json
{
  "id": "req_prep_01",
  "query": "PREPARE get_user AS SELECT * FROM users WHERE id = $1;"
}
```

Response:

```json
{
  "id": "req_prep_01",
  "status": "ok",
  "type": "ddl",
  "message": "PREPARE"
}
```

### 6.2 Executing with Parameters

```json
{
  "id": "req_prep_02",
  "query": "EXECUTE get_user;",
  "params": [42]
}
```

The `params` array values are bound to `$1`, `$2`, etc. in order. The server validates parameter count and types.

Response:

```json
{
  "id": "req_prep_02",
  "status": "ok",
  "type": "select",
  "columns": ["id", "name", "email"],
  "rows": [[42, "alice", "alice@example.com"]],
  "affected": 0
}
```

### 6.3 Deallocating

```json
{
  "id": "req_prep_03",
  "query": "DEALLOCATE get_user;"
}
```

### 6.4 Inline Parameterized Queries

Parameters can also be used inline without explicit `PREPARE`:

```json
{
  "id": "req_inline_01",
  "query": "SELECT * FROM users WHERE name = $1 AND age > $2;",
  "params": ["alice", 25]
}
```

This is equivalent to an anonymous prepared statement.

---

## 7. Time Travel

Time Travel allows querying historical state of the database. The `as_of` field in the request specifies the point in time to query.

### 7.1 `as_of` Values

| Value | Type | Example | Description |
|-------|------|---------|-------------|
| `null` | null | `null` | Current state (default) |
| Timestamp | string (ISO 8601) | `"2024-01-15T10:30:00Z"` | State as of the given UTC timestamp |
| Transaction ID | integer | `12345` | State as of the given transaction ID |

### 7.2 Request with Time Travel

```json
{
  "id": "req_tt_01",
  "query": "SELECT * FROM users;",
  "as_of": "2024-01-15T10:30:00Z"
}
```

Or by transaction ID:

```json
{
  "id": "req_tt_02",
  "query": "SELECT * FROM users;",
  "as_of": 12345
}
```

### 7.3 Response with Time Travel

```json
{
  "id": "req_tt_01",
  "status": "ok",
  "type": "select",
  "columns": ["id", "name"],
  "rows": [[1, "alice"], [2, "bob"]],
  "affected": 0,
  "as_of_note": "as of 2024-01-15T10:30:00Z",
  "duration_ms": 12
}
```

The `as_of_note` field provides a human-readable description of the Time Travel point. It is present whenever `as_of` was specified.

### 7.4 Error Conditions

- If the requested timestamp is before the earliest available history, the server returns an error with `error_code: "E005"`.
- If the requested transaction ID does not exist, the server returns an error with `error_code: "E005"`.

---

## 8. Error Handling

### 8.1 Error Response Format

```json
{
  "id": "req_01J9ABC123",
  "status": "error",
  "error_code": "E004",
  "message": "syntax error at or near \"SELEC\"",
  "duration_ms": 1
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Echoes the request ID |
| `status` | string | Yes | MUST be `"error"` |
| `error_code` | string | Yes | Machine-readable error code |
| `message` | string | Yes | Human-readable error description |
| `duration_ms` | number | No | Execution time before failure |

### 8.2 Error Codes

| Code | Description |
|------|-------------|
| `E001` | **Invalid protocol version** — Client and server cannot negotiate a compatible version |
| `E002` | **Authentication required** — Server requires auth but no token was provided |
| `E003` | **Invalid token** — Token is malformed or does not match a valid key |
| `E004` | **Query parse error** — SQL syntax error or invalid query structure |
| `E005` | **Query execution error** — Query failed during execution (e.g. constraint violation, table not found) |
| `E006` | **Transaction error** — Invalid transaction state (e.g. commit without begin, nested transaction) |
| `E007` | **Rate limit exceeded** — Too many requests; client should back off and retry |
| `E008` | **Request too large** — Request exceeds the maximum message size |
| `E009` | **Query timeout** — Query exceeded the configured execution timeout |
| `E010` | **Internal error** — Unexpected server error; may be transient |

### 8.3 Error Sanitization

Server error messages are sanitized for security:

- Only messages containing known safe patterns pass through.
- Unknown internal errors are replaced with `"internal error"`.
- Messages are truncated at 200 characters.

---

## 9. Binary Mode (Future)

Binary mode is reserved for future implementation. It will use Protocol Buffers or a custom binary format for reduced overhead and higher throughput.

### 9.1 Negotiation

A client that supports binary mode advertises the `binary_mode` feature in its handshake:

```json
{
  "type": "handshake",
  "client_version": "2.0",
  "client_name": "vaultdb-go-client",
  "supported_features": ["binary_mode"]
}
```

If the server also supports binary mode, the handshake response includes `binary_mode` in `supported_features`. A subsequent `SET binary_mode = true` query (or equivalent) switches the connection to binary framing.

### 9.2 Status

Binary mode is NOT part of the v2.0.0 release. The handshake feature negotiation mechanism is in place for forward compatibility.

---

## 10. Backward Compatibility

### 10.1 v1 Client Detection

The server detects a v1 client by the absence of a handshake message. If the first message from a client is a query (has an `"id"` field but no `"type": "handshake"`), the server treats the connection as v1.

### 10.2 v1 Behavior

When operating in v1 compatibility mode:

- No handshake is performed.
- New v2 fields (`params`, `database`, `as_of`, `isolation`, `duration_ms`, `encryption_meta`) are not sent by v1 clients; the server ignores their absence.
- v1 responses are a strict subset of v2 responses (no `duration_ms` or `encryption_meta`).
- Authentication follows v1 rules (token required on each request).

### 10.3 Forward Compatibility

v2 servers MUST accept v1 requests without modification. v1 requests are valid v2 requests with no optional fields.

v1 clients MUST tolerate unknown fields in v2 responses. Per the NDJSON contract, clients that use a JSON parser that ignores unknown keys will function correctly.

---

## 11. Changelog

| Version | Date | Description |
|---------|------|-------------|
| v2.0.0 | 2025-07-02 | Initial release — handshake, version negotiation, parameterized queries, Time Travel `as_of` field, transaction isolation levels, `duration_ms`, `encryption_meta`, formal error codes |

---

*End of specification.*
