# VaultDB — Production Readiness Code Review

> **Дата:** 2026-06-27
> **Цель:** Оценить готовность проекта к выпуску в продакшен
> **Покрытие:** Go сервер, C++ клиент, CI/CD, тесты

---

## Executive Summary

| Категория | CRITICAL | HIGH | MEDIUM | LOW | Итого |
|-----------|----------|------|--------|-----|-------|
| Go сервер | 3 | 5 | 7 | 3 | 18 |
| C++ клиент | 2 | 4 | 7 | 4 | 17 |
| CI/CD & Docker | 2 | 4 | 7 | 5 | 18 |
| Тесты (gaps) | 10 untested features | — | — | — | 10 |
| **Итого** | **17** | **13** | **21** | **12** | **63** |

**Вердикт: НЕ ГОТОВ к продакшену.** Есть 17 критических и серьёзных проблем, которые могут привести к потере данных, утечке токенов или компрометации образов.

---

## КРИТИЧЕСКИЕ ПРОБЛЕМЫ (must-fix перед релизом)

### 1. SQL Injection через unescaped идентификаторы в C++ TUI
- **Файл:** `client/tui/app/app.cpp:263,270,276,290,296,311`
- **Проблема:** Имена БД и таблиц из навигатора конкатенируются в SQL без экранирования
- **Влияние:** Удаление/модификация данных через вредоносные имена
- **Фикс:** Использовать `sqlIdent()` во всех местах конструирования SQL

### 2. Токены в URL query string — утечка в логи
- **Файл:** `server/internal/httpserver/server.go:208-210`
- **Проблема:** `/api/` обработчик принимает токены через `?token=`
- **Влияние:** Токены попадают в логи прокси, историю браузера
- **Фикс:** Убрать fallback на query parameter, требовать `Authorization` header

### 3. Нет сканирования уязвимостей Docker образов перед публикацией
- **Файл:** `.github/workflows/cd.yml`
- **Проблема:** Образ публикуется в GHCR без Trivy/CVE сканирования
- **Влияние:** Продакшен-образ может содержать известные уязвимости
- **Фикс:** Добавить Trivy сканирование перед push

### 4. Нет подписи Docker образов
- **Файл:** `.github/workflows/cd.yml`
- **Проблема:** `provenance: false` отключает SLSA attestation
- **Влияние:** Supply chain атака — образ может быть подменён
- **Фикс:** Включить attestation или использовать cosign

### 5. WriteFullPageImage без TableID при WAL replay
- **Файл:** `server/internal/storage/buffer_pool.go:190-206`
- **Проблема:** Пустые `DB` и `Table` строки при replay full page images
- **Влияние:** Torn page protection не работает при восстановлении
- **Фикс:** Передавать реальные имена DB/Table

### 6. ConnectionRateLimiter без мьютекса
- **Файл:** `server/cmd/vaultdb-server/main.go:138-169`
- **Проблема:** Экспортируемый тип без синхронизации
- **Влияние:** Data race при использовании из нескольких горутин
- **Фикс:** Добавить `sync.Mutex` или сделать unexported

### 7. Thread-unsafe доступ к connection_ в C++ TUI
- **Файл:** `client/tui/app/app.cpp:57-67,155-176`
- **Проблема:** `executeSql()` вызывает `connection_.execute()` без блокировки
- **Влияние:** Data race на socket/SSL, потенциальный краш
- **Фикс:** Сделать `Connection` thread-safe или гарантировать вызов из одного потока

---

## СЕРЬЁЗНЫЕ ПРОБЛЕМЫ (high priority)

### 8. TCP port без rate limiting на аутентификацию
- **Файл:** `server/cmd/vaultdb-server/main.go:171-272`
- HTTP путь имеет rate limiting, TCP — нет. Brute-force токенов через TCP.

### 9. doCheckpoint блокирует storage пока пишет в WAL
- **Файл:** `server/internal/storage/page_engine.go:553-574`
- Возможен deadlock или долгая стalling при checkpoint.

### 10. undoInsert — O(n²) при большом батче
- **Файл:** `server/internal/executor/commands_tx.go:214-263`
- `ReadCurrentRows` + линейный поиск для каждого вставленного row.

### 11. WAL sync batching теряет коммиты при аварийном выключении
- **Файл:** `server/internal/wal/wal.go:307-337`
- До 63 последних транзакций могут быть потеряны. COMMIT запись должна форсировать fsync.

### 12. SSL cleanup в C++ хрупкий — 10 early-return путей
- **Файл:** `client/lib/src/connection.cpp:69-158`
- Каждый error path вручную освобождает ресурсы. Утечки при добавлении нового кода.

### 13. isConnected() игнорирует TLS state
- **Файл:** `client/lib/src/connection.cpp:168-170`
- Если TLS handshake провалился, socket может быть закрыт, но `isConnected()` вернёт true.

### 14. Config/history парсинг через regex вместо JSON парсера
- **Файл:** `client/tui/logic/config.cpp:17-42`, `history.cpp:19-44`
- Regex ломается на escaped quotes, вложенных объектах.

### 15. JSON escape не экранирует control characters
- **Файл:** `client/lib/src/json_utils.cpp:134-150`
- `\b`, `\f`, `\u0000-\u001F` проходят без экранирования (нарушает RFC 8259).

### 16. golangci-lint настроен но не используется в CI
- **Файл:** `.github/workflows/ci.yml`
- Конфиг `.golangci.yml` есть, но CI его не запускает.

### 17. Нет контрольных сумм для релизов
- **Файл:** `.github/workflows/cd.yml`
- Артефакты не подписываются, невозможно проверить целостность.

### 18. C++ тесты тихо пропускаются при ошибке
- **Файл:** `.github/workflows/ci.yml`
- Если GoogleTest не найден, CI просто пишет "skipped" вместо fail.

