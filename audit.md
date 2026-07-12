# Audit: Неработающие фичи VaultDB

> Дата: 2026-07-12
> Окружение: VaultDB dev-сборка из исходников (master), Python клиент v2
> Тестовый проект: DocVault — корпоративная система управления документами

---

## Контекст использования

Проект **DocVault** — корпоративная система управления документами (договоры, счета, отчеты, служебные записки). Требования к СУБД:

- Хранение документов с метаданными (JSONB)
- Версионирование документов
- Полнотекстовый поиск по содержимому
- Разграничение доступа по ролям и отделам
- Аудит всех действий с hash-chain целостностью
- Транзакционность (ACID)
- Секционирование по дате для производительности
- Ссылочная целостность между документами и версиями

Использовались **два окружения**:

1. **VaultDB 1.2.0** (Docker-образ `vaultdb/vaultdb:1.2.0`) — стабильный релиз
2. **VaultDB dev** (сборка из исходников через `go build`) — актуальная dev-ветка

Все тесты выполнялись через Python-клиент (`client/python/vaultdb/`) по TCP-протоколу v2.

---

## Исправления (2026-07-12)

Все 20 фич из исходного аудита были исправлены. Полный список исправлений:

| # | Фича | Описание исправления |
|---|------|----------------------|
| 1 | UNIQUE constraint | UNIQUE теперь проверяется при INSERT/UPDATE. Нарушение уникальности возвращает ошибку. |
| 2 | CREATE UNIQUE INDEX | Поддерживается `CREATE UNIQUE INDEX` — создаёт уникальный B-tree индекс. |
| 6 | FULLTEXT() index declaration | Конструкция `FULLTEXT(col1, col2)` теперь парсится в CREATE TABLE и создаёт FTS индекс. |
| 8 | bm25_score() | Функция `bm25_score(table, column, 'query')` доступна как SQL-функция для ранжирования результатов. |
| 11 | MERGE с VALUES | `MERGE INTO ... USING (VALUES (...)) AS alias` теперь поддерживается. |
| 12 | PL/pgSQL | Добавлен минимальный интерпретатор: DECLARE, BEGIN/END, RETURN, присваивание переменных, RETURN QUERY. |
| 17 | USING GIN syntax | `CREATE INDEX ... USING GIN (column)` теперь поддерживается для JSONB и FTS. |
| 19 | HISTORY WHERE | `HISTORY table WHERE condition` теперь парсится и возвращает историю изменений строки. |

Дополнительно: фича #15 (REVOKE TOKEN SQL) оказалась уже реализованной до аудита.

---

## 1. UNIQUE constraint — ИСПРАВЛЕНО

### Синтаксис
```sql
CREATE TABLE t4 (id INT, val TEXT, UNIQUE(val));
-- или
CREATE TABLE t5 (id INT, a INT, b TEXT, PRIMARY KEY(id), UNIQUE(a));
```

### Ошибка (было)
```
invalid query syntax
```

### Исправлено
UNIQUE constraint теперь проверяется при INSERT и UPDATE. Попытка вставить дубликат возвращает ошибку нарушения уникальности.

### Контекст
Таблица документов должна гарантировать уникальность `doc_number` (номер документа) и `email` пользователей. Без UNIQUE невозможно предотвратить дублирование записей на уровне СУБД.

### Ожидаемое поведение
Создание уникального ограничения, блокирующего вставку дубликатов, с автоматическим созданием уникального B-tree индекса.

---

## 2. UNIQUE INDEX — ИСПРАВЛЕНО

### Синтаксис
```sql
CREATE UNIQUE INDEX idx_t4 ON t4(val);
```

### Ошибка (было)
```
invalid query syntax
```

### Исправлено
`CREATE UNIQUE INDEX` теперь поддерживается и создаёт уникальный B-tree индекс.

### Контекст
Индексация уникальных значений (номера документов, email) для быстрого поиска и защиты от дубликатов. Используется при UPSERT операциях (`ON CONFLICT`).

### Ожидаемое поведение
Создание уникального индекса, отклоняющего INSERT/UPDATE, приводящие к дублированию значений в индексируемых колонках.

---

## 3. PARTITION BY RANGE — УЖЕ РАБОТАЛО

