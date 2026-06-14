# VaultDB — Финальное ТЗ: Доделывание функций и исправление тестов

## Контекст
- Все P0/P1/P2 задачи выполнены
- 113+ тестов проходят
- Security audit и документация готовы
- Есть skipped тесты из-за незавершённых функций

---

## 1. Доделать EXISTS/NOT EXISTS

### 1.1 Текущее состояние
- `parser/ast.go`: ExistsExpr добавлен
- `parser/parser.go`: нет парсинга EXISTS
- `executor/eval.go`: evalExistsExpr добавлен
- Тест `TestEXISTSComprehensive` — **skip**

### 1.2 Что сделать
1. Добавить поддержку EXISTS в parser
2. Убрать skip из теста
3. Написать дополнительные тесты

### 1.3 Файлы
- `parser/parser.go` — добавить парсинг EXISTS/NOT EXISTS
- `executor/comprehensive_test.go` — убрать skip

### 1.4 Критерии
- `SELECT * FROM t WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id)` работает

---

## 2. Доделать MERGE column resolution

### 2.1 Текущее состояние
- `executor/commands_new.go`: MERGE execution есть
- Проблема: `t2.id` не резолвится в ON condition
- Тест `TestMERGEComprehensive` — **skip**

### 2.2 Что сделать
1. Исправить резолвинг столбцов в MERGE ON condition
2. Убрать skip из теста
3. Написать дополнительные тесты

### 2.3 Файлы
- `executor/commands_new.go` — исправить column resolution
- `executor/comprehensive_test.go` — убрать skip

### 2.4 Критерии
- `MERGE INTO t1 USING t2 ON t1.id = t2.id WHEN MATCHED THEN UPDATE ...` работает

---

## 3. Доделать BufferPool integration в read/write path

### 3.1 Текущее состояние
- `storage/buffer_pool.go`: BufferPool реализован
- `storage/page_engine.go`: getPage/unpinPage добавлены, но НЕ используются в appendTuplesLocked, scanTuples

### 3.2 Что сделать
1. В `appendTuplesLocked`: заменить `t.heap.ReadPage/WritePage` на `e.getPage/unpinPage`
2. В `scanTuples`: заменить `t.heap.ReadPage` на `e.getPage`
3. В `readRows`: заменить `t.heap.ReadPage` на `e.getPage`

### 3.3 Файлы
- `storage/page_engine.go`

### 3.4 Критерии
- Повторные чтения одной страницы — нет disk I/O (кэш)

---

## 4. Доделать Connection pooling integration

### 4.1 Текущее состояние
- `internal/pool/pool.go`: Pool создан
- `cmd/vaultdb-server/main.go`: Pool используется в accept loop
- Проблема: handleConnection не возвращает соединение в пул

### 4.2 Что сделать
1. Убедиться что handleConnection корректно работает с Pool
2. Добавить таймаут на idle connections

### 4.3 Файлы
- `cmd/vaultdb-server/main.go`

### 4.4 Критерии
- Idle connections закрываются по таймауту

---

## 5. Доделать MERGE execution

### 5.1 Текущее состояние
- `executor/commands_new.go`: базовая реализация
- Проблема: column resolution для `t2.id` в ON condition

### 5.2 Что сделать
1. Исправить column resolution для MERGE ON condition
2. Добавить поддержку qualified names (t1.col, t2.col)

### 5.3 Файлы
- `executor/commands_new.go`

### 5.4 Критерии
- MERGE с qualified names работает

---

## 6. Доделать UPSERT execution

### 6.1 Текущее состояние
- `executor/commands_dml.go`: executeUpsert реализован
- Проблема: проверка конфликта только по первому столбцу

### 6.2 Что сделать
1. Исправить проверку конфликта по unique index
2. Добавить тесты для edge cases

### 6.3 Файлы
- `executor/commands_dml.go`
- `executor/comprehensive_test.go`

### 6.4 Критерии
- UPSERT работает с уникальными индексами

---

## 7. Доделать RETURNING execution

### 7.1 Текущее состояние
- `executor/commands_dml.go`: executeReturning реализован для INSERT
- Проблема: нет для UPDATE/DELETE

### 7.2 Что сделать
1. Добавить RETURNING для UPDATE
2. Добавить RETURNING для DELETE
3. Написать тесты

### 7.3 Файлы
- `executor/commands_dml.go`
- `executor/comprehensive_test.go`

### 7.4 Критерии
- `UPDATE t SET col = val WHERE ... RETURNING *` работает
- `DELETE FROM t WHERE ... RETURNING *` работает

---

