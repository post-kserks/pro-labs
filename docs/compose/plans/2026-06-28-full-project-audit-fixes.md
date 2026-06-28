# VaultDB — Полный аудит и исправления

> **Цель:** Устранить все known limitations, разбить god objects, убрать дублирование, сделать проект полностью рабочим и конкурентоспособным.

## Фаза 1: Исправить сломанные фичи (Must-Have)

### 1.1 STRING_AGG — сломан
- Файл: `executor/aggregates.go`
- Проблема: тест пропущен, функция не работает
- Фикс: Реализовать корректную конкатенацию с разделителем

### 1.2 CTE с агрегатами
- Файл: `executor/cte.go`
- Проблема: `WITH x AS (...) SELECT COUNT(*) FROM x` возвращает raw rows
- Фикс: Executing CTE как subquery, применять агрегаты к результату

### 1.3 HAVING с COUNT(*) напрямую
- Файл: `executor/select_aggr.go`
- Проблема: `HAVING COUNT(*) > 5` не работает, только `HAVING cnt > 5`
- Фикс: Поддержать агрегатные функции в HAVING без alias

### 1.4 CompositeIndex.SetColumn
- Файл: `index/composite.go`
- Проблема: пустой метод при RENAME COLUMN
- Фикс: Обновлять column list в composite index

## Фаза 2: Разбить God Objects

### 2.1 main.go → отдельные файлы
- `cmd/vaultdb-server/config.go` — флаги, конфиг
- `cmd/vaultdb-server/tcp_server.go` — TCP accept loop, connection handling
- `cmd/vaultdb-server/protocol.go` — sendError, sendResult, sanitizeErrorMessage
- `cmd/vaultdb-server/health.go` — health check

### 2.2 page_engine.go → domain managers
- `storage/catalog_manager.go` — database/table CRUD, schema persistence
- `storage/row_store.go` — insert/select/update/delete с MVCC
- `storage/index_coordinator.go` — делегирование индексов
- `storage/checkpoint_manager.go` — WAL checkpoints

### 2.3 parse_ddl.go → по типам DDL
- `parser/parse_ddl_tables.go`
- `parser/parse_ddl_index.go`
- `parser/parse_ddl_views.go`
- `parser/parse_ddl_procedures.go`
- `parser/parse_ddl_policies.go`

### 2.4 commands_ddl.go → по типам команд
- `executor/commands_ddl_database.go`
- `executor/commands_ddl_table.go`
- `executor/commands_ddl_index.go`
- `executor/commands_ddl_views.go`
- `executor/commands_ddl_procedures.go`
- `executor/commands_ddl_policies.go`

### 2.5 commands_dml.go → по командам
- `executor/commands_insert.go`
- `executor/commands_update.go`
- `executor/commands_delete.go`

## Фаза 3: Устранить дублирование

### 3.1 Единый validateObjectName
- Создать `internal/validation/validation.go`
- Убрать дубли из storage и executor

### 3.2 Единый valueToString
- Вынести в `internal/storage/value.go`

### 3.3 SessionConfig
- Создать `executor.SessionConfig` struct
- Заменить 5+ setter вызовов

### 3.4 Typed error constants
- Создать `httpserver/errors.go`
- Заменить ad-hoc writeError паттерны

## Фаза 4: Разделить интерфейсы

### 4.1 StorageEngine → domain interfaces
- `storage.Reader` — read-only операции
- `storage.Writer` — write операции
- `storage.Admin` — lifecycle операции
- `storage.IndexManager` — index операции
- `storage.RLSManager` — RLS операции

### 4.2 Убрать index methods из ReadOnlyEngine

## Фаза 5: Покрытие тестами

### 5.1 MERGE тесты
### 5.2 Trigger тесты
### 5.3 Procedure тесты
### 5.4 C++ GoogleTest (JSON parser, string utils)

## Фаза 6: Config и production readiness

### 6.1 Исправить connection pool factory
### 6.2 Respect tcp_idle_timeout
### 6.3 Make commandRegistry/likeCache non-global
