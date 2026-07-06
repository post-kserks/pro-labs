# Functions and Operators

VaultDB provides 130+ built-in functions and operators organized by category.

## Operators

### Arithmetic Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `+` | Addition | `SELECT 2 + 3;` → `5` |
| `-` | Subtraction | `SELECT 10 - 4;` → `6` |
| `*` | Multiplication | `SELECT 3 * 7;` → `21` |
| `/` | Division | `SELECT 10 / 3;` → `3` |
| `%` | Modulo | `SELECT 10 % 3;` → `1` |
| `MOD(a, b)` | Modulo (function) | `SELECT MOD(10, 3);` → `1` |

Integer division preserves integer type when both operands are integers. Division by zero returns an error.

### Comparison Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `=` | Equal | `SELECT 1 = 1;` → `true` |
| `!=` or `<>` | Not equal | `SELECT 1 != 2;` → `true` |
| `<` | Less than | `SELECT 1 < 2;` → `true` |
| `>` | Greater than | `SELECT 2 > 1;` → `true` |
| `<=` | Less or equal | `SELECT 1 <= 1;` → `true` |
| `>=` | Greater or equal | `SELECT 2 >= 1;` → `true` |

### Logical Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `AND` | Logical AND | `SELECT true AND false;` → `false` |
| `OR` | Logical OR | `SELECT true OR false;` → `true` |
| `NOT` | Logical NOT | `SELECT NOT true;` → `false` |

### JSON Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `->` | Extract raw JSON | `col->'key'` |
| `->>` | Extract as text | `col->>'key'` |
| `@>` | Contains | `'{"a":1}' @> '{"a":1}'` |
| `<@` | Contained by | `'{"a":1}' <@ '{"a":1,"b":2}'` |
| `?` | Has key | `'{"a":1}' ? 'a'` |

---

## String Functions

| Function | Description | Example |
|----------|-------------|---------|
| `UPPER(s)` | Convert to uppercase | `UPPER('hello')` → `'HELLO'` |
| `LOWER(s)` | Convert to lowercase | `UPPER('HELLO')` → `'hello'` |
| `INITCAP(s)` | Capitalize first letter of each word | `INITCAP('hello world')` → `'Hello World'` |
| `LENGTH(s)` / `LEN(s)` | String length | `LENGTH('hello')` → `5` |
| `CONCAT(s1, s2, ...)` | Concatenate strings | `CONCAT('a', 'b', 'c')` → `'abc'` |
| `SUBSTRING(s, start [, len])` | Extract substring | `SUBSTRING('hello', 2, 3)` → `'ell'` |
| `SUBSTR(s, start [, len])` | Alias for SUBSTRING | `SUBSTR('hello', 2, 3)` → `'ell'` |
| `TRIM(s)` | Remove leading/trailing spaces | `TRIM('  hi  ')` → `'hi'` |
| `LTRIM(s)` | Remove leading spaces | `LTRIM('  hi')` → `'hi'` |
| `RTRIM(s)` | Remove trailing spaces | `RTRIM('hi  ')` → `'hi'` |
| `REPLACE(s, from, to)` | Replace substring | `REPLACE('hello', 'l', 'r')` → `'herro'` |
| `POSITION(sub IN s)` | Find substring position | `POSITION('ll' IN 'hello')` → `3` |
| `LEFT(s, n)` | First n characters | `LEFT('hello', 3)` → `'hel'` |
| `RIGHT(s, n)` | Last n characters | `RIGHT('hello', 3)` → `'llo'` |
| `LPAD(s, len [, pad])` | Left-pad string | `LPAD('hi', 5, '0')` → `'000hi'` |
| `RPAD(s, len [, pad])` | Right-pad string | `RPAD('hi', 5, '0')` → `'hi000'` |
| `REVERSE(s)` | Reverse string | `REVERSE('hello')` → `'olleh'` |

---

## Math Functions

| Function | Description | Example |
|----------|-------------|---------|
| `ABS(x)` | Absolute value | `ABS(-5)` → `5` |
| `CEIL(x)` / `CEILING(x)` | Round up | `CEIL(3.1)` → `4` |
| `FLOOR(x)` | Round down | `FLOOR(3.9)` → `3` |
| `ROUND(x [, places])` | Round to places | `ROUND(3.14159, 2)` → `3.14` |
| `POWER(a, b)` / `POW(a, b)` | Exponentiation | `POWER(2, 10)` → `1024` |
| `SQRT(x)` | Square root | `SQRT(16)` → `4` |
| `LN(x)` | Natural logarithm | `LN(2.718281828)` → `1.0` |
| `LOG(x)` / `LOG10(x)` | Base-10 logarithm | `LOG(100)` → `2` |
| `EXP(x)` | e^x | `EXP(0)` → `1` |
| `SIGN(x)` | Sign: -1, 0, or 1 | `SIGN(-42)` → `-1` |
| `GREATEST(a, b, ...)` | Maximum value | `GREATEST(1, 5, 3)` → `5` |
| `LEAST(a, b, ...)` | Minimum value | `LEAST(1, 5, 3)` → `1` |
| `MOD(a, b)` | Modulo | `MOD(10, 3)` → `1` |

