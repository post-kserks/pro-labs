# Audit: Неработающие фичи VaultDB

> Дата: 2026-07-11
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

## 1. UNIQUE constraint

### Синтаксис
```sql
CREATE TABLE t4 (id INT, val TEXT, UNIQUE(val));
-- или
CREATE TABLE t5 (id INT, a INT, b TEXT, PRIMARY KEY(id), UNIQUE(a));
```

### Ошибка
```
invalid query syntax
```

### Контекст
Таблица документов должна гарантировать уникальность `doc_number` (номер документа) и `email` пользователей. Без UNIQUE невозможно предотвратить дублирование записей на уровне СУБД.

### Ожидаемое поведение
Создание уникального ограничения, блокирующего вставку дубликатов, с автоматическим созданием уникального B-tree индекса.

---

## 2. UNIQUE INDEX

### Синтаксис
```sql
CREATE UNIQUE INDEX idx_t4 ON t4(val);
```

### Ошибка
```
invalid query syntax
```

### Контекст
Индексация уникальных значений (номера документов, email) для быстрого поиска и защиты от дубликатов. Используется при UPSERT операциях (`ON CONFLICT`).

### Ожидаемое поведение
Создание уникального индекса, отклоняющего INSERT/UPDATE, приводящие к дублированию значений в индексируемых колонках.

---

## 3. PARTITION BY RANGE

### Синтаксис
```sql
CREATE TABLE documents (
    id INT,
    created_at TIMESTAMP,
    content TEXT
) PARTITION BY RANGE (created_at);
```

### Ошибка
```
invalid query syntax
```

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

## 4. FOREIGN KEY

### Синтаксис
```sql
CREATE TABLE document_versions (
    id INT PRIMARY KEY,
    doc_id INT REFERENCES documents(id) ON DELETE CASCADE,
    version_number INT
);
```

### Ошибка
```
invalid query syntax
```

### Контекст
Таблица `document_versions` хранит версии документов. Каждая версия ссылается на документ через `doc_id`. Без FK невозможно гарантировать:
- Ссылочную целостность (версия без документа)
- Каскадное удаление версий при удалении документа
- Защиту от «осиротевших» записей

### Ожидаемое поведение
Создание внешнего ключа с проверкой при INSERT/UPDATE и каскадным удалением (`ON DELETE CASCADE`).

---

## 5. COPY TO CSV

### Синтаксис
```sql
COPY documents TO '/tmp/export.csv' WITH (FORMAT CSV, HEADER);
```

### Ошибка
```
invalid query syntax
```

### Контекст
Массовый экспорт данных для:
- Отчетности (бухгалтерия, юристы получают данные в Excel)
- Интеграции с внешними системами (1С, CRM)
- Резервного копирования в читаемом формате

### Ожидаемое поведение
Экспорт всех строк таблицы в CSV-файл с заголовками столбцов.

---

## 6. FULLTEXT индекс

### Синтаксис
```sql
CREATE TABLE documents (
    id INT PRIMARY KEY,
    title TEXT,
    content TEXT,
    FULLTEXT(title, content)
);
```

### Ошибка
```
invalid query syntax
```

### Контекст
Поиск по содержимому документов — ключевая фича системы управления документами. Пользователи ищут по ключевым словам в заголовках и тексте.

### Ожидаемое поведение
Создание индекса для полнотекстового поиска, поддерживающего:
- Токенизацию текста
- Инвертированный индекс
- Стоп-слова

---

## 7. FTS MATCH

### Синтаксис
```sql
SELECT * FROM documents WHERE content MATCH 'договор поставка';
```

### Ошибка
```
invalid query syntax
```

### Контекст
Поиск документов по содержимому. Пользователь вводит ключевые слова, система находит релевантные документы.

### Ожидаемое поведение
Полнотекстовый поиск с ранжированием по релевантности (BM25 или аналог).

---

## 8. BM25 score

### Синтаксис
```sql
SELECT id, title, bm25_score(documents, content) AS score
FROM documents
WHERE content MATCH 'договор поставка'
ORDER BY score DESC;
```

### Ошибка
```
invalid query syntax
```

### Контекст
Ранжирование результатов поиска по степени релевантности. Без BM25 (или аналога) пользователь видит неранжированный список, что неприемлемо для системы с сотнями/тысячами документов.

### Ожидаемое поведение
Функция `bm25_score()` возвращает числовой score, по которому можно сортировать результаты.

---

## 9. VERIFY AUDIT LOG

### Синтаксис
```sql
VERIFY AUDIT LOG;
```

### Ошибка
```
internal error
```

