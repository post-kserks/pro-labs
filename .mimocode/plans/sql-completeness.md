# VaultDB — План доработки SQL до 80%

## Текущее покрытие

| Категория | Сейчас | Цель |
|-----------|--------|------|
| DDL | 40% | 70% |
| DML | 60% | 85% |
| SELECT | 70% | 90% |
| Функции | 30% | 60% |
| Агрегаты | 30% | 60% |
| Window functions | 25% | 50% |
| Транзакции | 50% | 70% |

## Приоритетные задачи

### P0: Критичные (нужны для基本工作оспособности)

#### 1. DISTINCT (1 час)
- `SELECT DISTINCT col FROM t`
- `SELECT DISTINCT ON (col) ...` (PostgreSQL)
- **Файл:** `executor/commands_select.go`

#### 2. LEFT/RIGHT/FULL JOIN корректность (2-3 часа)
- Сейчас nested loop только для INNER/CROSS
- LEFT JOIN: добавить NULL-fill для unmatched строк
- RIGHT/FULL JOIN: аналогично
- **Файл:** `executor/commands_select.go:233-303`

#### 3. EXISTS subquery (2 часа)
- `WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t1.id)`
- **Файлы:** `parser/ast.go`, `executor/eval.go`

#### 4. BETWEEN ... AND ... (1 час)
- `WHERE col BETWEEN 10 AND 20`
- **Файл:** `parser/parser.go`, `executor/eval.go`

#### 5. NULLIF function (30 минут)
- `NULLIF(a, b)` — возвращает NULL если a = b
- **Файл:** `executor/eval.go`

#### 6. CORRELATED subqueries (3-4 часа)
- Подзапросы ссылаются на внешние столбцы
- **Файл:** `executor/eval.go:443-476`

### P1: Высокий приоритет

#### 7. INSERT ... SELECT (2-3 часа)
- `INSERT INTO t2 SELECT * FROM t1 WHERE ...`
- **Файл:** `executor/commands_dml.go`

#### 8. INSERT ON CONFLICT парсинг (2 часа)
- AST есть, нужен парсинг в parser.go
- **Файл:** `parser/parser.go`

#### 9. RETURNING clause (2-3 часа)
- `INSERT ... RETURNING *`
- `UPDATE ... RETURNING *`
- `DELETE ... RETURNING *`
- **Файлы:** `parser/ast.go`, `executor/commands_dml.go`

#### 10. String functions (2-3 часа)
- `LEFT(str, n)`, `RIGHT(str, n)`
- `LPAD`, `RPAD`
- `INITCAP`
- `REVERSE`
- `SPLIT_PART`
- **Файл:** `executor/eval.go`

#### 11. Numeric functions (2-3 часа)
- `MOD(a, b)` / `a % b`
- `POWER(a, b)` / `a ** b`
- `SQRT(n)`
- `LN(n)`, `LOG(n)`, `LOG10(n)`
- `GREATEST(a, b, ...)`, `LEAST(a, b, ...)`
- `SIGN(n)`
- **Файл:** `executor/eval.go`

#### 12. Date/Time functions (3-4 часа)
- `CURRENT_DATE`, `CURRENT_TIME`, `CURRENT_TIMESTAMP`
- `DATE_TRUNC(part, timestamp)`
- `EXTRACT(part FROM timestamp)`
- `AGE(timestamp)`
- `TO_DATE(str, format)`, `TO_CHAR(timestamp, format)`
- **Файл:** `executor/eval.go`

#### 13. Additional aggregates (2-3 часа)
- `STRING_AGG(col, delimiter)`
- `ARRAY_AGG(col)`
- `BOOL_AND(col)`, `BOOL_OR(col)`
- `STDDEV(col)`, `VARIANCE(col)`
- **Файл:** `executor/aggregates.go`

#### 14. Additional window functions (2-3 часа)
- `LAG(col, n)`, `LEAD(col, n)`
- `FIRST_VALUE(col)`, `LAST_VALUE(col)`
- `NTILE(n)`
- `DENSE_RANK()`
- **Файл:** `executor/commands_select.go`

### P2: Средний приоритет

#### 15. CTE execution (3-4 часа)
- Parser должен парсить WITH clause
- Executor должен materialize CTE
- **Файлы:** `parser/parser.go`, `executor/commands_select.go`

#### 16. MERGE statement (4-5 часов)
- `MERGE INTO t USING s ON condition WHEN MATCHED THEN UPDATE ... WHEN NOT MATCHED THEN INSERT ...`
- **Файлы:** `parser/ast.go`, `parser/parser.go`, новый executor command

#### 17. TRUNCATE TABLE (1 час)
- **Файлы:** `parser/ast.go`, `parser/parser.go`, `executor/commands_ddl.go`

#### 18. CREATE VIEW (2-3 часа)
- **Файлы:** `parser/ast.go`, `parser/parser.go`, `executor/commands_ddl.go`, `storage/storage.go`

#### 19. Column constraints (3-4 часа)
- `NOT NULL`, `DEFAULT`, `UNIQUE`, `PRIMARY KEY` в CREATE TABLE
- **Файлы:** `parser/ast.go`, `executor/commands_ddl.go`

#### 20. SAVEPOINT (2-3 часа)
- `SAVEPOINT sp`, `ROLLBACK TO SAVEPOINT sp`, `RELEASE SAVEPOINT sp`
- **Файлы:** `parser/ast.go`, `parser/parser.go`, `executor/commands_tx.go`

#### 21. UPDATE ... FROM (2 часа)
- `UPDATE t1 SET col = t2.val FROM t2 WHERE t1.id = t2.id`
- **Файлы:** `parser/ast.go`, `executor/commands_dml.go`

#### 22. Regex operators (2-3 часа)
- `~` (POSIX regex), `~*` (case-insensitive)
- `SIMILAR TO`
- **Файлы:** `parser/parser.go`, `executor/eval.go`

### P3: Низкий приоритет

#### 23. CREATE SCHEMA
#### 24. CREATE SEQUENCE
#### 25. CREATE TRIGGER
#### 26. GRANT / REVOKE
#### 27. COPY / IMPORT / EXPORT
#### 28. SET TRANSACTION ISOLATION LEVEL
#### 29. LATERAL joins
#### 30. GROUPING SETS / ROLLUP / CUBE
#### 31. SELECT INTO
#### 32. FETCH FIRST N ROWS ONLY

---

## Порядок реализации

### Неделя 1: P0 задачи
1. DISTINCT
2. BETWEEN
3. NULLIF
4. LEFT/RIGHT/FULL JOIN
5. EXISTS
6. CORRELATED subqueries

### Неделя 2: P1 часть 1
7. INSERT ... SELECT
8. INSERT ON CONFLICT парсинг
9. RETURNING
10. String functions

### Неделя 3: P1 часть 2
11. Numeric functions
12. Date/Time functions
13. Additional aggregates
14. Additional window functions

### Неделя 4-5: P2 задачи
15-22. CTE, MERGE, TRUNCATE, VIEW, constraints, SAVEPOINT, UPDATE FROM, regex

### Неделя 6+: P3 задачи
23-32. Остальное
