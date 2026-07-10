# VaultDB Enterprise Deployment Guide

## Overview

This guide covers production deployment best practices for VaultDB in enterprise environments. For basic Docker/native setup, see [deployment.md](deployment.md).

---

## Resource Sizing

### Small Deployment (< 1 GB data)

| Resource | Recommended |
|----------|-------------|
| CPU | 2+ cores |
| RAM | 4 GB |
| Storage | 20 GB SSD |
| GOGC | 100 (default) |
| GOMEMLIMIT | Not set |

### Medium Deployment (1–10 GB data)

| Resource | Recommended |
|----------|-------------|
| CPU | 4+ cores |
| RAM | 16 GB |
| Storage | 100 GB SSD |
| GOGC | 75 |
| GOMEMLIMIT | 80% of container RAM (e.g., 12 GB for 16 GB container) |

### Large Deployment (10 GB+ data)

| Resource | Recommended |
|----------|-------------|
| CPU | 8+ cores |
| RAM | 32 GB+ |
| Storage | 500 GB+ NVMe SSD |
| GOGC | 50 |
| GOMEMLIMIT | 80% of container RAM |

---

## GC Tuning

### GOGC

GOGC controls the garbage collector target percentage. Lower values = more frequent GC, less memory usage but more CPU.

| GOGC | Behavior | Use Case |
|------|----------|----------|
| 100 (default) | GC triggers when heap grows 100% beyond live data | < 1 GB data |
| 75 | GC triggers at 75% growth. Reduces peak memory by ~25% at cost of ~10% more CPU | 1–10 GB data |
| 50 | GC triggers at 50% growth. Best for 10 GB+ where memory pressure is high | 10 GB+ data |

### GOMEMLIMIT

GOMEMLIMIT sets a soft memory limit (Go 1.19+).

- Set to **80% of available RAM** to leave room for OS page cache and other processes
- Prevents OOM kills in containerized environments
- Works best with GOGC tuning

### Setting Environment Variables

```bash
# Docker
docker run -e GOGC=75 -e GOMEMLIMIT=12GiB vaultdb:v2.0

# Docker Compose
services:
  vaultdb:
    environment:
      - GOGC=75
      - GOMEMLIMIT=12GiB

# Kubernetes
env:
- name: GOGC
  value: "75"
- name: GOMEMLIMIT
  value: "12GiB"
```

### Tuning Procedure

1. Start with default GOGC=100
2. Monitor heap usage via `runtime.MemStats` or `/metrics`
3. If heap spikes exceed available headroom, lower GOGC in steps of 25
4. Set GOMEMLIMIT to 80% of container memory limit
5. Verify no OOM kills: `kubectl describe pod <vaultdb-pod>`

---

## sync.Pool Optimization

VaultDB uses `sync.Pool` for hot row allocations. This reduces GC pressure significantly for query-heavy workloads.

- Pool is automatic — no configuration needed
- Works best with consistent query patterns
- Pool warms up within seconds of startup

---

## Buffer Pool Configuration

The buffer pool caches frequently accessed pages in memory. Larger pools improve read-heavy workloads at the cost of memory.

```yaml
storage:
  # Small deployment (default)
  buffer_pool_pages: 16384  # 128 MB

  # Medium deployment
  buffer_pool_pages: 32768  # 256 MB

  # Large deployment
  buffer_pool_pages: 65536  # 512 MB
```

**Formula:** `buffer_pool_pages = target_memory_MB * 1024 * 1024 / page_size`

---

## WAL Configuration

```yaml
storage:
  # Default: fsync after checkpoint
  wal_sync_mode: "normal"

  # Maximum durability: fsync after every commit
  # Use for financial/transactional workloads
  wal_sync_mode: "always"
```

| Mode | Durability | Performance |
|------|-----------|-------------|
| `normal` | Checkpoint-level | Higher throughput |
| `always` | Commit-level | Lower throughput |

---

## Monitoring

### Key Metrics

| Metric | Description | Alert Threshold |
|--------|-------------|-----------------|
| `vaultdb_wal_entries_total` | WAL entry count | — |
| `vaultdb_buffer_pool_hit_ratio` | Buffer pool efficiency | < 0.90 |
| `vaultdb_query_duration_seconds` | Query latency | p99 > 1s |
| `vaultdb_active_connections` | Connection count | > 80% of `max_connections` |
| `vaultdb_connections_active` | Active connections | > 80% of `max_connections` |
| `vaultdb_storage_pages_read_total` | Disk reads | — |
| `vaultdb_storage_pages_written_total` | Disk writes | — |
| `go_memstats_heap_inuse_bytes` | Go heap in use | > 80% of GOMEMLIMIT |
| `go_gc_duration_seconds` | GC pause duration | p99 > 10ms |

### Health Endpoint

```bash
curl http://localhost:5433/health
```

### Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: vaultdb
    static_configs:
      - targets: ['vaultdb:5433']
    scrape_interval: 10s
```

---

## Kubernetes Deployment

### StatefulSet (Recommended for Production)

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: vaultdb
spec:
  serviceName: vaultdb
  replicas: 1
  selector:
    matchLabels:
      app: vaultdb
  template:
    metadata:
      labels:
        app: vaultdb
    spec:
      containers:
      - name: vaultdb
        image: vaultdb/vaultdb:latest
        ports:
        - containerPort: 5432
          name: tcp
        - containerPort: 8080
          name: http
        - containerPort: 5433
          name: metrics
        env:
        - name: GOGC
          value: "75"
        - name: GOMEMLIMIT
          value: "12GiB"
        - name: VAULTDB_AUTH_SECRET
          valueFrom:
            secretKeyRef:
              name: vaultdb-secrets
              key: auth-secret
        resources:
          requests:
            memory: "16Gi"
            cpu: "4"
          limits:
            memory: "20Gi"  # 125% of GOMEMLIMIT for headroom
            cpu: "8"
        volumeMounts:
        - name: data
          mountPath: /data
        readinessProbe:
          httpGet:
            path: /health
            port: 5433
          initialDelaySeconds: 5
          periodSeconds: 10
        livenessProbe:
          httpGet:
            path: /health
            port: 5433
          initialDelaySeconds: 15
          periodSeconds: 20
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      storageClassName: fast-ssd
      resources:
        requests:
          storage: 100Gi
```

