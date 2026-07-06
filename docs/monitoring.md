# Monitoring and Metrics

VaultDB exposes metrics in Prometheus format and provides health/readiness endpoints for monitoring.

## Health Endpoints

### Liveness Probe

```bash
GET /health
```

Returns:
```json
{
  "status": "ok",
  "version": "1.1.1",
  "uptime_seconds": 3600,
  "active_connections": 5,
  "storage": "ok"
}
```

### Readiness Probe

```bash
GET /ready
```

Returns 200 if storage is reachable, 503 otherwise.

### Ports

| Port | Auth Required | Description |
|------|---------------|-------------|
| 5433 | No | Monitor port (always accessible) |
| 8080 | Yes | API port (auth-gated) |

## Prometheus Metrics

```bash
GET /metrics
```

### Query Metrics

```
# Total queries by type and status
vaultdb_queries_total{type="select",status="ok"} 12345
vaultdb_queries_total{type="insert",status="ok"} 890
vaultdb_queries_total{type="update",status="ok"} 456
vaultdb_queries_total{type="delete",status="ok"} 123
vaultdb_queries_total{type="ddl",status="ok"} 67
vaultdb_queries_total{type="explain",status="ok"} 89
vaultdb_queries_total{type="transaction",status="ok"} 345
vaultdb_queries_total{type="other",status="ok"} 12

# Error counts
vaultdb_queries_total{type="select",status="error"} 3
vaultdb_queries_total{type="insert",status="error"} 1
```

### Duration Histogram

```
vaultdb_query_duration_seconds_bucket{le="0.001"} 5000
vaultdb_query_duration_seconds_bucket{le="0.005"} 8000
vaultdb_query_duration_seconds_bucket{le="0.01"} 9500
vaultdb_query_duration_seconds_bucket{le="0.025"} 9900
vaultdb_query_duration_seconds_bucket{le="0.05"} 9990
vaultdb_query_duration_seconds_bucket{le="0.1"} 9999
vaultdb_query_duration_seconds_bucket{le="0.25"} 10000
vaultdb_query_duration_seconds_bucket{le="0.5"} 10000
vaultdb_query_duration_seconds_bucket{le="1.0"} 10000
vaultdb_query_duration_seconds_bucket{le="+Inf"} 10000
vaultdb_query_duration_seconds_sum 45.678
vaultdb_query_duration_seconds_count 10000
```

### Percentile Summary

```
vaultdb_query_duration_percentile{quantile="0.5"} 0.003
vaultdb_query_duration_percentile{quantile="0.95"} 0.015
vaultdb_query_duration_percentile{quantile="0.99"} 0.045
```

### Gauge Metrics

```
vaultdb_active_connections 5
vaultdb_uptime_seconds 3600
```

### WAL Metrics

```
vaultdb_wal_entries_total 15000
vaultdb_wal_checkpoint_total 120
```

### Index Metrics

```
vaultdb_index_lookups_total{result="hit"} 8500
vaultdb_index_lookups_total{result="miss"} 1500
```

### Storage Row Counts

```
vaultdb_storage_rows{database="mydb",table="users"} 1000
vaultdb_storage_rows{database="mydb",table="orders"} 5000
```

Capped at 1000 table entries. Overflow indicated by `vaultdb_storage_rows_overflow 1`.

## Grafana Dashboard

Example Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: 'vaultdb'
    static_configs:
      - targets: ['localhost:5433']
    metrics_path: '/metrics'
```

### Useful PromQL Queries

```promql
# Query rate per second
rate(vaultdb_queries_total[5m])

# Error rate
rate(vaultdb_queries_total{status="error"}[5m])

# P99 latency
histogram_quantile(0.99, rate(vaultdb_query_duration_seconds_bucket[5m]))

# Active connections
vaultdb_active_connections

# WAL growth rate
rate(vaultdb_wal_entries_total[5m])

# Index hit ratio
vaultdb_index_lookups_total{result="hit"} / (vaultdb_index_lookups_total{result="hit"} + vaultdb_index_lookups_total{result="miss"})
```

## Log Rotation

VaultDB supports size-based log file rotation:

```go
rotator := logging.NewRotator("vaultdb.log", 100, 5) // 100MB max, 5 backups
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `maxSizeMB` | 100 | Rotate when file exceeds this size |
| `maxBackups` | 5 | Keep at most this many rotated files |

### Audit Logging

DDL operations are logged as structured JSON:

```json
{
  "timestamp": "2026-07-01T14:30:00Z",
  "type": "ddl",
  "operation": "create_table",
  "database": "mydb",
  "target": "users",
  "detail": "CREATE TABLE users (id INT, name TEXT)"
}
```

## Security Dashboard

VaultDB provides a security dashboard endpoint for monitoring security-related metrics.

### Endpoint

```bash
GET /admin/security-status
```

### Authentication

Requires admin privileges. Include authentication token:

```
Authorization: Bearer vdb_sk_your_token_here
```

### Response

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
  },
  "recent_security_events": [
    {
      "timestamp": "2026-07-01T14:30:00Z",
      "type": "token_revoked",
      "detail": "Token vdb_sk_*** revoked"
    }
  ]
}
```

### Security Metrics

| Metric | Description |
|--------|-------------|
| `encryption.enabled` | Whether encryption is enabled |
| `encryption.algorithm` | Encryption algorithm used |
| `encryption.key_source` | Key management source |
| `encryption.databases_encrypted` | Number of encrypted databases |
| `authentication.enabled` | Whether authentication is enabled |
| `authentication.active_tokens` | Number of active tokens |
| `authentication.revoked_tokens` | Number of revoked tokens |
| `audit_log.enabled` | Whether audit logging is enabled |
| `audit_log.total_entries` | Total audit log entries |
| `audit_log.chain_intact` | Whether audit log chain is intact |

### Use Cases

- Monitor encryption status across databases
- Track authentication token usage
- Verify audit log integrity
- Detect security anomalies
- Compliance reporting