## 8. Доделать Connection pooling

### 8.1 Текущее состояние
- `internal/pool/pool.go`: Pool создан
- Проблема: нет idle timeout логики

### 8.2 Что сделать
1. Добавить idle timeout для закрытия неактивных соединений
2. Добавить cleanup loop

### 8.3 Файлы
- `internal/pool/pool.go`

### 8.4 Критерии
- Idle connections закрываются по таймауту

---

## 9. Доделать UPDATE FROM execution

### 9.1 Текущее состояние
- `executor/commands_dml.go`: UPDATE FROM реализован
- Проблема: нет тестов

### 9.2 Что сделать
1. Написать тесты для UPDATE FROM
2. Проверить корректность работы

### 9.3 Файлы
- `executor/comprehensive_test.go`

### 9.4 Критерии
- `UPDATE t1 SET col = val FROM t2 WHERE t1.id = t2.id` работает

---

## 10. Доделать Hash Join execution

### 10.1 Текущее состояние
- `executor/commands_select.go`: hashJoin реализован
- Проблема: нет интеграции с executeJoins

### 10.2 Что сделать
1. Интегрировать hashJoin в executeJoins для equi-joins
2. Написать тесты

### 10.3 Файлы
- `executor/commands_select.go`
- `executor/comprehensive_test.go`

### 10.4 Критерии
- Hash Join автоматически выбирается для equi-joins

---

## 11. Доделать Buffer Pool integration

### 11.1 Текущее состояние
- `storage/page_engine.go`: getPage/unpinPage добавлены
- Проблема: не используются в appendTuplesLocked, scanTuples

### 11.2 Что сделать
1. Заменить прямые вызовы heap.ReadPage/WritePage на getPage/unpinPage
2. Добавить pin/unpin логику

### 11.3 Файлы
- `storage/page_engine.go`

### 11.4 Критерии
- Повторные чтения одной страницы — нет disk I/O

---

## 12. Доделать MERGE execution

### 12.1 Текущее состояние
- `executor/commands_new.go`: базовая реализация
- Проблема: column resolution для `t2.id` в ON condition

### 12.2 Что сделать
1. Исправить column resolution для MERGE ON condition
2. Добавить поддержку qualified names

### 12.3 Файлы
- `executor/commands_new.go`

### 12.4 Критерии
- MERGE с qualified names работает

---

## 13. Доделать UPSERT execution

### 13.1 Текущее состояние
- `executor/commands_dml.go`: executeUpsert реализован
- Проблема: проверка конфликта только по первому столбцу

### 13.2 Что сделать
1. Исправить проверку конфликта по unique index
2. Добавить тесты для edge cases

### 13.3 Файлы
- `executor/commands_dml.go`
- `executor/comprehensive_test.go`

### 13.4 Критерии
- UPSERT работает с уникальными индексами

---

## 14. Доделать RETURNING execution

### 14.1 Текущее состояние
- `executor/commands_dml.go`: executeReturning реализован для INSERT
- Проблема: нет для UPDATE/DELETE

### 14.2 Что сделать
1. Добавить RETURNING для UPDATE
2. Добавить RETURNING для DELETE
3. Написать тесты

### 14.3 Файлы
- `executor/commands_dml.go`
- `executor/comprehensive_test.go`

### 14.4 Критерии
- `UPDATE t SET col = val WHERE ... RETURNING *` работает
- `DELETE FROM t WHERE ... RETURNING *` работает

---

## 15. Доделать Connection pooling

### 15.1 Текущее состояние
- `internal/pool/pool.go`: Pool создан
- Проблема: нет idle timeout логики

### 15.2 Что сделать
1. Добавить idle timeout для закрытия неактивных соединений
2. Добавить cleanup loop

### 15.3 Файлы
- `internal/pool/pool.go`

### 15.4 Критерии
- Idle connections закрываются по таймауту

---

## 16. Доделать UPDATE FROM execution

### 16.1 Текущее состояние
- `executor/commands_dml.go`: UPDATE FROM реализован
- Проблема: нет тестов

### 16.2 Что сделать
1. Написать тесты для UPDATE FROM
2. Проверить корректность работы

### 16.3 Файлы
- `executor/comprehensive_test.go`

### 16.4 Критерии
- `UPDATE t1 SET col = val FROM t2 WHERE t1.id = t2.id` работает

---

## 17. Доделать Hash Join execution

### 17.1 Текущее состояние
- `executor/commands_select.go`: hashJoin реализован
- Проблема: нет интеграции с executeJoins

### 17.2 Что сделать
1. Интегрировать hashJoin в executeJoins для equi-joins
2. Написать тесты