---

## Date/Time Functions

| Function | Description | Example |
|----------|-------------|---------|
| `NOW()` | Current timestamp | `SELECT NOW();` |
| `CURRENT_DATE` | Today's date | `SELECT CURRENT_DATE;` |
| `CURRENT_TIME` | Current time | `SELECT CURRENT_TIME;` |
| `CURRENT_TIMESTAMP` | Current timestamp | `SELECT CURRENT_TIMESTAMP;` |
| `DATE_TRUNC(part, ts)` | Truncate to precision | `DATE_TRUNC('month', NOW())` |
| `EXTRACT(field FROM ts)` | Extract component | `EXTRACT(year FROM NOW())` |
| `AGE(ts)` | Age from now | `AGE('2000-01-01')` |
| `AGE(ts1, ts2)` | Age between | `AGE('2026-07-01', '2000-01-01')` |
| `TO_DATE(str, fmt)` | Parse date | `TO_DATE('07/01/2026', 'MM/DD/YYYY')` |
| `TO_CHAR(ts, fmt)` | Format timestamp | `TO_CHAR(NOW(), 'YYYY-MM-DD')` |
| `TO_TIMESTAMP(str, fmt)` | Parse timestamp | `TO_TIMESTAMP('2026-07-01', 'YYYY-MM-DD')` |
| `DATE_ADD(date, n, unit)` | Add interval | `DATE_ADD(NOW(), 7, 'DAY')` |
| `DATE_SUB(date, n, unit)` | Subtract interval | `DATE_SUB(NOW(), 1, 'MONTH')` |
| `DATE_DIFF(unit, d1, d2)` | Difference | `DATE_DIFF('DAY', '2026-01-01', '2026-07-01')` |
| `INTERVAL(str)` | Create interval | `INTERVAL('3 days')` |

### EXTRACT Fields

| Field | Description |
|-------|-------------|
| `YEAR` | Year component |
| `MONTH` | Month (1-12) |
| `DAY` | Day of month (1-31) |
| `HOUR` | Hour (0-23) |
| `MINUTE` | Minute (0-59) |
| `SECOND` | Second (0-59) |
| `DOW` | Day of week (0=Sunday, 6=Saturday) |
| `DOY` | Day of year (1-366) |

### DATE_TRUNC Parts

`YEAR`, `MONTH`, `DAY`, `HOUR`, `MINUTE`, `SECOND`

### DATE_DIFF Units

`DAY`, `HOUR`, `MINUTE`, `SECOND`, `MONTH`, `YEAR`, `WEEK`

### Interval Arithmetic

```sql
SELECT '2026-07-01'::TIMESTAMP + INTERVAL '7 days';
SELECT NOW() - INTERVAL '1 month';
SELECT DATE_ADD('2026-01-01', 3, 'MONTH');  -- 2026-04-01
```

---

## Aggregate Functions

| Function | Description | Example |
|----------|-------------|---------|
| `COUNT(*)` | Count all rows | `SELECT COUNT(*) FROM users;` |
| `COUNT(col)` | Count non-null values | `SELECT COUNT(email) FROM users;` |
| `SUM(col)` | Sum of values | `SELECT SUM(amount) FROM orders;` |
| `AVG(col)` | Average of values | `SELECT AVG(price) FROM products;` |
| `MIN(col)` | Minimum value | `SELECT MIN(price) FROM products;` |
| `MAX(col)` | Maximum value | `SELECT MAX(price) FROM products;` |
| `VARIANCE(col)` | Sample variance | `SELECT VARIANCE(scores) FROM results;` |
| `STDDEV(col)` | Sample standard deviation | `SELECT STDDEV(scores) FROM results;` |

Aggregate functions ignore NULL values (except `COUNT(*)`). All aggregates use Welford's online algorithm for numerical stability with large datasets.

---

## Window Functions

