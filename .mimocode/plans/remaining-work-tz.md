# VaultDB — ТЗ на оставшиеся работы

## Контекст
VaultDB — Go-based DBMS с SQL support, dual storage engines (JSON + page), WAL для crash recovery.
Текущий статус: ~85% готовности. Все P0/P1 задачи выполнены. Остались критические пробелы.

---

## 1. CRITICAL: BufferPool в read/write пути

### 1.1 Текущее состояние
- `storage/buffer_pool.go`: BufferPool реализован (LRU, dirty tracking, flush)
- `storage/page_engine.go`: **НЕ ИСПОЛЬЗУЕТ** BufferPool — читает/пишет напрямую в HeapFile
- Проблема: `appendTuplesLocked`, `scanTuples`, `readRows` — все используют `t.heap.ReadPage/WritePage`

### 1.2 Что сделать
1. Добавить метод `getPage(pid, heapFile) *page.Page` — берёт из кэша или читает с диска
2. Добавить метод `unpinPage(pid, dirty)` — помечает страницу как использованную
3. Заменить все вызовы `t.heap.ReadPage` → `e.getPage`
4. Заменить все вызовы `t.heap.WritePage` → `e.unpinPage(pid, true)`
5. В `appendTuplesLocked`: FetchPage → InsertTuple → Unpin(dirty=true)
6. В `scanTuples`: FetchPage → чтение → Unpin(dirty=false)

### 1.3 Файлы для изменения
- `storage/page_engine.go` — основные изменения
- `storage/buffer_pool.go` — возможно добавить методы

### 1.4 Критерии приёмки
- `go test ./... -race` — 0 data races
- Повторные чтения одной страницы — нет disk I/O (кэш)
- Checkpoint сбрасывает dirty pages

---

## 2. CRITICAL: undoUpdate/undoDelete

### 2.1 Текущее состояние
- `executor/commands_tx.go:160-176`: undoInsert/delete/update — заглушки
- `undoInsert`: удаляет последние строки (works, но хрупко)
- `undoUpdate`: return nil
- `undoDelete`: return nil

### 2.2 Что сделать
1. `undoUpdate`: восстанавливать OldRow из PendingOp
2. `undoDelete`: вставлять обратно Row из PendingOp
3. Добавить `OldRow` в PendingOp при UPDATE/DELETE в executor
4. Добавить `Row` в PendingOp при DELETE в executor

### 2.3 Файлы для изменения
- `executor/commands_dml.go` — UpdateCommand/DeleteCommand
- `executor/commands_tx.go` — undoUpdate/undoDelete
- `txmanager/manager.go` — PendingOp (уже есть OldRow)

### 2.4 Критерии приёмки
- UPDATE + ROLLBACK — данные восстанавливаются
- DELETE + ROLLBACK — строки возвращаются

---

## 3. CRITICAL: Connection Pooling integration

### 3.1 Текущее состояние
- `internal/pool/pool.go`: Pool создан (Acquire, Release, cleanup)
- `cmd/vaultdb-server/main.go`: 使用 semaphore, не Pool
- `handleConnection`: не использует Pool

### 3.2 Что сделать
1. Заменить semaphore на Pool в `main.go`
2. `handleConnection`: Acquire → session → Release
3. Idle timeout для закрытия неактивных соединений

### 3.3 Файлы для изменения
- `cmd/vaultdb-server/main.go`

### 3.4 Критерии приёмки
- 1000+ concurrent connections — memory стабильно
- Idle connections закрываются по таймауту

---

## 4. HIGH: Hash Join execution

### 4.1 Текущее состояние
- `executor/optimizer.go`: HashJoin определён как вариант
- `executor/commands_select.go`: использует только Nested Loop

### 4.2 Что сделать
1. Добавить `executeHashJoin` в `commands_select.go`
2. В `executeJoins` выбирать между Nested Loop и Hash Join
3. Hash Join: построить хеш-таблицу по smaller table, probe larger table

### 4.3 Файлы для изменения
- `executor/commands_select.go`

### 4.4 Критерии приёмки
- JOIN на 10K строк — < 100ms
- Hash Join выбран для equi-joins

---

## 5. HIGH: MERGE execution (полная)

