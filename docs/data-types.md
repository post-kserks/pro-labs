# Data Types

VaultDB supports the following column types. Every value in a column must conform to its declared type — type coercion is applied automatically where possible, and errors are returned for incompatible conversions.

## Type Summary

| Type | Storage | Range / Limits | Example |
|------|---------|----------------|---------|
| `INT` | 8 bytes (int64) | -2^63 to 2^63-1 | `42`, `-7`, `0` |
| `BIGINT` | 8 bytes (int64) | Alias for INT | `42`, `-7`, `0` |
| `FLOAT` | 8 bytes (float64) | IEEE 754 double | `3.14`, `-0.5`, `1e10` |
| `NUMERIC(p,s)` | String (arbitrary precision) | Precision/scale | `'123.45'` |
| `BOOL` | 1 byte | `true` / `false` | `true`, `false` |
| `TEXT` | Variable length | No limit | `'Hello'` |
| `VARCHAR(n)` | Variable length | Max `n` characters | `'abc'` (VARCHAR(10)) |
| `DATE` | String (ISO format) | — | `'2026-07-01'` |
| `TIME` | String (ISO format) | — | `'14:30:00'` |
| `TIMESTAMP` | String (ISO format) | — | `'2026-07-01T14:30:00Z'` |
| `TIMESTAMPTZ` | String (ISO format) | Timezone-aware timestamp | `'2026-07-01T14:30:00+03:00'` |
| `DECIMAL` | String (arbitrary precision) | — | `'123.456'` |
| `SERIAL` | Shorthand for INT AUTO_INCREMENT | — | — |
| `JSONB` | String (JSON text) | — | `'{"key": "value"}'` |
| `JSON` | String (JSON text) | — | `'[1, 2, 3]'` |
| `ARRAY` | JSON array string | — | `'[1, 2, 3]'` |
| `VECTOR` | []float64 | — | `'[0.1, 0.2, 0.3]'` |
| `FLEXIBLE` | map[string]interface{} | Any JSON object | `'{"a": 1}'` |
| `ENUM` | String | Listed values only | `'active'` |
| `UUID` | String (UUIDv4 format) | — | `'550e8400-...'` |
| `INTERVAL` | String | — | `'3 days'` |
| `JSONB` | String | — | See JSONB section |

## INTEGER

Exact whole numbers. Stored as Go `int64` (8 bytes).

```sql
CREATE TABLE products (
    id INT PRIMARY KEY,
    quantity INT
);

INSERT INTO products VALUES (1, 100);
INSERT INTO products VALUES (2, -50);
```

**Coercion**: Any numeric Go type is converted to `int64`. Float values with non-zero fractional parts are rejected with an error.

## BIGINT

Alias for INT. Both map to 8-byte int64. Use BIGINT for semantic clarity when storing large identifiers.

```sql
CREATE TABLE events (
    id BIGINT PRIMARY KEY,
    payload TEXT
);
```

## FLOAT

Approximate floating-point numbers. Stored as Go `float64` (8 bytes, IEEE 754).

```sql
CREATE TABLE measurements (
    id INT,
    value FLOAT
);

INSERT INTO measurements VALUES (1, 3.14159);
INSERT INTO measurements VALUES (2, -0.001);
INSERT INTO measurements VALUES (3, 1e10);
```

**Coercion**: Any numeric Go type is converted to `float64`.

## NUMERIC(p, s)

Arbitrary-precision decimal numbers with explicit precision and scale. Useful for financial calculations.

```sql
CREATE TABLE prices (
    id INT,
    amount NUMERIC(15, 2)
);

INSERT INTO prices VALUES (1, '1234567890123.45');
```

Precision (p) is total digits, scale (s) is digits after decimal point. Internally stored as FLOAT.

## BOOLEAN

Logical values: `true` or `false`.

```sql
CREATE TABLE flags (
    id INT,
    enabled BOOL
);

INSERT INTO flags VALUES (1, true);
INSERT INTO flags VALUES (2, false);
```

