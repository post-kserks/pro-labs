# HTTP API Reference

VaultDB exposes a REST API on port 8080 (configurable). All endpoints accept and return JSON.

## Base URL

```
http://localhost:8080
```

## Authentication

All API endpoints require authentication (unless `auth.enabled: false`):

```
Authorization: Bearer vdb_sk_your_token_here
```

or

```
X-VaultDB-Token: vdb_sk_your_token_here
```

Localhost requests bypass authentication.

---

## POST /api/query

Execute a single SQL statement.

### Request

```json
{
  "database": "mydb",
  "query": "SELECT * FROM users WHERE age > 25;",
  "params": ["param1", "param2"],
  "session_id": "optional-session-id"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `database` | string | Yes | Database name (use `""` for DDL like CREATE DATABASE) |
| `query` | string | Yes | SQL statement |
| `params` | array | No | Positional parameters for `$1`, `$2`, ... |
| `session_id` | string | No | Session ID for transaction support. Omit for stateless requests. |

### Response (SELECT)

```json
{
  "status": "ok",
  "type": "select",
  "columns": ["id", "name", "age"],
  "rows": [
    [1, "Alice", 30],
    [2, "Bob", 25]
  ],
  "affected": 0,
  "duration_ms": 1.23,
  "session_id": "session-id-if-used"
}
```

### Response (INSERT/UPDATE/DELETE)

```json
{
  "status": "ok",
  "type": "insert",
  "affected": 3,
  "message": "inserted 3 rows",
  "duration_ms": 0.5
}
```

### Error Response

```json
{
  "status": "error",
  "error_code": 3002,
  "message": "parse error: syntax error near 'FROM'"
}
```

### Error Codes

| Code | Description |
|------|-------------|
| 3001 | Bad request |
| 3002 | Parse error |
| 3003 | Unknown column |
| 3004 | Storage error |
| 3005 | Transaction unsupported over HTTP |
| 3006 | Rate limited |
| 3007 | NOT NULL constraint violation |
| 3008 | Type mismatch |
| 3009 | Table not found |
| 3010 | Database not found |
| 3011 | Duplicate value (PK/UNIQUE) |
| 3012 | CHECK constraint violation |
| 3013 | Foreign key violation |
| 3014 | Query timeout |
| 5000 | Internal error |

---

## POST /api/query/stream

Execute a query and stream results via Server-Sent Events (SSE).

### Request

Same fields as `/api/query` except `params` (parameterized queries not supported in stream mode).

### Response

```
Content-Type: text/event-stream

event: columns
data: ["id","name","age"]

event: row
data: ["1","Alice","30"]

event: row
data: ["2","Bob","25"]

event: done
data: {"affected":0,"duration_ms":1.5}
```

---

## POST /api/transaction

Execute transaction control commands.

### Request

```json
{
  "database": "mydb",
  "action": "begin"
}
```

**Actions**: `begin`, `commit`, `rollback`

### Session-based Transactions

For multi-statement transactions over HTTP, use the `session_id` field in `/api/query`:

```json
// Step 1: Begin transaction
{"database": "mydb", "query": "BEGIN", "session_id": "sess-123"}

// Step 2: Execute queries in the same session
{"database": "mydb", "query": "INSERT INTO users VALUES (1, 'Alice')", "session_id": "sess-123"}
{"database": "mydb", "query": "UPDATE accounts SET balance = balance - 100", "session_id": "sess-123"}

