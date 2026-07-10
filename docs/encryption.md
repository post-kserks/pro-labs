# Encryption

VaultDB provides Transparent Data Encryption (TDE) with AES-256-GCM, envelope encryption, and OS-level disk encryption detection.

## Quick Start

### For a New Database

```bash
# 1. Set passphrase
export VAULTDB_ENCRYPTION_PASSPHRASE="your-secure-passphrase"

# 2. Start server with encryption enabled
vaultdb-server --config vaultdb.yaml

# 3. Create encrypted database via SQL
psql -c "CREATE DATABASE secure_db ENCRYPTED WITH KEY 'passphrase';"
```

### For an Existing Database

```bash
# 1. Set passphrase
export VAULTDB_ENCRYPTION_PASSPHRASE="your-secure-passphrase"

# 2. Initialize encryption
vaultdb-encrypt init --database /path/to/data/existing_db

# 3. Restart server with encryption enabled
vaultdb-server --config vaultdb.yaml

# 4. Verify encryption status
vaultdb-encrypt status --database /path/to/data/existing_db
```

### Configuration (vaultdb.yaml)

```yaml
encryption:
  enabled: true
  key_source: "passphrase"
  default_scope: "all"
  encrypt_wal: true
```

## SQL Syntax

### Creating an Encrypted Database

```sql
CREATE DATABASE secure_db ENCRYPTED WITH KEY 'passphrase-or-keyid';
```

### Table-Level Encryption

```sql
CREATE TABLE users (
    id    INT,
    name  VARCHAR(64),
    ssn   VARCHAR(11) ENCRYPTED,
    email VARCHAR(100)
) ENCRYPTED;
```

### Field-Level Encryption

```sql
ALTER TABLE users ALTER COLUMN ssn SET ENCRYPTED;
```

### Checking Encryption Status

```sql
SHOW ENCRYPTION STATUS;
```

Output:
```
+----------+------------+---------------+------------+
| database | encrypted  | algorithm     | key_source |
+----------+------------+---------------+------------+
| mydb     | yes        | AES-256-GCM   | passphrase |
+----------+------------+---------------+------------+
```

## Configuration

```yaml
# vaultdb.yaml
encryption:
  enabled: true
  key_source: "passphrase"       # passphrase | os_keychain | kms
  default_scope: "all"           # all | tables_only | off
  encrypt_catalog: false
  encrypt_wal: true

  # KMS configuration (when key_source: "kms")
  kms:
    provider: "aws-kms"          # aws-kms | hashicorp-vault | azure-keyvault
    key_id: "arn:aws:kms:..."    # provider-specific key identifier
    region: "us-east-1"          # for AWS KMS
    # endpoint: "..."            # for HashiCorp Vault
    # token: "..."               # for HashiCorp Vault
```

### Encryption Scope

| Scope | What Gets Encrypted | Use Case |
|-------|---------------------|----------|
| `all` | Table pages, WAL, TOAST | Maximum protection (default) |
| `tables_only` | Table pages only | Debugging, compliance (encrypt PII only) |
| `off` | Nothing | Disable encryption |

**Note:** With `tables_only`, an attacker with disk access can read table schemas (catalog) and transaction logs (WAL). Only actual row data is protected.

### Key Sources

| Source | Description |
|--------|-------------|
| `passphrase` | Key derived via Argon2id (64MB memory, 3 iterations). Passphrase provided via `VAULTDB_ENCRYPTION_PASSPHRASE` env var |
| `os_keychain` | Stored in system keychain (macOS Keychain, Linux libsecret, Windows DPAPI) |
| `kms` | External KMS (AWS KMS, HashiCorp Vault, Azure Key Vault) |
| `file_kms` | File-based KMS for testing and development. Stores encrypted DEK in a local file |

> **All features are fully implemented:**
> - `vaultdb-encrypt migrate` — online re-encryption without downtime
> - `vaultdb-encrypt rotate-kek` — instant KEK rotation (seconds)
> - `vaultdb-encrypt rotate-dek` — online DEK rotation with multi-version support
> - `os_keychain` — macOS Keychain, Linux libsecret, Windows DPAPI
> - `kms` — AWS KMS, HashiCorp Vault, Azure Key Vault
> - `file_kms` — File-based KMS for testing and development

### Key Management

VaultDB uses envelope encryption:

- **KEK (Key Encryption Key)**: Stored outside the database (OS keychain, KMS, or derived from passphrase)
- **DEK (Data Encryption Key)**: Stored inside the database in encrypted form (`{db}/.dek.enc`)

KEK rotation is instant (< 1 second) — only the small `.dek.enc` file is re-encrypted. DEK rotation is performed online without downtime.

### FileKMS Encryption

