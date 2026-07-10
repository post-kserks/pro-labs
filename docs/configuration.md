# Configuration

VaultDB can be configured via a YAML file, command-line flags, or environment variables. CLI flags take precedence over the config file, which takes precedence over environment variables.

## Configuration File

Default path: `vaultdb.yaml` (override with `--config` flag).

```yaml
server:
  host: "127.0.0.1"
  port: 5432
  http_port: 8080
  monitor_port: 5433
  max_request_size_bytes: 67108864
  max_rows: 1000000
  query_timeout_sec: 30
  max_connections: 1000
  shutdown_timeout_sec: 30
  tcp_keepalive_sec: 30
  tcp_idle_timeout_sec: 300
  max_prepared_statements: 1000
  rate_limit_rps: 100
  rate_limit_burst: 200
  allowed_origins: []
  live_queries:
    buffer_size: 256
    drop_policy: "drop"
    block_timeout_s: 5

storage:
  engine: "page"
  data_dir: "./data"
  result_cache_size: 256
  result_cache_ttl_seconds: 30
  buffer_pool_pages: 16384  # 128 MB (default)
  # For large deployments:
  # buffer_pool_pages: 65536  # 512 MB

tls:
  enabled: false
  cert_file: ""
  key_file: ""
  min_version: "1.2"             # "1.2" or "1.3"
  enforce: false                 # reject non-TLS connections when true

auth:
  enabled: true
  mtls_enabled: false
  mtls_ca_file: ""
  rate_window_seconds: 60
  max_fails: 10
  block_for_seconds: 300

encryption:
  enabled: false
  key_source: "passphrase"       # passphrase | os_keychain | kms
  default_scope: "all"           # all | tables_only | off
  encrypt_catalog: false
  encrypt_wal: true

# WASM UDF functions are configured via SQL, not YAML.
# Memory limits and timeouts are per-function.

ai:
  provider: ""
  endpoint: ""
  model: ""
  api_key: ""
  cache_enabled: false
  cache_size: 0
```

## Server Options

### `server.host`

- **Type**: string
- **Default**: `"127.0.0.1"`
- **Description**: Network interface to bind to. Use `"0.0.0.0"` for all interfaces.

### `server.port`

- **Type**: integer
- **Default**: `5432`
- **Range**: 1–65535
- **Description**: TCP port for the native SQL protocol (C++ and Go clients).

### `server.http_port`

- **Type**: integer
- **Default**: `8080`
- **Range**: 1–65535
- **Description**: HTTP API port for REST endpoints, SSE streaming, and the web dashboard.

### `server.monitor_port`

- **Type**: integer
- **Default**: `5433`
- **Range**: 1–65535
- **Description**: Health check and Prometheus metrics port. Must differ from `http_port`.

### `server.max_request_size_bytes`

- **Type**: integer
- **Default**: `67108864` (64 MB)
- **Description**: Maximum size of a single HTTP request body or TCP message.

### `server.max_rows`

- **Type**: integer
- **Default**: `1000000`
- **Description**: Maximum number of rows returned by a single SELECT query.

### `server.query_timeout_sec`

- **Type**: integer
- **Default**: `30`
- **Description**: Per-query timeout in seconds. Set to 0 for no timeout.

### `server.max_connections`

- **Type**: integer
- **Default**: `1000`
- **Description**: Maximum number of concurrent TCP connections. Excess connections are rejected.

### `server.shutdown_timeout_sec`

- **Type**: integer
- **Default**: `30`
- **Description**: Seconds to wait for active connections during graceful shutdown before forcing close.

### `server.tcp_keepalive_sec`

- **Type**: integer
- **Default**: `30`
- **Description**: TCP keepalive interval in seconds.

### `server.tcp_idle_timeout_sec`

- **Type**: integer
- **Default**: `300`
- **Description**: Connection idle timeout in seconds. Connections with no activity are closed.

### `server.max_prepared_statements`

- **Type**: integer
- **Default**: `1000`
- **Description**: Maximum prepared statements per session. Excess statements cause an error.

### `server.rate_limit_rps`

- **Type**: integer
- **Default**: `100`
- **Description**: Token bucket refill rate (tokens per second) for rate limiting.

### `server.rate_limit_burst`

- **Type**: integer
- **Default**: `200`
- **Description**: Token bucket burst capacity. Maximum requests allowed in a single burst.

### `server.allowed_origins`

- **Type**: array of strings
- **Default**: `[]` (empty = no CORS)
- **Description**: List of allowed CORS origins. Add `"*"` to allow all origins.

### `server.live_queries.buffer_size`

- **Type**: integer
- **Default**: `256`
- **Description**: Per-subscription channel buffer for live query SSE streams.

### `server.live_queries.drop_policy`

