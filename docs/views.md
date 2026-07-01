# Views

Views are named SQL queries stored in the database schema.

## Creating Views

```sql
CREATE VIEW active_users AS
SELECT id, name, email
FROM users
WHERE status = 'active';
```

## Querying Views

```sql
SELECT * FROM active_users WHERE name LIKE 'A%';
```

## Dropping Views

```sql
DROP VIEW active_users;
```

## Limitations

- Views are not materialized (re-evaluated on each query)
- Views cannot be updated (INSERT/UPDATE/DELETE through views is not supported)
- Views reference tables by name at creation time
- Views support all SELECT features including JOINs, subqueries, CTEs, and window functions
