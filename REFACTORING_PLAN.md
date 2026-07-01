# VaultDB — План рефакторинга

**Дата**: 2026-06-29
**Статус**: Черновик — требует ревью

---

## Обзор

Комплексный аудит выявил критичные баги,结构性 gaps в парсере, и архитектурные проблемы. План разбит на 4 фазы по приоритету.

---

## Фаза 1: Критичные баги (данные / функциональность)

### 1.1 MERGE UPDATE — column references не резолвятся

**Симптом**: `MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET val = s.val` — тихая ошибка, обновление не происходит.

**Причина**: `eval.go:75-82` — `evalOperand` для `ColumnRef` проверяет только `Table == "old"` и `Table == "new"`. Для `s.val` (Table="s") вызывается `resolveColumn(row, schema, "val")` — ищет колонку `"val"` без квалификатора. В combined schema колонки named `"heroes.val"`, `"source.val"` — суффикс-матч находит первую попавшуюся (`heroes.val` = target), результат — `UPDATE t SET val = t.val` (no-op).

**Исправление**:
- `eval_utils.go:14-31` — `resolveColumn`: если `ColumnRef.Table` непустой и не "old"/"new", сначала искать `table + "." + name` (exact match в combined schema), затем fallback на bare name.
- `parse_dml.go:294` — парсер SET: разрешить table-qualified LHS (`SET t.col = expr`).

**Файлы**: `server/internal/executor/eval_utils.go`, `server/internal/executor/eval.go`, `server/internal/parser/parse_dml.go`

---

### 1.2 INSERT ... SELECT — парсер не поддерживает

**Симптом**: `INSERT INTO t SELECT * FROM src` → `expected 'VALUES'`

**Причина**: `parse_dml.go:32` — безусловный `consume(TOKEN_VALUES)`. AST уже имеет поле `SelectQuery *SelectStatement` (`ast.go:227`), executor тоже обрабатывает его (`commands_insert.go:83-85`), но парсер никогда его не заполняет.

**Исправление**:
```
parse_dml.go:32 — заменить:
  if p.current().Type == TOKEN_VALUES { ... }
  else if p.current().Type == TOKEN_SELECT {
    selectStmt, err := p.parseSelect()
    InsertStatement.SelectQuery = selectStmt
  }
```

**Файлы**: `server/internal/parser/parse_dml.go`

---

### 1.3 SubqueryExpr panic на UNION/INTERSECT/EXCEPT

**Симптом**: `SELECT * FROM t WHERE col = (SELECT 1 UNION ALL SELECT 2)` → runtime panic

**Причина**: `parse_utils.go:285` — `stmt.(*SelectStatement)` каст без проверки типа. Если subquery — `SetOperationStatement`, каст паникует.

**Исправление**:
- `ast.go:521-523` — типизировать `SubqueryExpr.Query` как `Statement` вместо `*SelectStatement`
- `parse_utils.go:285` — проверять оба типа перед кастом

**Файлы**: `server/internal/parser/ast.go`, `server/internal/parser/parse_utils.go`

---

## Фаза 2: Парсер —结构性 gaps

### 2.1 NOT IN (subquery) не поддерживается

**Причина**: `parse_utils.go:152-165` — `NOT IN` вызывает `parseValueListUntilRParen()` вместо проверки `TOKEN_SELECT`.

**Исправление**: Добавить ветку `TOKEN_SELECT` в NOT IN, аналогично IN.

**Файл**: `server/internal/parser/parse_utils.go:152-165`

---

### 2.2 SET LHS не поддерживает table-qualified имена

**Причина**: `parse_dml.go:153` — `consumeIdent` принимает только bare identifier. `UPDATE t SET t.col = 1` падает на точке.

**Исправление**: После `consumeIdent` проверить `TOKEN_DOT` и потребовать ещё один identifier (аналогично column reference в WHERE).

**Файл**: `server/internal/parser/parse_dml.go:153`

---

### 2.3 UPDATE ... FROM не поддерживает subquery

**Причина**: `parse_dml.go:134-145` — FROM принимает только `consumeIdent`. Стандарт SQL允许 `FROM (SELECT ...) AS sub`.

**Исправление**: Проверить `TOKEN_LPAREN` после FROM и вызвать `parseSelect` для subquery.

