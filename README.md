# VaultDB

SQL-совместимая СУБД с Go-сервером и C++ клиентами.

**Версия: 1.1.1**

---

## Запуск

### Через Docker Compose (рекомендуется)

```bash
# 1. Создать .env файл
echo 'VAULTDB_API_TOKENS=vdb_my_token_123' > .env
echo 'VAULTDB_AUTH_SECRET=my-secret-key' >> .env

# 2. Запустить VaultDB
docker compose up -d --build

# 3. Проверить
curl http://localhost:8080/health
```

### Через Docker (отдельно)

```bash
# Собрать образ
docker build -t vaultdb .

# Запустить
docker run -d \
  -p 5432:5432 -p 8080:8080 \
  -e VAULTDB_API_TOKENS=vdb_my_token_123 \
  -e VAULTDB_AUTH_SECRET=my-secret-key \
  -v vaultdb-data:/data \
  vaultdb
```

### Напрямую (для разработки)

```bash
# Собрать
cd server && go build -o ../vaultdb-server ./cmd/vaultdb-server

# Запустить
./vaultdb-server \
  --host 127.0.0.1 \
  --port 5432 \
  --http-port 8080 \
  --data ./data \
  --config vaultdb.yaml
```

---

## Порты

| Порт | Протокол | Назначение |
|------|----------|------------|
| 5432 | TCP | Клиентский протокол (C++ клиент) |
| 8080 | HTTP | REST API |
| 5433 | HTTP | Monitor (health/metrics) |

---

## Работа с базой данных

### Через HTTP API

```bash
# Создать базу
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "mydb", "query": "CREATE DATABASE mydb;"}'

# Создать таблицу
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "mydb", "query": "CREATE TABLE users (id INT PRIMARY KEY, name TEXT, age INT);"}'

# Вставить данные
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "mydb", "query": "INSERT INTO users VALUES (1, '\''Alice'\'', 30);"}'

# Запрос
curl -X POST http://localhost:8080/api/query \
  -H "Content-Type: application/json" \
  -d '{"database": "mydb", "query": "SELECT * FROM users;"}'
```

### Демо-интерфейс

