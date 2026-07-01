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