FileKMS is a file-based Key Management Service designed for testing and development environments. It stores the encrypted DEK in a local file rather than using external KMS providers.

**Use Cases:**
- Development and testing environments
- Local development without cloud dependencies
- Integration testing with encryption enabled

**Configuration:**

```yaml
encryption:
  enabled: true
  key_source: "file_kms"
  kms:
    provider: "file"
    key_id: "/path/to/kms.enc"
```

**Security Properties:**
- DEK is encrypted with a randomly generated KEK
- KEK is stored in the same file as the encrypted DEK
- File is encrypted using AES-256-GCM
- Suitable for development but NOT for production use

**Limitations:**
- Not suitable for production (no key recovery mechanism)
- KEK is stored alongside encrypted data
- No external key management or audit trail

## Encryption Levels

| Object | Encrypted | Notes |
|--------|-----------|-------|
| Data pages (heap) | Yes | |
| Indexes (B-Tree) | Yes | Index values reveal data |
| WAL | Yes | Contains same data as heap before apply |
| TOAST (large values) | Yes | |
| Catalog (table/column names) | Optional | Default: not encrypted. Set `encrypt_catalog: true` for maximum protection |
| Metrics / logs | No | Should not contain sensitive data by design |

## Encrypted Page Format

Each encrypted page on disk:

```
[magic: "VDBE" (4 bytes)] [key_version: uint32] [nonce: 12 bytes] [ciphertext + GCM tag]
```

- Total page size remains 8192 bytes
- Useful payload reduced by 36 bytes (20-byte header + 16-byte GCM tag)

## Security Properties

- AES-256-GCM provides authenticated encryption (confidentiality + integrity)
- Unique nonce per page prevents replay attacks
- PageID bound as AAD prevents page swap attacks
- WAL is encrypted to prevent data leakage through journal

## Performance

| Operation | Without Encryption | With AES-256-GCM (AES-NI) | Overhead |
|-----------|-------------------|---------------------------|----------|
| INSERT (10K rows) | 0.18s | 0.21s | ~17% |
| SELECT * (full scan) | 0.09s | 0.10s | ~11% |
| SELECT WHERE (index) | 0.01ms | 0.012ms | ~20% |

With AES-NI: ~17% overhead. Without AES-NI: 300-500% overhead (warning logged at startup).

## CLI Tools

```bash
# Initialize encryption for existing database
vaultdb-encrypt init --database mydb --key-source passphrase

# Check encryption status
vaultdb-encrypt status --database mydb

# Migrate existing unencrypted database (online, no downtime)
vaultdb-encrypt migrate --database mydb --key-source os_keychain

# Rotate KEK (instant, no data rewrite)
vaultdb-encrypt rotate-kek --database mydb

# Rotate DEK (online, multi-version)
vaultdb-encrypt rotate-dek --database mydb
```

## OS-Level Disk Encryption Detection

VaultDB can detect and enforce OS-level disk encryption (LUKS, FileVault, BitLocker):

```yaml
storage:
  require_encrypted_disk: false
  encrypted_disk_check: "warn"   # off | warn | enforce
```

When `encrypted_disk_check: enforce`, the server refuses to start if `data_dir` is not on an encrypted volume.

**Note:** OS disk encryption and TDE are complementary layers, not alternatives. TDE protects data even if disk access is obtained (e.g., backup theft, root access). OS encryption protects the physical disk.

## Disaster Recovery

### Passphrase Loss

**If the passphrase is lost and no backup exists, all encrypted data is permanently unrecoverable.** The DEK is encrypted with the KEK derived from the passphrase, and there is no backdoor.

### Best Practices

1. **Backup your passphrase** in a secure location (password manager, physical safe)
2. **Use KMS for production** — cloud KMS providers offer key recovery mechanisms
3. **Test recovery** before deploying to production
4. **Consider `encrypt_catalog: false`** — allows schema inspection without the key (but data remains encrypted)

### Recovery Options

| Scenario | Recovery |
|----------|----------|
| Passphrase lost, KMS available | Use KMS to decrypt DEK |
| Passphrase lost, OS keychain available | Use keychain to retrieve KEK |
| Passphrase lost, no backups | **Data is lost** |
| Server crash during migration | Re-run migration (idempotent for key setup) |

## Migration Atomicity

The `vaultdb-encrypt migrate` command performs two file writes:
1. `.dek.enc` — encrypted DEK
2. `.encryption_meta.json` — metadata

If the process crashes between these writes:
- `.dek.enc` exists but no metadata → metadata will be recreated on next `status` check
- Partial `.dek.enc` → re-run `migrate` to regenerate

The actual page encryption happens when the server starts with `encryption.enabled: true`. Migration only sets up the keys.
