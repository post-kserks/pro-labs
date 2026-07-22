# Wire Protocols

VaultDB supports dual network protocols for client connections:

1. **PostgreSQL Wire Protocol (`pgwire`)** (port 5432 / 5433): Implemented in `internal/protocol/pgwire`, offering native wire compatibility with standard PostgreSQL drivers (`pgx`, `lib/pq`, `psql`, JDBC, etc.).
2. **Native JSON-over-TCP Protocol (Protocol v2)** (port 5432): High-performance newline-delimited JSON wire protocol for official VaultDB client SDKs (Go, Python, JavaScript/TypeScript, C++).

---

## 1. PostgreSQL Wire Protocol (`pgwire`)

VaultDB's `pgwire` protocol engine (`internal/protocol/pgwire`) accepts standard PostgreSQL frontend connections.

### Connection Details

```bash
# Connect using psql
psql -h localhost -p 5432 -U postgres -d demo

# Connect using pgx or lib/pq connection string
postgres://postgres:secret@localhost:5432/demo?sslmode=disable
```

### Protocol Features

- **Authentication**: Cleartext / MD5 / SCRAM-SHA-256 handshake compatibility.
- **Query Execution**: Simple Query (`Q`) and Extended Query (`P`/`B`/`E`/`S`/`D`/`C`/`H`) protocols.
- **Prepared Statements**: Full support for `Parse`, `Bind`, `Execute`, and `Describe`.
- **System Catalogs**: Native response formatting for `pg_catalog` metadata queries.
- **Driver Compatibility**: Tested against `psql`, Go `pgx` / `lib/pq`, Python `psycopg2` / `asyncpg`, Node.js `pg`, and Java JDBC.

---

## 2. Native JSON-over-TCP Protocol (Protocol v2)

### Connection

```
TCP connect to localhost:5432
```

## Protocol Versioning

VaultDB supports protocol v2 with handshake negotiation.

### Handshake (Recommended)

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

## Wire Format

All messages are newline-delimited JSON. Each request is a single JSON object terminated by `\n`. Each response is a single JSON object terminated by `\n`.

## Request Format

```json
{
  "id": "req-1",
  "token": "vdb_sk_your_token_here",
  "query": "SELECT * FROM users WHERE id = 1;"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Client-assigned request identifier |
| `token` | string | Yes | Authentication token |
| `query` | string | Yes | SQL statement |
| `version` | string | No | Protocol version, e.g. `"2.0"` for v2 features |
| `params` | array | No | Typed parameters for parameterized queries |
| `database` | string | No | Target database name |
| `as_of` | string | No | Time Travel — timestamp or snapshot ID |
| `isolation` | string | No | Transaction isolation level (`"serializable"`, `"read_committed"`, etc.) |

## Response Format

### Success

```json
{
  "id": "req-1",
  "status": "ok",
  "type": "select",
  "columns": ["id", "name", "email"],
  "rows": [
    ["1", "Alice", "alice@example.com"]
  ],
  "affected": 0,
  "message": "",
  "as_of_note": ""
}
```

### Error

```json
{
  "id": "req-1",
  "status": "error",
  "message": "table 'users' does not exist"
}
```

## Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Echoes the request ID |
| `status` | string | `"ok"` or `"error"` |
| `type` | string | `"select"`, `"insert"`, `"update"`, `"delete"`, `"ddl"`, etc. |
| `columns` | array | Column names (for SELECT) |
| `rows` | array | Row data as arrays of stringified values |
| `affected` | int | Number of rows affected (for DML) |
| `message` | string | Human-readable message |
| `as_of_note` | string | Time-travel annotation (for AS OF queries) |
| `duration_ms` | float | Query execution time in milliseconds |
| `encryption_meta` | object | TDE metadata (algorithm, key ID, encrypted columns) |

## Transaction Support

TCP connections support full transaction management:

```json
{"id":"1","token":"vdb_sk_...","query":"BEGIN;"}
{"id":"2","token":"vdb_sk_...","query":"INSERT INTO t VALUES (1);"}
{"id":"3","token":"vdb_sk_...","query":"COMMIT;"}
```

## Prepared Statements

```json
{"id":"1","token":"vdb_sk_...","query":"PREPARE get_user AS SELECT * FROM users WHERE id = $1;"}
{"id":"2","token":"vdb_sk_...","query":"EXECUTE get_user (42);"}
{"id":"3","token":"vdb_sk_...","query":"DEALLOCATE get_user;"}
```

## Connection Behavior

- **Keepalive**: TCP keepalive enabled (default 30s interval)
- **Idle timeout**: Connections with no activity for 300s are closed
- **Rate limiting**: Per-connection token-bucket rate limiter
- **Auto-rollback**: If connection drops with active transaction, it is automatically rolled back
- **Max request size**: Configurable (default 64 MB)

## Error Sanitization

TCP error messages are sanitized for security:
- Only messages containing known safe patterns pass through
- Unknown errors become `"internal error"`
- Messages truncated at 200 characters

## Admin Commands

Administrative operations are available via HTTP endpoints, not the TCP protocol:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/admin/revoke-token` | POST | Revoke a token (body: `{"token": "..."}`) |
| `/admin/security-status` | GET | Security status overview |
| `/health` | GET | Health check (no auth required on monitor port) |
| `/ready` | GET | Readiness check (no auth required on monitor port) |
| `/metrics` | GET | Prometheus metrics |
| `/dashboard` | GET | Web dashboard |