**Файл**: `server/internal/parser/parse_dml.go:134-145`

---

### 2.4 MERGE USING не поддерживает subquery

**Причина**: `parse_dml.go:248-253` — USING принимает только `consumeIdent`.

**Исправление**: Аналогично 2.3 — проверить `TOKEN_LPAREN`.

**Файл**: `server/internal/parser/parse_dml.go:248-253`

---

### 2.5 MERGE WHEN NOT MATCHED — только VALUES, нет SELECT

**Причина**: `parse_dml.go:343` — безусловный `consume(TOKEN_VALUES)`.

**Исправление**: Аналогично 1.2 — ветка для `TOKEN_SELECT`.

**Файл**: `server/internal/parser/parse_dml.go:343`

---

### 2.6 FormatExpression неполный

**Причина**: `parser.go:48-78` — default case возвращает `"<expr>"` для `AggregateExpr`, `FunctionCall`, `CastExpr`, `CaseExpr`, `SubqueryExpr`, `WindowFunctionExpr`, `ExistsExpr`, `BetweenExpr`, `ParamRef`, `JsonPathExpr`.

**Исправление**: Добавить case для каждого типа. Критично для отладки и EXPLAIN.

**Файл**: `server/internal/parser/parser.go:48-78`

---

### 2.7 FormatExpression теряет ColumnRef.Table

**Причина**: `parser.go:59-60` — выводит только `e.Name`, игнорируя `e.Table`.

**Исправление**: Если `e.Table != ""`, выводить `e.Table + "." + e.Name`.

**Файл**: `server/internal/parser/parser.go:59-60`

---

### 2.8 Дублирующийся код old/new в парсере

**Причина**: `parse_utils.go:461-472` и `parse_utils.go:529-541` — два идентичных блока проверки `old`/`new`.

**Исправление**: Удалить дубликат, оставить один блок в `parsePrimary`.

**Файл**: `server/internal/parser/parse_utils.go:529-541`

---

## Фаза 3: Executor и Storage

### 3.1 Checkpoint пишет двойной checkpoint record

**Причина**: `page_engine.go:574` — `wal.Append(OpCheckpoint)`, затем `wal.Checkpoint()` (line 580) пишет ЕЩЁ ОДИН `OpCheckpoint` перед truncation. Первая запись немедленно удаляется.

**Исправление**: Убрать `wal.Append(OpCheckpoint)` из `doCheckpoint` — `wal.Checkpoint()` уже пишет свою запись.

**Файлы**: `server/internal/storage/page_engine.go:574`

---

### 3.2 FlushDirtyPagesUpToLSN игнорирует maxLSN

**Причина**: `buffer_pool.go:210-212` — комментарий: "For simplicity, ignores maxLSN".

**Исправление**: Реализовать фильтрацию по LSN или хотя бы добавить TODO с警告ой в лог.

**Файл**: `server/internal/storage/buffer_pool.go:210-212`

---

### 3.3 Каталог сохраняется до checkpoint record

**Причина**: `page_engine.go:564` — `saveCatalogLocked()` до `wal.Append(OpCheckpoint)`. Recovery не может определить checkpoint LSN из каталога.

**Исправление**: Сохранять catalog ПОСЛЕ checkpoint record, записывая checkpoint LSN.

**Файл**: `server/internal/storage/page_engine.go:564`

---

### 3.4 Double old/new prefix в parsePrimary

**Причина**: `parse_utils.go:461-472` и `parse_utils.go:529-541` — два одинаковых блока.

**Исправление**: Удалить дубликат (529-541).

**Файл**: `server/internal/parser/parse_utils.go`

---

### 3.5 getTableForRead/Write дупликация

**Причина**: `page_engine.go:808-826` — две функции по 8 строк, различаются только RLock/Lock.

**Исправление**: Вынести в обобщённую `getTableForLock(lockType)` с параметром.

**Файл**: `server/internal/storage/page_engine.go:808-826`

---

## Фаза 4: Web UI и Infrastructure

### 4.1 site/public/ — мёртвый код

**Находка**: `site/public/` (index.html, app.js, style.css) не подключён ни через embed, ни через build script. Сервер отдаёт `server/internal/httpserver/web/dist/` (React SPA).

**Действие**: Удалить `site/public/` или переименовать в `site/legacy/`.

