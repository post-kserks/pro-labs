# Backup and Restore

VaultDB provides backup and restore capabilities using compressed tar archives.

## Backup Format

Backups are gzipped tar archives containing the complete `pagedb/` and `wal/` directories:

```
backup.tar.gz
├── pagedb/
│   ├── _catalog.json
│   └── <database>/
│       └── <table>/
│           ├── _schema.json
│           ├── _indexes.json
│           ├── seg_0000.heap
│           ├── seg_0000.fsm
│           └── idx_*.json
└── wal/
    └── vaultdb.wal
```

## Creating a Backup

### Using the CLI Tool

```bash
vaultdb-backup -mode backup -data ./data -output backup.tar.gz
```

### Using the Go API

```go
import "vaultdb/internal/backup"

err := backup.Backup("./data", "backup.tar.gz")
if err != nil {
    log.Fatal(err)
}
```

### What Gets Backed Up

- All database catalog data
- All table schemas and constraints
- All table data (heap files)
- All indexes (B-tree, Hash, GIN, GiST, Composite)
- Free Space Maps
- WAL (for crash recovery)

### What Does NOT Get Backed Up

- In-flight transactions (not yet committed)
- Temporary files
- Auto-generated tokens (`.generated-token`)

## Restoring from Backup

### Using the CLI Tool

```bash
vaultdb-backup -mode restore -data ./data -output backup.tar.gz
```

### Using the Go API

```go
import "vaultdb/internal/backup"

err := backup.Restore("backup.tar.gz", "./data")
if err != nil {
    log.Fatal(err)
}
```

### Restore Process

1. Extract the gzipped tar archive
2. Create directories as needed
3. Write files with original permissions from tar headers
4. Existing data in the data directory is overwritten

### Important Notes

- **Stop the server** before restoring to prevent data corruption
- The restore overwrites ALL existing data in the data directory
- File permissions from the tar archive are preserved

## Backup CLI Reference

```
vaultdb-backup [flags]

Flags:
  -mode string     backup or restore (required)
  -data string     data directory path (required)
  -output string   archive file path (required)
```

### Examples

```bash
# Full backup
vaultdb-backup -mode backup -data /var/lib/vaultdb -output /backups/vaultdb-$(date +%Y%m%d).tar.gz

# Restore
vaultdb-backup -mode restore -data /var/lib/vaultdb -output /backups/vaultdb-20260701.tar.gz
```

## Docker Backup

```bash
# Create backup from running container
docker exec vaultdb vaultdb-backup -mode backup -data /data -output /tmp/backup.tar.gz
docker cp vaultdb:/tmp/backup.tar.gz ./backup.tar.gz

# Restore to new container
docker cp backup.tar.gz vaultdb:/tmp/backup.tar.gz
docker exec vaultdb vaultdb-backup -mode restore -data /data -output /tmp/backup.tar.gz
```

## Backup Best Practices

1. **Stop writes** before backup for point-in-time consistency
2. **Test restores** regularly to verify backup integrity
3. **Store backups** in a different location from the data directory
4. **Automate backups** with cron or systemd timers
5. **Rotate backups** to manage disk space
6. **Verify checksums** after backup creation

## Crash Recovery During Backup

If a crash occurs during backup:
- The backup may be incomplete
- The original data directory is not affected
- Use the most recent complete backup for restore

## Incremental Backups

VaultDB does not currently support incremental backups. Each backup is a full snapshot of the data directory.