### Resource Limits Guide

| Deployment Size | CPU Request | CPU Limit | Memory Request | Memory Limit |
|----------------|-------------|-----------|----------------|--------------|
| Small | 2 | 4 | 4Gi | 5Gi |
| Medium | 4 | 8 | 16Gi | 20Gi |
| Large | 8 | 16 | 32Gi | 40Gi |

**Rule:** Set memory limit = GOMEMLIMIT / 0.8 (leaves 20% headroom).

---

## Bare Metal

### Systemd Service

```ini
[Unit]
Description=VaultDB
After=network.target

[Service]
Type=simple
User=vaultdb
Group=vaultdb
Environment=GOGC=75
Environment=GOMEMLIMIT=24G
ExecStart=/usr/local/bin/vaultdb --config /etc/vaultdb/vaultdb.yaml
Restart=on-failure
LimitNOFILE=65536
LimitNPROC=4096

[Install]
WantedBy=multi-user.target
```

### Kernel Tuning

```bash
# Increase file descriptor limit
echo "vaultdb soft nofile 65536" >> /etc/security/limits.conf
echo "vaultdb hard nofile 65536" >> /etc/security/limits.conf

# Increase memory map count for large buffer pools
sysctl -w vm.max_map_count=262144
```

---

## Security Checklist

- [ ] TLS enabled for all connections (see [deployment.md](deployment.md#tls-setup))
- [ ] `tls.enforce: true` — reject non-TLS connections in production
- [ ] `tls.min_version: "1.3"` for maximum security (requires compatible clients)
- [ ] mTLS enabled for client authentication
- [ ] RBAC configured: assign tokens with explicit roles (`admin`, `writer`, `reader`)
- [ ] API tokens rotated regularly (minimum every 90 days)
- [ ] Audit logging enabled (`vaultdb_audit_log`)
- [ ] Network policies restrict access (Kubernetes NetworkPolicy or iptables)
- [ ] Encryption at rest (LUKS, FileVault, or cloud KMS)
- [ ] Run as non-root user
- [ ] `VAULTDB_AUTH_SECRET` set with cryptographically random value
- [ ] Monitoring alerts configured for auth failures

### RBAC Deployment Notes

Assign tokens with the least-privilege role required:

```bash
# Production: separate admin, application, and read-only tokens
export VAULTDB_API_TOKENS="admin-token:ops:admin,app-token:myapp:writer,reader-token:monitoring:reader"
```

| Role | Use Case |
|------|----------|
| `admin` | Operations team, schema migrations, key rotation |
| `writer` | Application servers (INSERT/UPDATE/DELETE) |
| `reader` | Monitoring, reporting, dashboards |

> **Note:** Role definitions are currently hardcoded (`admin`, `writer`, `reader`). Roles can be managed via SQL (CREATE ROLE, DROP ROLE, GRANT, REVOKE).

### TLS Enforcement Notes

For production, enable TLS enforcement to prevent accidental plaintext connections:

```yaml
tls:
  enabled: true
  cert_file: /etc/vaultdb/tls/server.crt
  key_file: /etc/vaultdb/tls/server.key
  min_version: "1.3"
  enforce: true
```

```bash
# Environment variable equivalent
export VAULTDB_TLS_ENABLED=true
export VAULTDB_TLS_CERT_FILE=/etc/vaultdb/tls/server.crt
export VAULTDB_TLS_KEY_FILE=/etc/vaultdb/tls/server.key
```

> **Warning:** Clients must support TLS 1.3 when `min_version` is set to `"1.3"`. Test client compatibility before enforcing.

---

## Backup and Recovery

### Automated Backup

```bash
# Using vaultdb-backup tool
vaultdb-backup --data /data --output /backup/vaultdb-$(date +%Y%m%d).tar.gz

# Cron job (daily at 2 AM)
0 2 * * * /usr/local/bin/vaultdb-backup --data /data --output /backup/vaultdb-$(date +\%Y\%m\%d).tar.gz
```

### Kubernetes Backup with Velero

```bash
velero backup create vaultdb-backup \
  --selector app=vaultdb \
  --include-namespaces vaultdb
```

### Recovery

```bash
# Stop server, restore data, restart
docker compose down
tar -xzf /backup/vaultdb-20260627.tar.gz -C /data
docker compose up -d
```

VaultDB uses WAL with ARIES protocol for crash recovery. On startup, it automatically replays the WAL to restore consistency.

---

## Performance Tuning Checklist

1. Set GOGC based on deployment size (see Resource Sizing table)
2. Set GOMEMLIMIT to 80% of available container RAM
3. Size buffer pool for working set (see Buffer Pool Configuration)
4. Use NVMe SSDs for large deployments
5. Monitor `vaultdb_buffer_pool_hit_ratio` — target > 0.95
6. Keep `max_connections` appropriate for CPU cores (rule of thumb: 2x cores)
7. Use `wal_sync_mode: "always"` only for critical transactional workloads