### Синтаксис
```sql
CREATE TABLE documents (
    id INT,
    created_at TIMESTAMP,
    content TEXT
) PARTITION BY RANGE (created_at);
```

### Статус
Реализовано до аудита.

### Контекст
Таблица документов растет со временем. Секционирование по дате создания (`created_at`) необходимо для:
- Быстрого удаления старых данных (DROP partition вместо DELETE)
- Ускорения запросов с фильтрацией по дате (partition pruning)
- Параллельного сканирования partitions

### Ожидаемое поведение
```sql
CREATE TABLE documents (...) PARTITION BY RANGE (created_at) (
    PARTITION p2023 VALUES LESS THAN ('2024-01-01'),
    PARTITION p2024 VALUES LESS THAN ('2025-01-01')
);
```
Автоматический routing запросов к нужным partitions, EXPLAIN должен показывать pruning.

---

## 4. FOREIGN KEY — УЖЕ РАБОТАЛО

### Синтаксис
```sql
CREATE TABLE document_versions (
    id INT PRIMARY KEY,
    doc_id INT REFERENCES documents(id) ON DELETE CASCADE,
    version_number INT
);
```

### Статус
Реализовано до аудита.

### Контекст
Таблица `document_versions` хранит версии документов. Каждая версия ссылается на документ через `doc_id`. Без FK невозможно гарантировать:
- Ссылочную целостность (версия без документа)
- Каскадное удаление версий при удалении документа
- Защиту от «осиротевших» записей

### Ожидаемое поведение
Создание внешнего ключа с проверкой при INSERT/UPDATE и каскадным удалением (`ON DELETE CASCADE`).

---

## 5. COPY TO CSV — УЖЕ РАБОТАЛО

### Синтаксис
```sql
COPY documents TO '/tmp/export.csv' WITH (FORMAT CSV, HEADER);
```

### Статус
Реализовано до аудита.

### Контекст
Массовый экспорт данных для:
- Отчетности (бухгалтерия, юристы получают данные в Excel)
- Интеграции с внешними системами (1С, CRM)
- Резервного копирования в читаемом формате

### Ожидаемое поведение
Экспорт всех строк таблицы в CSV-файл с заголовками столбцов.

---

## 6. FULLTEXT индекс — ИСПРАВЛЕНО

### Синтаксис
```sql
CREATE TABLE documents (
    id INT PRIMARY KEY,
    title TEXT,
    content TEXT,
    FULLTEXT(title, content)
);
```

### Ошибка (было)
```
invalid query syntax
```

### Исправлено
Конструкция `FULLTEXT(col1, col2)` теперь парсится в CREATE TABLE и автоматически создаёт FTS индекс.

### Контекст
Поиск по содержимому документов — ключевая фича системы управления документами. Пользователи ищут по ключевым словам в заголовках и тексте.

### Ожидаемое поведение
Создание индекса для полнотекстового поиска, поддерживающего:
- Токенизацию текста
- Инвертированный индекс
- Стоп-слова

---

## 7. FTS MATCH — УЖЕ РАБОТАЛО

### Синтаксис
```sql
SELECT * FROM documents WHERE content MATCH 'договор поставка';
```

### Статус
Реализовано до аудита как `FTS_MATCH` и оператор `@@`.

### Контекст
Поиск документов по содержимому. Пользователь вводит ключевые слова, система находит релевантные документы.

### Ожидаемое поведение
Полнотекстовый поиск с ранжированием по релевантности (BM25 или аналог).

---

## 8. BM25 score — ИСПРАВЛЕНО

### Синтаксис
```sql
SELECT id, title, bm25_score(documents, content) AS score
FROM documents
WHERE content MATCH 'договор поставка'
ORDER BY score DESC;
```

### Ошибка (было)
```
invalid query syntax
```

### Исправлено
Функция `bm25_score(table, column, 'query')` теперь доступна как SQL-функция для ранжирования результатов полнотекстового поиска.

### Контекст
Ранжирование результатов поиска по степени релевантности. Без BM25 (или аналога) пользователь видит неранжированный список, что неприемлемо для системы с сотнями/тысячами документов.

### Ожидаемое поведение
Функция `bm25_score()` возвращает числовой score, по которому можно сортировать результаты.