- **Type**: string
- **Default**: `"drop"`
- **Options**: `"drop"`, `"block"`, `"evict"`
- **Description**: How to handle slow subscribers. `drop` silently drops events; `block` blocks until space available; `evict` disconnects the subscriber.

### `server.live_queries.block_timeout_s`

- **Type**: integer
- **Default**: `5`
- **Description**: Seconds to block before disconnecting a slow subscriber (when `drop_policy` is `"block"`).

## Storage Options

### `storage.engine`

- **Type**: string
- **Default**: `"page"`
- **Options**: `"page"`, `"json"`
- **Description**: Storage engine type. `"page"` is the production engine with WAL and MVCC.

### `storage.data_dir`

- **Type**: string
- **Default**: (none — required)
- **Description**: Path to the data directory. All database files are stored here.

### `storage.result_cache_size`

- **Type**: integer
- **Default**: `256`
- **Description**: Number of entries in the query result LRU cache.

### `storage.result_cache_ttl_seconds`

- **Type**: integer
- **Default**: `30`
- **Description**: Time-to-live for cached query results in seconds.

### `storage.buffer_pool_pages`

- **Type**: integer
- **Default**: `16384` (128 MB)
- **Description**: Number of 8KB pages in the buffer pool. Increase for large datasets (e.g., `65536` for 512 MB).

## TLS Options

### `tls.enabled`

- **Type**: boolean
- **Default**: `false`
- **Description**: Enable TLS encryption for TCP and HTTP connections. Requires `cert_file` and `key_file`.

### `tls.cert_file`

- **Type**: string
- **Default**: `""`
- **Description**: Path to the TLS certificate file (PEM format).

### `tls.key_file`

- **Type**: string
- **Default**: `""`
- **Description**: Path to the TLS private key file (PEM format).

### `tls.min_version`

- **Type**: string
- **Default**: `"1.2"`
- **Options**: `"1.2"`, `"1.3"`
- **Description**: Minimum TLS protocol version. `"1.2"` allows TLS 1.2+; `"1.3"` requires TLS 1.3 only. Cipher suites are restricted to ECDHE+AES-GCM regardless of version.

### `tls.enforce`

- **Type**: boolean
- **Default**: `false`
- **Description**: When `true`, the server rejects all non-TLS connections. Produces an error at startup if TLS is not configured (`tls.enabled: false`).

## Authentication Options

### `auth.enabled`

- **Type**: boolean
- **Default**: `true`
- **Description**: Enable token-based authentication. When disabled, all requests are allowed.

### `auth.mtls_enabled`

- **Type**: boolean
- **Default**: `false`
- **Description**: Enable mutual TLS (client certificate verification).

### `auth.mtls_ca_file`

- **Type**: string
- **Default**: `""`
- **Description**: Path to the CA certificate file for mTLS client verification.

### `auth.rate_window_seconds`

- **Type**: integer
- **Default**: `60`
- **Description**: Time window for counting failed authentication attempts.

### `auth.max_fails`

- **Type**: integer
- **Default**: `10`
- **Description**: Number of failed attempts before an IP is temporarily blocked.

### `auth.block_for_seconds`

- **Type**: integer
- **Default**: `300`
- **Description**: Duration of IP block after exceeding `max_fails`.

## Encryption Options

### `encryption.enabled`

- **Type**: boolean
- **Default**: `false`
- **Description**: Enable Transparent Data Encryption (TDE). When enabled, all data pages and WAL are encrypted with AES-256-GCM.

### `encryption.key_source`

- **Type**: string
- **Default**: `"passphrase"`
- **Options**: `"passphrase"`, `"os_keychain"`, `"kms"`
- **Description**: Source of the Key Encryption Key (KEK). `passphrase` derives KEK from password via Argon2id. `os_keychain` uses system keychain (macOS/Linux/Windows). `kms` uses external KMS (AWS/Vault/Azure).

### `encryption.default_scope`

- **Type**: string
- **Default**: `"all"`
- **Options**: `"all"`, `"tables_only"`, `"off"`
- **Description**: Default encryption scope for new databases. `all` encrypts everything, `tables_only` encrypts only table data, `off` disables encryption.

### `encryption.encrypt_catalog`

- **Type**: boolean
- **Default**: `false`
- **Description**: Whether to encrypt table/column names in the catalog. When false, schema is readable without the key (useful for recovery).

### `encryption.encrypt_wal`

- **Type**: boolean
- **Default**: `true`
- **Description**: Whether to encrypt WAL records. Should be `true` when `encryption.enabled` is `true`.

## RBAC Roles

VaultDB includes built-in role-based access control. Tokens are assigned roles at registration time.

