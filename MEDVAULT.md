# MedVault — электронная медицинская карта на базе VaultDB

MedVault — демонстрационное приложение, построенное поверх **VaultDB v1.0.1**
(этот репозиторий). Оно показывает ключевые возможности движка на реальном
медицинском сценарии: Time Travel, MVCC/историю строк, ACID-транзакции,
Hash-индексы (`EXPLAIN ANALYZE`), `VACUUM` и WAL-recovery.

---

## Что входит в стек

| Сервис | Технология | Порт | Назначение |
|---|---|---|---|
| `vaultdb` | Go (этот репозиторий, `server/`) | 5432 / 8080 / 5433 | СУБД: TCP SQL, HTTP API/Web UI, health+metrics |
| `gateway` | Go 1.21 (`gateway/`), только stdlib | 4000 | REST API → SQL, JWT-аутентификация |
| `seed` | Go 1.21 (`tools/seed/`) | — | Генерация демо-данных (запускается один раз) |
| `frontend` | React 18 + TS + Vite + Tailwind (`frontend/`) | 3000 | Веб-интерфейс |

---

## Запуск

Требуется только **Docker** (+ Docker Compose v2). Go/Node для запуска не нужны —
всё собирается в контейнерах.

```bash
# из корня репозитория
docker compose -f docker-compose.medvault.yml up -d --build

# (опционально) явный .env
cp .env.medvault.example .env.medvault
docker compose -f docker-compose.medvault.yml --env-file .env.medvault up -d --build
```

Через ~20–30 секунд:

- Frontend:    http://localhost:3000
- API Gateway: http://localhost:4000
- VaultDB UI:  http://localhost:8080
- Метрики:     http://localhost:5433/metrics

Сервис `seed` стартует автоматически после готовности VaultDB и идемпотентен
(повторный запуск ничего не дублирует). При необходимости — вручную:

```bash
docker compose -f docker-compose.medvault.yml run --rm seed
```

Остановка / полная очистка:

```bash
docker compose -f docker-compose.medvault.yml down       # стоп
docker compose -f docker-compose.medvault.yml down -v     # + удалить данные
```

### Демо-аккаунты (пароль `demo123`)

| Роль | Email |
|---|---|
| Врач | `doctor@clinic.ru` |
| Администратор | `admin@clinic.ru` |
| Регистратор | `receptionist@clinic.ru` |

---

## Демо-сценарии

1. **Машина времени (Time Travel).** Пациенты → пациент №1 → «История (Time
   Travel)». Слайдер двигается по реальным версиям диагноза: J00 (лёгкая) →
   J06.9 (умеренная) → J06.9 (лёгкая). Под слайдером — реальный SQL
   `SELECT … AS OF TIMESTAMP '…'`.
2. **История изменений (MVCC).** Карта пациента → вкладка «Диагнозы» → «История
   изменений». Показываются все версии строки через `HISTORY diagnoses KEY …`.
3. **Мощь индексов (EXPLAIN).** Администратор → EXPLAIN → запрос по `patient_id`.
   Виден `Index Scan using idx_diagnoses_patient` и время выполнения.
4. **VACUUM.** Администратор → VACUUM → «Запустить VACUUM»: статистика
   освобождённых версий и места.
5. **Транзакции (атомарность).** Завершение приёма (`POST /visits/:id/complete`)
   атомарно обновляет визит и вставляет диагнозы/назначения в одной транзакции
   VaultDB (`BEGIN … COMMIT`). При ошибке — полный откат.
6. **WAL recovery.** `docker kill medvault-db && docker start medvault-db` — в
   логах VaultDB появится восстановление из WAL, данные сохранятся.

---

## Архитектура и важные технические решения

Реализация опирается на **фактическое** поведение VaultDB v1.0.1 (проверено по
исходникам `server/`), а не только на текст ТЗ. Ключевые моменты:

- **Gateway ↔ VaultDB по TCP (:5432), а не по HTTP.** HTTP API VaultDB создаёт
  **новую сессию на каждый запрос**, поэтому многошаговые транзакции
  (`BEGIN`/`COMMIT` отдельными запросами) по HTTP невозможны. TCP-протокол держит
  **одну сессию на соединение**, что и нужно для транзакций и демо изоляции «в
  двух терминалах». Gateway содержит небольшой пул TCP-соединений; на транзакцию
  выделяется отдельное соединение.
- **Time Travel на реальных версионных метках времени.** `INSERT`/`UPDATE` в
  VaultDB всегда штампуют версию `time.Now()` (см. `file_storage.go`), а
  `AS OF TIMESTAMP` сопоставляет момент по «настенному» времени. Поэтому
  «исторические» даллы из ТЗ (фейковый 2025-й) не дали бы эффекта. Вместо этого
  `seed` создаёт **по-настоящему разнесённые во времени** версии диагноза
  пациента №1 и записывает реальные метки в таблицу `timeline_markers`. Слайдер
  ходит по этим реальным меткам — `AS OF` честно возвращает разные состояния.
- **Джойны/сортировка/поиск/пагинация — на стороне Gateway.** SQL VaultDB не
  поддерживает `JOIN`, `ORDER BY`, `OFFSET`, `LIKE`. Имена врачей, последний
  визит, поиск по подстроке и постраничный вывод собираются в Go.
- **Без внешних зависимостей в Go.** `gateway` и `seed` используют только
  стандартную библиотеку (свой роутер, ручной HS256-JWT, TCP-клиент). Сборка
  детерминирована и не требует `go.sum`.

---

## Карта REST API (Gateway, `/api/v1`)

```
POST   /auth/login            GET /auth/me
GET    /patients              GET /patients/:id           POST /patients
GET    /patients/:id/visits | diagnoses | prescriptions | lab_results | allergies
GET    /patients/:id/timeline           ← метки для слайдера
GET    /patients/:id/snapshot?at=…       ← AS OF TIMESTAMP
GET    /diagnoses/:id/history             ← HISTORY … KEY
GET    /doctors
GET    /visits   POST /visits   GET /visits/:id   POST /visits/:id/complete (tx)
POST   /diagnoses   PUT /diagnoses/:id   DELETE /diagnoses/:id
POST   /prescriptions
GET    /admin/stats | metrics | wal_status | indexes
GET    /admin/vacuum   POST /admin/vacuum
POST   /admin/explain
```

---

## Что сделано из ТЗ и что отложено

Согласовано: сначала **рабочий вертикальный срез**, который целиком поднимается и
демонстрирует ключевые фичи end-to-end.

**Готово и проверено:** запуск всего стека одной командой; вход с ролями; список
пациентов с поиском и пагинацией; карта пациента (визиты/диагнозы/назначения/
анализы/аллергии); Time Travel (слайдер + `AS OF` + разные версии); история
диагноза (`HISTORY`); атомарное завершение приёма (транзакция); админ-панель
(EXPLAIN с Index Scan, VACUUM со статистикой, WAL-статус, метрики, индексы);
seed на 50 пациентов / 10 врачей / 260 визитов.

**Отложено (не входило в срез):** WebSocket live-updates; экранные формы создания
пациента/визита и UI завершения приёма (эндпоинты готовы, UI — нет);
автоматические тесты (Vitest/Playwright) и `tools/crash_test.sh`; shadcn/ui и
Recharts (использованы Tailwind + нативные элементы).
