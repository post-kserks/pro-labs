# Encryption

VaultDB provides Transparent Data Encryption (TDE) with AES-256-GCM, envelope encryption, and OS-level disk encryption detection.

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

## Configuration

```yaml
# vaultdb.yaml
encryption:
  enabled: true
  key_source: "passphrase"       # passphrase | os_keychain | kms
  default_scope: "all"           # all | tables_only | off
  encrypt_catalog: false
  encrypt_wal: true
```

### Key Sources

| Source | Description |
|--------|-------------|
| `passphrase` | Key derived via Argon2id (64MB memory, 3 iterations). Passphrase provided via `VAULTDB_ENCRYPTION_PASSPHRASE` env var |
| `os_keychain` | Stored in system keychain (macOS Keychain, Linux libsecret, Windows DPAPI) |
| `kms` | External KMS (AWS KMS, HashiCorp Vault, Azure Key Vault) |

### Key Management

VaultDB uses envelope encryption:

- **KEK (Key Encryption Key)**: Stored outside the database (OS keychain, KMS, or derived from passphrase)
- **DEK (Data Encryption Key)**: Stored inside the database in encrypted form (`{db}/.dek.enc`)

KEK rotation is instant (< 1 second) — only the small `.dek.enc` file is re-encrypted. DEK rotation is performed online without downtime.

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
