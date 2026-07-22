# Quick Start

Get VaultDB running and execute your first queries in under 5 minutes.

## 1. Start the Server

### Option A: Docker

```bash
docker run -d --name vaultdb \
  -p 5432:5432 -p 8080:8080 -p 5433:5433 \
  -e VAULTDB_AUTH_ENABLED=false \
  vaultdb:latest
```

### Option B: Build from Source

```bash
cd server
go build -o vaultdb-server ./cmd/vaultdb-server
./vaultdb-server --data ./data
```

## 2. Create a Database

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "", "query": "CREATE DATABASE demo;"}'
```

## 3. Create a Table

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "CREATE TABLE users (
      id INT AUTO_INCREMENT PRIMARY KEY,
      name TEXT NOT NULL,
      email TEXT UNIQUE,
      age INT,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
    );"
  }'
```

## 4. Insert Data

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "INSERT INTO users (name, email, age) VALUES
      ('"'"'Alice'"'"', '"'"'alice@example.com'"'"', 30),
      ('"'"'Bob'"'"', '"'"'bob@example.com'"'"', 25),
      ('"'"'Carol'"'"', '"'"'carol@example.com'"'"', 35);"
  }'
```

## 5. Query Data

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "SELECT * FROM users WHERE age > 20 ORDER BY name;"}'
```

Response:

```json
{
  "status": "ok",
  "type": "select",
  "columns": ["id", "name", "email", "age", "created_at"],
  "rows": [
    ["1", "Alice", "alice@example.com", "30", "2026-07-01T..."],
    ["2", "Bob", "bob@example.com", "25", "2026-07-01T..."],
    ["3", "Carol", "carol@example.com", "35", "2026-07-01T..."]
  ]
}
```

## 6. Create an Index

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "CREATE INDEX idx_users_email ON users (email);"}'
```

## 7. Connect via PostgreSQL Wire Protocol (`psql` or `pgx`)

VaultDB supports native PostgreSQL wire protocol (`pgwire`):

```bash
# Connect using standard psql CLI
psql "postgres://postgres:secret@localhost:5432/demo?sslmode=disable"
```

## 8. Use Transactions & Session Options

Protocol v2 uses a JSON-based handshake on connect, or standard `BEGIN`/`COMMIT` over `pgwire`:

```bash
# Connect via TCP
nc localhost 5432

# Step 1: Perform handshake
{"type":"handshake","protocol_version":"2.0","client_name":"manual"}

# Step 2: Execute queries with synchronous_commit tuning
{"id":"1","query":"SET synchronous_commit = 'off';"}
{"id":"2","query":"BEGIN;"}
{"id":"3","query":"UPDATE users SET age = 31 WHERE name = 'Alice';"}
{"id":"4","query":"COMMIT;"}
```

## 8. Full-Text Search (BM25)

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "CREATE TABLE articles (
      id INT AUTO_INCREMENT PRIMARY KEY,
      title TEXT,
      body TEXT,
      FULLTEXT (title, body)
    );"
  }'

# Search with BM25 scoring
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "SELECT title, bm25_score(articles, body) AS score FROM articles WHERE body MATCH '"'"'database engine'"'"' ORDER BY score DESC;"
  }'
```

## 9. COPY FROM/TO (Bulk Import/Export)

```bash
# Export to CSV
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "COPY users TO '"'"'/tmp/users.csv'"'"' WITH (FORMAT CSV, HEADER);"
  }'

# Import from JSON
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "COPY users FROM '"'"'/tmp/users.json'"'"' WITH (FORMAT JSON);"
  }'
```

## 10. DISTINCT ON

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "SELECT DISTINCT ON (age) id, name, age FROM users ORDER BY age, id;"
  }'
```

## 11. JSONB Operators

```bash
# Create a table with JSONB
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "CREATE TABLE events (
      id INT AUTO_INCREMENT PRIMARY KEY,
      data JSONB
    );"
  }'

# Insert JSON data
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "INSERT INTO events (data) VALUES
      ('"'"'{"type": "click", "x": 100}'"'"'),
      ('"'"'{"type": "view", "x": 200}'"'"');"
  }'

# Containment check (@>)
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "SELECT * FROM events WHERE data @> '"'"'{"type": "click"}'"'"';"
  }'
```

## 12. Table Partitioning

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{
    "database": "demo",
    "query": "CREATE TABLE logs (
      id INT AUTO_INCREMENT,
      ts TIMESTAMP,
      msg TEXT
    ) PARTITION BY RANGE (ts);"
  }'
```

## 13. CBO Optimizer Statistics (`ANALYZE`)

Gather table statistics for cost-based optimization:

```bash
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "ANALYZE users;"}'

# Inspect system statistics
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "SELECT * FROM system.pg_statistic;"}'
```

## 14. System Views & Query Control (`KILL QUERY`)

Inspect running sessions and locks, or terminate a long-running query:

```bash
# View active sessions
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "SELECT * FROM system.pg_stat_activity;"}'

# View lock table
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "SELECT * FROM system.pg_locks;"}'

# Terminate query by session/request ID
curl -s -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "demo", "query": "KILL QUERY 12345;"}'
```

## 15. Check Health

```bash
curl http://localhost:5433/health
```

## 16. Use as Go Library (Embedded Mode)

VaultDB can be embedded directly in your Go application without running a separate server:

```go
package main

import (
    "fmt"
    "vaultdb"
)

func main() {
    db, err := vaultdb.Open("./data")
    if err != nil {
        panic(err)
    }
    defer db.Close()

    _, err = db.Query("", "CREATE DATABASE myapp;")
    if err != nil {
        panic(err)
    }

    _, err = db.Query("myapp", "CREATE TABLE users (id INT PRIMARY KEY, name TEXT);")
    if err != nil {
        panic(err)
    }

    result, err := db.Query("myapp", "INSERT INTO users VALUES (1, 'Alice');")
    if err != nil {
        panic(err)
    }
    fmt.Printf("Inserted %d rows\n", result.Affected)

    result, err = db.Query("myapp", "SELECT * FROM users;")
    if err != nil {
        panic(err)
    }
    for _, row := range result.Rows {
        fmt.Printf("User: %s\n", row[1])
    }
}
```

## 18. Use as Python Client

```python
from vaultdb import Client

# Connect (Protocol v2 handshake is automatic)
conn = Client("localhost", 5432)
conn.connect()

# Execute queries
result = conn.query("SELECT * FROM users WHERE age > 20;", database="demo")
for row in result["rows"]:
    print(f"User: {row[1]}")

conn.close()
```

Install: `pip install vaultdb-client`

## 19. Use as JS/TS Client

```typescript
import { Client } from "@vaultdb/client";

const client = new Client("localhost", 5432);
await client.connect();

const result = await client.query("SELECT * FROM users WHERE age > 20;", undefined, "demo");
console.log(result.rows);

await client.close();
```

Install: `npm install @vaultdb/client`

## Next Steps

- [SQL Reference](sql-reference.md) — Complete SQL syntax
- [Configuration](configuration.md) — All options
- [HTTP API](api-reference.md) — REST endpoint details
- [Indexes](indexes.md) — Index types and optimization
- [Encryption](encryption.md) — TDE and key management
- [Security](security.md) — Audit logging, token revocation
