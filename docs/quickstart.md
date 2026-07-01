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

## 7. Use Transactions (TCP)

```bash
# Connect via TCP
nc localhost 5432

# Send requests (newline-delimited JSON)
{"id":"1","query":"BEGIN;"}
{"id":"2","query":"UPDATE users SET age = 31 WHERE name = '"'"'Alice'"'"';"}
{"id":"3","query":"COMMIT;"}
```

## 8. Check Health

```bash
curl http://localhost:5433/health
```

## Next Steps

- [SQL Reference](sql-reference.md) — Complete SQL syntax
- [Configuration](configuration.md) — All options
- [HTTP API](api-reference.md) — REST endpoint details
- [Indexes](indexes.md) — Index types and optimization
