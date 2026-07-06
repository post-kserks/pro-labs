# Installation

## Requirements

- **Go 1.25+** (for building from source)
- **CMake + g++** (for the C++ client, optional)
- **Docker** (for container deployment, optional)

## Building from Source

### Server

```bash
cd server
go build -o vaultdb-server ./cmd/vaultdb-server
```

### C++ Client

```bash
cd client
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -- -j$(nproc)
```

### Backup Tool

```bash
cd server
go build -o vaultdb-backup ./cmd/vaultdb-backup
```

## Go Install

```bash
go install github.com/vaultdb/vaultdb/server/cmd/vaultdb-server@latest
```

## Docker

### Build Image

```bash
docker build -t vaultdb:latest .
```

### Run Container

```bash
docker run -d \
  --name vaultdb \
  -p 5432:5432 \
  -p 8080:8080 \
  -p 5433:5433 \
  -v vaultdb-data:/data \
  -e VAULTDB_AUTH_ENABLED=false \
  vaultdb:latest
```

### Docker Compose

```yaml
version: '3.8'
services:
  vaultdb:
    build: .
    ports:
      - "5432:5432"
      - "8080:8080"
      - "5433:5433"
    volumes:
      - vaultdb-data:/data
    environment:
      - VAULTDB_AUTH_ENABLED=false
    restart: unless-stopped

volumes:
  vaultdb-data:
```

```bash
docker compose up -d
```

## Direct Launch

```bash
./vaultdb-server \
  --host 0.0.0.0 \
  --port 5432 \
  --http-port 8080 \
  --monitor-port 5433 \
  --data ./data \
  --config vaultdb.yaml
```

## Generating a Config File

```bash
cat > vaultdb.yaml << 'EOF'
server:
  host: "0.0.0.0"
  port: 5432
  http_port: 8080
  monitor_port: 5433

storage:
  engine: "page"
  data_dir: "./data"

auth:
  enabled: true
EOF
```

## Environment Variables

All config values can be overridden via environment variables with the `VAULTDB_` prefix:

| Variable | Config Path | Description |
|----------|-------------|-------------|
| `VAULTDB_HOST` | `server.host` | Bind address |
| `VAULTDB_PORT` | `server.port` | TCP port |
| `VAULTDB_HTTP_PORT` | `server.http_port` | HTTP port |
| `VAULTDB_MONITOR_PORT` | `server.monitor_port` | Monitor port |
| `VAULTDB_DATA_DIR` | `storage.data_dir` | Data directory |
| `VAULTDB_AUTH_SECRET` | (required) | HMAC signing key |
| `VAULTDB_AUTH_ENABLED` | `auth.enabled` | Enable/disable auth |
| `VAULTDB_API_TOKENS` | (token list) | Comma-separated tokens |
| `VAULTDB_AI_API_KEY` | `ai.api_key` | AI embedding API key |

## Verifying the Installation

```bash
# Health check
curl http://localhost:5433/health

# Create a test database and table
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"CREATE DATABASE test;"}'

curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"CREATE TABLE hello (id INT, msg TEXT);"}'

curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"INSERT INTO hello VALUES (1, '\''Hello, VaultDB!'\'');"}'

curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database":"test","query":"SELECT * FROM hello;"}'
```