Интерактивный SQL Lab с веб-интерфейсом доступен на ветке **[test](https://github.com/post-kserks/pro-labs/tree/test)**.

---

## Основные фичи

### SQL

- **DML**: INSERT, UPDATE, DELETE, UPSERT (ON CONFLICT), MERGE, TRUNCATE
- **DQL**: SELECT с JOIN, CTE (включая recursive), window functions, подзапросы
- **DDL**: CREATE/DROP DATABASE/TABLE/INDEX, ALTER TABLE, IF EXISTS/IF NOT EXISTS, GENERATED ALWAYS AS IDENTITY, GENERATED ALWAYS AS (вычисляемые колонки), SERIAL
- **Типы данных**: INT, BIGINT, FLOAT, BOOL, TEXT, VARCHAR, NUMERIC(p,s), JSONB, VECTOR, TIMESTAMPTZ
- **Операторы**: арифметика, сравнение, JSONB (->, ->>, @>, <@), LIKE, ILIKE

### Транзакции

```sql
BEGIN;
  INSERT INTO accounts VALUES (1, 1000);
  UPDATE accounts SET val = val - 100 WHERE id = 1;
COMMIT;
```

HTTP API также поддерживает транзакции через отслеживание сессий.

### Шифрование (TDE)

Transparent Data Encryption — AES-256-GCM на уровне страниц.

```bash
# Инициализировать шифрование
export VAULTDB_ENCRYPTION_PASSPHRASE="my-secret-passphrase"
vaultdb-encrypt init --database mydb

# Проверить статус
vaultdb-encrypt status --database mydb
```

### Time Travel

```sql
SELECT * FROM t AS OF TIMESTAMP '2024-01-01 00:00:00';
HISTORY t KEY 1;
```

### Recursive CTE

```sql
WITH RECURSIVE seq AS (
  SELECT 1 AS n
  UNION ALL
  SELECT n + 1 FROM seq WHERE n < 5
)
SELECT * FROM seq;
```

---

## Конфигурация

### vaultdb.yaml

```yaml
server:
  host: "0.0.0.0"
  port: 5432
  http_port: 8080
  monitor_port: 5433

storage:
  engine: "page"
  data_dir: "/data"

auth:
  enabled: true
```

### Переменные окружения

| Переменная | Описание |
|------------|----------|
| VAULTDB_HOST | Хост сервера |
| VAULTDB_PORT | TCP порт |
| VAULTDB_HTTP_PORT | HTTP порт |
| VAULTDB_DATA_DIR | Директория данных |
| VAULTDB_AUTH_ENABLED | Включить auth (true/false) |
| VAULTDB_API_TOKENS | API токены |
| VAULTDB_AUTH_SECRET | HMAC секрет |
| VAULTDB_LOG_LEVEL | Уровень логирования |
| VAULTDB_ENCRYPTION_PASSPHRASE | Passphrase для шифрования TDE |

---

## Архитектура

```
Client (C++) → TCP/HTTP → Lexer → Parser → Optimizer → Executor → Storage Engine
                                                ↓
                                          Transaction Manager
                                                ↓
                                          WAL (crash recovery)
                                                ↓
                                          Buffer Pool (LRU cache)
                                                ↓
                                          Heap Files (disk)
```

Подробнее: [ARCHITECTURE.md](ARCHITECTURE.md)

---

## Документация

Полная документация доступна в директории [`docs/`](docs/):

### Начало работы

| Документ | Описание |
|----------|----------|
| [Введение](docs/introduction.md) | Что такое VaultDB, возможности, сравнение |
| [Установка](docs/installation.md) | Сборка, Docker, Docker Compose |
| [Быстрый старт](docs/quickstart.md) | Первые запросы за 5 минут |

### Пользовательское руководство

| Документ | Описание |
|----------|----------|
| [Конфигурация](docs/configuration.md) | YAML, CLI флаги, переменные окружения |
| [Справочник SQL](docs/sql-reference.md) | Полный синтаксис SQL |
| [Типы данных](docs/data-types.md) | INT, FLOAT, TEXT, JSONB, VECTOR и др. |
| [Функции и операторы](docs/functions.md) | 130+ встроенных функций |
| [Индексы](docs/indexes.md) | B-tree, Hash, GIN, GiST, Composite |
| [Транзакции](docs/transactions.md) | BEGIN/COMMIT/ROLLBACK, SAVEPOINT |
| [Представления](docs/views.md) | Views |
| [Триггеры](docs/triggers.md) | AFTER триггеры |
| [Последовательности](docs/sequences.md) | AUTO_INCREMENT |
| [UDF](docs/udf.md) | Пользовательские функции и процедуры |

### Архитектура

| Документ | Описание |
|----------|----------|
| [Архитектура](docs/architecture.md) | Системная архитектура, компоненты |
| [Storage Engine](docs/storage.md) | Page-based storage, heap files, tuple format |
| [WAL и восстановление](docs/wal.md) | Write-Ahead Log, ARIES recovery |
| [MVCC](docs/mvcc.md) | Multi-Version Concurrency Control |
| [Оптимизатор](docs/optimizer.md) | Cost-based optimization |

### Администрирование

| Документ | Описание |
|----------|----------|
| [Бэкап и восстановление](docs/backup.md) | Backup/restore, CLI tool |
| [Мониторинг](docs/monitoring.md) | Prometheus metrics, health endpoints |
| [Безопасность](docs/security.md) | Auth, TLS, mTLS, RLS |
| [Шифрование](docs/encryption.md) | TDE, AES-256-GCM, управление ключами |

### Справочник API

| Документ | Описание |
|----------|----------|
| [HTTP API](docs/api-reference.md) | REST endpoints, запросы/ответы |
| [TCP Protocol](docs/tcp-protocol.md) | Wire protocol для нативных клиентов |
| [C++ клиент](docs/client.md) | Библиотека, сборка, использование |
| [AI и семантический поиск](docs/ai.md) | Embeddings, семантический поиск |
| [Глоссарий](docs/glossary.md) | Терминология |

## Структура проекта

```
├── server/                    # Go сервер
│   ├── cmd/vaultdb-server/    # Точка входа
│   ├── cmd/vaultdb-backup/    # Утилита бэкапа
│   ├── cmd/vaultdb-encrypt/   # Утилита шифрования
│   ├── internal/              # Ядро (19 пакетов)
│   │   ├── executor/          # Выполнение запросов
│   │   ├── parser/            # SQL парсер
│   │   ├── storage/           # Storage engine + buffer pool
│   │   ├── wal/               # Write-Ahead Log
│   │   ├── txmanager/         # MVCC транзакции
│   │   ├── crypto/            # Шифрование (AES-256-GCM)
│   │   ├── osdisk/            # Детекция шифрования диска
│   │   └── ...                # auth, metrics, index и др.
│   └── benchmark/             # Бенчмарки
├── client/                    # C++ клиент (libvaultdb, shell, TUI)
├── tools/                     # Benchmark tools
├── docker-compose.yml         # Docker deployment
└── Dockerfile
```

---

## Разработка

```bash
# Тесты
cd server && go test ./... -v

# Race detector
go test ./... -race

# Сборка
go build ./cmd/vaultdb-server

# Docker
docker compose up -d
```

## Лицензия

MIT