// Step 3: Commit
{"database": "mydb", "query": "COMMIT", "session_id": "sess-123"}
```

Sessions expire after 5 minutes of inactivity.

---

## POST /api/batch

Execute multiple queries sequentially.

### Request

```json
{
  "database": "mydb",
  "queries": [
    {"query": "INSERT INTO users VALUES (1, 'Alice');"},
    {"query": "INSERT INTO users VALUES (2, 'Bob');"},
    {"query": "SELECT * FROM users;"}
  ]
}
```

### Response

```json
{
  "results": [
    {"status": "ok", "type": "insert", "affected": 1},
    {"status": "ok", "type": "insert", "affected": 1},
    {"status": "ok", "type": "select", "columns": ["id","name"], "rows": [["1","Alice"],["2","Bob"]]}
  ]
}
```

---

## GET /api/databases

List all databases.

### Response

```json
{
  "status": "ok",
  "databases": [
    {"name": "mydb"},
    {"name": "analytics"},
    {"name": "logs"}
  ]
}
```

---

## GET /api/databases/{db}/tables

List tables in a database.

### Response

```json
{
  "status": "ok",
  "tables": [
    {"name": "users", "row_count": 1000, "created_at": "2026-07-01T10:00:00Z"},
    {"name": "orders", "row_count": 5000, "created_at": "2026-07-01T10:05:00Z"}
  ]
}
```

---

## GET /api/databases/{db}/tables/{table}/schema

Get table schema.

### Response

```json
{
  "status": "ok",
  "table": "users",
  "columns": [
    {"name": "id", "type": "INT", "primary_key": true},
    {"name": "name", "type": "TEXT", "not_null": true},
    {"name": "email", "type": "VARCHAR", "varchar_len": 255}
  ],
  "row_count": 1000
}
```

---

## GET /api/databases/{db}/tables/{table}/data

Fetch rows with optional filtering.

### Query Parameters

Filter by column values using operators:

| Parameter | Operator | Example |
|-----------|----------|---------|
| `col=value` | Equality | `?name=Alice` |
| `col=gt.value` | Greater than | `?age=gt.25` |
| `col=lt.value` | Less than | `?age=lt.50` |
| `col=like.pattern` | LIKE match | `?name=like.Ali%` |

### Response

```json
{
  "status": "ok",
  "rows": [
    {"id": 1, "name": "Alice", "email": "alice@example.com"},
    {"id": 2, "name": "Bob", "email": "bob@example.com"}
  ]
}
```

---

## POST /api/databases/{db}/tables/{table}/data

Insert rows.

### Request

```json
[
  {"name": "Alice", "email": "alice@example.com", "age": 30},
  {"name": "Bob", "email": "bob@example.com", "age": 25}
]
```

### Response

```json
{
  "status": "ok",
  "message": "inserted 2 rows"
}
```

---

## GET /api/docs/openapi.json

Auto-generated OpenAPI 3.0 specification covering all discovered database+table pairs.

---

## GET /api/live

Subscribe to live query updates via SSE.

### Query Parameters

| Parameter | Description |
|-----------|-------------|
| `database` | Database name |
| `query` | SELECT query to subscribe to |

### Response

SSE stream with initial snapshot followed by incremental updates:

```
event: snapshot
data: {"columns":["id","name"],"rows":[["1","Alice"]]}

event: row
data: {"op":"insert","row":["2","Bob"]}

event: row
data: {"op":"update","row":["1","Alice Jr."]}
```

---

## GET /health

Liveness probe.

### Response

```json
{
  "status": "ok",
  "version": "1.1.1",
  "uptime_s": 3600,
  "connections": 5,
  "wal_enabled": true,
  "time_travel": true,
  "checks": {
    "storage": {"status": "pass"},
    "wal": {"status": "pass"},
    "session_pool": {"status": "pass", "active": 1, "idle": 9, "max": 10}
  }
}
```

---

## GET /ready

Readiness probe. Returns 200 if storage is reachable, 503 otherwise.

---

## GET /metrics

Prometheus metrics endpoint. See [Monitoring](monitoring.md) for details.

---

## GET /dashboard

Inline HTML dashboard (single-file SPA). Requires authentication.

---

## Admin Endpoints

Admin endpoints require admin privileges and are used for security and management operations.

### POST /admin/revoke-token

Revokes an authentication token, preventing it from being used for future requests.

#### Request

```json
{
  "token": "vdb_sk_token_to_revoke"
}
```

#### Response

```json
{
  "status": "ok",
  "message": "Token revoked successfully"
}
```

**Notes:**
- Requires admin privileges
- Revoked tokens are immediately rejected
- Token revocation is permanent
- Revoked tokens are cleaned up after 24 hours

### GET /admin/security-status

Returns security-related metrics and status information.

#### Response

```json
{
  "status": "ok",
  "encryption": {
    "enabled": true,
    "algorithm": "AES-256-GCM",
    "key_source": "passphrase",
    "databases_encrypted": 3
  },
  "authentication": {
    "enabled": true,
    "active_tokens": 5,
    "revoked_tokens": 2
  },
  "audit_log": {
    "enabled": true,
    "total_entries": 15000,
    "chain_intact": true
  }
}
```

**Notes:**
- Requires admin privileges
- Provides encryption status across databases
- Shows authentication token metrics
- Verifies audit log integrity

---

## Protocol v2

VaultDB supports Protocol v2 with enhanced features. See [TCP Protocol](tcp-protocol.md) for details.

### Key Features

- **Handshake negotiation**: Client and server negotiate protocol version
- **Typed parameters**: Support for parameterized queries with type information
- **Time travel queries**: Read data as of a specific timestamp or snapshot
- **Isolation levels**: Specify transaction isolation level per request
- **Prepared statements**: Server-side prepared statement support

### Handshake

After TCP connect, send a handshake message:

```json
{"type": "handshake", "client_version": "2.0", "client_name": "my-app", "supported_features": ["time_travel", "transactions"]}
```

Server responds:

```json
{"type": "handshake", "protocol_version": "2.0", "server": "VaultDB", "server_version": "2.0.0", "supported_features": ["time_travel", "transactions", "prepared_statements"]}
```

### Backward Compatibility

If no handshake is sent, the server operates in v1 compatibility mode.