**Coercion**: Only Go `bool` values are accepted.

## TEXT

Variable-length character strings with no length limit.

```sql
CREATE TABLE articles (
    id INT,
    title TEXT,
    body TEXT
);

INSERT INTO articles VALUES (1, 'Hello', 'World');
```

**Coercion**: Must be a Go `string`.

## VARCHAR(n)

Variable-length character strings with a maximum length of `n` characters (runes). Enforced at insert and update time.

```sql
CREATE TABLE users (
    username VARCHAR(50) NOT NULL,
    email VARCHAR(255)
);

INSERT INTO users VALUES ('alice', 'alice@example.com');  -- OK
INSERT INTO users VALUES ('a_very_long_name...', 'x');    -- Error: exceeds VARCHAR(50)
```

**Coercion**: Must be a Go `string`. Length is validated against `VarcharLen`.

## DATE

Calendar dates in ISO 8601 format.

```sql
CREATE TABLE events (
    id INT,
    event_date DATE
);

INSERT INTO events VALUES (1, '2026-07-01');
```

Accepted input formats:
- `2026-07-01`
- `07/01/2026` (US format)
- `01-07-2026` (European format)

## TIME

Time of day in ISO 8601 format.

```sql
INSERT INTO events VALUES (1, '14:30:00');
```

## TIMESTAMP

Date and time combined. Supports timezone-aware and naive timestamps.

```sql
CREATE TABLE logs (
    id INT,
    created_at TIMESTAMP
);

INSERT INTO logs VALUES (1, '2026-07-01T14:30:00Z');
INSERT INTO logs VALUES (2, '2026-07-01T14:30:00+03:00');
```

Accepted formats:
- RFC 3339: `2026-07-01T14:30:00Z`
- ISO-like: `2026-07-01 14:30:00`
- US format: `07/01/2026 14:30:00`

## TIMESTAMPTZ

Timezone-aware timestamp. Internally stored as TIMESTAMP but accepts timezone offsets.

```sql
CREATE TABLE events (
    id INT,
    created_at TIMESTAMPTZ
);

INSERT INTO events VALUES (1, '2026-07-01T14:30:00+03:00');
INSERT INTO events VALUES (2, '2026-07-01T14:30:00Z');
```

## DECIMAL

Arbitrary-precision decimal numbers stored as strings. Useful for financial calculations where floating-point rounding is unacceptable.

```sql
CREATE TABLE prices (
    id INT,
    amount DECIMAL
);

INSERT INTO prices VALUES (1, '19.99');
INSERT INTO prices VALUES (2, '0.001');
```

## JSON / JSONB

JSON data stored as text. VaultDB distinguishes between raw JSON and JSONB (binary JSON), but both are stored as strings internally.

```sql
CREATE TABLE configs (
    id INT,
    settings JSONB
);

INSERT INTO configs VALUES (1, '{"theme": "dark", "notifications": true}');
```

### JSON Path Expressions

```sql
-- Extract raw JSON value
SELECT settings->'theme' FROM configs;       -- "dark"

-- Extract as text
SELECT settings->>'theme' FROM configs;      -- dark
```

### JSON Functions

```sql
SELECT JSON_EXTRACT('{"a": {"b": 1}}', 'a', 'b');  -- 1
SELECT JSONB_TYPEOF('{"a": 1}');                     -- object
SELECT JSONB_ARRAY_LENGTH('[1,2,3]');                -- 3
```

## ARRAY

JSON arrays used as a list type. All array operations work on JSON arrays stored as text.

```sql
CREATE TABLE tags (
    id INT,
    labels ARRAY
);

INSERT INTO tags VALUES (1, '["go", "database", "sql"]');
```

### Array Functions

```sql
SELECT ARRAY_LENGTH('[1,2,3]');              -- 3
SELECT ARRAY_CONTAINS('[1,2,3]', '2');       -- true
SELECT ARRAY_APPEND('[1,2]', '3');           -- [1,2,3]
SELECT ARRAY_SLICE('[1,2,3,4]', 1, 2);      -- [2,3]
SELECT ARRAY_TO_STRING('["a","b"]', ',');    -- a,b
```