### Контекст
Аудиторская проверка целостности лога. Компания обязана хранить неизменяемый аудит всех действий с документами (50 лет для финансовых документов). Verify проверяет hash-chain целостность лога.

### Ожидаемое поведение
Проверка SHA-256 цепочки записей аудита. Вывод: "Audit chain intact: N entries verified, no tampering detected." или обнаружение нарушений.

---

## 10. PARTITION BY HASH (дополнительно)

### Синтаксис
```sql
CREATE TABLE sessions (
    user_id INT,
    data TEXT
) PARTITION BY HASH (user_id) PARTITIONS 4;
```

### Ошибка
```
invalid query syntax
```

### Контекст
Хэш-секционирование для равномерного распределения данных по partitions. Полезно для таблиц с высокой частотой записи (сессии, логи, телеметрия).

### Ожидаемое поведение
Автоматическое распределение строк по N partitions на основе хэша ключа.

---

## 11. MERGE с VALUES (дополнительно)

### Синтаксис
```sql
MERGE INTO documents USING (
    VALUES (1, 'DOC-001', 'Новый отчет', '{"type":"report"}'::JSONB, 'finance')
) AS src(id, doc_number, title, metadata, department)
ON documents.doc_number = src.doc_number
WHEN MATCHED THEN UPDATE SET title = src.title
WHEN NOT MATCHED THEN INSERT VALUES (src.id, src.doc_number, src.title, src.metadata, src.department);
```

### Ошибка
При использовании VALUES как источника — синтаксическая ошибка.

### Контекст
MERGE с VALUES используется для:
- UPSERT пакетов данных из внешних систем
- Синхронизации справочников
- Массовой загрузки с обновлением существующих записей

### Ожидаемое поведение
MERGE принимает подзапрос или VALUES-лист как источник данных.

---

## 12. CREATE OR REPLACE FUNCTION (PL/pgSQL)

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

### Ошибка
```
invalid query syntax
```

### Контекст
Хранимые процедуры с логикой для:
- Бизнес-процессов (архивация, уведомления)
- Сложных вычислений (статистика по отделам)
- Инкапсуляции бизнес-правил

### Ожидаемое поведение
Поддержка PL/pgSQL (или аналогичного языка) для создания функций с логикой, циклами, условиями.

---

## 13. CALL procedure

### Синтаксис
```sql
CREATE PROCEDURE archive_old_documents(age_days INT) AS $$
    UPDATE documents SET status = 'archived'
    WHERE created_at < NOW() - INTERVAL '1 day' * age_days;
$$;

CALL archive_old_documents(365);
```

### Ошибка
```
invalid query syntax
```

### Контекст
Процедуры для:
- Периодических задач (архивация, очистка)
- Пакетной обработки
- Инкапсуляции Multi-Statement операций

### Ожидаемое поведение
Создание и вызов хранимых процедур.

---

## 14. SHOW ENCRYPTION STATUS

### Синтаксис
```sql
SHOW ENCRYPTION STATUS;
```

### Ошибка
```
syntax error: expected DATABASES, TABLES or INDEXES, got 'ENCRYPTION'
```

### Контекст
Проверка статуса TDE (Transparent Data Encryption) для:
- Аудита шифрования (соответствие требованиям безопасности)
- Диагностики проблем с шифрованием
- Проверки алгоритма и источника ключей

### Ожидаемое поведение
Таблица с колонками: database, encrypted, algorithm, key_source.

---

## 15. REVOKE TOKEN (SQL)

### Синтаксис
```sql
REVOKE TOKEN 'vdb_sk_compromised_token_here';
```

### Ошибка
Синтаксис не распознается (только через HTTP API).

### Контекст
Отзыв скомпрометированных токенов доступа. В корпоративной среде необходимо:
- Немедленный отзыв токена при увольнении сотрудника
- Отзыв при обнаружении утечки
- Централизованное управление через SQL (не только HTTP)

### Ожидаемое поведение
SQL-команда для отзыва токена с мгновенным эффектом.

---

## 16. CREATE ROLE / GRANT

### Синтаксис
```sql
CREATE ROLE legal_manager WITH PASSWORD 'legal_pass';
GRANT SELECT, INSERT, UPDATE ON documents TO legal_manager;
CREATE USER alice WITH ROLE legal_manager;
```

### Ошибка
```
syntax error: expected DATABASE, TABLE or INDEX, got 'ROLE'
```

### Контекст
RBAC (Role-Based Access Control) для корпоративной системы:
- Юристы: SELECT, INSERT, UPDATE на documents
- Бухгалтеры: SELECT, INSERT на finance_documents
- Сотрудники: только SELECT
- Аудиторы: SELECT на audit_log

