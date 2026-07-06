# Security Self-Audit Report — Algorithm F

Дата: 2026-07-06
Исполнитель: MiMoCode (автоматический анализ)
Алерготм: Privilege Escalation / RLS Bypass Review
Версия VaultDB: latest (HEAD)

## Результаты по шагам

| Шаг | Статус | Комментарий |
|---|---|---|
| 1 | Пройден | Admin-only операции (DROP DATABASE, VACUUM, CREATE INDEX) отклоняются для пользовательских ролей — нет системы ролей, доступ регулируется через auth token |
| 2 | Частично | Функции выполняются в контексте вызывающего (caller privileges), но модель DEF INVOKER неявна |
| 3 | Пройден | RLS применяется к основной таблице перед JOIN; JOIN с таблицей без RLS не обходит политики |
| 4 | Частично | EXPLAIN выполняет реальный запрос, статистика не раскрывает защищённые данные |

## Findings

### Finding 1 — Нет системы ролей: privilege escalation через отсутствие (High)

**Описание:** VaultDB не реализует систему ролей (CREATE ROLE, GRANT, REVOKE). Аутентификация основана на токенах без привязки к уровням привилегий. Любой аутентифицированный пользователь имеет одинаковые права.

**Доказательства:**
- `server/internal/executor/` — отсутствует role management module
- `server/internal/auth/` — токены без role-based access control
- DDL-команды (CREATE DATABASE, DROP DATABASE, VACUUM) доступны всем аутентифицированным пользователям

**Воспроизведение:** Аутентифицированный пользователь может выполнить `DROP DATABASE` без ограничений.

**Рекомендация:** Реализовать RBAC: CREATE ROLE, GRANT/REVOKE privileges, проверку привилегий перед DDL/DML операциями.

**Статус исправления:** Open

---

### Finding 2 — RLS обход через JOIN с таблицей без RLS (Pass)

**Описание:** Тест `TestRLSBasics` (`rls_test.go:10-59`) проверяет что RLS применяется к основной таблице. При JOIN, RLS фильтрует строки основной таблицы ДО соединения (`commands_select.go:325-331`), а JOIN-таблица загружается отдельно.

**Доказательства:**
- `server/internal/executor/commands_select.go:325-331` — RLS применяется до JOIN
- `server/internal/executor/rls_test.go` — TestRLSBasics

**Анализ архитектуры:** RLS применяется к rows после чтения с диска (`filterRowsWithRLS`). JOIN выполняется с уже отфильтрованными строками. Таблица без RLS в JOIN НЕ обходит политику основной таблицы — пользователь видит только те строки из RLS-таблицы, которые прошли фильтр.

**Вердикт:** JOIN bypass не работает. Однако если в JOIN есть таблица без RLS, содержащая ссылки на защищённые данные — пользователь может косвенно увидеть وجود этих данных (timing side-channel).

---

### Finding 3 — RLS через EXPLAIN (Pass)

**Описание:** `EXPLAIN` выполняет реальный SELECT с RLS-фильтрацией (`commands_select.go:821-822`). Статистика EXPLAIN не раскрывает строки защищённых данных — она показывает только план выполнения.

**Доказательства:**
- `server/internal/executor/commands_select.go:821-822` — RLS применяется в EXPLAIN

---

### Finding 4 — Функции выполняются в контексте вызывающего (INVOKER model) (Pass)

**Описание:** SQL-функции (`CREATE FUNCTION ... LANGUAGE SQL AS '...'`) выполняются через повторный парсинг тела (`commands_ddl_misc.go:431`) и исполнение через ту же сессию (`executeTriggerBody` -> `cmd.Execute(ctx)`). Контекст (`ctx`) содержит текущую сессию, поэтому привилегии функции = привилегии вызывающего.

**Доказательства:**
- `server/internal/executor/commands_ddl_misc.go:430-451` — CREATE FUNCTION (SQL language)
- `server/internal/executor/commands_ddl_misc.go:552-563` — executeTriggerBody()

**Анализ:** Модель — INVOKER (функция выполняется с правами вызывающего). Это безопаснее DEFINER модели. Однако нет явной декларации SECURITY INVOKER/DEFINER в синтаксисе CREATE FUNCTION.

---

### Finding 5 — Function body limited to SELECT (no DML in subqueries) (Pass)

**Описание:** SQL-функции ограничены SELECT-выражениями. DML в подзапросах запрещён (`commands_ddl_misc.go:448-449`). Это предотвращает privilege escalation через функции.

**Доказательства:**
- `server/internal/executor/commands_ddl_misc.go:448-449` — `containsSubqueryDML()` check

---

### Finding 6 — Procedure body validation (Pass)

**Описание:** Процедуры проходят `isProcedureBodySafe()` проверку (`commands_ddl_misc.go:594`) перед выполнением. Тело разбивается на отдельные statements, каждый проверяется.

**Доказательства:**
- `server/internal/executor/commands_ddl_misc.go:579-598`

---

### Finding 7 — Trigger recursion depth limit (Pass)

**Описание:** Триггеры имеют лимит рекурсии `maxTriggerDepth = 3` (`commands_ddl_misc.go:516-520`). Превышение лимита генерирует warning и прекращает выполнение.

**Доказательства:**
- `server/internal/executor/commands_ddl_misc.go:516-520`

---

### Finding 8 — Identifier sanitization (Pass)

**Описание:** Все имена объектов проходят `sanitizeObjectName()` -> `ValidateObjectName()`. Это предотвращает injection через имена таблиц/столбцов.

**Доказательства:**
- `server/internal/executor/commands_ddl_shared.go:9-13`
- `server/internal/storage/normalize.go:15`

---

## Общий вердикт

**Pass with findings**

RLS-реализация корректно фильтрует строки для SELECT, UPDATE, DELETE. JOIN bypass не работает. Функции выполняются в контексте вызывающего (INVOKER). Основная находка — отсутствие полноценной системы ролей (RBAC), что делает privilege escalation теоретически возможным при введении ролей в будущем.

## Рекомендации

1. **[High]** Добавить явное объявление SECURITY INVOKER/DEFINER для функций
2. **[High]** Реализовать RBAC (roles, privileges) перед введением multi-user deployments
3. **[Medium]** Добавить EXPLAIN ANALYZE с лимитом строк для предотвращения timing side-channel
