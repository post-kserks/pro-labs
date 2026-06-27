# VaultDB HTTP API Reference

## Base URL

```
http://<host>:<http_port>
```

Default: `http://127.0.0.1:8080`

## Authentication

All API endpoints require Bearer token authentication when `auth.enabled: true`:

```
Authorization: Bearer <token>
```

Tokens are configured via `VAULTDB_API_TOKENS` environment variable or `vaultdb.yaml`.

---

## Endpoints

### POST /api/query

Execute a SQL query.

**Request:**
```json
{
  "database": "mydb",
  "query": "SELECT * FROM users LIMIT 10;"
}
```

**Response (success):**
```json
{
  "type": "rows",
  "columns": ["id", "name", "email"],
  "rows": [
    ["1", "Alice", "alice@example.com"],
    ["2", "Bob", "bob@example.com"]
  ],
  "affected_rows": 0,
  "duration_ms": 5
}
```

**Response (error):**
```json
{
  "type": "error",
  "message": "table \"users\" does not exist"
}
```

---

### GET /api/databases

List all databases.

**Response:**
```json
{
  "databases": ["mydb", "test", "analytics"]
}
```

---

### GET /api/databases/:db/tables

List tables in a database.

**Response:**
```json
{
  "tables": [
    {"name": "users", "row_count": 1500, "created_at": "2026-01-15T10:30:00Z"},
    {"name": "orders", "row_count": 50000, "created_at": "2026-01-15T10:31:00Z"}
  ]
}
```

---

### GET /api/databases/:db/tables/:table/data

Get data from a table with optional filtering and pagination.

**Query Parameters:**
- `where` — SQL WHERE clause (e.g., `age > 25`)
- `limit` — Maximum rows returned (default: 100)
- `offset` — Skip N rows (default: 0)

**Response:**
```json
{
  "columns": ["id", "name", "age"],
  "rows": [
    ["1", "Alice", 30],
    ["2", "Bob", 25]
  ],
  "total": 1500
}
```

---

### POST /api/databases/:db/tables/:table/data

Insert rows into a table.

**Request:**
```json
[
  {"id": 1, "name": "Alice", "age": 30},
  {"id": 2, "name": "Bob", "age": 25}
]
```

**Response:**
```json
{
  "message": "inserted 2 rows"
}
```

**Status Codes:**
- `201 Created` — rows inserted successfully
- `400 Bad Request` — invalid JSON or missing table
- `500 Internal Server Error` — storage error

---

### GET /api/openapi.json

Returns auto-generated OpenAPI specification for all database tables.

---

### GET /health

Health check endpoint (no authentication required).

**Response:** `200 OK`

---

### WebSocket /ws/live

Subscribe to live query results.

**Protocol:** WebSocket with JSON messages

**Subscribe:**
```json
{"action": "subscribe", "query": "SELECT * FROM orders WHERE status = 'pending'"}
```

**Unsubscribe:**
```json
{"action": "unsubscribe", "id": "subscription-id"}
```

**Live update:**
```json
{"type": "update", "id": "subscription-id", "rows": [...], "columns": [...]}
```

---

## Error Codes

| Code | Description |
|------|-------------|
| `bad_request` | Invalid request format or parameters |
| `unauthorized` | Missing or invalid authentication token |
| `rate_limit_exceeded` | Too many requests |
| `storage_error` | Internal storage engine error |
| `not_implemented` | Endpoint not yet implemented |
| `query_error` | SQL parsing or execution error |

---

## Rate Limiting

- **TCP connections:** Token bucket per connection (100 req/s, burst 200)
- **HTTP API:** Global token bucket (configurable via `rate_limit`)
- **Auth failures:** IP-based (10 failures/min → 5 min block)

---

## Metrics (Prometheus)

Endpoint: `GET /metrics` (monitor port, default 5433)

Available metrics:
- `vaultdb_queries_total` — total queries executed
- `vaultdb_query_duration_seconds` — query latency histogram (p50/p95/p99)
- `vaultdb_connections_active` — active connections
- `vaultdb_storage_pages_read_total` — pages read from disk
- `vaultdb_storage_pages_written_total` — pages written to disk
