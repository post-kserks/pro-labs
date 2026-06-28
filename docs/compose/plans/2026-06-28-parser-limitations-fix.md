# VaultDB — Исправление ограничений парсера и executor

> **Цель:** Устранить все known limitations в парсере и executor, сделать SQL движок полностью функциональным.

## Фаза 1: Парсер — синтаксические ограничения

### 1.1 STRING_AGG — multi-arg aggregates
- **Файл:** `parser/parse_utils.go:356-366`
- **Проблема:** STRING_AGG возвращает `FunctionCall` вместо `AggregateExpr`. Парсер не может отличить aggregate от function.
- **Фикс:** Вернуть `AggregateExpr` (сделано ранее, но откатилось). Или перенаправить через IDENT path.
- **Тест:** `SELECT STRING_AGG(name, ',') FROM heroes;`

### 1.2 CROSS JOIN — пропускает ключевое слово JOIN
- **Файл:** `parser/parse_select.go:304`
- **Проблема:** `if strings.ToUpper(joinType) != "CROSS"` пропускает `p.consume(TOKEN_JOIN, "JOIN")`.
- **Фикс:** Убрать guard или добавить отдельную ветку для CROSS.
- **Тест:** `SELECT * FROM a CROSS JOIN b;`

### 1.3 Nested CTE — parseSelect не обрабатывает WITH
- **Файл:** `parser/parse_select.go:51`
- **Проблема:** `parseSelect()` не обрабатывает `TOKEN_WITH`. Вызывает `parseSelect()` instead of `parseStatement()`.
- **Фикс:** Вызывать `parseStatement()` и обрабатывать `*CTEStatement`.
- **Тест:** `WITH cte1 AS (WITH cte2 AS (...) SELECT ...) SELECT * FROM cte1;`

### 1.4 UNION в подзапросах — нет parseSetOperation
- **Файл:** `parser/parse_utils.go:272-280`
- **Проблема:** `parsePrimary` для subquery вызывает `parseSelect()` без `parseSetOperation()`.
- **Фикс:** Добавить `parseSetOperation(stmt)` передconsumption `)`.
- **Тест:** `SELECT * FROM (SELECT id FROM t1 UNION SELECT id FROM t2) sub;`

### 1.5 SUBSTRING(x FROM n FOR m) — нет синтаксиса с ключевыми словами
- **Файл:** `lexer/lexer.go` + `parser/parse_utils.go`
- **Проблема:** Нет `TOKEN_SUBSTRING`, нет handler в `parsePrimary`.
- **Фикс:** Добавить токен и парсинг `SUBSTRING(expr FROM start [FOR length])`.
- **Тест:** `SELECT SUBSTRING('hello' FROM 2 FOR 3);`

### 1.6 INT64_MIN/MAX как именованные константы
- **Файл:** `parser/parse_utils.go` + executor
- **Проблема:** Парсер не знает констант INT64_MIN/INT64_MAX.
- **Фикс:** Добавить predefined constants в парсер или executor mapping.

---

## Фаза 2: Executor — логические ограничения

### 2.1 CTE с агрегатами/join
- **Файл:** `executor/cte.go:201-233`
- **Проблема:** `ExecuteSelectWithCTE` возвращает raw CTE result без применения внешнего SELECT.
- **Фикс:** Материализовать CTE как temp table, затем выполнить внешний SELECT.
- **Тест:** `WITH x AS (SELECT id FROM t) SELECT COUNT(*) FROM x;`

### 2.2 HAVING с COUNT(*) напрямую
- **Файл:** `executor/select_aggr.go`
- **Проблема:** HAVING evaluator сравнивает с column aliases, не с aggregate results.
- **Фикс:** Поддержать aggregate expressions в HAVING.
- **Тест:** `SELECT dept, COUNT(*) FROM t GROUP BY dept HAVING COUNT(*) > 5;`

### 2.3 JSONB @> для объектов
- **Файл:** `executor/eval_json.go`
- **Проблема:** `@>` работает только для массивов, не для объектов.
- **Фикс:** Реализовать object containment check.

### 2.4 CompositeIndex.SetColumn
- **Файл:** `index/composite.go:39`
- **Проблема:** Пустой метод при RENAME COLUMN.
- **Фикс:** Обновлять column list.

---

## Фаза 3: Производительность парсера

### 3.1 Lazy tokenization
- **Файл:** `parser/parser.go:110-120`
- **Проблема:** Eager tokenization всего input в `[]Token`.
- **Фикс:** Streaming lexer или aggressive pre-allocation.

### 3.2 Line/Col cache
- **Файл:** `lexer/lexer.go:222-235`
- **Проблема:** 16 bytes per character для line/col tracking.
- **Фикс:** Lazy computation при ошибке.

### 3.3 isReservedKeyword allocation
- **Файл:** `parser/parse_utils.go:721-728`
- **Проблема:** `strings.ToUpper` на каждый вызов.
- **Фикс:** Использовать map или pre-computed set.

---

## Глобальные ограничения

- Go 1.23, только `gopkg.in/yaml.v3`
- Не менять публичные API
- Каждое изменение коммитится отдельно
- Все тесты должны проходить
