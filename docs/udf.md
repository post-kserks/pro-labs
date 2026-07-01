# User-Defined Functions and Procedures

VaultDB supports creating custom functions and procedures with SQL bodies.

## Functions

Functions return a single value and can be used in expressions.

### Creating a Function

```sql
CREATE FUNCTION double_value(x INT) RETURNS INT AS
    SELECT x * 2;
```

### Using a Function

```sql
SELECT double_value(5);  -- Returns 10
SELECT double_value(age) FROM users;
```

### Dropping a Function

```sql
DROP FUNCTION double_value;
```

## Procedures

Procedures execute multi-statement SQL blocks and do not return values.

### Creating a Procedure

```sql
CREATE PROCEDURE update_status(user_id INT, new_status TEXT) AS
    UPDATE users SET status = new_status WHERE id = user_id;
    INSERT INTO audit_log (user_id, action) VALUES (user_id, 'status_update');
```

### Calling a Procedure

```sql
CALL update_status(1, 'active');
```

### Dropping a Procedure

```sql
DROP PROCEDURE update_status;
```

## Parameter Binding

Functions and procedures support positional parameters (`$1`, `$2`, ...):

```sql
CREATE FUNCTION get_user_name(user_id INT) RETURNS TEXT AS
    SELECT name FROM users WHERE id = $1;
```

## Limitations

- Functions return a single value (scalar)
- Procedures execute multi-statement blocks
- Function/procedure bodies are SQL statements (not Go code)
- No support for PL/pgSQL-style procedural logic
- Parameters are passed as SQL values
