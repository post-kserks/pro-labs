# Sequences

VaultDB provides AUTO_INCREMENT for automatic sequential ID generation.

## AUTO_INCREMENT

Any `INT` column can be declared with `AUTO_INCREMENT`:

```sql
CREATE TABLE users (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name TEXT
);

INSERT INTO users (name) VALUES ('Alice');  -- id = 1
INSERT INTO users (name) VALUES ('Bob');    -- id = 2
```

## How It Works

1. When a row is inserted without a value for the AUTO_INCREMENT column, VaultDB generates the next value
2. The sequence is tracked per table in memory
3. The next value is `max(existing values) + 1`
4. AUTO_INCREMENT columns should typically be PRIMARY KEY or UNIQUE

## Limitations

- AUTO_INCREMENT is per-table, not global
- Sequence values are not gaps-free (deletes create gaps)
- AUTO_INCREMENT only works with INT columns
- The sequence resets when the table is truncated