**Файлы**: `site/public/*`

---

### 4.2 Stateless HTTP sessions — нет персистентности

**Причина**: `server_handlers.go:105-106` — новая сессия на каждый запрос, немедленно уничтожается. `USE database`, prepared statements, result cache не сохраняются.

**Действие**: Документировать как design choice (stateless REST) или добавить session pool для web UI.

**Файл**: `server/internal/httpserver/server_handlers.go:105-106`

---

### 4.3 Auth блокирует web UI API calls

**Причина**: `server.go:208-217` — статические файлы отдаются без auth, но API запросы требуют токены. Web UI не имеет login mechanism.

**Действие**: Добавить автоматический token для web UI или отключать auth для localhost.

**Файл**: `server/internal/httpserver/server.go:208-217`

---

### 4.4 Нет SPA fallback

**Причина**: `server.go:219` — file server не перенаправляет unknown paths на `index.html`. Deep links 404.

**Действие**: Добавить catch-all handler для SPA routing.

**Файл**: `server/internal/httpserver/server.go:219`

---

### 4.5 renderResult — хрупкий ID matching

**Причина**: `site/public/app.js:34-37` — fallback `containerId.replace('Result','')` создаёт несуществующие ID.

**Действие**: Использовать единый naming convention для всех result areas.

**Файл**: `site/public/app.js:34-37`

---

### 4.6 Transaction Lab / Feature Gallery — нет Duration элемента

**Причина**: HTML не содержит `<span id="txResultDuration">` и `<span id="featureResultDuration">`. `duration_ms` теряется.

**Действие**: Добавить Duration spans в HTML.

**Файл**: `site/public/index.html:125-128, 172-178`

---

### 4.7 esc() создаёт DOM node в hot loop

**Причина**: `site/public/app.js:85-89` — `document.createElement('div')` на каждую ячейку. 1000 строк × 5 колонок = 5000 аллокаций.

**Исправление**: Заменить на regex-based escape: `s.replace(/&/g,'&amp;').replace(/</g,'&lt;')`.

**Файл**: `site/public/app.js:85-89`

---

### 4.8 StorageEngine — нет Truncate

**Причина**: `storage.go` — `DeleteRows` есть, но `Truncate` (быстрое удаление всех строк без per-row WAL) отсутствует.

**Действие**: Добавить `TruncateTable(db, table)` в `WriteEngine`.

**Файл**: `server/internal/storage/storage.go:114-131`

---

### 4.9 StorageEngine — нет ModifyColumn

**Причина**: `AlterTableAddColumn`, `DropColumn`, `RenameColumn` есть, но `ModifyColumn` (изменение типа/ограничений) нет.

**Действие**: Добавить `AlterTableModifyColumn(db, table, col, newType)`.

**Файл**: `server/internal/storage/storage.go:114-131`

---

## Приоритеты

| Приоритет | Фаза | Задач | Влияние |
|-----------|------|-------|---------|
| P0 | 1.1 | MERGE UPDATE column resolution | Data corruption |
| P0 | 1.2 | INSERT...SELECT parser | Feature completely broken |
| P0 | 1.3 | SubqueryExpr panic | Runtime crash |
| P1 | 2.1 | NOT IN (subquery) | SQL compliance |
| P1 | 2.2 | SET LHS qualified names | SQL compliance |
| P1 | 3.1 | Double checkpoint record | Performance waste |
| P2 | 2.3-2.5 | UPDATE FROM / MERGE USING subqueries | SQL compliance |
| P2 | 3.2-3.3 | Checkpoint LSN handling | Recovery correctness |
| P2 | 4.1-4.7 | Web UI cleanup | UX / dead code |
| P3 | 4.8-4.9 | StorageEngine gaps | Feature completeness |

---

## Ожидаемый результат

После Фазы 1-2:
- MERGE UPDATE корректно обновляет данные
- `INSERT INTO t SELECT ... FROM src` работает
- Subqueries в WHERE/IN не паникуют
- NOT IN (subquery) поддерживается
- SET LHS принимает table-qualified имена

После Фазы 3-4:
- Checkpoint не тратит I/O на двойную запись
- Web UI не содержит мёртвого кода
- Auth не блокирует web UI
- StorageEngine имеет Truncate и ModifyColumn
