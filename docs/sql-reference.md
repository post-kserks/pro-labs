# VaultDB SQL Language Reference

## Overview

VaultDB supports a subset of SQL with extensions. This document covers all supported syntax.

---

## DDL (Data Definition Language)

### CREATE DATABASE

```sql
CREATE DATABASE mydb;
```

### DROP DATABASE

```sql
DROP DATABASE mydb;
```

### CREATE TABLE

```sql
CREATE TABLE users (
    id INT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(255),
    age INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Column Types:**
- `INT` — 64-bit integer
- `FLOAT` — 64-bit floating point
- `VARCHAR(n)` — variable-length string with optional max length
- `TEXT` — unlimited text
- `BOOL` — boolean (true/false)
- `TIMESTAMP` — date and time
- `JSON` — JSON data

**Constraints:**
- `PRIMARY KEY` — unique identifier
- `NOT NULL` — required field
- `DEFAULT value` — default value

### ALTER TABLE

```sql
ALTER TABLE users ADD COLUMN phone VARCHAR(20);
ALTER TABLE users DROP COLUMN phone;
ALTER TABLE users RENAME COLUMN name TO full_name;
ALTER TABLE users RENAME TO customers;
```

### CREATE INDEX

```sql
CREATE INDEX idx_users_email ON users (email);
CREATE INDEX idx_users_name_age ON users (name, age);  -- composite index
DROP INDEX idx_users_email;
```

### DROP TABLE

```sql
DROP TABLE users;
```

---

## DML (Data Manipulation Language)

### INSERT

```sql
INSERT INTO users (id, name, email) VALUES (1, 'Alice', 'alice@example.com');
INSERT INTO users VALUES (2, 'Bob', 'bob@example.com', 25, CURRENT_TIMESTAMP);
```

### UPDATE

```sql
UPDATE users SET email = 'newemail@example.com' WHERE id = 1;
UPDATE users SET age = age + 1 WHERE age < 30;
```

### DELETE

```sql
DELETE FROM users WHERE id = 1;
DELETE FROM users WHERE age < 18;
```

---

## SELECT

### Basic SELECT

```sql
SELECT * FROM users;
SELECT id, name, email FROM users;
SELECT DISTINCT age FROM users;
```

### WHERE Clause

```sql
SELECT * FROM users WHERE age > 25;
SELECT * FROM users WHERE name LIKE 'A%';
SELECT * FROM users WHERE id IN (1, 2, 3);
SELECT * FROM users WHERE email IS NOT NULL;
SELECT * FROM users WHERE age BETWEEN 20 AND 30;
```

### ORDER BY

```sql
SELECT * FROM users ORDER BY name ASC;
SELECT * FROM users ORDER BY age DESC, name ASC;
```

### LIMIT and OFFSET

```sql
SELECT * FROM users LIMIT 10;
SELECT * FROM users LIMIT 10 OFFSET 20;
```

### JOINs

```sql
SELECT u.name, o.total
FROM users u
JOIN orders o ON u.id = o.user_id;

SELECT u.name, o.total
FROM users u
LEFT JOIN orders o ON u.id = o.user_id;

SELECT u.name, o.total
FROM users u
RIGHT JOIN orders o ON u.id = o.user_id;
```

### Aggregates

```sql
SELECT COUNT(*) FROM users;
SELECT age, COUNT(*) FROM users GROUP BY age;
SELECT age, COUNT(*) FROM users GROUP BY age HAVING COUNT(*) > 5;
SELECT SUM(amount), AVG(amount), MIN(amount), MAX(amount) FROM orders;
```

### Window Functions

```sql
SELECT name, age, ROW_NUMBER() OVER (ORDER BY age) FROM users;
SELECT name, age, RANK() OVER (PARTITION BY department ORDER BY salary DESC) FROM employees;
SELECT name, salary, SUM(salary) OVER (ORDER BY id) FROM employees;
```

### Common Table Expressions (CTEs)

```sql
WITH active_users AS (
    SELECT * FROM users WHERE last_login > '2026-01-01'
)
SELECT * FROM active_users WHERE age > 25;
```

### Subqueries

```sql
SELECT * FROM users WHERE id IN (SELECT user_id FROM orders WHERE amount > 100);
SELECT * FROM users WHERE age > (SELECT AVG(age) FROM users);
```

### EXPLAIN

```sql
EXPLAIN SELECT * FROM users WHERE age > 25;
EXPLAIN ANALYZE SELECT * FROM users WHERE age > 25;
```

---

## Transactions

```sql
BEGIN TRANSACTION;
INSERT INTO accounts (id, balance) VALUES (1, 1000);
UPDATE accounts SET balance = balance - 100 WHERE id = 1;
UPDATE accounts SET balance = balance + 100 WHERE id = 2;
COMMIT;

-- Or rollback on error:
ROLLBACK;
```

---

## Triggers

```sql
CREATE TRIGGER log_changes
AFTER INSERT ON users
BEGIN
    INSERT INTO audit_log (table_name, action, row_id)
    VALUES ('users', 'INSERT', NEW.id);
END;
```

---

## Views

```sql
CREATE VIEW active_users AS
SELECT * FROM users WHERE status = 'active';

SELECT * FROM active_users;
DROP VIEW active_users;
```

---

## Built-in Functions

### String Functions

- `LENGTH(str)` — string length
- `LOWER(str)` — convert to lowercase
- `UPPER(str)` — convert to uppercase
- `TRIM(str)` — remove leading/trailing whitespace
- `SUBSTR(str, start, length)` — substring
- `CONCAT(str1, str2, ...)` — concatenate strings
- `REPLACE(str, from, to)` — replace substring

### Math Functions

- `ABS(n)` — absolute value
- `ROUND(n, decimals)` — round to N decimal places
- `CEIL(n)` — ceiling
- `FLOOR(n)` — floor
- `POWER(base, exp)` — exponentiation
- `SQRT(n)` — square root

### Date/Time Functions

- `CURRENT_TIMESTAMP` — current date and time
- `NOW()` — current timestamp
- `DATE('2026-01-15')` — parse date string
- `EXTRACT(YEAR FROM timestamp)` — extract component
- `DATE_ADD(date, interval)` — add interval
- `DATE_SUB(date, interval)` — subtract interval

### JSON Functions

- `JSON_EXTRACT(json, path)` — extract value from JSON
- `JSON_ARRAY_LENGTH(json)` — array length
- `JSON_OBJECT(keys, values)` — create JSON object
- `JSON_ARRAY(values)` — create JSON array

### Aggregate Functions

- `COUNT(*)` / `COUNT(column)` — count rows
- `SUM(column)` — sum values
- `AVG(column)` — average
- `MIN(column)` — minimum
- `MAX(column)` — maximum

### Semantic Search

- `SEMANTIC_MATCH(column, 'search query')` — vector similarity search (requires AI embedding provider)
- `AI_EMBED('text')` — generate embedding vector

---

## USE Database

```sql
USE mydb;
```

Switches the current database context.

---

## DESCRIBE

```sql
DESCRIBE users;
DESCRIBE users FROM mydb;
```

Shows table schema.

---

## SHOW

```sql
SHOW DATABASES;
SHOW TABLES;
SHOW INDEXES ON users;
```