## VECTOR

Fixed-dimensional float vectors for similarity search. Stored as JSON arrays of floats.

```sql
CREATE TABLE embeddings (
    id INT,
    embedding VECTOR
);

INSERT INTO embeddings VALUES (1, '[0.1, 0.2, 0.3, 0.4]');
```

Used with `AI_EMBED()` and `SEMANTIC_MATCH()` for vector-based search.

## FLEXIBLE

A schemaless JSON object type. Can store any valid JSON object without pre-defined column structure.

```sql
CREATE TABLE events (
    id INT,
    payload FLEXIBLE
);

INSERT INTO events VALUES (1, '{"type": "click", "x": 100, "y": 200}');
```

## ENUM

A string type restricted to a predefined list of values. The allowed values are specified at table creation time.

```sql
CREATE TABLE orders (
    id INT,
    status ENUM
);
-- The ENUM accepts any string, but application-level validation
-- should enforce the allowed values.
```

## SERIAL

Shorthand for `INT AUTO_INCREMENT`. Common in PostgreSQL schemas.

```sql
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    name TEXT
);

-- Equivalent to:
-- id INT AUTO_INCREMENT PRIMARY KEY
```

## AUTO_INCREMENT

Any `INT` column can be declared with `AUTO_INCREMENT` to automatically generate sequential values. Can be placed before or after `PRIMARY KEY`.

```sql
CREATE TABLE users (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name TEXT
);

-- Also valid:
CREATE TABLE users (
    id INT PRIMARY KEY AUTO_INCREMENT,
    name TEXT
);
```

## PRIMARY KEY

Enforces uniqueness and creates a B-tree index automatically.

```sql
CREATE TABLE users (
    id INT PRIMARY KEY,
    email TEXT UNIQUE
);
```

## NOT NULL

Prevents null values in a column.

```sql
CREATE TABLE users (
    id INT PRIMARY KEY,
    name TEXT NOT NULL
);
```

## GENERATED ALWAYS AS IDENTITY

PostgreSQL-compatible syntax for identity columns. Maps to AUTO_INCREMENT.

```sql
CREATE TABLE users (
    id INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name TEXT
);

-- Also valid with BY DEFAULT:
CREATE TABLE users (
    id INT GENERATED BY DEFAULT AS IDENTITY,
    name TEXT
);
```

## GENERATED ALWAYS AS (Computed Columns)

Computed columns are evaluated at INSERT/UPDATE time. The expression is stored and re-evaluated automatically.

```sql
CREATE TABLE products (
    id INT PRIMARY KEY,
    price INT,
    quantity INT,
    total INT GENERATED ALWAYS AS (price * quantity) STORED
);

INSERT INTO products (id, price, quantity) VALUES (1, 10, 5);
SELECT total FROM products WHERE id = 1;  -- 50
```

## IF EXISTS / IF NOT EXISTS

Idempotent DDL operations for migration scripts.

```sql
CREATE TABLE IF NOT EXISTS users (id INT PRIMARY KEY);
DROP TABLE IF EXISTS users;
```

## Type Conversion

### CAST

Explicit type conversion:

```sql
SELECT CAST(42 AS TEXT);          -- '42'
SELECT CAST('3.14' AS FLOAT);    -- 3.14
SELECT CAST(1 AS BOOL);          -- true
SELECT CAST('2026-07-01' AS TIMESTAMP);
```

### Implicit Coercion

VaultDB applies implicit type conversion where safe:

- Numeric types: `int` ↔ `float` (lossy in float→int direction)
- `TEXT` → `VARCHAR` (truncated if exceeds length)
- String → `DATE`/`TIMESTAMP` (parsed from multiple formats)
- `[]interface{}` → `VECTOR` (converted to `[]float64`)
