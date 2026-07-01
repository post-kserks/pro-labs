# VaultDB

SQL-совместимая СУБД с Go-сервером и C++ клиентами.

**Версия: 1.1.0**

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
- **DDL**: CREATE/DROP DATABASE/TABLE/INDEX, ALTER TABLE
- **Типы данных**: INT, FLOAT, BOOL, TEXT, VARCHAR, JSONB, VECTOR
- **Операторы**: арифметика, сравнение, JSONB (->, ->>, @>, <@), LIKE

### Транзакции

```sql
BEGIN;
  INSERT INTO accounts VALUES (1, 1000);
  UPDATE accounts SET val = val - 100 WHERE id = 1;
COMMIT;
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

## Структура проекта

```
├── server/                    # Go сервер
│   ├── cmd/vaultdb-server/    # Точка входа
│   ├── cmd/vaultdb-backup/    # Утилита бэкапа
│   ├── internal/              # Ядро (18 пакетов)
│   │   ├── executor/          # Выполнение запросов
│   │   ├── parser/            # SQL парсер
│   │   ├── storage/           # Storage engine + buffer pool
│   │   ├── wal/               # Write-Ahead Log
│   │   ├── txmanager/         # MVCC транзакции
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