All admin endpoints (except `/health` and `/ready` on port 5433) require authentication.

```bash
# Token revocation example
curl -X POST http://localhost:8080/admin/revoke-token \
  -H "Authorization: Bearer vdb_sk_admin_token" \
  -H "Content-Type: application/json" \
  -d '{"token": "vdb_sk_token_to_revoke"}'
```

## Client Examples

### PostgreSQL Client Drivers (pgwire)

#### psql CLI
```bash
psql "postgres://postgres:secret@localhost:5432/demo?sslmode=disable"
```

#### Go (pgx)
```go
import (
    "context"
    "fmt"
    "github.com/jackc/pgx/v5"
)

conn, err := pgx.Connect(context.Background(), "postgres://postgres:secret@localhost:5432/demo?sslmode=disable")
if err != nil {
    log.Fatal(err)
}
defer conn.Close(context.Background())

var id int
var name string
err = conn.QueryRow(context.Background(), "SELECT id, name FROM users WHERE id = $1", 42).Scan(&id, &name)
```

#### Go (database/sql + lib/pq)
```go
import (
    "database/sql"
    _ "github.com/lib/pq"
)

db, err := sql.Open("postgres", "postgres://postgres:secret@localhost:5432/demo?sslmode=disable")
if err != nil {
    log.Fatal(err)
}
defer db.Close()
```

### Go (Native VaultDB Client)

```go
import vaultdb "github.com/post-kserks/vaultdb/client/go"

client, err := vaultdb.TCPDial("localhost:5432", "vdb_sk_...")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

result, err := client.Query("mydb", "SELECT * FROM users WHERE id = $1", "42")
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Rows)
```

### Python (Official Client)

```python
from vaultdb import Client

with Client("localhost", 5432, "vdb_sk_...") as client:
    client.connect()
    result = client.query("SELECT * FROM users WHERE id = $1", [42])
    print(result["rows"])
```

### JavaScript/TypeScript (Official Client)

```typescript
import { Client } from '@vaultdb/client';

const client = new Client('localhost', 5432, 'vdb_sk_...');
await client.connect();

const result = await client.query('SELECT * FROM users WHERE id = $1', [42]);
console.log(result.rows);

await client.close();
```

### Raw TCP (Low-level)

```python
import socket, json

sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.connect(('localhost', 5432))

request = json.dumps({
    "id": "1",
    "token": "vdb_sk_...",
    "query": "SELECT 1;"
}) + "\n"

sock.send(request.encode())
response = sock.recv(4096).decode().strip()
data = json.loads(response)
print(data)
```
