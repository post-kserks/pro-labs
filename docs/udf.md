# User-Defined Functions and Procedures

VaultDB supports creating custom functions and procedures with SQL bodies and WebAssembly modules.

## SQL User-Defined Functions

Functions return a single value and can be used in expressions.

### Creating a SQL Function

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

## WASM User-Defined Functions

Create custom SQL functions backed by WebAssembly modules for performance-critical operations.

### Creating a WASM Function

```sql
CREATE FUNCTION hash_pii(value TEXT) RETURNS TEXT
LANGUAGE WASM
AS 'file:///plugins/hash_pii.wasm'
WITH (memory_limit = '16MB', timeout = '100ms');
```

### WASM Options

| Option | Description | Example |
|--------|-------------|---------|
| `memory_limit` | Maximum memory (KB, MB, GB) | `'16MB'`, `'512KB'`, `'1GB'` |
| `timeout` | Maximum execution time (Go duration format) | `'100ms'`, `'5s'`, `'1m'` |

### Using a WASM Function

```sql
-- Call it like any SQL function
SELECT hash_pii(email) FROM users;
```

### WASM Function Requirements

- WASM modules must export a `main` function
- Functions accept and return string values
- Memory is isolated per execution
- Timeouts prevent infinite loops

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
- SQL function bodies must be SELECT statements
- No support for PL/pgSQL-style procedural logic
- Parameters are passed as SQL values
- WASM functions are sandboxed and cannot access the database directly