| Role | Permissions |
|------|-------------|
| `admin` | All operations (`*`) |
| `writer` | SELECT, INSERT, UPDATE, DELETE, CREATE/DROP TABLE, CREATE/DROP INDEX, COPY FROM/TO, CREATE/DROP VIEW, CREATE/DROP TRIGGER, ALTER TABLE, TRUNCATE, MERGE |
| `reader` | SELECT, EXPLAIN |

Roles are assigned when tokens are registered:

```bash
# Token format: token:label:role (role defaults to "admin" if omitted)
export VAULTDB_API_TOKENS="token1:label1:admin,token2:label2:writer,token3:label3:reader"
```

> **Note:** Roles can be managed via SQL (CREATE ROLE, DROP ROLE, GRANT, REVOKE) and stored in system.roles table. Built-in roles (admin, writer, reader) are available by default.

## AI Options

### `ai.provider`

- **Type**: string
- **Default**: `""`
- **Options**: `"openai"`, `"ollama"`, `""` (disabled)
- **Description**: Embedding provider. Set to `""` to disable AI features.

### `ai.endpoint`

- **Type**: string
- **Default**: `""`
- **Description**: Embedding API URL (e.g., `https://api.openai.com/v1`).

### `ai.model`

- **Type**: string
- **Default**: `""`
- **Description**: Model name for embeddings (e.g., `text-embedding-3-small`).

### `ai.api_key`

- **Type**: string
- **Default**: `""`
- **Description**: API key for the embedding provider. Can also be set via `VAULTDB_AI_API_KEY`.

### `ai.cache_enabled`

- **Type**: boolean
- **Default**: `false`
- **Description**: Enable LRU cache for embedding results.

### `ai.cache_size`

- **Type**: integer
- **Default**: `1000`
- **Description**: Maximum number of cached embedding entries.

## Command-Line Flags

| Flag | Default | Config Equivalent | Description |
|------|---------|-------------------|-------------|
| `-host` | `127.0.0.1` | `server.host` | Bind address |
| `-port` | `5432` | `server.port` | TCP port |
| `-http-port` | `8080` | `server.http_port` | HTTP API port |
| `-monitor-port` | `5433` | `server.monitor_port` | Monitor port |
| `-data` | `./data` | `storage.data_dir` | Data directory |
| `-config` | (none) | — | Path to config file |
| `-health-check` | `false` | — | Run health check and exit |
| `-tls-cert` | (none) | — | TLS certificate file |
| `-tls-key` | (none) | — | TLS private key file |
| `-tls-ca` | (none) | `auth.mtls_ca_file` | CA file for mTLS |
| `-tls-enforce` | `false` | `tls.enforce` | Reject non-TLS connections |

## Environment Variables

| Variable | Config Path | Description |
|----------|-------------|-------------|
| `VAULTDB_HOST` | `server.host` | Bind address |
| `VAULTDB_PORT` | `server.port` | TCP port |
| `VAULTDB_HTTP_PORT` | `server.http_port` | HTTP port |
| `VAULTDB_MONITOR_PORT` | `server.monitor_port` | Monitor port |
| `VAULTDB_DATA_DIR` | `storage.data_dir` | Data directory |
| `VAULTDB_LOG_LEVEL` | — | Set to `debug` for verbose logging |
| `VAULTDB_AUTH_SECRET` | (required) | HMAC signing key for tokens |
| `VAULTDB_AUTH_ENABLED` | `auth.enabled` | Enable/disable authentication |
| `VAULTDB_API_TOKENS` | — | Comma-separated list of valid tokens |
| `VAULTDB_MTLS_ENABLED` | `auth.mtls_enabled` | Enable mTLS |
| `VAULTDB_MTLS_CA_FILE` | `auth.mtls_ca_file` | CA file for mTLS |
| `VAULTDB_AI_API_KEY` | `ai.api_key` | AI embedding API key |
| `VAULTDB_ENCRYPTION_PASSPHRASE` | `encryption.key_source` | Passphrase for TDE encryption (when using `passphrase` key source) |

## Hard Limits (Not Configurable)

| Limit | Value | Description |
|-------|-------|-------------|
| COPY row limit | 1,000,000 | Maximum rows per `COPY FROM` import |
| Parser depth | 32 | Maximum nested subquery/EXISTS depth |
| Segment size | 65,536 pages | Pages per heap file segment (512 MB) |
| Object name length | 128 chars | Max length for database/table names |

## Validation Rules

- All three ports must be in range 1–65535.
- Ports must be distinct from each other.
- `query_timeout_sec` must not be negative.
- `drop_policy` must be one of: `drop`, `block`, `evict`.
- `engine` must be `page` or `json`.
- `storage.data_dir` must not be empty.
- When `auth.enabled` is `true`, `VAULTDB_AUTH_SECRET` must be set.
- When `tls.enforce` is `true`, `tls.enabled` must also be `true`.
- `tls.min_version` must be `"1.2"` or `"1.3"` (or empty).
