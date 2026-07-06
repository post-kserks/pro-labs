# Triggers

VaultDB supports AFTER triggers for INSERT, UPDATE, and DELETE operations.

## Creating Triggers

```sql
CREATE TRIGGER audit_insert
AFTER INSERT ON users
BEGIN
    INSERT INTO audit_log (table_name, operation, changed_at)
    VALUES ('users', 'INSERT', CURRENT_TIMESTAMP);
END;
```

## Trigger Events

| Event | Fires After |
|-------|-------------|
| `INSERT` | New row inserted |
| `UPDATE` | Row updated |
| `DELETE` | Row deleted |

## Trigger Timing

Only `AFTER` triggers are supported. The trigger body executes after the operation completes.

## Dropping Triggers

```sql
DROP TRIGGER audit_insert ON users;
```

## Trigger Body

The trigger body is a SQL statement (or block) that executes when the trigger fires. It can reference:

- `NEW` — the new row values (for INSERT/UPDATE)
- `OLD` — the old row values (for UPDATE/DELETE)

## Limitations

- Only AFTER triggers are supported (no BEFORE or INSTEAD OF)
- One trigger body per event per table
- Trigger bodies are executed as standalone SQL statements
- Triggers do not support transaction control (BEGIN/COMMIT/ROLLBACK)

## Re-Entrant Guard

VaultDB implements a re-entrant guard to prevent infinite recursion in triggers. The maximum trigger recursion depth is **3 levels**.

**How it works:**
- Each trigger execution increments a depth counter
- When the depth counter reaches 3, further trigger invocations are skipped
- A warning is logged: "trigger recursion depth limit reached"
- The triggering operation still completes successfully

**Example scenario:**
```sql
-- Trigger A fires on INSERT into table1
CREATE TRIGGER trigger_a
AFTER INSERT ON table1
BEGIN
    INSERT INTO table2 VALUES (NEW.id, NEW.name);
END;

-- Trigger B fires on INSERT into table2
CREATE TRIGGER trigger_b
AFTER INSERT ON table2
BEGIN
    INSERT INTO table1 VALUES (NEW.id, NEW.name);  -- This would cause recursion
END;

-- With re-entrant guard:
-- 1. INSERT into table1 fires trigger_a (depth=1)
-- 2. trigger_a INSERTs into table2, fires trigger_b (depth=2)
-- 3. trigger_b INSERTs into table1, would fire trigger_a (depth=3)
-- 4. At depth 3, trigger_a is skipped (re-entrant guard)
-- 5. All operations complete successfully
```

**Configuration:**
The maximum depth is hardcoded at 3 and cannot be configured. This limit is sufficient for most use cases while preventing infinite recursion.