---

## СРЕДНИЕ ПРОБЛЕМЫ (medium priority)

| # | Компонент | Проблема |
|---|-----------|----------|
| 19 | auth | `authRateLimiter` — утечка памяти при большом количестве IP |
| 20 | auth | `sanitizeErrorMessage` не ловит Windows пути (`C:\`) |
| 21 | http | Health check token в URL query parameter |
| 22 | http | CORS wildcard молча игнорируется |
| 23 | http | `handleQuery` не проверяет пустой database |
| 24 | pool | `isHealthy` читает 0 байт — мёртвые коннекты в пуле |
| 25 | build | `$(date)` в ldflags — non-reproducible builds |
| 26 | ci | Node.js `"20"` без minor version |
| 27 | ci | staticcheck `v0.5.1` устарел |
| 28 | ci | gosec G104 исключён глобально |
| 29 | ci | CD permissions без approval gate |
| 30 | ci | Smoke test с `AUTH_ENABLED=false` — auth не проверяется |
| 31 | ci | Smoke test не парсит response body |
| 32 | cpp | `parseArray()` — depth_ не декрементируется на пустом массиве |
| 33 | cpp | Дублирующие string utils в разных namespaces |
| 34 | cpp | `parseNumber()` — silent precision loss для больших int |
| 35 | cpp | `handleEvent()` — мёртвый код (двойной `isCtrl('Q')`) |
| 36 | cpp | Editor history — Up/Down работают только при пустом query |

---

## НИЗКИЕ ПРОБЛЕМЫ (low priority)

| # | Компонент | Проблема |
|---|-----------|----------|
| 37 | storage | WAL без advisory file lock — два процесса = коррупция |
| 38 | storage | `PageLockManager.evictIfTooLarge` — non-deterministic (map iteration) |
| 39 | executor | Plan cache отключен — тратит аллокации |
| 40 | ci | Нет SBOM генерации |
| 41 | ci | npm audit без `--audit-level` |
| 42 | docker | `FROM scratch` — нет shell для дебага |
| 43 | ci | `npm install` вместо `npm ci` в CD |
| 44 | docker | docker-compose placeholder вместо примера токена |
| 45 | cpp | `Value` ~100+ байт даже для null — memory overhead |
| 46 | cpp | History regex `\{[^\}]*\}` ломается на `}` в query |
| 47 | cpp | Нет `CMAKE_POSITION_INDEPENDENT_CODE` |
| 48 | cpp | Port validation — `stoi` без проверки диапазона |

---

## КРИТИЧЕСКИЕ GAPS В ТЕСТАХ

Парсер уже парсит эти фичи, но executor НЕ имеет ни одного теста:

| Фича | Статус |
|-------|--------|
| RLS (Row-Level Security) | Парсится, 0 тестов |
| Views | Парсится, 0 тестов |
| Triggers | Парсится, 0 тестов |
| Stored Procedures | Парсится, 0 тестов |
| Stored Functions | Парсится, 0 тестов |
| Composite Indexes | Код есть, 0 тестов |
| Savepoints | Парсится, 0 тестов |
| Migrations | Парсится, 0 тестов |
| DROP VIEW/TRIGGER/FUNCTION | Парсится, 0 тестов |
| SHOW INDEXES | Парсится, 0 тестов |

**Concurrency gaps:**
- WAL — нет concurrent writer тестов
- BTree index — нет concurrent insert/lookup теста
- Buffer pool — нет concurrent fetch/evict теста
- Нет concurrent DDL тестов (CREATE + DROP из разных сессий)

---

## СИЛЬНЫЕ СТОРОНЫ (что сделано хорошо)

### Go сервер
- HMAC-SHA256 хеши токенов (никогда в открытом виде)
- WAL с CRC32 чексуммами
- Трёхфазное восстановление WAL (Analysis → Redo → Undo) как в PostgreSQL ARIES
- Page-level locking с sorted acquisition order
- Full page images для torn page protection
- Graceful shutdown с drain пула соединений
- Security headers (CSP, X-Frame-Options, X-Content-Type-Options)
- TLS 1.2+ с сильными cipher suites, mTLS
- Input validation — `validateObjectName` блокирует path traversal
- Rate limiting на TCP и HTTP
- Query timeout через `context.WithTimeout`
- Prepared statement limits

### C++ клиент
- Кросс-платформенная поддержка (Windows/Linux/macOS)
- JSON парсер с depth limit (DoS protection)
- 64MB response limit
- Socket timeouts, `MSG_NOSIGNAL`, `TCP_NODELAY`
- `static_cast<unsigned char>` для ctype functions (нет UB)
- RAII в деструкторе `Connection`

### Тесты
- `storage/crash_test.go` — production-grade crash recovery тесты
- `executor/chaos_test.go` — cumulative data integrity через shutdown cycles
- `storage/page/page_test.go` — 10k random ops fuzz test
- Parser fuzz test
- Undo type assertion safety tests

---

## РЕКОМЕНДАЦИИ ПО ПРИОРИТЕТУ

### Перед релизом (Block release)
1. Исправить SQL injection в C++ TUI (Task 2.4 из плана)
2. Убрать токены из URL query string
3. Добавить Trivy сканирование в CD
4. Исправить WriteFullPageImage WAL replay
5. Добавить мьютекс в ConnectionRateLimiter

### В первые 2 недели после релиза
6. TCP rate limiting
7. Checkpoint deadlock fix
8. WAL COMMIT fsync
9. JSON escape completeness
10. C++ SSL cleanup (RAII)

### В первый месяц
11. Тесты для RLS, Views, Triggers
12. Concurrent тесты для WAL, BTree
13. golangci-lint в CI
14. SBOM генерация
15. Image signing