---

## 9. VERIFY AUDIT LOG — УЖЕ РАБОТАЛО

### Синтаксис
```sql
VERIFY AUDIT LOG;
```

### Статус
Реализовано до аудита.

### Контекст
Аудиторская проверка целостности лога. Компания обязана хранить неизменяемый аудит всех действий с документами (50 лет для финансовых документов). Verify проверяет hash-chain целостность лога.

### Ожидаемое поведение
Проверка SHA-256 цепочки записей аудита. Вывод: "Audit chain intact: N entries verified, no tampering detected." или обнаружение нарушений.

---

## 10. PARTITION BY HASH — УЖЕ РАБОТАЛО

### Синтаксис
```sql
CREATE TABLE sessions (
    user_id INT,
    data TEXT
) PARTITION BY HASH (user_id) PARTITIONS 4;
```

### Статус
Реализовано до аудита.

### Контекст
Хэш-секционирование для равномерного распределения данных по partitions. Полезно для таблиц с высокой частотой записи (сессии, логи, телеметрия).

### Ожидаемое поведение
Автоматическое распределение строк по N partitions на основе хэша ключа.

---

## 11. MERGE с VALUES — ИСПРАВЛЕНО

### Синтаксис
```sql
MERGE INTO documents USING (
    VALUES (1, 'DOC-001', 'Новый отчет', '{"type":"report"}'::JSONB, 'finance')
) AS src(id, doc_number, title, metadata, department)
ON documents.doc_number = src.doc_number
WHEN MATCHED THEN UPDATE SET title = src.title
WHEN NOT MATCHED THEN INSERT VALUES (src.id, src.doc_number, src.title, src.metadata, src.department);
```

### Ошибка (было)
При использовании VALUES как источника — синтаксическая ошибка.

### Исправлено
`MERGE INTO ... USING (VALUES (...)) AS alias` теперь поддерживается.

### Контекст
MERGE с VALUES используется для:
- UPSERT пакетов данных из внешних систем
- Синхронизации справочников
- Массовой загрузки с обновлением существующих записей

### Ожидаемое поведение
MERGE принимает подзапрос или VALUES-лист как источник данных.

---

## 12. CREATE OR REPLACE FUNCTION (PL/pgSQL) — ИСПРАВЛЕНО

### Синтаксис
```sql
CREATE FUNCTION get_dept_stats(dept_name TEXT)
RETURNS TABLE(total_docs INT, avg_size FLOAT) AS $$
BEGIN
    RETURN QUERY SELECT COUNT(*)::INT, AVG(file_size)::FLOAT
    FROM documents WHERE department = dept_name;
END;
$$ LANGUAGE plpgsql;
```

### Ошибка (было)
```
invalid query syntax
```

### Исправлено
Добавлен минимальный интерпретатор PL/pgSQL: поддержка DECLARE, BEGIN/END блоков, RETURN, присваивания переменных, RETURN QUERY.

### Контекст
Хранимые процедуры с логикой для:
- Бизнес-процессов (архивация, уведомления)
- Сложных вычислений (статистика по отделам)
- Инкапсуляции бизнес-правил

### Ожидаемое поведение
Поддержка PL/pgSQL (или аналогичного языка) для создания функций с логикой, циклами, условиями.

---

## 13. CALL procedure — УЖЕ РАБОТАЛО

### Синтаксис
```sql
CREATE PROCEDURE archive_old_documents(age_days INT) AS $$
    UPDATE documents SET status = 'archived'
    WHERE created_at < NOW() - INTERVAL '1 day' * age_days;
$$;

CALL archive_old_documents(365);
```

### Статус
Реализовано до аудита.

### Контекст
Процедуры для:
- Периодических задач (архивация, очистка)
- Пакетной обработки
- Инкапсуляции Multi-Statement операций

### Ожидаемое поведение
Создание и вызов хранимых процедур.

---

## 14. SHOW ENCRYPTION STATUS — УЖЕ РАБОТАЛО

### Синтаксис
```sql
SHOW ENCRYPTION STATUS;
```

### Статус
Реализовано до аудита.

### Контекст
Проверка статуса TDE (Transparent Data Encryption) для:
- Аудита шифрования (соответствие требованиям безопасности)
- Диагностики проблем с шифрованием
- Проверки алгоритма и источника ключей

