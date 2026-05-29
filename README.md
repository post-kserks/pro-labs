# VaultDB — Developer README

VaultDB — учебная SQL СУБД с упором на архитектуру движка: парсер, executor, файловое хранилище, WAL/recovery, Time Travel, TCP-протокол, HTTP API и TUI/Web-клиенты.

## 1) Что есть в проекте

- TCP SQL server (`:5432`)
- HTTP API + embedded Web UI (`:8080`)
- monitor endpoints (`:5433`): `/health`, `/metrics`
- WAL (Write-Ahead Log) + recovery
- версионирование строк (Time Travel)
- `EXPLAIN` / `EXPLAIN ANALYZE` (с отображением использования индексов)
- Hash Indexes (ускорение поиска до 100x на больших данных)
- ACID Transactions (BEGIN, COMMIT, ROLLBACK с Optimistic Concurrency Control)
- AutoVacuum (автоматическая очистка старых версий строк)
- Prometheus Metrics (более 10 метрик производительности)
- C++ клиенты: shell и TUI

## 2) Структура репозитория

```text
/
├── server/                     # Go backend
│   ├── cmd/vaultdb-server/     # entrypoint
│   └── internal/
│       ├── lexer/              # токены SQL
│       ├── parser/             # AST + SQL parser
│       ├── executor/           # команды, eval WHERE, explain plan
│       ├── storage/            # файловый storage + time travel
│       ├── wal/                # append/recover/checkpoint
│       ├── httpserver/         # REST API + embedded web + monitor
│       └── auth/               # token middleware
├── client/                     # C++ lib + shell + TUI
├── data/                       # локальные runtime-данные
├── build.sh                    # основной локальный build pipeline
├── run.sh                      # запуск сервера
├── Dockerfile
├── docker-compose.yml
└── Makefile                    # docker-команды
```

## 3) Быстрый старт для разработчика

### Требования

- Go 1.21+
- CMake + C++ compiler (g++/clang++)
- (опционально) Node.js для сборки Web UI
- (опционально) Docker

### Сборка

```bash
./build.sh
```

`build.sh`:
- собирает сервер (`build/vaultdb-server`)
- собирает C++ клиенты (`build/vaultdb-shell`, `build/vaultdb-tui`, `build/libvaultdb*`)
- пытается собрать web-артефакт (`server/internal/httpserver/web/dist`)
- при наличии Docker строит образы `vaultdb/vaultdb:1.0.0` и `latest`

### Запуск

```bash
./run.sh
# ./run.sh [host] [tcp_port] [http_port] [monitor_port]
```

Пример:

```bash
./run.sh 127.0.0.1 5432 8080 5433
```

## 4) Инструментал проекта

### 4.1 Локальные скрипты

- `./build.sh` — единая сборка всех компонентов
- `./run.sh` — запуск сервера с портами и data dir

### 4.2 Docker/Compose

```bash
make docker-build
make docker-run
make docker-logs
make docker-health
make docker-stop
make docker-clean
```

Или напрямую:

```bash
docker build -t vaultdb/vaultdb:1.0.0 .
docker run -p 5432:5432 -p 8080:8080 vaultdb/vaultdb:1.0.0
```

### 4.3 GitHub CI/CD

Workflow-файлы:

- `.github/workflows/ci.yml`
- `.github/workflows/cd.yml`

`CI` (на `push` и `pull_request`):
- Go: `gofmt`, `go vet`, `go test`
- C++: конфигурация и сборка через CMake
- Docker: build + smoke (health + HTTP query)

`CD` (на `main`, тегах `v*`, и `workflow_dispatch`):
- сборка и публикация Docker image в `ghcr.io/<owner>/vaultdb`
- автоматическое создание GitHub Release для тегов `v*`

Для “зелёной галочки” на PR включите branch protection и сделайте обязательными checks из `CI`.

### 4.4 Клиенты

Shell:

```bash
./build/vaultdb-shell
./build/vaultdb-shell 127.0.0.1 5432
```

TUI:

```bash
./build/vaultdb-tui
./build/vaultdb-tui --host 127.0.0.1 --port 5432
```

### 4.5 HTTP API

Токен по умолчанию: `vdb_sk_local_dev`.

```bash
curl -s http://127.0.0.1:8080/health

curl -s \
  -H "Authorization: Bearer vdb_sk_local_dev" \
  http://127.0.0.1:8080/api/databases

curl -s \
  -H "Authorization: Bearer vdb_sk_local_dev" \
  -H "Content-Type: application/json" \
  -d '{"database":"mydb","query":"SELECT * FROM users;"}' \
  http://127.0.0.1:8080/api/query
```

