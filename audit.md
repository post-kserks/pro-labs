# Audit: Проблемы VaultDB dev-сборки

> Дата: 2026-07-12
> Окружение: VaultDB dev-сборка (commit master, `go build`), Python клиент v2
> Тестовый проект: DocVault — корпоративная система управления документами

---

## Контекст

Проект **DocVault** — система управления документами (договоры, счета, отчеты). Все фичи тестировались через Python-клиент по TCP-протоколу v2. Результаты проверены воспроизводимо (скрипт `test_features.py`).

### Что реально работает (подтверждено тестами)

| Фича | Синтаксис | Статус |
|------|-----------|--------|
| PRIMARY KEY | `id INT PRIMARY KEY` | ✅ |
| NOT NULL | `name TEXT NOT NULL` | ✅ |
| AUTO_INCREMENT | `id INT PRIMARY KEY AUTO_INCREMENT` | ✅ |
| SERIAL | `id SERIAL PRIMARY KEY` | ✅ |
| B-tree INDEX | `CREATE INDEX idx ON t(col)` | ✅ |
| UNIQUE INDEX | `CREATE UNIQUE INDEX idx ON t(col)` | ✅ |
| UNIQUE constraint | `ALTER TABLE t ADD CONSTRAINT uq UNIQUE (col)` | ✅ |
| FOREIGN KEY | `ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY ...` | ✅ |
| INSERT / UPDATE / DELETE | Стандартный DML | ✅ |
| UPSERT | `ON CONFLICT ... DO UPDATE SET` | ✅ |
| JSONB тип | `data JSONB` | ✅ |
| JSONB операторы | `->>`, `@>`, `?`, `\|\|` | ✅ |
| GIN индекс | `CREATE INDEX ... USING GIN (col)` | ✅ |
| MERGE | `MERGE INTO ... USING ... ON ...` | ✅ |
| MERGE VALUES | `USING (VALUES (...)) AS alias` | ✅ |
| WINDOW functions | `ROW_NUMBER`, `RANK`, `LAG`, `LEAD` | ✅ |
| CTE | `WITH cte AS (...) SELECT ...` | ✅ |
| TRIGGER (AFTER) | `CREATE TRIGGER trg AFTER INSERT ON t ...` | ✅ |
| TRANSACTIONS | `BEGIN` / `COMMIT` / `ROLLBACK` | ✅ |
| EXPLAIN | `EXPLAIN SELECT ...` | ✅ |
| PL/pgSQL | `CREATE FUNCTION ... LANGUAGE plpgsql` | ✅ |
| SHOW DATABASES/TABLES/INDEXES | `SHOW ...` | ✅ |
| DESCRIBE | `DESCRIBE table` | ✅ |
| CREATE ROLE / GRANT | `CREATE ROLE r WITH PASSWORD 'p'; GRANT ... TO r;` | ✅ |
| COPY TO JSON | `COPY t TO 'file.json' WITH (FORMAT JSON)` | ✅ |
| PARTITION BY HASH | `PARTITION BY HASH (id) PARTITIONS 4` | ✅ |
| SHOW ENCRYPTION STATUS | `SHOW ENCRYPTION STATUS` | ✅ |
| HISTORY | `HISTORY t WHERE id = 1` | ✅ |
| GROUP BY / HAVING | Стандартный SQL | ✅ |
| CASE WHEN | Стандартный SQL | ✅ |
| DISTINCT | Стандартный SQL | ✅ |
| UNION / UNION ALL | Стандартный SQL | ✅ |
| Вложенные подзапросы | `WHERE x > (SELECT AVG(...))` | ✅ |

---

## Оставшиеся проблемы

### Проблема 1: UNIQUE inline в CREATE TABLE

**Синтаксис (не работает):**
```sql
CREATE TABLE t (id INT, val TEXT, UNIQUE(val));
-- или
CREATE TABLE t (id INT, a INT, PRIMARY KEY(id), UNIQUE(a));
```

**Ошибка:**
```
invalid query syntax
```

**Рабочий workaround:**
```sql
CREATE TABLE t (id INT, val TEXT);
ALTER TABLE t ADD CONSTRAINT uq_val UNIQUE (val);
```

**Влияние:** Невозможно объявить UNIQUE ограничение в CREATE TABLE. Нужно всегда делать два запроса. Это нарушает ожидаемый PostgreSQL-синтаксис.

---

### Проблема 2: Inline FOREIGN KEY в CREATE TABLE

**Синтаксис (не работает):**
```sql
CREATE TABLE child (
    id INT PRIMARY KEY,
    parent_id INT REFERENCES parent(id) ON DELETE CASCADE
);
```

**Ошибка:**
```
invalid query syntax
```

**Рабочий workaround:**
```sql
CREATE TABLE child (id INT PRIMARY KEY, parent_id INT);
ALTER TABLE child ADD CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES parent(id) ON DELETE CASCADE;
```

**Влияние:** Невозможно объявить FK в CREATE TABLE. Аналогично UNIQUE — требует ALTER TABLE.

---

### Проблема 3: PARTITION BY RANGE

**Синтаксис (не работает):**
```sql
CREATE TABLE documents (
    id INT PRIMARY KEY,
    created_at TIMESTAMP
) PARTITION BY RANGE (created_at);
```

**Ошибка:**
```
invalid query syntax
```

**Что работает:** `PARTITION BY HASH (id) PARTITIONS N` — работает.

**Влияние:** Секционирование по диапазону дат (ключевая фича для временных данных) недоступно. HASH partitioning работает, но не подходит для запросов с фильтрацией по времени.