### 5.1 Текущее состояние
- `executor/commands_new.go`: базовая реализация
- `parser/ast.go`: MergeStatement, MergeWhenClause

### 5.2 Что сделать
1. Полная реализация WHEN MATCHED THEN UPDATE
2. Полная реализация WHEN NOT MATCHED THEN INSERT
3. Обработка ON condition

### 5.3 Файлы для изменения
- `executor/commands_new.go`

### 5.4 Критерии приёмки
- `MERGE INTO t1 USING t2 ON t1.id = t2.id WHEN MATCHED THEN UPDATE SET ... WHEN NOT MATCHED THEN INSERT ...` работает

---

## 6. HIGH: UPDATE FROM execution

### 6.1 Текущее состояние
- `parser/ast.go`: FromTable, FromAlias в UpdateStatement
- `parser/parser.go`: парсинг UPDATE ... FROM
- `executor/commands_dml.go`: execution нет

### 6.2 Что сделать
1. Прочитать FROM таблицу
2. Объединить данные для обновления
3. Применить UPDATE с учётом JOIN

### 6.3 Файлы для изменения
- `executor/commands_dml.go`

### 6.4 Критерии приёмки
- `UPDATE t1 SET col = t2.val FROM t2 WHERE t1.id = t2.id` работает

---

## 7. HIGH: TLS для TCP

### 7.1 Текущее состояние
- `internal/tls/tls.go`: LoadTLSConfig, WrapListener
- `cmd/vaultdb-server/main.go`: нет TLS интеграции

### 7.2 Что сделать
1. Добавить CLI флаги `--tls-cert`, `--tls-key`
2. Обернуть listener в TLS если сертификат указан
3. Добавить в config.yaml

### 7.3 Файлы для изменения
- `cmd/vaultdb-server/main.go`
- `internal/config/config.go`

### 7.4 Критерии приёмки
- `vaultdb --tls-cert=cert.pem --tls-key=key.pem` — encrypted connections
- curl с https работает

---

## 8. MEDIUM: RETURNING execution

### 8.1 Текущее состояние
- `parser/ast.go`: Returning []SelectColumn в Insert/Update/Delete
- `parser/parser.go`: парсинг RETURNING
- `executor/commands_dml.go`: нет execution

### 8.2 Что сделать
1. В InsertCommand/UpdateCommand/DeleteCommand: после выполнения вернуть затронутые строки
2. Форматировать результат по RETURNING columns

### 8.3 Файлы для изменения
- `executor/commands_dml.go`

### 8.4 Критерии приёмки
- `INSERT INTO t VALUES (1, 'a') RETURNING *` — возвращает вставленную строку

---

## 9. MEDIUM: UPSERT execution

### 9.1 Текущее состояние
- `parser/ast.go`: OnConflictClause в InsertStatement
- `parser/parser.go`: парсинг ON CONFLICT
- `executor/commands_dml.go`: нет execution

### 9.2 Что сделать
1. Проверка конфликта по unique index
2. DO NOTHING — пропуск если конфликт
3. DO UPDATE — обновление если конфликт

### 9.3 Файлы для изменения
- `executor/commands_dml.go`

### 9.4 Критерии приёмки
- `INSERT INTO t VALUES (1, 'a') ON CONFLICT DO NOTHING` — работает
- `INSERT INTO t VALUES (1, 'a') ON CONFLICT DO UPDATE SET name = 'b'` — работает

---

## 10. MEDIUM: Correlated subqueries

### 10.1 Текущее состояние
- `executor/eval.go:451-476`: executeSubquery — non-correlated only

### 10.2 Что сделать
1. Передавать outerRow/outerSchema в subquery
2. Заменять столбцы внешней таблицы в subquery

### 10.3 Файлы для изменения
- `executor/eval.go`

### 10.4 Критерии приёмки
- `SELECT * FROM t1 WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t1.id)` работает

---

## Порядок выполнения

1. BufferPool integration (#1)
2. undoUpdate/undoDelete (#2)
3. Connection pooling (#3)
4. Hash Join (#4)
5. MERGE execution (#5)
6. UPDATE FROM (#6)
7. TLS (#7)
8. RETURNING (#8)
9. UPSERT (#9)
10. Correlated subqueries (#10)
