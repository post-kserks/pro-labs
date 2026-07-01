# TCP Protocol

VaultDB uses a native JSON-over-TCP wire protocol on port 5432 for client connections.

## Connection

```
TCP connect to localhost:5432
```

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

## Client Examples

### Go

```go
conn, _ := net.Dial("tcp", "localhost:5432")
fmt.Fprintf(conn, `{"id":"1","token":"vdb_sk_...","query":"SELECT 1;"}%s`, "\n")
scanner := bufio.NewScanner(conn)
scanner.Scan()
var resp map[string]interface{}
json.Unmarshal(scanner.Bytes(), &resp)
fmt.Println(resp)
```

### Python

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

### C++ Client

See [C++ Client documentation](client.md) for the full C++ client library.