### Ожидаемое поведение
Создание ролей, назначение привилегий, привязка пользователей к ролям.

---

## 17. JSONB с GIN индексом (ограничения)

### Синтаксис
```sql
CREATE INDEX idx_docs_meta ON documents USING GIN (metadata);
SELECT * FROM documents WHERE metadata @> '{"type": "contract"}';
```

### Статус
`@>` работает, но индекс GIN не поддерживается (только B-tree).

### Контекст
Поиск по JSONB-метаданным документов:
- Найти все договоры: `@> '{"type": "contract"}'`
- Найти документы с определенным ключом: `? 'counterparty'`
- Поиск по вложенным значениям

### Ожидаемое поведение
GIN индекс для JSONB колонок с поддержкой операторов `@>`, `<@`, `?`.

---

## 18. TIME TRAVEL — AS OF (ограничения)

### Синтаксис
```sql
SELECT * FROM documents AS OF '2024-01-01';
```

### Статус
Фича заявлена в health-check (`"time_travel":true`), но синтаксис `AS OF` не реализован.

### Контекст
Путешествие во времени для:
- Просмотра состояния базы на момент времени
- Восстановления удаленных данных
- Аудита изменений (что было до/после)

### Ожидаемое поведение
Запросы `AS OF timestamp` возвращают снимок данных на указанное время.

---

## 19. HISTORY

### Синтаксис
```sql
HISTORY documents WHERE id = 1;
```

### Ошибка
Синтаксис не распознается.

### Контекст
История изменений конкретной записи — для аудита и отслеживания кто, когда и что менял.

### Ожидаемое поведение
Список всех версий строки с временными метками и авторами изменений.

---

## 20. COPY с относительными путями

### Синтаксис
```sql
COPY documents TO 'export.csv' WITH (FORMAT CSV, HEADER);
```

### Статус
Абсолютные пути запрещены (`COPY filename must not be absolute`), относительные — работают только для JSON.

### Контекст
Экспорт данных из контейнера/сервера для аналитики.

### Ожидаемое поведение
Поддержка относительных путей для CSV и JSON.

---

## Сводная таблица

| # | Фича | Docker 1.2.0 | Dev-сборка | Приоритет |
|---|------|-------------|-----------|-----------|
| 1 | UNIQUE constraint | ❌ | ❌ | Высокий |
| 2 | UNIQUE INDEX | ❌ | ❌ | Высокий |
| 3 | PARTITION BY RANGE | ❌ | ❌ | Средний |
| 4 | FOREIGN KEY | ❌ | ❌ | Высокий |
| 5 | COPY TO CSV | ❌ | ❌ | Средний |
| 6 | FULLTEXT индекс | ❌ | ❌ | Высокий |
| 7 | FTS MATCH | ❌ | ❌ | Высокий |
| 8 | BM25 score | ❌ | ❌ | Высокий |
| 9 | VERIFY AUDIT LOG | ❌ | ❌ | Средний |
| 10 | PARTITION BY HASH | ❌ | ❌ | Низкий |
| 11 | MERGE с VALUES | ❌ | ⚠️ | Средний |
| 12 | PL/pgSQL функции | ❌ | ❌ | Низкий |
| 13 | CALL procedure | ❌ | ❌ | Низкий |
| 14 | SHOW ENCRYPTION STATUS | ❌ | ❌ | Низкий |
| 15 | REVOKE TOKEN (SQL) | ❌ | ❌ | Средний |
| 16 | CREATE ROLE / GRANT | ❌ | ❌ | Высокий |
| 17 | GIN индекс для JSONB | ❌ | ❌ | Средний |
| 18 | AS OF (time travel) | ❌ | ❌ | Средний |
| 19 | HISTORY | ❌ | ❌ | Низкий |
| 20 | COPY CSV (относительные) | ❌ | ❌ | Низкий |

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
| MERGE (с таблицами-источниками) | ✅ |
| WINDOW functions (ROW_NUMBER, RANK, LAG, LEAD) | ✅ |
| CTE (WITH) | ✅ |
| TRIGGER (AFTER) | ✅ |
| TRANSACTIONS (BEGIN/COMMIT/ROLLBACK) | ✅ |
| EXPLAIN / EXPLAIN ANALYZE | ✅ |
| SHOW DATABASES / TABLES / INDEXES | ✅ |
| DESCRIBE | ✅ |
| COPY TO JSON | ✅ |
| GROUP BY / HAVING | ✅ |
| CASE WHEN | ✅ |
| DISTINCT | ✅ |
| UNION / UNION ALL | ✅ |
| Вложенные подзапросы в WHERE | ✅ |
| Health / Metrics HTTP endpoints | ✅ |