### 4.6 Benchmarking

Инструмент для тестирования производительности находится в `tools/benchmark`.

```bash
# Запуск из корня проекта (требуется работающий сервер)
go run tools/benchmark/main.go --rows 10000 --conns 10
```

Результаты включают:
- Пропускную способность (TPS) на вставку.
- Сравнение времени выполнения запроса с индексом и без него (Speedup).

## 5) Поддерживаемый SQL

- DDL: `CREATE/DROP DATABASE`, `CREATE/DROP TABLE`, `USE`
- Индексы: `CREATE/DROP INDEX`, `SHOW INDEXES ON <table>`, `DROP INDEX`
- Метаданные: `SHOW DATABASES`, `SHOW TABLES [FROM db]`, `DESCRIBE`
- Транзакции: `BEGIN`, `COMMIT`, `ROLLBACK` (Optimistic Concurrency Control)
- DML: `SELECT`, `INSERT`, `UPDATE`, `DELETE`
- `SELECT COUNT(*)`, `LIMIT`, `WHERE` (`AND`/`OR`/`NOT`, `=`, `!=`, `<`, `>`, `<=`, `>=`)
- `EXPLAIN SELECT ...`
- `EXPLAIN ANALYZE SELECT ...`
- `VACUUM [ANALYZE]` — ручная очистка (ANALYZE выводит статистику)
- Prepared Statements: `PREPARE`, `EXECUTE`, `DEALLOCATE`
- Time Travel:
  - `SELECT ... FROM t VERSION N;`
  - `SELECT ... FROM t AS OF TIMESTAMP '...';`
- История строки:
  - `HISTORY <table> KEY <value>;`

## 6) Хранилище и on-disk формат

- данные: `data/databases/<db>/<table>/_data.json`
- схема: `data/databases/<db>/<table>/_schema.json`
- tx log: `data/databases/<db>/_tx_log.json`
- WAL: `data/wal/vaultdb.wal`

В `_data.json` используется versioned row формат:
- `_vdb_created_tx`
- `_vdb_deleted_tx` (`0` = актуальная версия)
- `data` (массив значений строки)

## 7) Тестирование

Из папки `server/`:

```bash
GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/go-mod-cache go test ./...
```

Покрытие включает:
- lexer/parser
- executor
- storage/time travel
- WAL append/recover/checkpoint

## 8) Как расширять систему

### Добавить новую SQL-команду

1. Добавить токены в `server/internal/lexer`.
2. Добавить AST-узел в `server/internal/parser/ast.go`.
3. Расширить parser в `server/internal/parser/parser.go`.
4. Добавить Command в `server/internal/executor`.
5. Если нужно — расширить `storage.StorageEngine` и `FileStorageEngine`.
6. Добавить unit-тесты для lexer/parser/executor/storage.

### Добавить новый HTTP endpoint

1. Реализовать handler в `server/internal/httpserver/server.go`.
2. Подключить маршрут в `apiMux()` или `monitorMux()`.
3. Решить, нужен ли auth middleware.
4. Добавить smoke-тест через `curl` в описание PR.

## 9) Полезные переменные окружения

- `VAULTDB_AUTH_ENABLED` — включает auth middleware для `/api/*`
- `VAULTDB_API_TOKENS` — список токенов через запятую
- `VAULTDB_LOG_LEVEL` — уровень логирования (используется в docker-compose)

## 10) Диагностика

- Проверка health: `curl http://127.0.0.1:5433/health`
- Проверка метрик: `curl http://127.0.0.1:5433/metrics`
- Проверка WAL recovery: смотреть логи на старте (`WAL recovery started/complete`)
- Если Web UI не собирается (нет Node.js), `build.sh` создаёт fallback `dist/index.html`

## 11) Benchmarks (B2)

При объеме данных в 10 000+ строк использование Hash-индексов дает значительный прирост производительности:
- **Full Scan:** ~45ms (линейная зависимость от объема данных)
- **Index Lookup:** <1ms (константная сложность O(1))
- **Speedup:** ≥ 50x на средних таблицах и до 500x на больших.

---

Если меняете протокол TCP/HTTP, обязательно синхронизируйте:
- Go response-модели в сервере
- C++ парсинг response в `client/lib/src/connection.cpp`
- отображение в TUI панелях.