| Function | Description | Example |
|----------|-------------|---------|
| `ROW_NUMBER()` | Sequential row number | `ROW_NUMBER() OVER (ORDER BY id)` |
| `RANK()` | Rank with gaps | `RANK() OVER (ORDER BY score DESC)` |
| `DENSE_RANK()` | Rank without gaps | `DENSE_RANK() OVER (ORDER BY score DESC)` |
| `LAG(col, n)` | Value from n rows before | `LAG(sales, 1) OVER (ORDER BY date)` |
| `LEAD(col, n)` | Value from n rows after | `LEAD(sales, 1) OVER (ORDER BY date)` |
| `FIRST_VALUE(col)` | First value in frame | `FIRST_VALUE(sales) OVER (ORDER BY date)` |
| `LAST_VALUE(col)` | Last value in frame | `LAST_VALUE(sales) OVER (ORDER BY date)` |
| `NTH_VALUE(col, n)` | Nth value in frame | `NTH_VALUE(sales, 3) OVER (ORDER BY date)` |
| `SUM/AVG/COUNT/MIN/MIN(col)` | Aggregate over frame | `SUM(sales) OVER (PARTITION BY region)` |

### Window Frame Clauses

```sql
-- Default frame: RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW
SELECT SUM(sales) OVER (ORDER BY date) FROM orders;

-- Custom frame
SELECT SUM(sales) OVER (
    ORDER BY date
    ROWS BETWEEN 2 PRECEDING AND 2 FOLLOWING
) FROM orders;

-- Partition
SELECT name, SUM(sales) OVER (PARTITION BY region) FROM orders;
```

---

## JSON Functions

| Function | Description | Example |
|----------|-------------|---------|
| `JSON_OBJECT(k1, v1, ...)` | Create JSON object | `JSON_OBJECT('a', 1, 'b', 2)` |
| `JSON_ARRAY(v1, v2, ...)` | Create JSON array | `JSON_ARRAY(1, 2, 3)` |
| `JSON_EXTRACT(json, k1, k2)` | Extract nested value | `JSON_EXTRACT('{"a":{"b":1}}', 'a', 'b')` |
| `JSONB_BUILD_OBJECT(k1, v1, ...)` | Build JSONB object | Same as JSON_OBJECT |
| `JSONB_BUILD_ARRAY(v1, v2, ...)` | Build JSONB array | Same as JSON_ARRAY |
| `JSONB_ARRAY_ELEMENTS(json)` | Unnest array | `JSONB_ARRAY_ELEMENTS('[1,2,3]')` |
| `JSONB_TYPEOF(json)` | Return type string | `JSONB_TYPEOF('{"a":1}')` → `'object'` |
| `JSONB_SET(target, path, val)` | Set value at path | `JSONB_SET('{"a":1}', 'a', '2')` |
| `JSONB_EXTRACT_PATH(json, k...)` | Extract path | `JSONB_EXTRACT_PATH('{"a":{"b":1}}', 'a', 'b')` |

`JSONB_TYPEOF` returns: `string`, `number`, `boolean`, `null`, `array`, `object`.

---

## Array Functions

All operate on JSON arrays stored as text.

| Function | Description | Example |
|----------|-------------|---------|
| `ARRAY_APPEND(arr, val)` | Append element | `ARRAY_APPEND('[1,2]', 3)` → `[1,2,3]` |
| `ARRAY_LENGTH(arr)` | Element count | `ARRAY_LENGTH('[1,2,3]')` → `3` |
| `ARRAY_CONTAINS(arr, val)` | Check membership | `ARRAY_CONTAINS('[1,2,3]', 2)` → `true` |
| `ARRAY_TO_STRING(arr, delim)` | Join elements | `ARRAY_TO_STRING('["a","b"]', ',')` → `'a,b'` |
| `ARRAY_SLICE(arr, start, end)` | Subarray | `ARRAY_SLICE('[1,2,3,4]', 1, 2)` → `[2,3]` |

---

## Utility Functions

| Function | Description | Example |
|----------|-------------|---------|
| `UUID()` | Generate UUIDv4 | `SELECT UUID();` → `'550e8400-e29b-41d4-a716-446655440000'` |
| `AI_EMBED(text)` | Generate embedding vector | `SELECT AI_EMBED('hello world');` |
| `NULLIF(a, b)` | Return NULL if equal | `NULLIF(1, 1)` → `NULL` |
| `COALESCE(a, b, ...)` | First non-null value | `COALESCE(NULL, 2, 3)` → `2` |

---

## WASM User-Defined Functions

Create custom SQL functions backed by WebAssembly modules.

```sql
-- Create a WASM function
CREATE FUNCTION hash_pii(value TEXT) RETURNS TEXT
LANGUAGE WASM
AS 'file:///plugins/hash_pii.wasm'
WITH (memory_limit = '16MB', timeout = '100ms');

-- Call it like any SQL function
SELECT hash_pii(email) FROM users;
```

### WASM Options

| Option | Description | Example |
|--------|-------------|---------|
| `memory_limit` | Maximum memory (KB, MB, GB) | `'16MB'`, `'512KB'`, `'1GB'` |
| `timeout` | Maximum execution time (Go duration format) | `'100ms'`, `'5s'`, `'1m'` |