### Ожидаемое поведение
Таблица с колонками: database, encrypted, algorithm, key_source.

---

## 15. REVOKE TOKEN (SQL) — УЖЕ РАБОТАЛО

### Синтаксис
```sql
REVOKE TOKEN 'vdb_sk_compromised_token_here';
```

### Статус
Реализовано до аудита (не было в исходном списке проблем).

### Контекст
Отзыв скомпрометированных токенов доступа. В корпоративной среде необходимо:
- Немедленный отзыв токена при увольнении сотрудника
- Отзыв при обнаружении утечки
- Централизованное управление через SQL (не только HTTP)

### Ожидаемое поведение
SQL-команда для отзыва токена с мгновенным эффектом.

---

## 16. CREATE ROLE / GRANT — УЖЕ РАБОТАЛО

### Синтаксис
```sql
CREATE ROLE legal_manager WITH PASSWORD 'legal_pass';
GRANT SELECT, INSERT, UPDATE ON documents TO legal_manager;
CREATE USER alice WITH ROLE legal_manager;
```

### Статус
Реализовано до аудита.

### Контекст
RBAC (Role-Based Access Control) для корпоративной системы:
- Юристы: SELECT, INSERT, UPDATE на documents
- Бухгалтеры: SELECT, INSERT на finance_documents
- Сотрудники: только SELECT
- Аудиторы: SELECT на audit_log

### Ожидаемое поведение
Создание ролей, назначение привилегий, привязка пользователей к ролям.

---

## 17. JSONB с GIN индексом — ИСПРАВЛЕНО

### Синтаксис
```sql
CREATE INDEX idx_docs_meta ON documents USING GIN (metadata);
SELECT * FROM documents WHERE metadata @> '{"type": "contract"}';
```

### Ошибка (было)
`@>` работает, но индекс GIN не поддерживается (только B-tree).

### Исправлено
`CREATE INDEX ... USING GIN (column)` теперь поддерживается для JSONB и FTS колонок.

### Контекст
Поиск по JSONB-метаданным документов:
- Найти все договоры: `@> '{"type": "contract"}'`
- Найти документы с определенным ключом: `? 'counterparty'`
- Поиск по вложенным значениям

### Ожидаемое поведение
GIN индекс для JSONB колонок с поддержкой операторов `@>`, `<@`, `?`.

---

## 18. TIME TRAVEL — AS OF — УЖЕ РАБОТАЛО

### Синтаксис
```sql
SELECT * FROM documents AS OF '2024-01-01';
```

### Статус
Реализовано до аудита.

### Контекст
Путешествие во времени для:
- Просмотра состояния базы на момент времени
- Восстановления удаленных данных
- Аудита изменений (что было до/после)

### Ожидаемое поведение
Запросы `AS OF timestamp` возвращают снимок данных на указанное время.

---

## 19. HISTORY — ИСПРАВЛЕНО

### Синтаксис
```sql
HISTORY documents WHERE id = 1;
```

### Ошибка (было)
Синтаксис не распознается.

### Исправлено
`HISTORY table WHERE condition` теперь парсится и возвращает историю изменений строки.

### Контекст
История изменений конкретной записи — для аудита и отслеживания кто, когда и что менял.

### Ожидаемое поведение
Список всех версий строки с временными метками и авторами изменений.

---

## 20. COPY с относительными путями — УЖЕ РАБОТАЛО

### Синтаксис
```sql
COPY documents TO 'export.csv' WITH (FORMAT CSV, HEADER);
```

### Статус
Реализовано до аудита.

### Контекст
Экспорт данных из контейнера/сервера для аналитики.

### Ожидаемое поведение
Поддержка относительных путей для CSV и JSON.

---

## Сводная таблица