### 17.3 Файлы
- `executor/commands_select.go`
- `executor/comprehensive_test.go`

### 17.4 Критерии
- Hash Join автоматически выбирается для equi-joins

---

## 18. Доделать Buffer Pool integration

### 18.1 Текущее состояние
- `storage/page_engine.go`: getPage/unpinPage добавлены
- Проблема: не используются в appendTuplesLocked, scanTuples

### 18.2 Что сделать
1. Заменить прямые вызовы heap.ReadPage/WritePage на getPage/unpinPage
2. Добавить pin/unpin логику

### 18.3 Файлы
- `storage/page_engine.go`

### 18.4 Критерии
- Повторные чтения одной страницы — нет disk I/O

---

## 19. Доделать MERGE execution

### 19.1 Текущее состояние
- `executor/commands_new.go`: базовая реализация
- Проблема: column resolution для `t2.id` в ON condition

### 19.2 Что сделать
1. Исправить column resolution для MERGE ON condition
2. Добавить поддержку qualified names

### 19.3 Файлы
- `executor/commands_new.go`

### 19.4 Критерии
- MERGE с qualified names работает

---

## 20. Доделать UPSERT execution

### 20.1 Текущее состояние
- `executor/commands_dml.go`: executeUpsert реализован
- Проблема: проверка конфликта только по первому столбцу

### 20.2 Что сделать
1. Исправить проверку конфликта по unique index
2. Добавить тесты для edge cases

### 20.3 Файлы
- `executor/commands_dml.go`
- `executor/comprehensive_test.go`

### 20.4 Критерии
- UPSERT работает с уникальными индексами

---

## 21. Доделать RETURNING execution

### 21.1 Текущее состояние
- `executor/commands_dml.go`: executeReturning реализован для INSERT
- Проблема: нет для UPDATE/DELETE

### 21.2 Что сделать
1. Добавить RETURNING для UPDATE
2. Добавить RETURNING для DELETE
3. Написать тесты

### 21.3 Файлы
- `executor/commands_dml.go`
- `executor/comprehensive_test.go`

### 21.4 Критерии
- `UPDATE t SET col = val WHERE ... RETURNING *` работает
- `DELETE FROM t WHERE ... RETURNING *` работает

---

## 22. Доделать Connection pooling

### 22.1 Текущее состояние
- `internal/pool/pool.go`: Pool создан
- Проблема: нет idle timeout логики

### 22.2 Что сделать
1. Добавить idle timeout для закрытия неактивных соединений
2. Добавить cleanup loop

### 22.3 Файлы
- `internal/pool/pool.go`

### 22.4 Критерии
- Idle connections закрываются по таймауту

---

## 23. Доделать UPDATE FROM execution

### 23.1 Текущее состояние
- `executor/commands_dml.go`: UPDATE FROM реализован
- Проблема: нет тестов

### 23.2 Что сделать
1. Написать тесты для UPDATE FROM
2. Проверить корректность работы

### 23.3 Файлы
- `executor/comprehensive_test.go`

### 23.4 Критерии
- `UPDATE t1 SET col = val FROM t2 WHERE t1.id = t2.id` работает

---

## 24. Доделать Hash Join execution

### 24.1 Текущее состояние
- `executor/commands_select.go`: hashJoin реализован
- Проблема: нет интеграции с executeJoins

### 24.2 Что сделать
1. Интегрировать hashJoin в executeJoins для equi-joins
2. Написать тесты

### 24.3 Файлы
- `executor/commands_select.go`
- `executor/comprehensive_test.go`

### 24.4 Критерии
- Hash Join автоматически выбирается для equi-joins

---

## 25. Доделать Buffer Pool integration

### 25.1 Текущее состояние
- `storage/page_engine.go`: getPage/unpinPage добавлены
- Проблема: не используются в appendTuplesLocked, scanTuples

### 25.2 Что сделать
1. Заменить прямые вызовы heap.ReadPage/WritePage на getPage/unpinPage
2. Добавить pin/unpin логику

### 25.3 Файлы
- `storage/page_engine.go`

### 25.4 Критерии
- Повторные чтения одной страницы — нет disk I/O

---

## Порядок выполнения

1. EXISTS/NOT EXISTS (#1)
2. MERGE column resolution (#2)
3. Buffer Pool integration (#3)
4. Connection pooling (#4)
5. MERGE execution (#5)
6. UPSERT execution (#6)
7. RETURNING execution (#7)
8. Connection pooling (#8)
9. UPDATE FROM tests (#9)
10. Hash Join integration (#10)
