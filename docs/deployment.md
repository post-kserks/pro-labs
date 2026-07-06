# VaultDB Deployment Guide

## Prerequisites

- Docker 20.10+ (for container deployment)
- Or: Go 1.23+, C++17 compiler, OpenSSL (for native build)
- Or: Pre-built binaries from GitHub Releases

---

## Quick Start (Docker)

```bash
# 1. Set your API token
export VAULTDB_API_TOKENS="my-secret-token:admin"

# 2. Start VaultDB
docker compose up -d

# 3. Verify health
curl http://127.0.0.1:5433/health

# 4. Connect via Web UI
open http://localhost:8080
```

---

## Docker Deployment

### Using Docker Compose (recommended)

```bash
# Create vaultdb.yaml with your config
cp vaultdb.yaml.example vaultdb.yaml

# Set tokens in environment
export VAULTDB_API_TOKENS="token1:label1,token2:label2"

# Start
docker compose up -d

# View logs
docker compose logs -f vaultdb

# Stop
docker compose down
```

### Using Docker directly

```bash
docker run -d \
  --name vaultdb \
  -p 5432:5432 \
  -p 8080:8080 \
  -p 5433:5433 \
  -v vaultdb-data:/data \
  -v ./vaultdb.yaml:/etc/vaultdb/vaultdb.yaml:ro \
  -e VAULTDB_API_TOKENS="my-token:admin" \
  -e VAULTDB_AUTH_SECRET="your-secret-key" \
  vaultdb/vaultdb:latest
```

---

## Native Build

### Build

```bash
# Full build (server + clients + Web UI)
./build.sh

# Or server only
make build
```

### Run

```bash
# Quick start
./run.sh

# With custom config
./run.sh 0.0.0.0 5432 8080 ./vaultdb.yaml
```

---

## Configuration

### vaultdb.yaml

```yaml
server:
  host: 0.0.0.0
  port: 5432
  http_port: 8080
  monitor_port: 5433
  max_request_size_bytes: 67108864  # 64MB
  query_timeout_sec: 30
  max_connections: 1000
  tcp_keepalive_sec: 30
  tcp_idle_timeout_sec: 300
  max_prepared_statements: 1000

storage:
  engine: page
  data_dir: /data
  result_cache_size: 256
  result_cache_ttl_seconds: 30

auth:
  enabled: true
  mtls_enabled: false
  rate_window_seconds: 60
  max_fails: 10
  block_for_seconds: 300

ai:
  provider: openai  # or ollama
  endpoint: https://api.openai.com/v1/embeddings
  model: text-embedding-3-small
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `VAULTDB_HOST` | Listen host | `127.0.0.1` |
| `VAULTDB_PORT` | TCP port | `5432` |
| `VAULTDB_HTTP_PORT` | HTTP port | `8080` |
| `VAULTDB_MONITOR_PORT` | Metrics port | `5433` |
| `VAULTDB_DATA_DIR` | Data directory | `./data` |
| `VAULTDB_AUTH_ENABLED` | Enable auth | `true` |
| `VAULTDB_AUTH_SECRET` | HMAC secret (REQUIRED in prod) | — |
| `VAULTDB_API_TOKENS` | Comma-separated tokens | — |
| `VAULTDB_MTLS_ENABLED` | Enable mTLS | `false` |
| `VAULTDB_MTLS_CA_FILE` | CA certificate path | — |
| `VAULTDB_AI_API_KEY` | AI embedding API key | — |

---

## TLS Setup

### Self-signed certificate (development)

VaultDB auto-generates self-signed certificates on first start. Set:

```yaml
auth:
  mtls_enabled: true
```

### Production TLS

1. Obtain certificates (Let's Encrypt, internal CA)
2. Configure in `vaultdb.yaml`:

```yaml
auth:
  mtls_enabled: true
  mtls_ca_file: /path/to/ca.pem
```

3. Or pass via environment:

```bash
export VAULTDB_MTLS_ENABLED=true
export VAULTDB_MTLS_CA_FILE=/path/to/ca.pem
```

---

## Authentication Setup

### 1. Generate a secret

```bash
openssl rand -hex 32
```

### 2. Set environment variables

```bash
export VAULTDB_AUTH_SECRET="your-generated-secret"
export VAULTDB_API_TOKENS="token1:admin,token2:readonly"
```

### 3. Start the server

```bash
docker compose up -d
```

### 4. Connect with token

```bash
# TCP client
vaultdb-shell --token my-token

# HTTP API
curl -H "Authorization: Bearer my-token" http://localhost:8080/api/query \
  -d '{"database":"test","query":"SELECT 1;"}'
```

---

## Monitoring

### Prometheus metrics

Metrics endpoint: `http://localhost:5433/metrics`

Key metrics:
- `vaultdb_queries_total` — total queries
- `vaultdb_query_duration_seconds` — latency histogram
- `vaultdb_connections_active` — active connections
- `vaultdb_storage_pages_read_total` — disk reads
- `vaultdb_storage_pages_written_total` — disk writes

### Grafana dashboard

Import the VaultDB dashboard from `docs/grafana/` (if available) or create custom dashboards using the Prometheus metrics above.

---

## Backup and Recovery

### Backup

```bash
# Copy the data directory
cp -r /data /backup/vaultdb-$(date +%Y%m%d)

# Or with Docker
docker cp vaultdb:/data /backup/vaultdb-$(date +%Y%m%d)
```

### Recovery

```bash
# Stop the server
docker compose down

# Restore data
cp -r /backup/vaultdb-20260627/* /data/

# Start the server
docker compose up -d
```

VaultDB uses WAL (Write-Ahead Log) with ARIES protocol for crash recovery. On startup, it automatically replays the WAL to restore consistency.

---

## Troubleshooting

### Server won't start

```bash
# Check logs
docker compose logs vaultdb

# Common issues:
# - VAULTDB_AUTH_SECRET not set (required when auth.enabled: true)
# - Port already in use
# - Data directory not writable
```

### Connection refused

```bash
# Verify server is running
docker compose ps

# Check port bindings
docker compose port vaultdb 5432

# Test connectivity
nc -zv 127.0.0.1 5432
```

### Performance issues

```bash
# Check metrics
curl http://localhost:5433/metrics

# Increase connection pool
# Adjust result_cache_size in vaultdb.yaml
# Check disk I/O with iostat
```

### Data corruption

VaultDB has built-in WAL checksums and torn-page protection. If corruption is detected:

1. Check WAL integrity logs
2. Restore from backup
3. File an issue at https://github.com/post-kserks/vaultdb/issues

---

## Security Best Practices

1. **Always set `VAULTDB_AUTH_SECRET`** in production
2. **Use strong tokens** — generate with `openssl rand -hex 32`
3. **Enable mTLS** for production deployments
4. **Run as non-root** — Docker image already does this
5. **Restrict network access** — bind to specific IPs, use firewall rules
6. **Enable encryption at rest** — use filesystem-level encryption (LUKS, FileVault)
7. **Monitor auth failures** — check metrics for brute force attempts
8. **Keep updated** — run `govulncheck` regularly

---

## Enterprise Deployment

For production deployments with tuning recommendations, see [Enterprise Deployment Guide](deployment-enterprise.md).

Key topics:
- GOGC/GOMEMLIMIT tuning
- Resource sizing
- Kubernetes deployment
- Security checklist