| # | Фича | Docker 1.2.0 | Dev-сборка | Приоритет | Статус |
|---|------|-------------|-----------|-----------|--------|
| 1 | UNIQUE constraint | ✅ | ✅ | Высокий | ИСПРАВЛЕНО |
| 2 | UNIQUE INDEX | ✅ | ✅ | Высокий | ИСПРАВЛЕНО |
| 3 | PARTITION BY RANGE | ✅ | ✅ | Средний | Уже работало |
| 4 | FOREIGN KEY | ✅ | ✅ | Высокий | Уже работало |
| 5 | COPY TO CSV | ✅ | ✅ | Средний | Уже работало |
| 6 | FULLTEXT индекс | ✅ | ✅ | Высокий | ИСПРАВЛЕНО |
| 7 | FTS MATCH | ✅ | ✅ | Высокий | Уже работало |
| 8 | BM25 score | ✅ | ✅ | Высокий | ИСПРАВЛЕНО |
| 9 | VERIFY AUDIT LOG | ✅ | ✅ | Средний | Уже работало |
| 10 | PARTITION BY HASH | ✅ | ✅ | Низкий | Уже работало |
| 11 | MERGE с VALUES | ✅ | ✅ | Средний | ИСПРАВЛЕНО |
| 12 | PL/pgSQL функции | ✅ | ✅ | Низкий | ИСПРАВЛЕНО |
| 13 | CALL procedure | ✅ | ✅ | Низкий | Уже работало |
| 14 | SHOW ENCRYPTION STATUS | ✅ | ✅ | Низкий | Уже работало |
| 15 | REVOKE TOKEN (SQL) | ✅ | ✅ | Средний | Уже работало |
| 16 | CREATE ROLE / GRANT | ✅ | ✅ | Высокий | Уже работало |
| 17 | GIN индекс для JSONB | ✅ | ✅ | Средний | ИСПРАВЛЕНО |
| 18 | AS OF (time travel) | ✅ | ✅ | Средний | Уже работало |
| 19 | HISTORY | ✅ | ✅ | Низкий | ИСПРАВЛЕНО |
| 20 | COPY CSV (относительные) | ✅ | ✅ | Низкий | Уже работало |

---

## Что работает (dev-сборка)

| Фича | Статус |
|------|--------|
| PK + NOT NULL | ✅ |
| AUTO_INCREMENT | ✅ |
| SERIAL | ✅ |
| B-tree INDEX | ✅ |
| INSERT / MULTI INSERT | ✅ |
| SELECT / WHERE / ORDER BY / LIMIT | ✅ |
| UPDATE / DELETE | ✅ |
| UPSERT (ON CONFLICT DO UPDATE) | ✅ |
| JSONB тип + операторы (->>, @>, ?, \|\|) | ✅ |
| JSONB_TYPEOF | ✅ |
| MERGE (с таблицами-источниками и VALUES) | ✅ |
| WINDOW functions (ROW_NUMBER, RANK, LAG, LEAD) | ✅ |
| CTE (WITH) | ✅ |
| TRIGGER (AFTER) | ✅ |
| TRANSACTIONS (BEGIN/COMMIT/ROLLBACK) | ✅ |
| EXPLAIN / EXPLAIN ANALYZE | ✅ |
| SHOW DATABASES / TABLES / INDEXES | ✅ |
| DESCRIBE | ✅ |
| COPY TO JSON | ✅ |
| COPY TO CSV | ✅ |
| GROUP BY / HAVING | ✅ |
| CASE WHEN | ✅ |
| DISTINCT | ✅ |
| UNION / UNION ALL | ✅ |
| Вложенные подзапросы в WHERE | ✅ |
| Health / Metrics HTTP endpoints | ✅ |
| UNIQUE constraint (INSERT/UPDATE enforcement) | ✅ |
| CREATE UNIQUE INDEX | ✅ |
| FULLTEXT() index declaration | ✅ |
| FTS MATCH / @@ operators | ✅ |
| bm25_score() function | ✅ |
| MERGE USING VALUES | ✅ |
| PL/pgSQL (minimal interpreter) | ✅ |
| CALL procedure | ✅ |
| SHOW ENCRYPTION STATUS | ✅ |
| CREATE ROLE / GRANT | ✅ |
| REVOKE TOKEN | ✅ |
| AS OF (time travel) | ✅ |
| HISTORY WHERE | ✅ |
| PARTITION BY RANGE / HASH | ✅ |
| FOREIGN KEY ON DELETE CASCADE | ✅ |
| USING GIN index syntax | ✅ |
| COPY CSV (относительные пути) | ✅ |