---

### Проблема 4: COPY TO CSV

**Синтаксис (не работает):**
```sql
COPY documents TO '/tmp/export.csv' WITH (FORMAT CSV, HEADER);
```

**Ошибка:**
```
invalid query syntax
```

**Что работает:** `COPY t TO 'file.json' WITH (FORMAT JSON)` — работает.

**Влияние:** Экспорт в CSV (самый распространенный формат для обмена данными) недоступен. Работает только JSON.

---

### Проблема 5: VERIFY AUDIT LOG

**Синтаксис:**
```sql
VERIFY AUDIT LOG;
```

**Ошибка:**
```
internal error
```

**Влияние:** Проверка целостности hash-chain аудит-лога невозможна. Это критично для соответствия требованиям аудита (SOX, PCI DSS).

---

### Проблема 6: REVOKE TOKEN

**Синтаксис:**
```sql
REVOKE TOKEN 'vdb_sk_compromised_token_here';
```

**Ошибка:**
```
internal error
```

**Влияние:** Отзыв скомпрометированных токенов через SQL невозможен. Только через HTTP API (`POST /admin/revoke-token`).

---

### Проблема 7: CREATE USER

**Синтаксис (не работает):**
```sql
CREATE USER alice WITH PASSWORD 'pass';
CREATE USER bob WITH ROLE viewer;
```

**Ошибка:**
```
invalid query syntax
```

**Что работает:** `CREATE ROLE r WITH PASSWORD 'p'; GRANT ... TO r;` — работает.

**Влияние:** Нет изоляции между пользователями и ролями. `CREATE USER` и `CREATE ROLE` — разные концепции в PostgreSQL. Отсутствие USER означает, что нельзя назначить пароль конкретному пользователю (только роли).

---

### Проблема 8: FTS MATCH

**Синтаксис (не работает):**
```sql
SELECT * FROM documents WHERE content MATCH 'договор поставка';
```

**Ошибка:**
```
invalid query syntax
```

**Что работает:** `FULLTEXT(col)` индекс создается, `body LIKE '%keyword%'` работает.

**Влияние:** Полнотекстовый поиск с синтаксисом `MATCH` недоступен. Приходится использовать `LIKE`, который не поддерживает стоп-слова, стемминг и ранжирование.

---

### Проблема 9: bm25_score()

**Синтаксис (не работает):**
```sql
SELECT bm25_score(documents, content) AS score
FROM documents WHERE content MATCH 'query' ORDER BY score DESC;
```

**Ошибка:**
```
invalid query syntax
```

**Влияние:** Ранжирование результатов поиска по релевантности невозможно. Пользователь видит неранжированный список.

---

### Проблема 10: WAL recovery page is full

**Описание:** При перезапуске сервера с данными, созданными в предыдущем сеансе, WAL recovery падает с ошибкой:

```
WAL recovery failed: wal redo: wal replay: page is full
```

**Воспроизведение:**
1. Запустить сервер, создать таблицы/данные
2. Остановить сервер
3. Запустить сервер снова с теми же данными

**Влияние:** Данные теряются после перезапуска. Приходится каждый раз пересоздавать базу.

---

## Сводная таблица

| # | Проблема | Приоритет | Workaround |
|---|----------|-----------|------------|
| 1 | UNIQUE inline в CREATE TABLE | Средний | ALTER TABLE ADD CONSTRAINT |
| 2 | FK inline в CREATE TABLE | Средний | ALTER TABLE ADD CONSTRAINT |
| 3 | PARTITION BY RANGE | Высокий | Нет (только HASH) |
| 4 | COPY TO CSV | Средний | Только JSON |
| 5 | VERIFY AUDIT LOG | Высокий | Нет |
| 6 | REVOKE TOKEN (SQL) | Средний | HTTP API |
| 7 | CREATE USER | Низкий | CREATE ROLE + GRANT |
| 8 | FTS MATCH | Высокий | LIKE (без ранжирования) |
| 9 | bm25_score() | Высокий | Нет |
| 10 | WAL recovery page is full | Критический | Пересоздание базы |

---

## Что исправлено (подтверждено тестами)

| Фича | Статус |
|------|--------|
| UNIQUE constraint (ALTER TABLE) | ✅ Работает |
| CREATE UNIQUE INDEX | ✅ Работает |
| FULLTEXT() индекс в CREATE TABLE | ✅ Работает |
| MERGE USING VALUES | ✅ Работает |
| PL/pgSQL (минимальный) | ✅ Работает |
| USING GIN индекс | ✅ Работает |
| HISTORY WHERE | ✅ Работает |
| SHOW ENCRYPTION STATUS | ✅ Работает |
| CREATE ROLE / GRANT | ✅ Работает |
| PARTITION BY HASH | ✅ Работает |
| FOREIGN KEY (ALTER TABLE) | ✅ Работает |

---

## Рекомендации

### Критические (блокируют production)
1. **WAL recovery** —必须 исправить, иначе данные теряются при перезапуске
2. **PARTITION BY RANGE** — нужен для временных данных и архивации

### Высокий приоритет
3. **FTS MATCH + bm25_score()** — критично для поиска по документам
4. **VERIFY AUDIT LOG** — критично для соответствия аудиту

### Средний приоритет
5. **UNIQUE/FK inline в CREATE TABLE** — удобство разработки
6. **COPY TO CSV** — совместимость с экосистемой
7. **REVOKE TOKEN (SQL)** — единообразие управления токенами

### Низкий приоритет
8. **CREATE USER** — можно заменить CREATE ROLE
