# Security

VaultDB provides multiple layers of security: token authentication, TLS encryption, mutual TLS, rate limiting, brute-force protection, and row-level security.

## Authentication

### Token-Based Authentication

VaultDB uses HMAC-SHA256 token hashing. Raw tokens are never stored — only their HMAC digests appear in memory.

**Token format**: `vdb_sk_` prefix + 48 hex characters (24 random bytes)

**Example token**: `vdb_sk_a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4`

### Token Transmission

Two methods:

```
Authorization: Bearer vdb_sk_a1b2c3d4...
```

or

```
X-VaultDB-Token: vdb_sk_a1b2c3d4...
```

### Token Registration

Tokens are provided via:

1. **Environment variable**: `VAULTDB_API_TOKENS=token1,token2,token3`
2. **Auto-generation**: When no tokens are provided, VaultDB generates one and writes it to `{data_dir}/.generated-token`
3. **Secret key**: `VAULTDB_AUTH_SECRET` is required for HMAC signing

### Configuration

```yaml
auth:
  enabled: true           # Enable/disable authentication
  rate_window_seconds: 60 # Window for failed-login tracking
  max_fails: 10           # Failures before IP block
  block_for_seconds: 300  # Block duration (5 minutes)
```

### Bypass Rules

- When `auth.enabled: false`, all requests pass through
- **Monitor port (5433)**: `/health` and `/ready` endpoints do not require authentication
- **HTTP API (8080)**: All endpoints require authentication, including requests from localhost
- **TCP protocol (5432)**: Authentication is required for all connections

> **Note:** For local development, set `auth.enabled: false` or use the auto-generated token from `{data_dir}/.generated-token`.

## TLS Encryption

### Standard TLS

Encrypts client-server communication:

```bash
vaultdb-server \
  --tls-cert server.crt \
  --tls-key server.key
```

**Enforced settings**:
- Minimum TLS 1.2
- Curve preferences: X25519, P-256
- Cipher suites: ECDHE+AES-GCM only

### Mutual TLS (mTLS)

Adds client certificate verification:

```bash
vaultdb-server \
  --tls-cert server.crt \
  --tls-key server.key \
  --tls-ca ca.crt
```

**Settings**:
- `ClientAuth: tls.RequireAndVerifyClientCert`
- Client certificates verified against the CA
- Same cipher suite and version constraints as standard TLS

### Generating Certificates

```go
// Self-signed certificate
certPEM, keyPEM := tls.GenerateSelfSignedCert("localhost")
tls.SaveCertToFile(certPEM, keyPEM, "server.crt", "server.key")
```

## Brute-Force Protection

Per-IP sliding-window rate limiter for failed authentication attempts:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `rate_window_seconds` | 60 | Time window for counting failures |
| `max_fails` | 10 | Failures before blocking |
| `block_for_seconds` | 300 | Block duration (5 minutes) |

**Behavior**:
- Failed auth attempts are tracked per IP
- After `max_fails` within `rate_window_seconds`, the IP is blocked for `block_for_seconds`
- Periodic sweeps remove stale entries
- Blocked IPs receive HTTP 429

## Rate Limiting

Token-bucket rate limiter per client IP:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `rate_limit_rps` | 100 | Tokens per second (refill rate) |
| `rate_limit_burst` | 200 | Maximum burst capacity |

**Behavior**:
- Maximum 100,000 tracked IP keys
- LRU eviction when limit exceeded
- Background cleanup of idle buckets (> 5 minutes)
- HTTP 429 with JSON body when bucket is empty

## Row-Level Security (RLS)

Restricts which rows a user can see or modify.

### Enabling RLS

```sql
ENABLE RLS ON users;
```

### Creating Policies

```sql
CREATE POLICY user_isolation ON users
  TO user_role
  USING (user_id = current_user_id());
```

### How RLS Works

When RLS is enabled on a table:
- **SELECT**: Only rows matching the policy expression are returned
- **INSERT**: Only rows matching the policy expression can be inserted
- **UPDATE**: Only rows matching the policy expression can be updated
- **DELETE**: Only rows matching the policy expression can be deleted

### Policy Storage

Policies are stored in the table's `_schema.json`:

```json
{
  "rls_enabled": true,
  "rls_policies": [
    {
      "name": "user_isolation",
      "to": "user_role",
      "using": "user_id = current_user_id()"
    }
  ]
}
```

## Transparent Data Encryption (TDE)

VaultDB supports page-level encryption using AES-256-GCM.

### Enabling TDE

```yaml
# vaultdb.yaml
encryption:
  enabled: true
  key_source: "passphrase"
```

### Key Management

- **Envelope Encryption**: KEK (Key Encryption Key) encrypts DEK (Data Encryption Key)
- **Passphrase**: Key derived via Argon2id (64MB memory, 3 iterations)
- **OS Keychain**: macOS Keychain, Linux libsecret, Windows DPAPI
- **KMS**: AWS KMS, HashiCorp Vault, Azure Key Vault
- **Key Rotation**: KEK rotation is instant (<1 second), DEK rotation is online

### Security Properties

- Pages are encrypted with AES-256-GCM (authenticated encryption)
- Each page has unique nonce preventing replay attacks
- PageID is bound as AAD preventing page swap attacks
- WAL is also encrypted to prevent data leakage through journal

### Performance

- With AES-NI: ~17% overhead on INSERT/SELECT
- Without AES-NI: 300-500% overhead (warning logged)

## Network Security

### Port Separation

| Port | Purpose | Auth Required |
|------|---------|---------------|
| 5432 | TCP SQL protocol | Yes (token in request) |
| 8080 | HTTP API | Yes (Bearer token) |
| 5433 | Monitor/Health | Optional |

### CORS

Configurable allowed origins:

```yaml
server:
  allowed_origins:
    - "https://myapp.example.com"
    - "http://localhost:3000"
```

### Security Headers

HTTP responses include:
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Content-Security-Policy: default-src 'self'`

## Error Message Sanitization

TCP protocol error messages are sanitized before transmission:
- Only messages containing known safe patterns pass through
- Unknown errors become `"internal error"`
- Messages truncated at 200 characters
